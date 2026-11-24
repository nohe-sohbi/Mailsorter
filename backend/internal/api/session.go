package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mirror"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"github.com/nohe-sohbi/mailsorter/backend/internal/rules"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	gmailapi "google.golang.org/api/gmail/v1"
)

// Which transport a given user is on, and a session open on it.
//
// Until now every handler assumed one answer: build a Gmail client, use it.
// That assumption is what stood between the IMAP transport and the app. This
// file replaces it with a question asked per user, and it is deliberately
// STRICTER than the code it replaces:
//
//   - A user with a connected IMAP mailbox can no longer obtain a Gmail client.
//     Every path not yet ported refuses with errWrongTransport instead of
//     sending an IMAP UID to Gmail as a message id, which would act on an
//     unrelated message and journal it as a success.
//   - A datastore error does NOT resolve to Gmail. Only the absence of a
//     connected mailbox does. Falling back on error would mean a Mongo hiccup
//     silently turns an IMAP user into a Gmail user, which is the same wrong
//     mailbox by another route.

// errNoMailboxConnected indicates that no mailbox (IMAP or Gmail) has been linked yet.
var errNoMailboxConnected = errors.New("api: no mailbox connected for this account")

// errWrongTransport means the caller's mailbox is not reachable the way this
// code path assumes. It is not a failure of the request: it is a feature that
// has not been ported to the user's transport yet, and it maps to 501 so the
// answer says "not here, not yet" rather than "you did something wrong".
var errWrongTransport = errors.New("api: this path does not support the user's mailbox transport")

// mailSession is a user's connected mailbox, open for work.
//
// It is closed by the caller. The IMAP half holds a live connection, and an
// IMAP session is stateful (commands act on the selected folder), so a session
// belongs to one unit of work and is never shared between goroutines.
type mailSession struct {
	Transport provider.Transport

	h         *Handler
	userEmail string
	// account is the stored connection, set only on the IMAP transport.
	account models.MailAccount
	gmail   *gmailapi.Service
	imapc   *imap.Client
}

// transportFor answers which transport this user's mailbox is reached by.
//
// A stored mail_accounts row wins: it is the mailbox the user explicitly
// connected, and in the hosted edition it is the only one on offer. Its absence
// means the Google OAuth path, which is how every account worked before this
// existed and how every self-hosted account still works.
func (h *Handler) transportFor(ctx context.Context, userEmail string) (provider.Transport, models.MailAccount, error) {
	var account models.MailAccount
	err := h.db.MailAccounts().FindOne(ctx, bson.M{"userId": userEmail}).Decode(&account)
	switch {
	case err == nil:
		if account.Transport == string(provider.TransportIMAP) {
			return provider.TransportIMAP, account, nil
		}
		if account.Transport == "gmail" {
			return provider.TransportGmailAPI, account, nil
		}
		return provider.Transport(account.Transport), account, nil
	case errors.Is(err, mongo.ErrNoDocuments):
		// Check whether user has direct Google OAuth tokens
		var user models.User
		uErr := h.db.Users().FindOne(ctx, bson.M{"email": userEmail}).Decode(&user)
		if uErr == nil && (user.AccessToken != "" || user.RefreshToken != "") {
			return provider.TransportGmailAPI, models.MailAccount{
				UserID:    userEmail,
				Provider:  "google",
				Transport: "gmail",
				Username:  user.Email,
			}, nil
		}
		return "", models.MailAccount{}, errNoMailboxConnected
	default:
		// Deliberately NOT a fallback to Gmail. See the file comment: guessing
		// here sends one user's identifiers to another user's kind of mailbox.
		return "", models.MailAccount{}, fmt.Errorf("resolve mailbox transport: %w", err)
	}
}

// openSession resolves the transport and opens whatever it needs.
func (h *Handler) openSession(ctx context.Context, userEmail string) (*mailSession, error) {
	transport, account, err := h.transportFor(ctx, userEmail)
	if err != nil {
		return nil, err
	}
	session := &mailSession{Transport: transport, h: h, userEmail: userEmail, account: account}

	switch transport {
	case provider.TransportIMAP:
		client, err := h.openMailbox(ctx, userEmail)
		if err != nil {
			return nil, err
		}
		session.imapc = client
	default:
		client, err := h.newGmailClient(ctx, userEmail)
		if err != nil {
			return nil, err
		}
		session.gmail = client
	}
	return session, nil
}

// Close releases the connection. Harmless on the Gmail transport, which holds
// none, and required on IMAP, which holds a socket.
func (s *mailSession) Close() {
	if s != nil && s.imapc != nil {
		s.imapc.Close()
	}
}

// Mailbox is what mutations go through.
func (s *mailSession) Mailbox() mailbox.Mailbox {
	if s.Transport == provider.TransportIMAP {
		return mirroredIMAP{client: s.imapc, h: s.h, userEmail: s.userEmail}
	}
	return gmailMailbox{svc: s.h.gmailService, client: s.gmail}
}

// RefFor names a message the way THIS transport names one.
//
// This is the whole reason a session exists rather than a bare client. A call
// site holds a message id and no opinion about what kind of id it is; the
// session knows, because it knows the transport. Over the Gmail API the id
// names the message on the account and that is the end of it. Over IMAP a UID
// only means something inside a folder, so the folder is read back from the
// stored message, which is where the listing recorded it.
//
// The inbox is the fallback rather than an error: a message Mailsorter has
// never synced can still be acted on (the ledger replays ids it stored months
// ago), and the inbox is where a message is unless something moved it.
func (s *mailSession) RefFor(ctx context.Context, messageID string) mailbox.Ref {
	if s.Transport != provider.TransportIMAP {
		return mailbox.OnAccount(messageID)
	}
	folder := string(mailbox.FolderInbox)
	var stored models.Email
	err := s.h.db.Emails().FindOne(ctx, bson.M{"userId": s.userEmail, "messageId": messageID}).Decode(&stored)
	if err == nil && stored.Folder != "" {
		folder = stored.Folder
	}
	return mailbox.InFolder(folder, messageID)
}

// writeTransportError answers a request that reached a path its user's mailbox
// cannot use. 501 rather than 400 or 500: the request was well formed and the
// server is not broken, the capability simply is not implemented for this
// transport yet.
func writeTransportError(w http.ResponseWriter) {
	writeError(w, http.StatusNotImplemented,
		"Cette action n'est pas encore disponible sur une boîte branchée en IMAP.")
}

