package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mirror"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"go.mongodb.org/mongo-driver/bson"
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
// mirror it into Mongo.
//
// It does NOT apply rules. The rule engine mutates through a Gmail client and
// has not been ported, so running it here would either do nothing or do it to
// the wrong mailbox. Returning a rulesApplied of zero is the honest answer, and
// the sync still does the thing the user is waiting for.
func (h *Handler) syncInboxIMAP(ctx context.Context, userEmail string, session *mailSession) (synced, total int, err error) {
	const folder = string(mailbox.FolderInbox)

	emails, err := session.imapc.List(ctx, folder, imapListLimit)
	if err != nil {
		return 0, 0, err
	}
	// Messages that left the inbox since the last sync (moved by Mailsorter,
	// by the user's phone, by a filter on the server) must leave the copy the
	// list is served from, or they show forever.
	h.dropStaleInbox(ctx, userEmail, emails, len(emails) < imapListLimit)

	// A message that cannot be stored is not on the user's screen, and nothing
	// else will ever tell them it exists: the listing is served from this
	// mirror. So each failure is logged with what it was, the others are still
	// stored, and the sync answers an error rather than a success.
	failed := 0
	for _, email := range emails {
		email.UserID = userEmail
		// The folder is stored with the message because on IMAP it is half of
		// its identity: without it the UID below names nothing in particular.
		email.Folder = folder

		if _, uErr := h.db.Emails().UpdateOne(ctx,
			bson.M{"messageId": email.MessageID, "userId": userEmail},
			bson.M{"$set": email},
			options.Update().SetUpsert(true),
		); uErr != nil {
			failed++
			log.Printf("imap sync: message uid %s in %s not stored: %v", email.MessageID, folder, uErr)
			continue
		}
		synced++
	}
	if failed > 0 {
		return synced, len(emails), fmt.Errorf("%w (%d sur %d)", errMessagesNotStored, failed, len(emails))
	}
	return synced, len(emails), nil
}

// imapListLimit is how many of the most recent inbox messages a sync reads.
const imapListLimit = 100

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
}

