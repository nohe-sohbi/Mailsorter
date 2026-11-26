package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"github.com/nohe-sohbi/mailsorter/backend/internal/rules"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// loadRules returns the user's rules sorted by priority (then creation), so the
// first match in slice order is always the highest-priority one.
func (h *Handler) loadRules(ctx context.Context, userEmail string) ([]models.SortingRule, error) {
	cursor, err := h.db.SortingRules().Find(ctx, bson.M{"userId": userEmail},
		options.Find().SetSort(bson.D{{Key: "priority", Value: 1}, {Key: "createdAt", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	out := make([]models.SortingRule, 0)
	if err := cursor.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// enabledRules returns the caller's enabled rules, sorted by priority (loadRules
// already sorts). Shared by the manual apply, the dry-run preview, and the
// at-sync autopilot so all three see exactly the same ruleset.
func (h *Handler) enabledRules(ctx context.Context, userEmail string) []models.SortingRule {
	ruleset, err := h.loadRules(ctx, userEmail)
	if err != nil {
		return nil
	}
	enabled := make([]models.SortingRule, 0, len(ruleset))
	for _, ru := range ruleset {
		if ru.Enabled {
			enabled = append(enabled, ru)
		}
	}
	return enabled
}

// GetRules lists the caller's deterministic sorting rules.
func (h *Handler) GetRules(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	ruleset, err := h.loadRules(ctx, userEmail)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load rules")
		return
	}
	for i := range ruleset {
		ruleset[i].NameShown = shownRuleName(ruleset[i].Name)
		for j := range ruleset[i].Conditions {
			ruleset[i].Conditions[j].ValueShown = shownConditionValue(ruleset[i].Conditions[j].Value)
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"rules": ruleset})
}

// senderRulePrefix opens the name of a rule made from a sender
// (ruleForSender), which its address follows.
const senderRulePrefix = "Expéditeur : "

// ruleHitsView is a rule's count in the preview, with its name as the screen
// shows it (shownRuleName).
type ruleHitsView struct {
	rules.RuleHits
	RuleNameShown string `json:"ruleNameShown"`
}

// shownHits is the preview's counts by rule, each name in its shown form.
func shownHits(hits []rules.RuleHits) []ruleHitsView {
	out := make([]ruleHitsView, 0, len(hits))
	for _, h := range hits {
		out = append(out, ruleHitsView{RuleHits: h, RuleNameShown: shownRuleName(h.RuleName)})
	}
	return out
}

// shownRuleName is a rule's name as the screen shows it: a sender rule's
// address (the first word after senderRulePrefix) in the one form addresses
// are shown in (a rule saved before names were written so holds it as
// written), and everything else (what follows it, " (copie)" or a word the
// user typed; any other name) with what would hide or reorder made visible.
// The address alone goes to ShownAddress: with what follows it, it made one
// domain of "café.fr (copie)", which IDNA refuses.
func shownRuleName(name string) string {
	rest, ok := strings.CutPrefix(name, senderRulePrefix)
	if !ok {
		return protect.ShownText(name)
	}
	address, tail := rest, ""
	if i := strings.IndexFunc(rest, unicode.IsSpace); i >= 0 {
		address, tail = rest[:i], rest[i:]
	}
	if !strings.Contains(address, "@") {
		return protect.ShownText(name)
	}
	return senderRulePrefix + protect.ShownAddress(address) + protect.ShownText(tail)
}

// shownConditionValue is a rule's condition value as the screen shows it: an
// address (a sender rule's, written by the sender) in the one form addresses
// are shown in, any other text with what would hide or reorder made visible.
func shownConditionValue(value string) string {
	if strings.Contains(value, "@") {
		return protect.ShownAddress(value)
	}
	return protect.ShownText(value)
}

// CreateRule validates and persists a new rule.
func (h *Handler) CreateRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var in models.SortingRuleInput
	if !decodeJSON(w, r, &in) {
		return
	}

	rule := ruleFromInput(userEmail, in)
	if err := rules.Validate(rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	rule.CreatedAt = time.Now()
	rule.UpdatedAt = rule.CreatedAt

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	res, err := h.db.SortingRules().InsertOne(ctx, rule)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Impossible d'enregistrer la règle.")
		return
	}
	if oid, ok := res.InsertedID.(primitive.ObjectID); ok {
		rule.ID = oid.Hex()
	}

	writeJSON(w, http.StatusCreated, rule)
}

// UpdateRule replaces an existing rule's editable fields after validation.
func (h *Handler) UpdateRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	oid, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid rule ID")
		return
	}

	var in models.SortingRuleInput
	if !decodeJSON(w, r, &in) {
		return
	}

	rule := ruleFromInput(userEmail, in)
	if err := rules.Validate(rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	res, err := h.db.SortingRules().UpdateOne(ctx,
		bson.M{"_id": oid, "userId": userEmail},
		bson.M{"$set": bson.M{
			"name":       rule.Name,
			"enabled":    rule.Enabled,
			"matchAll":   rule.MatchAll,
			"conditions": rule.Conditions,
			"action":     rule.Action,
			"labelName":  rule.LabelName,
			"actions":    rule.Actions,
			"priority":   rule.Priority,
			"updatedAt":  time.Now(),
		}},
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to update rule")
		return
	}
	if res.MatchedCount == 0 {
		writeError(w, http.StatusNotFound, "Rule not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated"})
}

// DeleteRule removes a rule the caller owns.
func (h *Handler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	oid, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid rule ID")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	res, err := h.db.SortingRules().DeleteOne(ctx, bson.M{"_id": oid, "userId": userEmail})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete rule")
		return
	}
	if res.DeletedCount == 0 {
		writeError(w, http.StatusNotFound, "Rule not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// ApplyRules runs every enabled rule across the current inbox. Each email is
// matched against the rules in priority order and the first match's actions
// are applied to the mailbox, on either transport (see ruleCandidates for
// which messages). This never calls the AI and never consumes quota: it is
// the free, deterministic counterpart to the AI triage.
func (h *Handler) ApplyRules(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	enabled := h.enabledRules(ctx, userEmail)
	if len(enabled) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{"applied": 0, "scanned": 0, "byRule": map[string]int{}})
		return
	}

	session, err := h.openSession(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	defer session.Close()

	// The shield first: a protected list that cannot be read runs no rule
	// and holds no mail (503).
	protectedList, err := h.protectedValues(ctx, userEmail)
	if err != nil {
		writeProtectedUnreadable(w, userEmail, err)
		return
	}

	// Over IMAP the run holds the mails it judges (rulesClaim), as a sync
	// does: a mail a sync is judging right now is left to it, and a sync
	// leaves alone the ones held here.
	emails, already, hold, err := h.ruleCandidates(ctx, session, userEmail, enabled, true)
	if err != nil {
		writeCandidatesError(w, err)
		return
	}
	if hold != "" {
		defer h.releaseHold(ctx, userEmail, hold)
	}

	done := h.runRules(ctx, session, userEmail, emails, enabled, protectedList, already)
	if session.Transport == provider.TransportIMAP {
		// Judged here, a mail marked for the rules at a sync is not judged
		// again there (a second star, a second line in the history); one
		// whose rule failed half-way keeps what was done for the next try.
		h.settleMarks(ctx, userEmail, session.generation(), emails, done)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"applied":          done.applied,
		"scanned":          len(emails),
		"byRule":           done.byRule,
		"protectedSkipped": done.protectedSkipped,
	})
}

// previewSampleCap bounds how many example emails the dry-run returns.
const previewSampleCap = 12

// PreviewRules is a DRY RUN: it reports which emails each rule WOULD act on,
// without touching the mailbox and without consuming quota. This lets users
// see the blast radius of their ruleset (especially before enabling at-sync
// autopilot) and build trust before any irreversible archive/trash happens.
// It reads the same messages as ApplyRules (ruleCandidates). Over IMAP it
// opens the mailbox only when a rule reads the text: the rest is in the copy.
// There it counts the mails a rule would file in a folder, and shows what
// applying the rule does (rules.PreviewOnFolders: the flags, then one move).
func (h *Handler) PreviewRules(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	enabled := h.enabledRules(ctx, userEmail)
	if len(enabled) == 0 {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"scanned": 0, "willApply": 0, "byRule": []rules.RuleHits{}, "samples": []rules.PreviewItem{},
		})
		return
	}

	transport, account, err := h.transportFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var emails []models.Email
	if transport == provider.TransportIMAP && !rules.ReadsText(enabled) {
		emails, _, err = h.storedInboxCandidates(ctx, userEmail, generationOf(transport, account), false)
	} else {
		session, sErr := h.openSession(ctx, userEmail)
		if sErr != nil {
			writeAuthError(w, sErr)
			return
		}
		defer session.Close()
		emails, _, _, err = h.ruleCandidates(ctx, session, userEmail, enabled, false)
	}
	if err != nil {
		writeCandidatesError(w, err)
		return
	}

	var items []rules.PreviewItem
	var hits []rules.RuleHits
	if transport == provider.TransportIMAP {
		items, hits = rules.PreviewOnFolders(emails, enabled)
	} else {
		items, hits = rules.Preview(emails, enabled)
	}

	shown := items
	if len(shown) > previewSampleCap {
		shown = shown[:previewSampleCap]
	}
	// What the preview shows for each sender is what every screen shows
	// (protect.Label), never the From as written.
	// SenderLabelIsAddress says the label is the sender's address, which the
	// screen lays out so its domain is never cut.
	type previewSample struct {
		rules.PreviewItem
		SenderLabel          string `json:"senderLabel"`
		SenderLabelIsAddress bool   `json:"senderLabelIsAddress,omitempty"`
	}
	samples := make([]previewSample, 0, len(shown))
	for _, it := range shown {
		label, isAddress := protect.LabelOf(it.From)
		samples = append(samples, previewSample{PreviewItem: it, SenderLabel: label, SenderLabelIsAddress: isAddress})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"scanned":   len(emails),
		"willApply": len(items),
		"byRule":    shownHits(hits),
		"samples":   samples,
	})
}