// syncInboxIMAP is the IMAP half of syncInbox: read a page of the inbox and
// mirror it into Mongo, then, when the user turned on "appliquer mes règles
// automatiquement", apply the rules to the mails that arrived.
//
// The rules a sync applies on its own judge the mails that ARRIVE, where over
// the Gmail API they judge every mail listed (see syncRules.arrival): a mail
// this sync stores for the first time, received since the option counts, and
// not one the copy holds under another number. So turning the option on, or
// connecting a mailbox with the option on, leaves alone the mails already in
// the inbox ("Appliquer maintenant" is how those are sorted), and a mail put
// back by "Annuler" after a rule is not sorted again at the next sync. An
// arrival is marked (rulesDue) as it is stored, and judged until it is: a sync
// that could not judge it says so (syncRulesDone.pending, a success all the
// same: the mails were read and listed), and the next one judges it again.
func (h *Handler) syncInboxIMAP(ctx context.Context, userEmail string, session *mailSession) (synced, total int, ruled syncRulesDone, err error) {
	const folder = string(mailbox.FolderInbox)

	auto := h.syncRulesFor(ctx, userEmail, session.account)

	listedAt := time.Now()
	emails, err := session.imapc.List(ctx, folder, imapListLimit)
	if err != nil {
		return 0, 0, syncRulesDone{}, err
	}
	// Before dropStaleInbox, which may drop the row of a mail that left and
	// is coming back.
	comingBack := map[string]bool{}
	if auto.on() {
		comingBack, err = h.comingBack(ctx, userEmail, emails)
		if err != nil {
			// Not told apart, a mail the user put back could be sorted
			// again: none of this sync's mails is marked for the rules.
			log.Printf("imap sync: read which listed mails of %s the copy holds under another number, no mail of this sync is marked for the rules: %v", userEmail, err)
			auto.markNone = true
		}
	}
	// The listing reads the newest messages only; which of the older ones
	// stored are still in the inbox, what another mail program changed on
	// them (read, starred), and how large they are, it cannot tell. One FETCH
	// of the flags and the size of the whole inbox can, whatever its size.
	statesAt := time.Now()
	states, err := session.imapc.States(folder)
	if err != nil {
		return 0, 0, syncRulesDone{}, err
	}
	present := make(map[string]bool, len(states))
	for uid := range states {
		present[uid] = true
	}
	// Messages that left the inbox since the last sync (moved by Mailsorter,
	// by the user's phone, by a filter on the server) must leave the copy the
	// list is served from, or they show forever.
	dropErr := h.dropStaleInbox(ctx, userEmail, present, listedAt)

	// A message that cannot be stored is not on the user's screen, and nothing
	// else will ever tell them it exists: the listing is served from this
	// mirror. So each failure is logged with what it was, the others are still
	// stored, and the sync answers an error rather than a success.
	failed := 0
	for _, email := range emails {
		if !present[email.MessageID] {
			// Gone between the listing and the UID search: storing it would
			// list it again until the next sync.
			continue
		}
		// Marked only if the row is new: a mail the copy knew is not one.
		if sErr := h.storeInboxMessage(ctx, userEmail, email, auto.arrival(email, comingBack)); sErr != nil {
			failed++
			log.Printf("imap sync: message uid %s in %s not stored: %v", email.MessageID, folder, sErr)
			continue
		}
		synced++
	}
	// Right after they are stored, before the phases that may lose the
	// connection.
	ruled = h.rulesOnArrivals(ctx, userEmail, session, auto)

	// Each phase that reads by halves may open the connection again a few
	// times, when a FETCH answer it cannot parse closes it
	// (imap.ReadInHalves), with a budget of its own: a favorite whose
	// envelope cannot be read, tried again at each sync, must not leave the
	// structures with none.
	starredErr := h.storeStarredOutsideList(ctx, userEmail, session, states, h.sessionReopener(ctx, userEmail, session))
	// After every message is stored, so it covers them all.
	stateErr := h.refreshInboxState(ctx, userEmail, states, statesAt)
	structureErr := h.completeInboxStructure(ctx, userEmail, session, present, h.sessionReopener(ctx, userEmail, session))
	if failed > 0 {
		return synced, len(emails), ruled, fmt.Errorf("%w (%d sur %d)", errMessagesNotStored, failed, len(emails))
	}
	for _, err := range []error{dropErr, starredErr, stateErr, structureErr} {
		if err != nil {
			return synced, len(emails), ruled, err
		}
	}
	return synced, len(emails), ruled, nil
}

// imapListLimit is how many of the most recent inbox messages a sync reads.
const imapListLimit = 100

// syncRules is what a sync needs to apply the rules on its own: the enabled
// rules and the protected senders, zero when the option is off or the user
// has no enabled rule, and since which date a mail is an arrival.
type syncRules struct {
	ruleset   []models.SortingRule
	protected []string
	// since is the later of when the option was turned on and when the
	// mailbox was connected, to the second (INTERNALDATE has no finer): a
	// mail received before was already in the inbox.
	since time.Time
	// markNone: this sync cannot tell an arrival from a mail coming back, so
	// it marks none (the ones marked before are still judged).
	markNone bool
}

// on says whether the rules run at this sync.
func (r syncRules) on() bool { return len(r.ruleset) > 0 }

// arrival says whether a listed mail is one the rules judge on their own, if
// this sync stores it for the first time (storeInboxMessage marks a new row
// only): received (INTERNALDATE) since the option counts, and not a mail the
// copy holds under another number (comingBack).
func (r syncRules) arrival(email models.Email, comingBack map[string]bool) bool {
	return r.on() && !r.markNone && !email.ReceivedDate.Before(r.since) && !comingBack[email.MessageID]
}

// syncRulesFor reads what a sync of this user needs to apply the rules on its
// own, when the option "appliquer mes règles automatiquement" is on. A user
// row that cannot be read runs no rule at this sync: the mails it does not
// mark are left for "Appliquer maintenant", and the ones marked before wait
// for the next sync.
func (h *Handler) syncRulesFor(ctx context.Context, userEmail string, account models.MailAccount) syncRules {
	var user struct {
		AutoApplyRules      bool      `bson:"autoApplyRules"`
		AutoApplyRulesSince time.Time `bson:"autoApplyRulesSince"`
	}
	if err := h.db.Users().FindOne(ctx, bson.M{"email": userEmail},
		options.FindOne().SetProjection(bson.M{"autoApplyRules": 1, "autoApplyRulesSince": 1}),
	).Decode(&user); err != nil {
		if !errors.Is(err, mongo.ErrNoDocuments) {
			log.Printf("imap sync: read whether the rules of %s run at the sync, they do not this time: %v", userEmail, err)
		}
		return syncRules{}
	}
	if !user.AutoApplyRules {
		return syncRules{}
	}
	ruleset := h.enabledRules(ctx, userEmail)
	if len(ruleset) == 0 {
		return syncRules{}
	}
	return syncRules{ruleset: ruleset, protected: h.protectedValues(ctx, userEmail), since: arrivalsSince(user.AutoApplyRulesSince, account.CreatedAt)}
}

// arrivalsSince is from when a mail is an arrival: the later of when the
// option was turned on and when the mailbox was connected (an option turned on
// before Mailsorter stamped it is zero: the connection alone counts), to the
// second, since INTERNALDATE has no finer.
func arrivalsSince(optionOn, connected time.Time) time.Time {
	since := optionOn
	if connected.After(since) {
		since = connected
	}
	return since.Truncate(time.Second)
}