// dropStaleInbox brings the local copy of the inbox in line with a listing
// that just came back, and drops the pending AI proposals about what it
// removes (they would point at a message that is not there).
//
//   - A message the listing no longer shows goes (mirror.StaleUIDs; complete
//     says whether the listing covered the whole inbox).
//   - A message Mailsorter moved out (flagged by mirroredIMAP) stays, hidden
//     from the list, for undoWindow, so "Annuler" knows where it went; after
//     that, or when nothing says where it went, it goes. One the listing
//     still shows is simply unflagged.
//
// Best effort: a failure is logged and the list stays as it was.
func (h *Handler) dropStaleInbox(ctx context.Context, userEmail string, listed []models.Email, complete bool) {
	folder := string(mailbox.FolderInbox)
	cur, err := h.db.Emails().Find(ctx, bson.M{"userId": userEmail, "folder": folder},
		options.Find().SetProjection(bson.M{"messageId": 1, "leftInbox": 1, "leftAt": 1, "movedTo": 1}))
	if err != nil {
		log.Printf("imap sync: read the inbox copy of %s: %v", userEmail, err)
		return
	}
	var rows []movedRow
	if err := cur.All(ctx, &rows); err != nil {
		log.Printf("imap sync: read the inbox copy of %s: %v", userEmail, err)
		return
	}
	seen := make([]string, 0, len(listed))
	shown := make(map[string]bool, len(listed))
	for _, e := range listed {
		seen = append(seen, e.MessageID)
		shown[e.MessageID] = true
	}
	stored := make([]string, 0, len(rows))
	var gone, back, hidden []string
	now := time.Now()
	for _, r := range rows {
		if !r.LeftInbox {
			stored = append(stored, r.MessageID)
			continue
		}
		switch {
		case shown[r.MessageID]:
			back = append(back, r.MessageID)
		case r.MovedTo.Folder == "" || now.Sub(r.LeftAt) > undoWindow:
			gone = append(gone, r.MessageID)
		default:
			hidden = append(hidden, r.MessageID)
		}
	}
	// A message kept hidden for "Annuler" is out of the inbox all the same:
	// a pending AI proposal about it would invite tidying it a second time.
	if len(hidden) > 0 {
		if _, err := h.db.AISuggestions().DeleteMany(ctx, bson.M{"userId": userEmail, "status": "pending", "emailId": bson.M{"$in": hidden}}); err != nil {
			log.Printf("imap sync: drop proposals about messages moved out of the inbox of %s: %v", userEmail, err)
		}
	}
	gone = append(gone, mirror.StaleUIDs(stored, seen, complete)...)
	if len(back) > 0 {
		if _, err := h.db.Emails().UpdateMany(ctx,
			bson.M{"userId": userEmail, "folder": folder, "messageId": bson.M{"$in": back}},
			bson.M{"$unset": bson.M{"leftInbox": "", "leftAt": "", "movedTo": ""}},
		); err != nil {
			log.Printf("imap sync: unflag messages still in the inbox of %s: %v", userEmail, err)
		}
	}
	if len(gone) == 0 {
		return
	}
	if _, err := h.db.Emails().DeleteMany(ctx, bson.M{"userId": userEmail, "folder": folder, "messageId": bson.M{"$in": gone}}); err != nil {
		log.Printf("imap sync: drop %d messages that left the inbox of %s: %v", len(gone), userEmail, err)
		return
	}
	if _, err := h.db.AISuggestions().DeleteMany(ctx, bson.M{"userId": userEmail, "status": "pending", "emailId": bson.M{"$in": gone}}); err != nil {
		log.Printf("imap sync: drop proposals about messages that left the inbox of %s: %v", userEmail, err)
	}
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
//   - Anything else (flags) goes straight to the server.
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
	if op.Kind != mailbox.IMAPMove {
		return m.client.Apply(ctx, ref, mut)
	}
	inbox := string(mailbox.FolderInbox)
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
		// Already moved out by Mailsorter (a second click from a stale tab, a
		// proposal still pending): nothing to do, and above all nothing to
		// overwrite, or "Annuler" would forget where the message is. Said so,
		// so the caller neither counts nor records an action nothing did.
		return errAlreadyOut
	}

	landed, err := m.client.MoveTo(ctx, ref, op.To, row.HeaderMessageID)
	if errors.Is(err, imap.ErrMessageGone) {
		// Already out of the inbox: what the user asked for is true.
		landed, err = mailbox.Ref{}, nil
	}
	if err != nil {
		return err
	}
	if ref.Folder == inbox && findErr == nil {
		if _, err := m.h.db.Emails().UpdateOne(ctx,
			bson.M{"userId": m.userEmail, "folder": inbox, "messageId": ref.ID},
			bson.M{"$set": bson.M{
				"leftInbox": true,
				"leftAt":    time.Now(),
				"movedTo":   bson.M{"folder": landed.Folder, "uid": landed.ID},
			}},
		); err != nil {
			log.Printf("imap: message %s left the inbox of %s but stays in its copy: %v", ref.ID, m.userEmail, err)
		}
	}
	return nil
}

// putBack is "Annuler" for a message Mailsorter moved out of the inbox.
func (m mirroredIMAP) putBack(ctx context.Context, ref mailbox.Ref, row movedRow, findErr error) error {
	if findErr == nil && !row.LeftInbox {
		return nil // still in the inbox: nothing to bring back
	}
	if findErr != nil || row.MovedTo.Folder == "" {
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
		return errNotWhereLeft
	}
	from := mailbox.InFolder(row.MovedTo.Folder, uid)
	landed, err := m.client.MoveTo(ctx, from, mailbox.FolderInbox, row.HeaderMessageID)
	if errors.Is(err, imap.ErrMessageGone) {
		return errNotWhereLeft
	}
	if err != nil {
		return err
	}
	inbox := string(mailbox.FolderInbox)
	filter := bson.M{"userId": m.userEmail, "folder": inbox, "messageId": ref.ID}
	if landed.ID == "" {
		// Back on the server, number unknown: the next sync lists it.
		if _, err := m.h.db.Emails().DeleteOne(ctx, filter); err != nil {
			log.Printf("imap: message put back in the inbox of %s, stale copy kept: %v", m.userEmail, err)
		}
		return nil
	}
	if _, err := m.h.db.Emails().UpdateOne(ctx, filter, bson.M{
		"$set":   bson.M{"messageId": landed.ID},
		"$unset": bson.M{"leftInbox": "", "leftAt": "", "movedTo": ""},
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