// rulesScanLimit is how many of the most recent inbox messages "Appliquer
// maintenant" and "Aperçu" judge.
const rulesScanLimit = 200

// errInboxCopyUnread is the stored copy of an IMAP inbox that could not be
// read: the datastore failed, not the mailbox.
var errInboxCopyUnread = errors.New("the stored copy of the inbox could not be read")

// writeCandidatesError answers a run of the rules that could not read the
// messages it judges: 500 when the datastore failed, 502 when the mailbox did.
func writeCandidatesError(w http.ResponseWriter, err error) {
	if errors.Is(err, errInboxCopyUnread) {
		writeError(w, http.StatusInternalServerError, "Failed to read inbox: "+err.Error())
		return
	}
	writeError(w, http.StatusBadGateway, "Failed to read inbox: "+err.Error())
}

// ruleCandidates returns the messages a run of the rules on demand judges,
// the rulesScanLimit most recent of the inbox, as the rules engine reads them.
//
// Over the Gmail API they are read from Google, text included. Over IMAP they
// are the stored copy of the inbox, what the inbox screen shows (mirror.go):
// a message Mailsorter or the user moved out is not there, one arrived since
// the last sync not yet. The copy keeps no text, so when a rule reads it
// (rules.ReadsText) it is read from the server (withTexts), and only then.
// already is, by message, the actions its rule did at a sync that failed on
// another one (IMAP only): they are not done again. With hold, over IMAP, the
// mails are held (rulesClaim, the claim returned, which the caller releases,
// releaseHold) for as long as a sync would hold them: a mail a sync holds is
// left out, and a sync leaves alone the ones held here.
func (h *Handler) ruleCandidates(ctx context.Context, session *mailSession, userEmail string, ruleset []models.SortingRule, hold bool) (emails []models.Email, already map[string][]string, claim string, err error) {
	if session.Transport != provider.TransportIMAP {
		messages, err := h.gmailService.ListMessages(session.gmail, "in:inbox", rulesScanLimit)
		if err != nil {
			return nil, nil, "", err
		}
		emails := make([]models.Email, 0, len(messages))
		for _, msg := range messages {
			from, subject, to, date := gmail.ParseEmailHeaders(msg)
			emails = append(emails, models.Email{
				MessageID:    msg.Id,
				From:         from,
				To:           to,
				Subject:      subject,
				Snippet:      gmail.Snippet(msg),
				Body:         gmail.GetEmailBody(msg),
				ReceivedDate: date,
			})
		}
		return emails, nil, "", nil
	}

	gen := session.generation()
	emails, already, err = h.storedInboxCandidates(ctx, userEmail, gen, hold)
	if err != nil {
		return nil, nil, "", err
	}
	if hold {
		if emails, claim, err = h.holdRows(ctx, userEmail, gen, emails); err != nil {
			return nil, nil, "", err
		}
	}
	if rules.ReadsText(ruleset) {
		if emails, err = h.withTexts(ctx, userEmail, session, emails); err != nil {
			h.releaseHold(ctx, userEmail, claim)
			return nil, nil, "", err
		}
	}
	return emails, already, claim, nil
}