// comingBack is, among the listed mails, the ones whose Message-ID the copy of
// the inbox holds under another number when the sync starts, whatever the
// state of that row: still in the inbox, moved out by Mailsorter (kept 30
// days for "Annuler"), put back by "Annuler" (restoredFrom), or moved out by
// another mail program since the last sync (its row goes at this sync). Such a
// mail is not one that arrives: it is coming back, put back by "Annuler" when
// the server did not say its new number, or moved back by another mail
// program. A mail moved out by another program, then back after a sync went
// by, is not recognized: that sync dropped its row.
func (h *Handler) comingBack(ctx context.Context, userEmail string, listed []models.Email) (map[string]bool, error) {
	out := map[string]bool{}
	byHeader := map[string][]string{}
	for _, e := range listed {
		if e.HeaderMessageID != "" {
			byHeader[e.HeaderMessageID] = append(byHeader[e.HeaderMessageID], e.MessageID)
		}
	}
	if len(byHeader) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(byHeader))
	for id := range byHeader {
		ids = append(ids, id)
	}
	cur, err := h.db.Emails().Find(ctx, bson.M{
		"userId":          userEmail,
		"folder":          string(mailbox.FolderInbox),
		"headerMessageId": bson.M{"$in": ids},
	}, options.Find().SetProjection(bson.M{"messageId": 1, "headerMessageId": 1}))
	if err != nil {
		return nil, err
	}
	var rows []struct {
		MessageID       string `bson:"messageId"`
		HeaderMessageID string `bson:"headerMessageId"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	for _, r := range rows {
		for _, uid := range byHeader[r.HeaderMessageID] {
			if uid != r.MessageID {
				out[uid] = true
			}
		}
	}
	return out, nil
}

// maxRuleTries is how many syncs judge a marked mail before giving up on it
// (logged, and told once): a failure that does not go away (a folder the
// server refuses) must not be announced at every sync.
const maxRuleTries = 3

// syncRulesDone is what the rules did at a sync: the mails they acted on
// (applied), the arrivals they could not judge, left marked for the next sync
// (pending), and the ones given up after maxRuleTries syncs (givenUp), which
// stay in the inbox.
type syncRulesDone struct {
	applied, pending, givenUp int
}

// rulesMessage is what the screen tells after a sync whose rules could not
// judge every mail that arrived, as information: the sync itself worked, the
// mails were read and are listed. Empty when there is nothing to tell. A mail
// given up is told at the sync that gives it up, once.
func rulesMessage(pending, givenUp int) string {
	var parts []string
	switch {
	case pending == 1:
		parts = append(parts, "Vos règles n'ont pas pu être appliquées à 1 nouveau mail : la prochaine synchronisation réessaiera.")
	case pending > 1:
		parts = append(parts, fmt.Sprintf("Vos règles n'ont pas pu être appliquées à %d nouveaux mails : la prochaine synchronisation réessaiera.", pending))
	}
	switch {
	case givenUp == 1:
		parts = append(parts, "1 mail n'a pas pu être rangé par vos règles : laissé dans la boîte.")
	case givenUp > 1:
		parts = append(parts, fmt.Sprintf("%d mails n'ont pas pu être rangés par vos règles : laissés dans la boîte.", givenUp))
	}
	return strings.Join(parts, " ")
}

// rulesOnArrivals applies the rules to the mails marked for them (rulesDue):
// the arrivals of this sync and of the syncs before that could not judge
// them, still in the inbox and received since the option counts. Each action
// is written in the history like those of "Appliquer maintenant" (runRules). A
// mail judged, or whose text cannot be read (NotReached, unparseable: logged
// by withTexts), is unmarked; one whose text read stopped or whose rule failed
// stays marked for the next sync (pending), maxRuleTries times at most
// (givenUp). A datastore that cannot be asked which mails are marked is
// logged: they stay marked, and the next sync judges them.
func (h *Handler) rulesOnArrivals(ctx context.Context, userEmail string, session *mailSession, auto syncRules) syncRulesDone {
	if !auto.on() {
		return syncRulesDone{}
	}
	due, claim, err := h.claimRulesDue(ctx, userEmail, auto.since)
	if err != nil {
		log.Printf("imap sync: take the mails of %s marked for the rules, judged at the next sync: %v", userEmail, err)
		return syncRulesDone{}
	}
	if len(due) == 0 {
		return syncRulesDone{}
	}
	ids := make([]string, 0, len(due))
	for _, e := range due {
		ids = append(ids, e.MessageID)
	}
	judged := due
	if rules.ReadsText(auto.ruleset) {
		withText, err := h.withTexts(ctx, userEmail, session, due)
		if err != nil {
			log.Printf("imap sync: the text of %d mails of %s marked for the rules could not be read, judged at the next sync: %v", len(due), userEmail, err)
			pending, givenUp := h.releaseRulesDue(ctx, userEmail, claim, nil, ids)
			return syncRulesDone{pending: pending, givenUp: givenUp}
		}
		judged = withText
	}
	done := h.runRules(ctx, session, userEmail, judged, auto.ruleset, auto.protected)
	failed := make(map[string]bool, len(done.failed))
	for _, id := range done.failed {
		failed[id] = true
	}
	var ok []string
	for _, id := range ids {
		if !failed[id] {
			ok = append(ok, id)
		}
	}
	pending, givenUp := h.releaseRulesDue(ctx, userEmail, claim, ok, done.failed)
	return syncRulesDone{applied: done.applied, pending: pending, givenUp: givenUp}
}

// claimRulesDue takes the mails this sync judges: marked for the rules, still
// in the inbox, received since the option counts, and not held by another
// sync running at the same time (a claim older than moveInFlight is one that
// never ended). Of two syncs at once, one judges each mail.
func (h *Handler) claimRulesDue(ctx context.Context, userEmail string, since time.Time) ([]models.Email, string, error) {
	folder := string(mailbox.FolderInbox)
	claim := primitive.NewObjectID().Hex()
	now := time.Now()
	if _, err := h.db.Emails().UpdateMany(ctx, bson.M{
		"userId": userEmail, "folder": folder, "rulesDue": true, "leftInbox": notLeftInbox,
		"receivedDate": bson.M{"$gte": since},
		"$or": []bson.M{
			{"rulesClaimAt": bson.M{"$exists": false}},
			{"rulesClaimAt": bson.M{"$lt": now.Add(-moveInFlight)}},
		},
	}, bson.M{"$set": bson.M{"rulesClaim": claim, "rulesClaimAt": now}}); err != nil {
		return nil, "", err
	}
	cur, err := h.db.Emails().Find(ctx, bson.M{"userId": userEmail, "folder": folder, "rulesClaim": claim},
		options.Find().SetSort(bson.D{{Key: "receivedDate", Value: -1}}))
	if err != nil {
		return nil, "", err
	}
	due := make([]models.Email, 0)
	if err := cur.All(ctx, &due); err != nil {
		return nil, "", err
	}
	return due, claim, nil
}

// releaseRulesDue ends a claim: the mails judged are unmarked; the ones that
// failed stay marked for the next sync, with one try more (pending), and are
// unmarked once they reach maxRuleTries (givenUp). A write that fails is
// logged: the mail is then judged once more, or its claim expires
// (moveInFlight), and it counts as pending.
func (h *Handler) releaseRulesDue(ctx context.Context, userEmail, claim string, judged, failed []string) (pending, givenUp int) {
	folder := string(mailbox.FolderInbox)
	if len(judged) > 0 {
		if _, err := h.db.Emails().UpdateMany(ctx,
			bson.M{"userId": userEmail, "folder": folder, "rulesClaim": claim, "messageId": bson.M{"$in": judged}},
			bson.M{"$unset": bson.M{"rulesDue": "", "rulesTries": "", "rulesClaim": "", "rulesClaimAt": ""}},
		); err != nil {
			log.Printf("imap sync: %d mails of %s judged by the rules, not unmarked: %v", len(judged), userEmail, err)
		}
	}
	if len(failed) == 0 {
		return 0, 0
	}
	if _, err := h.db.Emails().UpdateMany(ctx,
		bson.M{"userId": userEmail, "folder": folder, "rulesClaim": claim, "messageId": bson.M{"$in": failed}},
		bson.M{"$unset": bson.M{"rulesClaim": "", "rulesClaimAt": ""}, "$inc": bson.M{"rulesTries": 1}},
	); err != nil {
		log.Printf("imap sync: %d mails of %s the rules could not judge, not released for the next sync: %v", len(failed), userEmail, err)
		return len(failed), 0
	}
	res, err := h.db.Emails().UpdateMany(ctx,
		bson.M{"userId": userEmail, "folder": folder, "messageId": bson.M{"$in": failed}, "rulesTries": bson.M{"$gte": maxRuleTries}},
		bson.M{"$unset": bson.M{"rulesDue": "", "rulesTries": ""}},
	)
	if err != nil {
		log.Printf("imap sync: give up the rules on mails of %s after %d tries: %v", userEmail, maxRuleTries, err)
		return len(failed), 0
	}
	givenUp = int(res.ModifiedCount)
	if givenUp > 0 {
		log.Printf("imap sync: the rules gave up on %d mails of %s after %d tries (among %v)", givenUp, userEmail, maxRuleTries, failed)
	}
	return len(failed) - givenUp, givenUp
}

// storeInboxMessage writes a message the sync read in full (its envelope)
// into the stored copy of the inbox. rulesDue marks the row for the rules,
// if the copy did not hold it until now (see syncRules.arrival).
func (h *Handler) storeInboxMessage(ctx context.Context, userEmail string, email models.Email, rulesDue bool) error {
	email.UserID = userEmail
	// The folder is stored with the message because on IMAP it is half of
	// its identity: without it the UID names nothing in particular.
	email.Folder = string(mailbox.FolderInbox)
	update, err := inboxUpsert(email, rulesDue)
	if err != nil {
		return err
	}
	_, err = h.db.Emails().UpdateOne(ctx,
		bson.M{"messageId": email.MessageID, "userId": userEmail},
		update,
		options.Update().SetUpsert(true),
	)
	return err
}

// inboxUpsert is the write that stores a message read by a sync.
//
// The read state and the star are written only when the row is created
// ($setOnInsert). On a row already stored they are refreshInboxState's,
// which leaves alone a row Mailsorter changed after the flags were read
// (flagsAt): written here, a star put on during the sync would be taken off
// again by a listing read just before it, until the next sync. So is the mark
// for the rules (rulesDue): a mail the copy already holds is not an arrival.
func inboxUpsert(email models.Email, rulesDue bool) (bson.M, error) {
	raw, err := bson.Marshal(email)
	if err != nil {
		return nil, fmt.Errorf("encode message %s: %w", email.MessageID, err)
	}
	var set bson.M
	if err := bson.Unmarshal(raw, &set); err != nil {
		return nil, fmt.Errorf("encode message %s: %w", email.MessageID, err)
	}
	delete(set, "isRead")
	delete(set, "isStarred")
	onInsert := bson.M{"isRead": email.IsRead, "isStarred": email.IsStarred}
	if rulesDue {
		onInsert["rulesDue"] = true
	}
	return bson.M{
		"$set":         set,
		"$setOnInsert": onInsert,
		// syncedAt tells a sync running at the same time that this row was
		// just written from a fresher listing than its own.
		"$currentDate": bson.M{"syncedAt": true},
	}, nil
}

// errMailboxLost is a sync that lost the connection to the mailbox and could
// not open it again. Its text is what the user is told.
var errMailboxLost = errors.New("la connexion à votre boîte a été perdue pendant la synchronisation")

// errMailboxUnavailable is a sync the mailbox stopped answering part of the
// way: it refused a read ([UNAVAILABLE], [THROTTLED]), or ran out of time.
// Nothing was concluded about any message; the next sync reads it again. Its
// text is what the user is told.
var errMailboxUnavailable = errors.New("votre boîte n'a pas répondu jusqu'au bout de la synchronisation, réessayez dans un instant")

// halvesError is the sync's answer to what imap.ReadInHalves returned: a
// connection that could not be opened again, or a read the server stopped.
// Logged with what it was; what the user reads names neither.
func halvesError(userEmail, what string, err error) error {
	log.Printf("imap sync: %s of %s: %v", what, userEmail, err)
	if errors.Is(err, imap.ErrReadStopped) {
		return errMailboxUnavailable
	}
	return errMailboxLost
}

// phaseReopens bounds how many times one phase of a sync (the favorites
// outside the list, the structures) opens the connection again after a FETCH
// answer it could not parse. Finding one unreadable message among a batch of
// 100 takes 8 (one per halving), so each phase finds at least one per sync,
// and a mailbox where every message is unreadable costs a sync seconds, not
// minutes.
const phaseReopens = 10

// sessionReopener opens the session's connection again, phaseReopens times at
// most for the phase it is made for, then answers imap.ErrReopenLimit.
func (h *Handler) sessionReopener(ctx context.Context, userEmail string, session *mailSession) func() error {
	left := phaseReopens
	return func() error {
		if left == 0 {
			return imap.ErrReopenLimit
		}
		left--
		// The connection the failed FETCH ended: closing it only releases it.
		_ = session.imapc.Close()
		client, err := h.openMailbox(ctx, userEmail)
		if err != nil {
			return err
		}
		session.imapc = client
		return nil
	}
}

// openForPhase opens the session's connection again before a phase reads,
// when the one before left it closed: its last answer could not be parsed
// and its own budget of reopens was spent.
func openForPhase(session *mailSession, reopen func() error) error {
	if !session.imapc.Closed() {
		return nil
	}
	return reopen()
}

// readBatch is how many messages one FETCH of a sync reads, besides the
// listing and the flags: the unit imap.ReadInHalves halves.
const readBatch = 100

// starredPerSync bounds how many starred messages a sync stores that it did
// not list, the most recent first; the next syncs store the rest.
const starredPerSync = 500

// storeStarredOutsideList stores the starred messages of the inbox the copy
// does not hold: older than the page a sync lists, and never in it since
// Mailsorter was connected. Without them the filter "Favoris" would show a
// favorite of years ago only if it had once been among the 100 most recent.
// One whose envelope cannot be parsed even alone is logged and not stored:
// the next sync tries it again, alone once the others are stored, so it costs
// one reopen of this phase's own budget, never the structures'.
func (h *Handler) storeStarredOutsideList(ctx context.Context, userEmail string, session *mailSession, states map[string]imap.State, reopen func() error) error {
	folder := string(mailbox.FolderInbox)
	stored, err := h.storedInboxUIDs(ctx, userEmail)
	if err != nil {
		log.Printf("imap sync: read the inbox copy of %s: %v", userEmail, err)
		return errMirrorNotReconciled
	}
	var missing []string
	for uid, st := range states {
		if st.Starred && !stored[uid] {
			missing = append(missing, uid)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	// The most recent first: a UID grows with each message a folder receives.
	sort.Slice(missing, func(i, j int) bool { return uidLess(missing[j], missing[i]) })
	if len(missing) > starredPerSync {
		missing = missing[:starredPerSync]
	}
	if err := openForPhase(session, reopen); err != nil {
		return halvesError(userEmail, "open the mailbox again to read the starred messages", err)
	}
	got, err := imap.ReadInHalves(missing, readBatch,
		func(uids []string) (map[string]models.Email, error) { return session.imapc.Envelopes(folder, uids) },
		reopen)
	for uid, rErr := range got.Unreadable {
		// Not stored: the next sync tries it again.
		log.Printf("imap sync: starred message uid %s of %s could not be read, not stored: %v", uid, userEmail, rErr)
	}
	if len(got.Left) > 0 {
		log.Printf("imap sync: %d starred messages of %s left for the next sync: %v", len(got.Left), userEmail, got.Stopped)
	}
	failed := false
	for _, email := range got.Read {
		if sErr := h.storeInboxMessage(ctx, userEmail, email, false); sErr != nil {
			log.Printf("imap sync: starred message uid %s of %s not stored: %v", email.MessageID, userEmail, sErr)
			failed = true
		}
	}
	if err != nil {
		return halvesError(userEmail, "read the starred messages", err)
	}
	if failed {
		return errMirrorNotReconciled
	}
	return nil
}

// storedInboxUIDs is the UID of every message the copy of the inbox holds.
func (h *Handler) storedInboxUIDs(ctx context.Context, userEmail string) (map[string]bool, error) {
	cur, err := h.db.Emails().Find(ctx, bson.M{"userId": userEmail, "folder": string(mailbox.FolderInbox)},
		options.Find().SetProjection(bson.M{"messageId": 1}))
	if err != nil {
		return nil, err
	}
	var rows []struct {
		MessageID string `bson:"messageId"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.MessageID] = true
	}
	return out, nil
}

