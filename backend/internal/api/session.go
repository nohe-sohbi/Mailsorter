package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"github.com/nohe-sohbi/mailsorter/backend/internal/mailbox"
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
		return provider.TransportIMAP, account, nil
	case errors.Is(err, mongo.ErrNoDocuments):
		return provider.TransportGmailAPI, models.MailAccount{}, nil
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
		return s.imapc
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
		"Cette action n'est pas encore disponible sur une boite branchee en IMAP.")
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

	emails, err := session.imapc.List(ctx, folder, 100)
	if err != nil {
		return 0, 0, err
	}

	for _, email := range emails {
		email.UserID = userEmail
		// The folder is stored with the message because on IMAP it is half of
		// its identity: without it the UID below names nothing in particular.
		email.Folder = folder

		if _, uErr := h.db.Emails().UpdateOne(ctx,
			bson.M{"messageId": email.MessageID, "userId": userEmail},
			bson.M{"$set": email},
			options.Update().SetUpsert(true),
		); uErr == nil {
			synced++
		}
	}
	return synced, len(emails), nil
}

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
