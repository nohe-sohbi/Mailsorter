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

	listedAt := time.Now()
	emails, err := session.imapc.List(ctx, folder, imapListLimit)
	if err != nil {
		return 0, 0, err
	}
	// The listing reads the newest messages only; which of the older ones
	// stored are still in the inbox, it cannot tell. One UID SEARCH can,
	// whatever the size of the inbox.
	present, err := session.imapc.UIDs(folder)
	if err != nil {
		return 0, 0, err
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
		email.UserID = userEmail
		// The folder is stored with the message because on IMAP it is half of
		// its identity: without it the UID below names nothing in particular.
		email.Folder = folder

		if _, uErr := h.db.Emails().UpdateOne(ctx,
			bson.M{"messageId": email.MessageID, "userId": userEmail},
			// syncedAt tells a sync running at the same time that this row
			// was just written from a fresher listing than its own.
			bson.M{"$set": email, "$currentDate": bson.M{"syncedAt": true}},
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
	if dropErr != nil {
		return synced, len(emails), dropErr
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
		m.mirrorSeen(ctx, ref, op)
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

// mirrorSeen carries a read or unread change into the stored copy the list
// is served from, so the list shows it at once rather than at the next sync.
// The server already changed; a failure here is only logged.
func (m mirroredIMAP) mirrorSeen(ctx context.Context, ref mailbox.Ref, op mailbox.IMAPOp) {
	var read *bool
	for _, f := range op.AddFlags {
		if f == mailbox.FlagSeen {
			t := true
			read = &t
		}
	}
	for _, f := range op.DropFlags {
		if f == mailbox.FlagSeen {
			f := false
			read = &f
		}
	}
	if read == nil || ref.Folder != string(mailbox.FolderInbox) {
		return
	}
	if _, err := m.h.db.Emails().UpdateOne(ctx,
		bson.M{"userId": m.userEmail, "folder": ref.Folder, "messageId": ref.ID},
		bson.M{"$set": bson.M{"isRead": *read}},
	); err != nil {
		log.Printf("imap: message %s of %s marked read=%v on the server, not in its copy: %v", ref.ID, m.userEmail, *read, err)
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
		return errNotWhereLeft
	}
	landed, err := m.moveFrom(ctx, row.MovedTo.Folder, uid, to, row.HeaderMessageID)
	if errors.Is(err, imap.ErrMessageGone) {
		// A second click may have moved it there already.
		if m.recordedIn(ctx, ref, dest) {
			return errAlreadyOut
		}
		// Otherwise another mail client moved it: forget where Mailsorter
		// had put it, as "Annuler" does.
		if _, uErr := m.h.db.Emails().UpdateOne(ctx,
			bson.M{"userId": m.userEmail, "folder": string(mailbox.FolderInbox), "messageId": ref.ID, "movedTo.folder": row.MovedTo.Folder, "movedTo.uid": row.MovedTo.UID},
			bson.M{"$unset": bson.M{"movedTo": ""}},
		); uErr != nil {
			log.Printf("imap: message %s of %s is not where it was put, and that is not recorded: %v", ref.ID, m.userEmail, uErr)
		}
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

// moveFrom moves the message Mailsorter recorded as uid in folder. When its
// Message-ID is known, the number is checked first against the Message-ID the
// server reports for it (the envelope, parsed as the listing parsed it): a
// server that renumbered its folders (a migration, a rebuilt index) would
// otherwise hand back another mail. When the number no longer names it, it
// is looked for by that Message-ID once.
func (m mirroredIMAP) moveFrom(ctx context.Context, folder, uid string, to mailbox.Folder, headerMessageID string) (mailbox.Ref, error) {
	if headerMessageID != "" {
		id, exists, err := m.client.IDAt(folder, uid)
		if err != nil {
			return mailbox.Ref{}, err
		}
		if !exists || id != headerMessageID {
			found, err := m.client.FindByMessageID(folder, headerMessageID)
			if err != nil {
				return mailbox.Ref{}, err
			}
			if found == "" {
				return mailbox.Ref{}, imap.ErrMessageGone
			}
			uid = found
		}
	}
	return m.client.MoveTo(ctx, mailbox.InFolder(folder, uid), to, headerMessageID)
}

// putBack is "Annuler" for a message Mailsorter moved out of the inbox.
func (m mirroredIMAP) putBack(ctx context.Context, ref mailbox.Ref, row movedRow, findErr error) error {
	return m.putBackOnce(ctx, ref, row, findErr, false)
}

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
			// Gone with no forwarding number: put back by the other one with
			// its new number unknown (the next sync lists it).
			return errAlreadyBack
		}
		if err != nil {
			return fmt.Errorf("read the stored copy of message %s: %w", ref.ID, err)
		}
		if r.RestoringAt.IsZero() {
			return errRetryReturn
		}
	}
	return fmt.Errorf("another \"Annuler\" of message %s is still running", ref.ID)
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
		if errors.Is(err, errRetryReturn) && !retried {
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
		// does not say so, and it forgets where it had put it: the history
		// stops offering "Annuler", and the next sync drops the row.
		if _, uErr := m.h.db.Emails().UpdateOne(ctx, filter, bson.M{"$unset": bson.M{"movedTo": "", "restoringAt": ""}}); uErr != nil {
			log.Printf("imap: message %s of %s is not where it was put, and that is not recorded: %v", ref.ID, m.userEmail, uErr)
		}
		return errNotWhereLeft
	}
	if err != nil {
		release()
		return err
	}
	if landed.ID == "" {
		// Back on the server, number unknown: the next sync lists it.
		if _, err := m.h.db.Emails().DeleteOne(ctx, filter); err != nil {
			log.Printf("imap: message put back in the inbox of %s, stale copy kept: %v", m.userEmail, err)
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