// uidLess orders two UIDs as numbers: "9" before "10".
func uidLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// stateRow is a stored inbox message, as the refresh of its state reads it.
type stateRow struct {
	MessageID    string `bson:"messageId"`
	IsRead       bool   `bson:"isRead"`
	IsStarred    bool   `bson:"isStarred"`
	SizeEstimate int64  `bson:"sizeEstimate"`
}

// flagState is the read state and the star of a message, what one write of
// refreshInboxState sets on many rows at once.
type flagState struct {
	read, starred bool
}

// refreshInboxState carries into the stored copy of the inbox the read state,
// the star and the size every stored message has on the server now (states,
// read at statesAt), the old ones a listing no longer reads included: a mail
// read or starred on the phone shows so after the next sync, whatever its
// age, and "Volumineux" knows the size of every stored mail. A row Mailsorter
// changed after the flags were read (flagsAt, see mirrorFlags) keeps its read
// state and its star: they are newer than the ones read.
func (h *Handler) refreshInboxState(ctx context.Context, userEmail string, states map[string]imap.State, statesAt time.Time) error {
	folder := string(mailbox.FolderInbox)
	cur, err := h.db.Emails().Find(ctx, bson.M{"userId": userEmail, "folder": folder},
		options.Find().SetProjection(bson.M{"messageId": 1, "isRead": 1, "isStarred": 1, "sizeEstimate": 1}))
	if err != nil {
		log.Printf("imap sync: read the state stored for the inbox of %s: %v", userEmail, err)
		return errMirrorNotReconciled
	}
	var rows []stateRow
	if err := cur.All(ctx, &rows); err != nil {
		log.Printf("imap sync: read the state stored for the inbox of %s: %v", userEmail, err)
		return errMirrorNotReconciled
	}
	// One write per state rather than per message: four at most, whatever the
	// size of the inbox. A size is per message, but written once: it never
	// changes for a UID.
	changed := map[flagState][]string{}
	var sizes []mongo.WriteModel
	for _, r := range rows {
		now, ok := states[r.MessageID]
		if !ok {
			continue
		}
		if now.Read != r.IsRead || now.Starred != r.IsStarred {
			key := flagState{read: now.Read, starred: now.Starred}
			changed[key] = append(changed[key], r.MessageID)
		}
		if now.Size > 0 && now.Size != r.SizeEstimate {
			sizes = append(sizes, mongo.NewUpdateOneModel().
				SetFilter(bson.M{"userId": userEmail, "folder": folder, "messageId": r.MessageID}).
				SetUpdate(bson.M{"$set": bson.M{"sizeEstimate": now.Size}}))
		}
	}
	failed := false
	for state, ids := range changed {
		if _, err := h.db.Emails().UpdateMany(ctx,
			bson.M{"userId": userEmail, "folder": folder, "messageId": bson.M{"$in": ids},
				"flagsAt": bson.M{"$not": bson.M{"$gte": statesAt}}},
			bson.M{"$set": bson.M{"isRead": state.read, "isStarred": state.starred}},
		); err != nil {
			log.Printf("imap sync: carry the read state and the star of %d messages of %s into the copy: %v", len(ids), userEmail, err)
			failed = true
		}
	}
	if len(sizes) > 0 {
		if _, err := h.db.Emails().BulkWrite(ctx, sizes, options.BulkWrite().SetOrdered(false)); err != nil {
			log.Printf("imap sync: store the size of %d messages of %s: %v", len(sizes), userEmail, err)
			failed = true
		}
	}
	if failed {
		return errMirrorNotReconciled
	}
	return nil
}