// holdRows holds these stored IMAP inbox messages for a run of the rules on
// demand (rulesClaim, rulesClaimAt), as claimRulesDue does for a sync, and
// answers the ones it could hold: not those another run or a sync holds now.
// A store that cannot hold them fails the run (errInboxCopyUnread): the rules
// do not act on mails a sync may be acting on.
func (h *Handler) holdRows(ctx context.Context, userEmail, gen string, emails []models.Email) ([]models.Email, string, error) {
	if len(emails) == 0 {
		return emails, "", nil
	}
	claim := primitive.NewObjectID().Hex()
	now := time.Now()
	ids := make([]string, 0, len(emails))
	for _, e := range emails {
		ids = append(ids, e.MessageID)
	}
	free := inboxOf(userEmail, gen)
	free["messageId"], free["rulesClaimAt"] = bson.M{"$in": ids}, notClaimedSince(now)
	if _, err := h.db.Emails().UpdateMany(ctx, free,
		bson.M{"$set": bson.M{"rulesClaim": claim, "rulesClaimAt": now}},
	); err != nil {
		log.Printf("rules: hold the inbox copy of %s: %v", userEmail, err)
		return nil, "", errInboxCopyUnread
	}
	held := inboxOf(userEmail, gen)
	held["rulesClaim"] = claim
	cur, err := h.db.Emails().Find(ctx, held,
		options.Find().SetProjection(bson.M{"messageId": 1}))
	if err != nil {
		log.Printf("rules: read what was held of the inbox copy of %s: %v", userEmail, err)
		h.releaseHold(ctx, userEmail, claim)
		return nil, "", errInboxCopyUnread
	}
	var rows []struct {
		MessageID string `bson:"messageId"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		log.Printf("rules: read what was held of the inbox copy of %s: %v", userEmail, err)
		h.releaseHold(ctx, userEmail, claim)
		return nil, "", errInboxCopyUnread
	}
	ours := make(map[string]bool, len(rows))
	for _, r := range rows {
		ours[r.MessageID] = true
	}
	out := make([]models.Email, 0, len(ours))
	for _, e := range emails {
		if ours[e.MessageID] {
			out = append(out, e)
		}
	}
	return out, claim, nil
}

// releaseHold ends a hold of holdRows, on a context of its own: the run's may
// have ended, and a hold left behind would keep the syncs off these mails for
// rulesClaimFor. A failure is logged: the hold then expires.
func (h *Handler) releaseHold(ctx context.Context, userEmail, claim string) {
	if claim == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if _, err := h.db.Emails().UpdateMany(ctx,
		bson.M{"userId": userEmail, "folder": string(mailbox.FolderInbox), "rulesClaim": claim},
		bson.M{"$unset": bson.M{"rulesClaim": "", "rulesClaimAt": ""}},
	); err != nil {
		log.Printf("rules: release the hold on the inbox copy of %s: %v", userEmail, err)
	}
}

// ruleRow is a stored IMAP inbox message as a run of the rules reads it: the
// message, and what the rules left on its row. RulesDone is the actions its
// rule did at a sync that failed on another one: kept so that the next try
// does only the rest, without a second line in the history nor a second count.
type ruleRow struct {
	models.Email `bson:",inline"`
	RulesDone    []string `bson:"rulesDone"`
}

// rulesClaimFor is how long a sync holds the mails it judges (rulesClaim):
// longer than any sync runs (the background sweep is bounded by
// autoSyncTimeout, a sync from the screen by 30 s), so a claim still held is
// one a sync is working on, and one older is one a sync never released.
const rulesClaimFor = 2 * autoSyncTimeout

// notClaimedSince is the filter of a row no sync holds at now.
func notClaimedSince(now time.Time) bson.M {
	return bson.M{"$not": bson.M{"$gte": now.Add(-rulesClaimFor)}}
}

// storedInboxCandidates is the stored copy of an IMAP inbox as a run of the
// rules on demand judges it, without the text: the rulesScanLimit most recent
// messages still in the inbox, and, by message, the actions a rule already
// did on it (ruleRow.RulesDone). skipClaimed leaves out a mail a sync holds.
func (h *Handler) storedInboxCandidates(ctx context.Context, userEmail, gen string, skipClaimed bool) ([]models.Email, map[string][]string, error) {
	filter := inboxOf(userEmail, gen)
	filter["leftInbox"] = notLeftInbox
	if skipClaimed {
		filter["rulesClaimAt"] = notClaimedSince(time.Now())
	}
	cur, err := h.db.Emails().Find(ctx, filter,
		options.Find().SetSort(bson.D{{Key: "receivedDate", Value: -1}}).SetLimit(rulesScanLimit))
	if err != nil {
		log.Printf("rules: read the inbox copy of %s: %v", userEmail, err)
		return nil, nil, errInboxCopyUnread
	}
	rows := make([]ruleRow, 0)
	if err := cur.All(ctx, &rows); err != nil {
		log.Printf("rules: read the inbox copy of %s: %v", userEmail, err)
		return nil, nil, errInboxCopyUnread
	}
	emails, already := splitRuleRows(rows)
	return emails, already, nil
}

// splitRuleRows is the messages of these rows, and the actions already done
// on those that have some.
func splitRuleRows(rows []ruleRow) ([]models.Email, map[string][]string) {
	emails := make([]models.Email, 0, len(rows))
	already := map[string][]string{}
	for _, r := range rows {
		emails = append(emails, r.Email)
		if len(r.RulesDone) > 0 {
			already[r.MessageID] = r.RulesDone
		}
	}
	return emails, already
}

// settleMarks keeps the marks for the rules (rulesDue) right after a run of
// the rules on demand over the stored IMAP inbox: a marked mail judged without
// a failure is unmarked, so a sync does not judge it again; one whose rule
// failed half-way keeps, for the sync's next try, what was done (rulesDone).
// A failure is logged: the next sync judges the mail once more.
func (h *Handler) settleMarks(ctx context.Context, userEmail, gen string, judged []models.Email, done rulesDone) {
	marked := func(id interface{}) bson.M {
		filter := inboxOf(userEmail, gen)
		filter["rulesDue"], filter["messageId"] = true, id
		return filter
	}
	ids := make([]string, 0, len(judged))
	for _, e := range judged {
		if _, failed := done.progress[e.MessageID]; !failed {
			ids = append(ids, e.MessageID)
		}
	}
	if len(ids) > 0 {
		if _, err := h.db.Emails().UpdateMany(ctx, marked(bson.M{"$in": ids}),
			bson.M{"$unset": bson.M{"rulesDue": "", "rulesTries": "", "rulesDone": ""}},
		); err != nil {
			log.Printf("rules: %d mails of %s judged, not unmarked for the sync: %v", len(ids), userEmail, err)
		}
	}
	for id, progress := range done.progress {
		if _, err := h.db.Emails().UpdateOne(ctx, marked(id),
			bson.M{"$set": bson.M{"rulesDone": progress}},
		); err != nil {
			log.Printf("rules: what the rule did on message %s of %s is not kept for the next try: %v", id, userEmail, err)
		}
	}
}

// textBatch is how many messages one FETCH of their text reads
// (imap.Client.Texts): a batch costs at most that many times what it reads of
// each message.
const textBatch = 20

// withTexts gives these stored IMAP inbox messages their text (Body, Snippet),
// read from the server, for a ruleset that reads it. The copy keeps none.
//
// A message whose text cannot be read is left out, logged: judged without
// it, "the body does not contain" would match it, and a rule archive it on a
// text nobody read. So is one whose text starts beyond what is read
// (imap.Text.NotReached). One the inbox no longer holds is left out too. A
// read the server stopped, or that ran out of the request's time (ctx,
// checked at each batch), is an error: the rules do not judge half the inbox
// and say nothing about the other half.
func (h *Handler) withTexts(ctx context.Context, userEmail string, session *mailSession, emails []models.Email) ([]models.Email, error) {
	if len(emails) == 0 {
		return emails, nil
	}
	folder := string(mailbox.FolderInbox)
	uids := make([]string, 0, len(emails))
	for _, e := range emails {
		uids = append(uids, e.MessageID)
	}
	reopen := h.sessionReopener(ctx, userEmail, session)
	if err := openForPhase(session, reopen); err != nil {
		return nil, fmt.Errorf("open the mailbox again to read the texts: %w", err)
	}
	got, err := imap.ReadInHalves(uids, textBatch,
		func(batch []string) (map[string]imap.Text, error) { return session.imapc.Texts(ctx, folder, batch) },
		reopen)
	if err != nil {
		return nil, fmt.Errorf("read the text of the messages: %w", err)
	}
	if len(got.Left) > 0 {
		return nil, fmt.Errorf("read the text of the messages: %d left unread: %v", len(got.Left), got.Stopped)
	}
	out := make([]models.Email, 0, len(emails))
	for _, e := range emails {
		text, ok := got.Read[e.MessageID]
		if !ok {
			if rErr, unreadable := got.Unreadable[e.MessageID]; unreadable {
				log.Printf("rules: the text of message uid %s of %s cannot be read, no rule judges it: %v", e.MessageID, userEmail, rErr)
			}
			continue
		}
		if text.NotReached {
			log.Printf("rules: the text of message uid %s of %s starts beyond what is read of it, no rule judges it", e.MessageID, userEmail)
			continue
		}
		e.Body, e.Snippet = text.Body, text.Snippet
		out = append(out, e)
	}
	return out, nil
}

// rulesDone is what one run of the rules over messages did. A message is
// counted in applied or in progress, never both.
type rulesDone struct {
	// applied counts the messages whose rule did everything it does (at this
	// run, or partly at an earlier try), something at least.
	applied int
	// byRule counts by rule name the messages a rule acted on for the first
	// time at this run, for appliedCount: a message is counted once, at its
	// first action, whatever the tries.
	byRule map[string]int
	// protectedSkipped counts the messages a rule matched and did nothing to,
	// their sender being protected.
	protectedSkipped int
	// progress is, for each message an action of its rule failed on (logged),
	// the actions done on it so far, this run and before: what a next try
	// does not do again.
	progress map[string][]string
}

// runRules applies the ruleset to these messages, on whichever transport the
// session is: for each, the first rule that matches (rules.FirstMatch), its
// actions in rules.ApplyOrder but those already done on it (already, a try
// that failed half-way), each written in the history (SourceRule), and the
// count of each rule kept on it (appliedCount).
func (h *Handler) runRules(ctx context.Context, session *mailSession, userEmail string, emails []models.Email, ruleset []models.SortingRule, protectedList protect.Shield, already map[string][]string) rulesDone {
	done := rulesDone{byRule: map[string]int{}, progress: map[string][]string{}}
	labelCache := map[string]string{} // labelName -> Gmail label ID
	for _, email := range emails {
		match := rules.FirstMatch(email, ruleset)
		if match == nil {
			continue
		}
		before := already[email.MessageID]
		// A protected sender is shielded from destructive actions, but
		// non-destructive actions in the same rule still run.
		appliedActs, skipped, failed := h.applyRuleToMessage(ctx, session, userEmail, email, *match, protectedList, labelCache, before)
		all := append([]string{}, before...)
		for _, act := range appliedActs {
			// A label is written with where it filed the mail: the label,
			// or over IMAP its folder, which the history shows.
			h.logActionFiled(ctx, userEmail, session.generation(), email.MessageID, act.Type, SourceRule, email.Subject, email.From, act.LabelName)
			all = append(all, act.Type)
		}
		if len(before) == 0 && len(appliedActs) > 0 {
			done.byRule[match.Name]++
		}
		switch {
		case failed:
			done.progress[email.MessageID] = all
		case len(all) > 0:
			done.applied++
		case skipped:
			done.protectedSkipped++
		}
	}
	h.countRuleHits(ctx, userEmail, done.byRule)
	return done
}

// countRuleHits adds to each rule the messages it acted on (appliedCount),
// the number the Règles screen shows. A failure costs a count, not the
// actions: it is logged.
func (h *Handler) countRuleHits(ctx context.Context, userEmail string, byRule map[string]int) {
	for name, n := range byRule {
		if _, err := h.db.SortingRules().UpdateOne(ctx,
			bson.M{"userId": userEmail, "name": name},
			bson.M{"$inc": bson.M{"appliedCount": n}},
		); err != nil {
			log.Printf("rules: count %d messages for the rule %q of %s: %v", n, name, userEmail, err)
		}
	}
}

// ruleForSender builds a deterministic "always do X to this sender" rule from a
// sender address and action. Pure (no I/O) so it is cheap to test. The sender
// is matched on From contains <address>, mirroring how the rest of the app
// scopes per-sender operations.
func ruleForSender(userEmail string, req models.CreateSenderRuleRequest) models.SortingRule {
	addr := extractSenderAddress(req.SenderEmail)
	now := time.Now()
	return models.SortingRule{
		UserID: userEmail,
		// The name is shown as it is: the address in its shown form, the
		// condition below keeps it as written, which is what matches.
		Name:     senderRulePrefix + protect.ShownAddress(addr),
		Enabled:  true,
		MatchAll: true,
		Conditions: []models.RuleCondition{
			{Field: rules.FieldFrom, Operator: rules.OpContains, Value: addr},
		},
		Action:    req.Action,
		LabelName: req.LabelName,
		Priority:  0,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// CreateSenderRule turns a sender into a permanent deterministic rule in one
// click, the concrete form of "learn once, apply forever". The new rule then
// runs for free on every manual apply and (if enabled) automatically at sync.
func (h *Handler) CreateSenderRule(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req models.CreateSenderRuleRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if extractSenderAddress(req.SenderEmail) == "" {
		writeError(w, http.StatusUnprocessableEntity, unreadableSenderActionMessage)
		return
	}

	rule := ruleForSender(userEmail, req)
	if err := rules.Validate(rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	res, err := h.db.SortingRules().InsertOne(ctx, rule)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Impossible d'enregistrer la règle.")
		return
	}
	if oid, ok := res.InsertedID.(primitive.ObjectID); ok {
		rule.ID = oid.Hex()
	}

	writeJSON(w, http.StatusCreated, rule)
}

// applyRuleToMessage runs the actions of a matched rule on one message, in the
// order of its transport: rules.ApplyOrder over the Gmail API (what changes
// the message where it is before what moves it out of the inbox), and
// rules.ApplyOrderOnFolders over IMAP, where a label is a folder and filing
// into it a move, and a mail moves once (the flags, then the last move
// written), since a moved message takes a new number. A protected (VIP)
// sender is shielded from what takes a mail out of the inbox (allowsOn:
// archive, trash, and over IMAP the filing into a folder); the other actions
// of the same rule still run. It returns the actions actually applied, a
// label with where it filed the mail (the label, or over IMAP its folder),
// whether an action was skipped for protection, and whether an action failed
// (logged). An action type in done was applied at an earlier try that failed
// on another one: it is not done again (a mail marked unread since stays
// unread).
func (h *Handler) applyRuleToMessage(ctx context.Context, session *mailSession, userEmail string, email models.Email, rule models.SortingRule, protectedList protect.Shield, labelCache map[string]string, done []string) (applied []models.RuleAction, protectedSkip, failed bool) {
	order := rules.ApplyOrder
	if session.Transport == provider.TransportIMAP {
		order = rules.ApplyOrderOnFolders
	}
	ref := session.RefFor(ctx, email.MessageID)
	for _, a := range order(rules.EffectiveActions(rule)) {
		if containsString(done, a.Type) {
			continue
		}
		if !allowsOn(session.Transport, a.Type, email.From, protectedList) {
			protectedSkip = true
			continue
		}
		filed, err := h.applyOneAction(ctx, session, ref, userEmail, a, labelCache)
		switch {
		case err == nil:
			applied = append(applied, models.RuleAction{Type: a.Type, LabelName: filed})
		case errors.Is(err, errAlreadyOut), errors.Is(err, errLeftInbox):
			// Nothing to do, and nothing went wrong: the message already left
			// the inbox (moved by hand, or by another mail program, since it
			// was read).
		case errors.Is(err, imap.ErrNotOwnFolder), errors.Is(err, imap.ErrBadFolderName), errors.Is(err, mailbox.ErrLabelRequired):
			// A folder no mail is filed into (a special one, a name no folder
			// is made under): trying again changes nothing, so it is not a
			// failure to retry. Logged, the rule's other actions run.
			log.Printf("rules: %s on message %s of %s (rule %q) left aside: %v", a.Type, email.MessageID, userEmail, rule.Name, err)
		default:
			// ErrTooManyNewFolders among them: the next sync files it.
			failed = true
			log.Printf("rules: %s on message %s of %s (rule %q): %v", a.Type, email.MessageID, userEmail, rule.Name, err)
		}
	}
	return applied, protectedSkip, failed
}

// applyOneAction performs a single rule action on one message, and says, for
// a label, where it filed it: the label's name over the Gmail API, the folder
// over IMAP.
func (h *Handler) applyOneAction(ctx context.Context, session *mailSession, ref mailbox.Ref, userEmail string, a models.RuleAction, labelCache map[string]string) (string, error) {
	var labelID string
	if a.Type == rules.ActionLabel {
		if session.Transport == provider.TransportIMAP {
			// A label is a folder over IMAP: the mail is filed in the user's
			// folder named alike, made when there is none (FilingName, then
			// Destination in the move), as an AI proposal to label it is.
			folder, err := session.imapc.FilingName(a.LabelName)
			if err != nil {
				return "", err
			}
			return folder, h.applyVerb(ctx, session.Mailbox(), ref, a.Type, folder)
		}
		// The label has to exist in the mailbox before a message can carry
		// it, and the cache keeps one rule application from creating it once
		// per message.
		id, ok := labelCache[a.LabelName]
		if !ok {
			created, err := h.ensureLabel(ctx, session.gmail, userEmail, a.LabelName)
			if err != nil {
				return "", err
			}
			id = created
			labelCache[a.LabelName] = created
		}
		labelID = id
	}

	// An action the rules engine validated but this transport does not know is
	// skipped rather than failed: it must not abort the rest of the ruleset.
	err := h.applyVerb(ctx, session.Mailbox(), ref, a.Type, labelID)
	if errors.Is(err, mailbox.ErrUnknownAction) {
		return "", nil
	}
	if a.Type == rules.ActionLabel {
		return a.LabelName, err
	}
	return "", err
}

// ruleFromInput maps an input payload onto a SortingRule owned by the caller. It
// normalizes the two authoring shapes into one: when a client sends the
// multi-action Actions list, the legacy Action/LabelName fields are backfilled
// with the primary action so older readers (and per-rule stats) keep working.
func ruleFromInput(userEmail string, in models.SortingRuleInput) models.SortingRule {
	rule := models.SortingRule{
		UserID:     userEmail,
		Name:       in.Name,
		Enabled:    in.Enabled,
		MatchAll:   in.MatchAll,
		Conditions: in.Conditions,
		Action:     in.Action,
		LabelName:  in.LabelName,
		Actions:    in.Actions,
		Priority:   in.Priority,
	}
	if len(rule.Actions) > 0 {
		rule.Action = rule.Actions[0].Type
		rule.LabelName = rule.Actions[0].LabelName
	}
	return rule
}
