package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/gmail"
	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
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

	writeJSON(w, http.StatusOK, map[string]interface{}{"rules": ruleset})
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

	emails, err := h.ruleCandidates(ctx, session, userEmail, enabled)
	if err != nil {
		writeCandidatesError(w, err)
		return
	}

	done := h.runRules(ctx, session, userEmail, emails, enabled, h.protectedValues(ctx, userEmail))
	if session.Transport == provider.TransportIMAP {
		// Judged here, a mail marked for the rules at a sync is not judged
		// again there (a second star, a second line in the history).
		h.unmarkJudged(ctx, userEmail, emails, done.failed)
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
// It leaves out what a rule cannot do there (label), and a mail whose rule
// would only do that.
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

	transport, _, err := h.transportFor(ctx, userEmail)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	var emails []models.Email
	if transport == provider.TransportIMAP && !rules.ReadsText(enabled) {
		emails, err = h.storedInboxCandidates(ctx, userEmail)
	} else {
		session, sErr := h.openSession(ctx, userEmail)
		if sErr != nil {
			writeAuthError(w, sErr)
			return
		}
		defer session.Close()
		emails, err = h.ruleCandidates(ctx, session, userEmail, enabled)
	}
	if err != nil {
		writeCandidatesError(w, err)
		return
	}

	var items []rules.PreviewItem
	var hits []rules.RuleHits
	if transport == provider.TransportIMAP {
		items, hits = rules.PreviewWithout(emails, enabled, rules.ActionLabel)
	} else {
		items, hits = rules.Preview(emails, enabled)
	}

	samples := items
	if len(samples) > previewSampleCap {
		samples = samples[:previewSampleCap]
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"scanned":   len(emails),
		"willApply": len(items),
		"byRule":    hits,
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
func (h *Handler) ruleCandidates(ctx context.Context, session *mailSession, userEmail string, ruleset []models.SortingRule) ([]models.Email, error) {
	if session.Transport != provider.TransportIMAP {
		messages, err := h.gmailService.ListMessages(session.gmail, "in:inbox", rulesScanLimit)
		if err != nil {
			return nil, err
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
		return emails, nil
	}

	emails, err := h.storedInboxCandidates(ctx, userEmail)
	if err != nil || !rules.ReadsText(ruleset) {
		return emails, err
	}
	return h.withTexts(ctx, userEmail, session, emails)
}

// storedInboxCandidates is the stored copy of an IMAP inbox as a run of the
// rules on demand judges it, without the text: the rulesScanLimit most recent
// messages still in the inbox.
func (h *Handler) storedInboxCandidates(ctx context.Context, userEmail string) ([]models.Email, error) {
	cur, err := h.db.Emails().Find(ctx,
		bson.M{"userId": userEmail, "folder": string(mailbox.FolderInbox), "leftInbox": notLeftInbox},
		options.Find().SetSort(bson.D{{Key: "receivedDate", Value: -1}}).SetLimit(rulesScanLimit))
	if err != nil {
		log.Printf("rules: read the inbox copy of %s: %v", userEmail, err)
		return nil, errInboxCopyUnread
	}
	emails := make([]models.Email, 0)
	if err := cur.All(ctx, &emails); err != nil {
		log.Printf("rules: read the inbox copy of %s: %v", userEmail, err)
		return nil, errInboxCopyUnread
	}
	return emails, nil
}

// unmarkJudged unmarks for the rules (rulesDue) the stored IMAP messages a run
// of the rules on demand judged without a failure. A failure is logged: the
// next sync judges the mail once more.
func (h *Handler) unmarkJudged(ctx context.Context, userEmail string, judged []models.Email, failed []string) {
	skip := make(map[string]bool, len(failed))
	for _, id := range failed {
		skip[id] = true
	}
	ids := make([]string, 0, len(judged))
	for _, e := range judged {
		if !skip[e.MessageID] {
			ids = append(ids, e.MessageID)
		}
	}
	if len(ids) == 0 {
		return
	}
	if _, err := h.db.Emails().UpdateMany(ctx,
		bson.M{"userId": userEmail, "folder": string(mailbox.FolderInbox), "rulesDue": true, "messageId": bson.M{"$in": ids}},
		bson.M{"$unset": bson.M{"rulesDue": "", "rulesTries": ""}},
	); err != nil {
		log.Printf("rules: %d mails of %s judged, not unmarked for the sync: %v", len(ids), userEmail, err)
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

// rulesDone is what one run of the rules over messages did.
type rulesDone struct {
	// applied counts the messages a rule acted on, byRule the same by rule
	// name.
	applied int
	byRule  map[string]int
	// protectedSkipped counts the messages a rule matched and did nothing to,
	// their sender being protected.
	protectedSkipped int
	// failed lists the messages an action of their rule failed on (logged).
	failed []string
}

// runRules applies the ruleset to these messages, on whichever transport the
// session is: for each, the first rule that matches (rules.FirstMatch), its
// actions in rules.ApplyOrder, each written in the history (SourceRule), and
// the count of each rule kept on it (appliedCount).
func (h *Handler) runRules(ctx context.Context, session *mailSession, userEmail string, emails []models.Email, ruleset []models.SortingRule, protectedList []string) rulesDone {
	done := rulesDone{byRule: map[string]int{}}
	labelCache := map[string]string{} // labelName -> Gmail label ID
	for _, email := range emails {
		match := rules.FirstMatch(email, ruleset)
		if match == nil {
			continue
		}
		// A protected sender is shielded from destructive actions, but
		// non-destructive actions in the same rule still run.
		appliedActs, skipped, failed := h.applyRuleToMessage(ctx, session, userEmail, email, *match, protectedList, labelCache)
		if failed {
			done.failed = append(done.failed, email.MessageID)
		}
		if len(appliedActs) == 0 {
			if skipped {
				done.protectedSkipped++
			}
			continue
		}
		done.applied++
		done.byRule[match.Name]++
		for _, act := range appliedActs {
			h.logActionMeta(ctx, userEmail, email.MessageID, act, SourceRule, email.Subject, email.From)
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
		UserID:   userEmail,
		Name:     "Expéditeur : " + addr,
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
		writeError(w, http.StatusBadRequest, "Adresse d'expéditeur requise")
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

// applyRuleToMessage runs every action of a matched rule on one message, in
// rules.ApplyOrder: what changes the message where it is before what moves it
// out of the inbox, since over IMAP a moved message takes a new number. A
// protected (VIP) sender shields against destructive actions only:
// archive/trash are skipped, but a non-destructive action in the same rule
// (label/star/markRead) still runs. It returns the action types actually
// applied (for the ledger), whether a destructive action was skipped for
// protection, and whether an action failed (logged).
func (h *Handler) applyRuleToMessage(ctx context.Context, session *mailSession, userEmail string, email models.Email, rule models.SortingRule, protectedList []string, labelCache map[string]string) (applied []string, protectedSkip, failed bool) {
	ref := session.RefFor(ctx, email.MessageID)
	for _, a := range rules.ApplyOrder(rules.EffectiveActions(rule)) {
		if !allows(a.Type, email.From, protectedList) {
			protectedSkip = true
			continue
		}
		err := h.applyOneAction(ctx, session, ref, userEmail, a, labelCache)
		switch {
		case err == nil:
			applied = append(applied, a.Type)
		case errors.Is(err, errNoLabelsOnIMAP), errors.Is(err, errAlreadyOut), errors.Is(err, errLeftInbox):
			// Nothing to do, and nothing went wrong: the mailbox has no
			// labels, or the message already left the inbox (moved by hand,
			// or by another mail program, since it was read).
		default:
			failed = true
			log.Printf("rules: %s on message %s of %s (rule %q): %v", a.Type, email.MessageID, userEmail, rule.Name, err)
		}
	}
	return applied, protectedSkip, failed
}

// applyOneAction performs a single rule action on one message.
func (h *Handler) applyOneAction(ctx context.Context, session *mailSession, ref mailbox.Ref, userEmail string, a models.RuleAction, labelCache map[string]string) error {
	// Only the label action needs anything resolved before it can run: the
	// label has to exist in the mailbox before a message can carry it, and the
	// cache keeps one rule application from creating it once per message.
	var labelID string
	if a.Type == rules.ActionLabel {
		if session.Transport == provider.TransportIMAP {
			// A message sits in one folder over IMAP: there is no label to
			// add, and filing it into a folder is not a rule action yet.
			return errNoLabelsOnIMAP
		}
		id, ok := labelCache[a.LabelName]
		if !ok {
			created, err := h.ensureLabel(ctx, session.gmail, userEmail, a.LabelName)
			if err != nil {
				return err
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
		return nil
	}
	return err
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