// structurePerSync bounds how many stored inbox messages one sync reads the
// MIME structure of, the most recent first: the ones it just stored, and
// older ones stored before Mailsorter kept it, which the next syncs complete
// rather than one sync holding the user for minutes.
const structurePerSync = 500

// completeInboxStructure reads whether the stored inbox messages carry a file
// (their MIME structure), for the filter "Pièces jointes", which is answered
// from the copy. A row without hasAttachments has not been read yet; once
// read it holds true or false. A message whose structure cannot be parsed
// even alone is stored without an attachment and marked structureUnreadable:
// it is logged, never asked again, and no longer fails the messages next to
// it. A refusal of the server or a time limit concludes nothing: the rest is
// read at the next sync, and this one answers errMailboxUnavailable.
// Only messages the inbox still holds (present): a number it no longer holds
// names nothing.
func (h *Handler) completeInboxStructure(ctx context.Context, userEmail string, session *mailSession, present map[string]bool, reopen func() error) error {
	folder := string(mailbox.FolderInbox)
	cur, err := h.db.Emails().Find(ctx,
		bson.M{"userId": userEmail, "folder": folder, "leftInbox": notLeftInbox, "hasAttachments": bson.M{"$exists": false}},
		options.Find().
			SetProjection(bson.M{"messageId": 1}).
			SetSort(bson.D{{Key: "receivedDate", Value: -1}}).
			SetLimit(structurePerSync))
	if err != nil {
		log.Printf("imap sync: look for the inbox messages of %s whose structure is not read: %v", userEmail, err)
		return errMirrorNotReconciled
	}
	var rows []struct {
		MessageID string `bson:"messageId"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		log.Printf("imap sync: look for the inbox messages of %s whose structure is not read: %v", userEmail, err)
		return errMirrorNotReconciled
	}
	uids := make([]string, 0, len(rows))
	for _, r := range rows {
		if present[r.MessageID] {
			uids = append(uids, r.MessageID)
		}
	}
	if len(uids) == 0 {
		return nil
	}
	if err := openForPhase(session, reopen); err != nil {
		return halvesError(userEmail, "open the mailbox again to read the structures", err)
	}
	got, readErr := imap.ReadInHalves(uids, readBatch,
		func(batch []string) (map[string]bool, error) { return session.imapc.Attachments(folder, batch) },
		reopen)
	writes := make([]mongo.WriteModel, 0, len(got.Read)+len(got.Unreadable))
	for uid, has := range got.Read {
		writes = append(writes, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"userId": userEmail, "folder": folder, "messageId": uid}).
			SetUpdate(bson.M{"$set": bson.M{"hasAttachments": has}}))
	}
	for uid, rErr := range got.Unreadable {
		log.Printf("imap sync: the structure of message uid %s of %s cannot be read, kept without an attachment: %v", uid, userEmail, rErr)
		writes = append(writes, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"userId": userEmail, "folder": folder, "messageId": uid}).
			SetUpdate(bson.M{"$set": bson.M{"hasAttachments": false, "structureUnreadable": true}}))
	}
	if len(got.Left) > 0 {
		log.Printf("imap sync: the structure of %d messages of %s left for the next sync: %v", len(got.Left), userEmail, got.Stopped)
	}
	failed := false
	if len(writes) > 0 {
		if _, err := h.db.Emails().BulkWrite(ctx, writes, options.BulkWrite().SetOrdered(false)); err != nil {
			log.Printf("imap sync: store the attachments of %d messages of %s: %v", len(writes), userEmail, err)
			failed = true
		}
	}
	if readErr != nil {
		return halvesError(userEmail, "read the structures", readErr)
	}
	if failed {
		return errMirrorNotReconciled
	}
	return nil
}

// undoWindow is how long a message Mailsorter moved out of the inbox stays
// in the local copy, remembering where it went, so "Annuler" in the history
// can bring it back. Thirty days, like most providers' trash.
const undoWindow = 30 * 24 * time.Hour

// movedRow is a copied inbox message, as the sync and "Annuler" read it.
type movedRow struct {
	MessageID       string    `bson:"messageId"`
	HeaderMessageID string    `bson:"headerMessageId"`
	LeftInbox       bool      `bson:"leftInbox"`
	LeftAt          time.Time `bson:"leftAt"`
	MovedTo         struct {
		Folder string `bson:"folder"`
		UID    string `bson:"uid"`
	} `bson:"movedTo"`
	// RestoringAt is set while an "Annuler" brings the message back.
	RestoringAt time.Time `bson:"restoringAt"`
}

// moveInFlight is how long a row claimed for a move (flagged, no movedTo
// yet) is left alone by the sync: the move it announces may still be on its
// way to the server. Past it, a claim that never completed (a crash between
// the claim and the move) is judged like any other row.
const moveInFlight = 2 * time.Minute

// errMirrorNotReconciled is a sync that read the mailbox but could not bring
// the stored copy in line with it. Its text is what the user is told.
var errMirrorNotReconciled = errors.New("la liste n'a pas pu être remise à jour avec votre boîte")

// dropStaleInbox brings the local copy of the inbox in line with the server,
// given the UIDs the inbox holds now (present) and when the listing started
// (listedAt), and sets moot the pending AI proposals about what left.
//
//   - A message no longer in the inbox goes, however old it is.
//   - A message Mailsorter moved out (flagged by mirroredIMAP) stays, hidden
//     from the list, for undoWindow, so "Annuler" knows where it went; after
//     that, or when nothing says where it went, it goes. A row claimed for a
//     move still on its way (moveInFlight) is left alone.
//   - A flagged message the inbox still holds is unflagged, but only when it
//     was flagged before the listing started: one flagged after was moved
//     after the server answered, and is not back.
func (h *Handler) dropStaleInbox(ctx context.Context, userEmail string, present map[string]bool, listedAt time.Time) error {
	folder := string(mailbox.FolderInbox)
	cur, err := h.db.Emails().Find(ctx, bson.M{"userId": userEmail, "folder": folder},
		options.Find().SetProjection(bson.M{"messageId": 1, "leftInbox": 1, "leftAt": 1, "movedTo": 1}))
	if err != nil {
		log.Printf("imap sync: read the inbox copy of %s: %v", userEmail, err)
		return errMirrorNotReconciled
	}
	var rows []movedRow
	if err := cur.All(ctx, &rows); err != nil {
		log.Printf("imap sync: read the inbox copy of %s: %v", userEmail, err)
		return errMirrorNotReconciled
	}
	judged := make([]mirror.Row, 0, len(rows))
	for _, r := range rows {
		judged = append(judged, mirror.Row{UID: r.MessageID, Left: r.LeftInbox, LeftAt: r.LeftAt, MovedTo: r.MovedTo.Folder})
	}
	v := mirror.Judge(judged, present, listedAt, time.Now(), mirror.Rules{InFlight: moveInFlight, UndoWindow: undoWindow})
	gone, back, hidden := v.Gone, v.Back, v.Hidden
	var failed bool
	// A message kept hidden for "Annuler" is out of the inbox all the same:
	// a pending AI proposal about it would invite tidying it a second time.
	if len(hidden) > 0 && !h.markProposalsMoot(ctx, userEmail, hidden) {
		failed = true
	}
	if len(back) > 0 {
		if _, err := h.db.Emails().UpdateMany(ctx,
			bson.M{"userId": userEmail, "folder": folder, "messageId": bson.M{"$in": back}, "leftAt": bson.M{"$lt": listedAt}},
			bson.M{"$unset": bson.M{"leftInbox": "", "leftAt": "", "movedTo": ""}},
		); err != nil {
			log.Printf("imap sync: unflag messages still in the inbox of %s: %v", userEmail, err)
			failed = true
		}
	}
	if len(gone) > 0 {
		// Only rows still as they were judged: one claimed for a move since is
		// left to that move, and one another sync stored since this one
		// started listing is left to that sync.
		if _, err := h.db.Emails().DeleteMany(ctx, bson.M{"userId": userEmail, "folder": folder, "messageId": bson.M{"$in": gone},
			"$or": []bson.M{
				{"leftInbox": bson.M{"$ne": true}, "syncedAt": bson.M{"$not": bson.M{"$gte": listedAt}}},
				{"leftInbox": true, "leftAt": bson.M{"$lt": listedAt}},
			}}); err != nil {
			log.Printf("imap sync: drop %d messages that left the inbox of %s: %v", len(gone), userEmail, err)
			failed = true
		} else if !h.markProposalsMoot(ctx, userEmail, gone) {
			failed = true
		}
	}
	if failed {
		return errMirrorNotReconciled
	}
	return nil
}

// proposalMoot is the status of an AI proposal about a mail that left the
// inbox some other way. It is not deleted: a screen loaded before still holds
// its id, and "Tout ranger" sent from there must skip it, neither applied nor
// failed. The plan only lists pending proposals, so it leaves the plan.
const proposalMoot = "moot"

// markProposalsMoot sets the pending proposals about these messages moot,
// and says whether it could.
func (h *Handler) markProposalsMoot(ctx context.Context, userEmail string, messageIDs []string) bool {
	if _, err := h.db.AISuggestions().UpdateMany(ctx,
		bson.M{"userId": userEmail, "status": "pending", "emailId": bson.M{"$in": messageIDs}},
		bson.M{"$set": bson.M{"status": proposalMoot}},
	); err != nil {
		log.Printf("imap sync: set moot the proposals about %d messages of %s: %v", len(messageIDs), userEmail, err)
		return false
	}
	return true
}

// errNotWhereLeft is "Annuler" on an IMAP message that is no longer where
// Mailsorter put it: another mail client moved or deleted it since, or nothing
// recorded where it went. Nothing moved; the user is told.
var errNotWhereLeft = errors.New("api: the message is no longer where Mailsorter put it")

// errAlreadyOut is moving out of the inbox a message Mailsorter already moved
// out. Nothing happened: callers answer success without counting it or
// writing it in the history.
var errAlreadyOut = errors.New("api: the message already left the inbox")

// notWhereLeftMessage is what the user reads for errNotWhereLeft.
const notWhereLeftMessage = "Ce mail n'est plus là où Mailsorter l'avait rangé."

// errLeftInbox is a read or unread change asked on an IMAP message that is no
// longer in the inbox (a screen left open): its number names nothing there,
// and the server would answer OK while changing nothing.
var errLeftInbox = errors.New("api: the message is no longer in the inbox")

// leftInboxMessage is what the user reads for errLeftInbox.
const leftInboxMessage = "Ce mail n'est plus dans votre boîte de réception."

// errAlreadyBack is "Annuler" on a message another "Annuler" brought back a
// moment ago (two tabs, or the button after the action and the history at
// once). Nothing more happened; callers answer success without recording it.
var errAlreadyBack = errors.New("api: the message is already back in the inbox")

// errUndoExpired is "Annuler" on an IMAP move Mailsorter no longer knows the
// destination of: older than undoWindow, or made before it recorded where
// moves land. Nothing moved; the user is told where to look.
var errUndoExpired = errors.New("api: the move is too old to be undone here")

// undoExpiredMessage is what the user reads for errUndoExpired.
const undoExpiredMessage = "Cette action est trop ancienne pour être annulée ici : retrouvez ce mail dans l'archive ou la corbeille de votre messagerie."

// mirroredIMAP is the IMAP mailbox as handlers mutate it, keeping the local
// copy of the inbox (what the list is served from) right.
//
//   - Moving a message out of the inbox (archive, trash) flags its row
//     (leftInbox) with where it landed (movedTo) and when: the list and the
//     counts skip it at once, the history entry written next still reads its
//     sender and subject, and "Annuler" knows where to fetch it.
//   - Moving it back (unarchive, untrash: what "Annuler" sends, with the
//     number the message had in the inbox) fetches it from there, and the row
//     takes the new number the inbox gave it, so it is listed again at once.
//   - Anything else (read, unread, star, unstar) is a flag: it goes to the
//     server, then into the row, so the list and its filters show it at once.
type mirroredIMAP struct {
	client    *imap.Client
	h         *Handler
	userEmail string
}

func (m mirroredIMAP) Apply(ctx context.Context, ref mailbox.Ref, mut mailbox.Mutation) error {
	op, err := mailbox.IMAPOpFor(mut)
	if err != nil {
		return err
	}
	inbox := string(mailbox.FolderInbox)
	if op.Kind != mailbox.IMAPMove {
		if ref.Folder == inbox {
			// A STORE on a number the inbox no longer holds answers OK and
			// changes nothing: say so instead.
			there, err := m.client.Has(ref)
			if err != nil {
				return err
			}
			if !there {
				return errLeftInbox
			}
		}
		if err := m.client.Apply(ctx, ref, mut); err != nil {
			return err
		}
		m.mirrorFlags(ctx, ref, op)
		return nil
	}
	var row movedRow
	findErr := m.h.db.Emails().FindOne(ctx, bson.M{"userId": m.userEmail, "folder": inbox, "messageId": ref.ID}).Decode(&row)
	if findErr != nil && !errors.Is(findErr, mongo.ErrNoDocuments) {
		// Not "unknown message": the store could not answer. Moving on would
		// lose where the message goes, or wrongly tell "Annuler" it is gone.
		return fmt.Errorf("read the stored copy of message %s: %w", ref.ID, findErr)
	}
	if op.To == mailbox.FolderInbox {
		return m.putBack(ctx, ref, row, findErr)
	}
	if findErr == nil && row.LeftInbox {
		return m.moveAgain(ctx, ref, row, op.To)
	}
	if ref.Folder != inbox || findErr != nil {
		// Not a stored inbox message: nothing to keep right.
		_, err := m.client.MoveTo(ctx, ref, op.To, row.HeaderMessageID)
		if errors.Is(err, imap.ErrMessageGone) {
			return errAlreadyOut
		}
		return err
	}

	// Claim the row before the move: of two requests moving the same message
	// at once (a second key press, a second tab), only the one whose claim
	// lands moves it; the other is told it already left. The claim also hides
	// it from the list at once, and tells a sync running meanwhile that a
	// move is on its way.
	filter := bson.M{"userId": m.userEmail, "folder": inbox, "messageId": ref.ID}
	claim := bson.M{"userId": m.userEmail, "folder": inbox, "messageId": ref.ID, "leftInbox": notLeftInbox}
	res, err := m.h.db.Emails().UpdateOne(ctx, claim, bson.M{
		"$set":   bson.M{"leftInbox": true, "leftAt": time.Now()},
		"$unset": bson.M{"movedTo": ""},
	})
	if err != nil {
		return fmt.Errorf("claim message %s for a move: %w", ref.ID, err)
	}
	if res.MatchedCount == 0 {
		return errAlreadyOut
	}
	landed, err := m.client.MoveTo(ctx, ref, op.To, row.HeaderMessageID)
	if errors.Is(err, imap.ErrMessageGone) {
		// Another mail client took it out of the inbox first: nothing was
		// done here. The row stays out of the list; with no destination, the
		// next sync drops it.
		return errAlreadyOut
	}
	if err != nil {
		// Not moved: release the claim, so it is listed again.
		if _, uErr := m.h.db.Emails().UpdateOne(ctx, filter, bson.M{"$unset": bson.M{"leftInbox": "", "leftAt": "", "movedTo": ""}}); uErr != nil {
			log.Printf("imap: message %s of %s not moved, and its claim not released: %v", ref.ID, m.userEmail, uErr)
		}
		return err
	}
	if _, err := m.h.db.Emails().UpdateOne(ctx, filter,
		bson.M{"$set": bson.M{"movedTo": bson.M{"folder": landed.Folder, "uid": landed.ID}}},
	); err != nil {
		log.Printf("imap: message %s of %s moved to %s, where it went is not recorded: %v", ref.ID, m.userEmail, landed.Folder, err)
	}
	return nil
}

// mirrorFlags carries a change of the read state or of the star into the
// stored copy the list is served from, so the list and its filters show it at
// once rather than at the next sync. flagsAt tells a sync that read the flags
// before this change not to put the old ones back (refreshInboxState). The
// server already changed; a failure here is only logged, and the next sync
// carries the change.
func (m mirroredIMAP) mirrorFlags(ctx context.Context, ref mailbox.Ref, op mailbox.IMAPOp) {
	if ref.Folder != string(mailbox.FolderInbox) {
		return
	}
	set := bson.M{}
	for _, change := range []struct {
		flags []string
		to    bool
	}{{op.AddFlags, true}, {op.DropFlags, false}} {
		for _, f := range change.flags {
			switch f {
			case mailbox.FlagSeen:
				set["isRead"] = change.to
			case mailbox.FlagFlagged:
				set["isStarred"] = change.to
			}
		}
	}
	if len(set) == 0 {
		return
	}
	set["flagsAt"] = time.Now()
	if _, err := m.h.db.Emails().UpdateOne(ctx,
		bson.M{"userId": m.userEmail, "folder": ref.Folder, "messageId": ref.ID},
		bson.M{"$set": set},
	); err != nil {
		log.Printf("imap: message %s of %s changed on the server (%v), not in its copy: %v", ref.ID, m.userEmail, set, err)
	}
}

// moveAgain is moving out of the inbox a message Mailsorter already moved out
// (a click from a screen left open). Toward where it already is, or when the
// move that took it out is still on its way or was done by another mail
// client (no destination recorded), nothing happens (errAlreadyOut: neither
// counted nor recorded). Toward elsewhere, a "Supprimer" on a mail already
// archived for instance, it moves from where it is, and the row records the
// new place, so "Annuler" still finds it.
func (m mirroredIMAP) moveAgain(ctx context.Context, ref mailbox.Ref, row movedRow, to mailbox.Folder) error {
	dest, ok := m.client.FolderName(to)
	if !ok {
		return fmt.Errorf("%w: %s", imap.ErrNoSuchFolder, to)
	}
	if row.MovedTo.Folder == "" || row.MovedTo.Folder == dest {
		return errAlreadyOut
	}
	uid := row.MovedTo.UID
	if uid == "" && row.HeaderMessageID != "" {
		found, err := m.client.FindByMessageID(row.MovedTo.Folder, row.HeaderMessageID)
		if err != nil {
			return err
		}
		uid = found
	}
	if uid == "" {
		m.forgetDestination(ctx, ref, row)
		return errNotWhereLeft
	}
	landed, err := m.moveFrom(ctx, row.MovedTo.Folder, uid, to, row.HeaderMessageID)
	if errors.Is(err, imap.ErrMessageGone) {
		// A second click may have moved it there already.
		if m.recordedIn(ctx, ref, dest) {
			return errAlreadyOut
		}
		// Otherwise another mail client moved it.
		m.forgetDestination(ctx, ref, row)
		return errNotWhereLeft
	}
	if err != nil {
		return err
	}
	// Only over what was read: of two requests moving it on at once, the one
	// that landed second does not overwrite where the first put it.
	if res, err := m.h.db.Emails().UpdateOne(ctx,
		bson.M{"userId": m.userEmail, "folder": string(mailbox.FolderInbox), "messageId": ref.ID,
			"movedTo.folder": row.MovedTo.Folder, "movedTo.uid": row.MovedTo.UID},
		bson.M{"$set": bson.M{"leftAt": time.Now(), "movedTo": bson.M{"folder": landed.Folder, "uid": landed.ID}}},
	); err != nil {
		log.Printf("imap: message %s of %s moved to %s, copy not updated: %v", ref.ID, m.userEmail, landed.Folder, err)
	} else if res.MatchedCount == 0 {
		return errAlreadyOut
	}
	return nil
}

// recordedIn says whether the stored copy says the message is now in folder.
func (m mirroredIMAP) recordedIn(ctx context.Context, ref mailbox.Ref, folder string) bool {
	var now movedRow
	err := m.h.db.Emails().FindOne(ctx, bson.M{"userId": m.userEmail, "folder": string(mailbox.FolderInbox), "messageId": ref.ID}).Decode(&now)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		log.Printf("imap: read the copy of message %s of %s again: %v", ref.ID, m.userEmail, err)
	}
	return err == nil && now.MovedTo.Folder == folder
}

// moveFrom moves the message Mailsorter recorded as uid in folder, checked
// against its Message-ID (imap.Client.MoveRecorded).
func (m mirroredIMAP) moveFrom(ctx context.Context, folder, uid string, to mailbox.Folder, headerMessageID string) (mailbox.Ref, error) {
	return m.client.MoveRecorded(ctx, folder, uid, to, headerMessageID)
}

// forgetDestination drops where Mailsorter had put a message, once it is
// found not to be there any more (another mail client moved it): the history
// stops offering "Annuler", and the next sync drops the row. Only over what
// was read: a destination another request recorded meanwhile stays.
func (m mirroredIMAP) forgetDestination(ctx context.Context, ref mailbox.Ref, row movedRow) {
	if _, err := m.h.db.Emails().UpdateOne(ctx,
		bson.M{"userId": m.userEmail, "folder": string(mailbox.FolderInbox), "messageId": ref.ID,
			"movedTo.folder": row.MovedTo.Folder, "movedTo.uid": row.MovedTo.UID},
		bson.M{"$unset": bson.M{"movedTo": "", "restoringAt": ""}},
	); err != nil {
		log.Printf("imap: message %s of %s is not where it was put, and that is not recorded: %v", ref.ID, m.userEmail, err)
	}
}

// putBack is "Annuler" for a message Mailsorter moved out of the inbox.
func (m mirroredIMAP) putBack(ctx context.Context, ref mailbox.Ref, row movedRow, findErr error) error {
	return m.putBackOnce(ctx, ref, row, findErr, false)
}

// errUndoBusy is "Annuler" on a message another "Annuler" still holds, longer
// than this one waits. The user is told to try again.
var errUndoBusy = errors.New("api: another undo of this message is running")

// undoBusyMessage is what the user reads for errUndoBusy.
const undoBusyMessage = "Une autre annulation de ce mail est en cours, réessayez dans un instant."

// errRetryReturn is another "Annuler" that ended without bringing the
// message back: this one may try itself.
var errRetryReturn = errors.New("api: the other return did not complete")

// awaitOtherReturn waits, a few seconds at most, for the "Annuler" that holds
// the message: errAlreadyBack once it is back, errRetryReturn if that one
// gave up.
func (m mirroredIMAP) awaitOtherReturn(ctx context.Context, ref mailbox.Ref) error {
	inbox := string(mailbox.FolderInbox)
	for i := 0; i < 24; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
		n, err := m.h.db.Emails().CountDocuments(ctx, bson.M{"userId": m.userEmail, "folder": inbox, "restoredFrom": ref.ID})
		if err != nil {
			return fmt.Errorf("look for message %s put back: %w", ref.ID, err)
		}
		if n > 0 {
			return errAlreadyBack
		}
		var r movedRow
		err = m.h.db.Emails().FindOne(ctx, bson.M{"userId": m.userEmail, "folder": inbox, "messageId": ref.ID}).Decode(&r)
		if errors.Is(err, mongo.ErrNoDocuments) {
			// Dropped by a sync: only a mark of the other "Annuler" says it
			// brought the message back, and there is none.
			return errNotWhereLeft
		}
		if err != nil {
			return fmt.Errorf("read the stored copy of message %s: %w", ref.ID, err)
		}
		if r.RestoringAt.IsZero() {
			return errRetryReturn
		}
	}
	return errUndoBusy
}

// retryPutBack runs putBack once more, on the row as it is now.
func (m mirroredIMAP) retryPutBack(ctx context.Context, ref mailbox.Ref) error {
	var row movedRow
	findErr := m.h.db.Emails().FindOne(ctx, bson.M{"userId": m.userEmail, "folder": string(mailbox.FolderInbox), "messageId": ref.ID}).Decode(&row)
	if findErr != nil && !errors.Is(findErr, mongo.ErrNoDocuments) {
		return fmt.Errorf("read the stored copy of message %s: %w", ref.ID, findErr)
	}
	return m.putBackOnce(ctx, ref, row, findErr, true)
}

func (m mirroredIMAP) putBackOnce(ctx context.Context, ref mailbox.Ref, row movedRow, findErr error, retried bool) error {
	inbox := string(mailbox.FolderInbox)
	if errors.Is(findErr, mongo.ErrNoDocuments) {
		// Another "Annuler" may have brought it back a moment ago: the row
		// then took its new number, and remembers the old one.
		n, err := m.h.db.Emails().CountDocuments(ctx, bson.M{"userId": m.userEmail, "folder": inbox, "restoredFrom": ref.ID})
		if err != nil {
			return fmt.Errorf("look for message %s put back: %w", ref.ID, err)
		}
		if n > 0 {
			return errAlreadyBack
		}
		// Past the undo window the sync dropped what it knew of the move, and
		// a move made before Mailsorter kept that is not known at all.
		return errUndoExpired
	}
	if !row.LeftInbox {
		// Stored as in the inbox: say so only if the server agrees, rather
		// than announce a return that did not happen.
		there, err := m.client.Has(ref)
		if err != nil {
			return err
		}
		if !there {
			return errNotWhereLeft
		}
		return nil
	}
	if time.Since(row.LeftAt) > undoWindow {
		// The same window the history offers "Annuler" in, whether or not a
		// sync has dropped the move yet.
		return errUndoExpired
	}
	if row.MovedTo.Folder == "" {
		return errNotWhereLeft
	}
	uid := row.MovedTo.UID
	if uid == "" && row.HeaderMessageID != "" {
		// The server did not say where it landed when it left: look for it
		// there by its Message-ID header now. A failed lookup is not "gone".
		found, err := m.client.FindByMessageID(row.MovedTo.Folder, row.HeaderMessageID)
		if err != nil {
			return err
		}
		uid = found
	}
	if uid == "" {
		m.forgetDestination(ctx, ref, row)
		return errNotWhereLeft
	}

	// Claim the return, as a move out claims the row: of two "Annuler" at
	// once (two tabs, or the button after the action and the history), only
	// one moves the message; the other is told it is already back. A claim
	// left by a crash expires.
	filter := bson.M{"userId": m.userEmail, "folder": inbox, "messageId": ref.ID}
	now := time.Now()
	res, err := m.h.db.Emails().UpdateOne(ctx, bson.M{
		"userId": m.userEmail, "folder": inbox, "messageId": ref.ID, "leftInbox": true,
		"$or": []bson.M{{"restoringAt": bson.M{"$exists": false}}, {"restoringAt": bson.M{"$lt": now.Add(-moveInFlight)}}},
	}, bson.M{"$set": bson.M{"restoringAt": now}})
	if err != nil {
		return fmt.Errorf("claim message %s for its return: %w", ref.ID, err)
	}
	if res.MatchedCount == 0 {
		// Another "Annuler" holds it: answer what that one achieves.
		err := m.awaitOtherReturn(ctx, ref)
		if errors.Is(err, errRetryReturn) {
			if retried {
				return errUndoBusy
			}
			return m.retryPutBack(ctx, ref)
		}
		return err
	}
	release := func() {
		if _, err := m.h.db.Emails().UpdateOne(ctx, filter, bson.M{"$unset": bson.M{"restoringAt": ""}}); err != nil {
			log.Printf("imap: message %s of %s not put back, and its claim not released: %v", ref.ID, m.userEmail, err)
		}
	}

	landed, err := m.moveFrom(ctx, row.MovedTo.Folder, uid, mailbox.FolderInbox, row.HeaderMessageID)
	if errors.Is(err, imap.ErrMessageGone) {
		// Not where Mailsorter put it: another mail client moved it, back to
		// the inbox or elsewhere. Mailsorter did not bring it back, so it
		// does not say so, and it forgets where it had put it.
		m.forgetDestination(ctx, ref, row)
		return errNotWhereLeft
	}
	if err != nil {
		release()
		return err
	}
	if landed.ID == "" {
		// Back on the server, number unknown: the next sync lists it under
		// its new number and drops this row (no destination left). Until
		// then it stays hidden, and says it was put back, for a second
		// "Annuler" sent at the same time.
		if _, err := m.h.db.Emails().UpdateOne(ctx, filter, bson.M{
			"$set":   bson.M{"restoredFrom": ref.ID},
			"$unset": bson.M{"movedTo": "", "restoringAt": ""},
		}); err != nil {
			log.Printf("imap: message put back in the inbox of %s, copy not updated: %v", m.userEmail, err)
		}
		return nil
	}
	if _, err := m.h.db.Emails().UpdateOne(ctx, filter, bson.M{
		// restoredFrom: the number "Annuler" still names it by, for a second
		// "Annuler" sent at the same time.
		"$set":   bson.M{"messageId": landed.ID, "restoredFrom": ref.ID},
		"$unset": bson.M{"leftInbox": "", "leftAt": "", "movedTo": "", "restoringAt": ""},
		// Written now, like a row a sync stores: a sync whose UID search ran
		// before the return must not judge it gone.
		"$currentDate": bson.M{"syncedAt": true},
	}); err != nil {
		log.Printf("imap: message put back in the inbox of %s, copy not updated: %v", m.userEmail, err)
	}
	return nil
}

// errMessagesNotStored is a sync that read the mailbox but could not mirror all
// of it. Its text is what the user is told, so it names no datastore detail.
var errMessagesNotStored = errors.New("des messages de la boîte n'ont pas pu être enregistrés")

// identityIn resolves a message's sender and subject for the ledger, through
// whichever transport the caller is on.
//
// The Gmail path may fall back to a metadata call when the local mirror does
// not know the message. The IMAP path cannot and must not: there is no client
// to ask (passing a nil one would panic), and the listing stored the identity
// moments ago anyway. An unknown message yields an empty identity, which the
// ledger renders as "sender unknown" rather than inventing one.
func (h *Handler) identityIn(ctx context.Context, session *mailSession, userEmail, messageID string) models.Email {
	if session.Transport == provider.TransportIMAP {
		return h.emailIdentities(ctx, userEmail, []string{messageID})[messageID]
	}
	return h.emailIdentity(ctx, session.gmail, userEmail, messageID)
}
