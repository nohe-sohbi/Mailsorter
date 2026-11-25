package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"github.com/nohe-sohbi/mailsorter/backend/internal/smtpsend"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// The mailbox a user connected over IMAP: connect, read, disconnect.
//
// This is the hosted edition's way in. Where a self-hosted instance sends the
// user through Google's OAuth consent screen, the hosted one asks for an
// address and an app password, because a shared OAuth client can never reach
// the Gmail API at scale (see internal/provider on the 100-authorization cap).
//
// Two rules hold this file together:
//
//   - The client supplies an address and a password, and NOTHING else. The
//     provider, host, port and TLS mode are resolved from internal/provider. A
//     body that could name its own host would let any caller make the server
//     open an authenticated connection wherever they liked, which is the same
//     class of hole the one-click unsubscribe had.
//   - The connection is proved before it is stored. A password accepted on
//     faith becomes a mailbox that fails on every sync afterwards, with the
//     failure surfacing far from the screen where it could be fixed.

// connectTimeout bounds the whole connect attempt, dial included. A user is
// waiting on the other end of it.
const connectTimeout = 30 * time.Second

// The three ways a connect attempt fails, as identities rather than as strings,
// so the authenticated route and the public sign-in map them to the same
// statuses without either one restating the mapping.
var (
	errMailboxNoRoute     = errors.New("api: this address has no IMAP route in this edition")
	errMailboxRejected    = errors.New("api: the provider refused these credentials")
	errMailboxUnreachable = errors.New("api: the provider could not be reached")
)

// connectAndStore proves the credentials against the real server, then stores
// the connection under userEmail.
//
// Both callers go through here, and that is the point: the sealing and the
// upsert are one piece of code, so a change to how a credential is kept cannot
// apply to one entry point and not the other.
func (h *Handler) connectAndStore(ctx context.Context, userEmail, address, password string) (models.MailAccount, error) {
	// Resolved, not received. Detect always answers (it falls back to the
	// generic IMAP entry), so the failure below is about the EDITION rather
	// than about an unknown provider.
	p := provider.Detect(address)
	route, err := provider.PickIMAP(p.Key, Edition)
	if err != nil {
		return models.MailAccount{}, errMailboxNoRoute
	}

	client, err := imap.Connect(ctx, imap.Credentials{
		Endpoint: *route.IMAP,
		Username: address,
		Password: password,
	})
	if err != nil {
		// A rejected password is the user's to fix and says so; anything else is
		// the server or the provider being unreachable, and saying "wrong
		// password" there would send them to regenerate a perfectly good one.
		if errors.Is(err, imap.ErrAuth) {
			return models.MailAccount{}, errMailboxRejected
		}
		return models.MailAccount{}, fmt.Errorf("%w: %v", errMailboxUnreachable, err)
	}
	client.Close()

	sealed, err := h.sealSecret(password)
	if err != nil {
		return models.MailAccount{}, fmt.Errorf("seal mailbox secret: %w", err)
	}

	// The same mailbox connected again (a new app password) keeps the moment
	// it was first connected; another one (another address, another server)
	// is connected now. Over IMAP the rules a sync applies on its own judge
	// the mails received after it (arrivalsSince).
	var prev models.MailAccount
	prevErr := h.db.MailAccounts().FindOne(ctx, bson.M{"userId": userEmail}).Decode(&prev)
	if prevErr != nil && !errors.Is(prevErr, mongo.ErrNoDocuments) {
		return models.MailAccount{}, fmt.Errorf("read the mail account: %w", prevErr)
	}
	same := prevErr == nil && sameMailbox(prev, address, route)
	if !same {
		// Another mailbox starts from nothing: the copy of the previous one,
		// and the AI's proposals about it, name mails this one does not hold.
		// Cleared before the new row is written: if that write fails, the
		// previous mailbox is still connected, and its next sync, its first
		// again, judges nothing as arrived.
		if err := h.clearMailboxCopy(ctx, userEmail); err != nil {
			return models.MailAccount{}, err
		}
	}

	account, update := mailboxRow(userEmail, p.Key, address, route, sealed, time.Now(), same)
	if same {
		account.CreatedAt = prev.CreatedAt
	}
	if _, err := h.db.MailAccounts().UpdateOne(ctx,
		bson.M{"userId": userEmail},
		update,
		options.Update().SetUpsert(true),
	); err != nil {
		return models.MailAccount{}, fmt.Errorf("store mail account: %w", err)
	}
	return account, nil
}

// clearMailboxCopy clears what Mailsorter kept of the mailbox connected over
// IMAP, when it is disconnected or another one is connected in its place:
// the stored copy of its inbox (the rows that carry a folder), the AI's
// pending proposals about its mails, the count of mails its rules gave up on
// not yet told, and the mark of its first sync, so the next sync is a first
// one. The history stays, but its lines no longer offer "Annuler"
// (mailboxGone): they name mails by numbers the next mailbox gives to its own
// mails, and an "Annuler" would act on one of those.
func (h *Handler) clearMailboxCopy(ctx context.Context, userEmail string) error {
	if _, err := h.db.ActionLog().UpdateMany(ctx,
		bson.M{"userId": userEmail, "createdAt": bson.M{"$lte": time.Now()}},
		bson.M{"$set": bson.M{"mailboxGone": true}}); err != nil {
		return fmt.Errorf("close the history of the mailbox: %w", err)
	}
	if _, err := h.db.MailAccounts().UpdateOne(ctx, bson.M{"userId": userEmail},
		bson.M{"$unset": bson.M{"firstSyncedAt": ""}}); err != nil {
		return fmt.Errorf("unmark the first sync of the mailbox: %w", err)
	}
	if _, err := h.db.Emails().DeleteMany(ctx, bson.M{"userId": userEmail, "folder": bson.M{"$exists": true}}); err != nil {
		return fmt.Errorf("clear the copy of the mailbox: %w", err)
	}
	if _, err := h.db.AISuggestions().DeleteMany(ctx, bson.M{"userId": userEmail, "status": "pending"}); err != nil {
		return fmt.Errorf("clear the proposals about the mailbox: %w", err)
	}
	if _, err := h.db.Users().UpdateOne(ctx, bson.M{"email": userEmail},
		bson.M{"$unset": bson.M{"rulesGivenUp": ""}}); err != nil {
		return fmt.Errorf("clear what the rules of the mailbox gave up on: %w", err)
	}
	return nil
}

// sameMailbox says whether a connection to address over route is the mailbox
// already connected (prev): the same address, on the same server.
func sameMailbox(prev models.MailAccount, address string, route provider.Route) bool {
	if !strings.EqualFold(strings.TrimSpace(prev.Username), strings.TrimSpace(address)) {
		return false
	}
	if route.IMAP == nil {
		return prev.Host == ""
	}
	return strings.EqualFold(prev.Host, route.IMAP.Host) && prev.Port == route.IMAP.Port
}

// mailboxRow is what a connection proved over route becomes: the account
// connectAndStore answers with, and the upsert that writes it over the user's
// previous row, sealed password included. The sending server comes from the
// same route, so the recap leaves from the provider whose IMAP server was
// just proved. A route without one clears what a mailbox connected before may
// have left on the row, rather than keep another provider's server. The
// moment of the connection (createdAt) is kept when it is the same mailbox
// connected again (same), and is now otherwise, the next sync being then the
// first of this mailbox (firstSyncedAt unset).
func mailboxRow(userEmail, providerKey, address string, route provider.Route, sealed string, now time.Time, same bool) (models.MailAccount, bson.M) {
	account := models.MailAccount{
		UserID:    userEmail,
		Provider:  providerKey,
		Transport: string(route.Transport),
		Username:  address,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if route.IMAP != nil {
		account.Host = route.IMAP.Host
		account.Port = route.IMAP.Port
		account.TLS = string(route.IMAP.TLS)
	}

	set := bson.M{
		"provider":  account.Provider,
		"transport": account.Transport,
		"username":  account.Username,
		"secret":    sealed,
		"host":      account.Host,
		"port":      account.Port,
		"tls":       account.TLS,
		"updatedAt": now,
	}
	onInsert := bson.M{"userId": userEmail, "createdAt": now}
	if !same {
		set["createdAt"] = now
		delete(onInsert, "createdAt")
	}
	update := bson.M{
		"$set":         set,
		"$setOnInsert": onInsert,
	}
	unset := bson.M{}
	if !same {
		unset["firstSyncedAt"] = ""
	}
	if route.SMTP != nil {
		account.SMTPHost = route.SMTP.Host
		account.SMTPPort = route.SMTP.Port
		account.SMTPTLS = string(route.SMTP.TLS)
		set["smtpHost"] = account.SMTPHost
		set["smtpPort"] = account.SMTPPort
		set["smtpTls"] = account.SMTPTLS
	} else {
		unset["smtpHost"], unset["smtpPort"], unset["smtpTls"] = "", "", ""
	}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	return account, update
}

// writeMailboxConnectError maps a connectAndStore failure to an answer.
func writeMailboxConnectError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, errMailboxNoRoute):
		writeError(w, http.StatusUnprocessableEntity,
			"Cette boîte ne se connecte pas par IMAP sur cette instance.")
	case errors.Is(err, errMailboxRejected):
		writeError(w, http.StatusUnauthorized,
			"Identifiants refusés par le fournisseur. Vérifiez le mot de passe d'application.")
	case errors.Is(err, errMailboxUnreachable):
		writeError(w, http.StatusBadGateway, "Connexion au serveur de mail impossible.")
	default:
		writeError(w, http.StatusInternalServerError, "Impossible d'enregistrer la connexion.")
	}
}

// ConnectMailbox proves an IMAP connection works, then stores it.
func (h *Handler) ConnectMailbox(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var req models.ConnectMailboxRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	address := strings.TrimSpace(req.Address)
	if address == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "Adresse et mot de passe requis.")
		return
	}
	// Enforce an upper bound on password length to prevent CPU/memory exhaustion (DoS)
	// during AES encryption and network transport operations.
	if len(req.Password) > 1024 {
		writeError(w, http.StatusBadRequest, "Mot de passe trop long.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), connectTimeout)
	defer cancel()

	account, err := h.connectAndStore(ctx, userEmail, address, req.Password)
	if err != nil {
		writeMailboxConnectError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, account)
}

// GetMailbox returns the connected mailbox, or a null one when there is none.
//
// The sealed password is never in the response: models.MailAccount tags it
// `json:"-"`, and a test pins that tag because it is one keystroke away from
// putting a credential on the wire.
func (h *Handler) GetMailbox(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var account models.MailAccount
	err := h.db.MailAccounts().FindOne(ctx, bson.M{"userId": userEmail}).Decode(&account)
	if err == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"mailbox": account})
		return
	}

	// Check if user has direct Google credentials (e.g. legacy or direct OAuth login)
	var user models.User
	if uErr := h.db.Users().FindOne(ctx, bson.M{"email": userEmail}).Decode(&user); uErr == nil {
		if user.AccessToken != "" || user.RefreshToken != "" {
			writeJSON(w, http.StatusOK, map[string]interface{}{
				"mailbox": models.MailAccount{
					UserID:    userEmail,
					Provider:  "google",
					Transport: "gmail",
					Username:  user.Email,
					Host:      "imap.gmail.com",
					Port:      993,
					TLS:       "tls",
				},
			})
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"mailbox": nil})
}

// DisconnectMailbox forgets the connection, app password or OAuth tokens included.
//
// Deleting the row and unsetting tokens is the whole operation: credentials only
// exist here, so this is also how a user revokes Mailsorter's access without going
// through their provider. Nothing is done to the mailbox itself.
func (h *Handler) DisconnectMailbox(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	// What Mailsorter kept of an IMAP mailbox goes with it, first: a failure
	// leaves it connected, and disconnecting again clears it. A mailbox
	// reached through the Gmail API keeps its listing, as before.
	transport, _, err := h.transportFor(ctx, userEmail)
	if err != nil && !errors.Is(err, errNoMailboxConnected) {
		log.Printf("mailbox: disconnect %s, which transport: %v", userEmail, err)
		writeError(w, http.StatusInternalServerError, "Impossible de déconnecter la boîte.")
		return
	}
	if transport == provider.TransportIMAP {
		if err := h.clearMailboxCopy(ctx, userEmail); err != nil {
			log.Printf("mailbox: disconnect %s: %v", userEmail, err)
			writeError(w, http.StatusInternalServerError, "Impossible de déconnecter la boîte.")
			return
		}
	}
	res, err := h.db.MailAccounts().DeleteOne(ctx, bson.M{"userId": userEmail})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Impossible de déconnecter la boîte.")
		return
	}

	// Also clear Google OAuth credentials on the user document if any
	_, _ = h.db.Users().UpdateOne(ctx,
		bson.M{"email": userEmail},
		bson.M{
			"$unset": bson.M{"accessToken": "", "refreshToken": "", "tokenExpiry": ""},
			"$set":   bson.M{"updatedAt": time.Now()},
		},
	)

	writeJSON(w, http.StatusOK, map[string]interface{}{"disconnected": res.DeletedCount > 0 || true})
}

// openMailbox opens the caller's stored mailbox for work.
//
// It is the IMAP counterpart of getUserToken, and the only way the rest of the
// app reaches a connection: nothing else reads the secret column, but
// sendThroughBox below, which logs in to the same mailbox's sending server.
// The caller closes the client, one connection per unit of work, because an
// IMAP session is stateful and cannot be shared between goroutines.
func (h *Handler) openMailbox(ctx context.Context, userEmail string) (*imap.Client, error) {
	var account models.MailAccount
	if err := h.db.MailAccounts().FindOne(ctx, bson.M{"userId": userEmail}).Decode(&account); err != nil {
		return nil, err
	}
	password, err := h.openSecret(account.Secret)
	if err != nil {
		return nil, err
	}
	return imap.Connect(ctx, imap.Credentials{
		Endpoint: provider.Endpoint{
			Host: account.Host,
			Port: account.Port,
			TLS:  provider.TLSMode(account.TLS),
		},
		Username: account.Username,
		Password: password,
	})
}

// sendThroughBox hands a message to the sending server of a mailbox reached
// over IMAP, logged in with the address and the app password the mailbox was
// connected with, from that address to that address: the daily recap, a
// message the person sends to themselves. Here, beside openMailbox, because
// those two are the only readers of the sealed app password.
func (h *Handler) sendThroughBox(ctx context.Context, account models.MailAccount, message []byte) error {
	server, err := provider.SendingServer(provider.Endpoint{
		Host: account.SMTPHost,
		Port: account.SMTPPort,
		TLS:  provider.TLSMode(account.SMTPTLS),
	}, account.Provider, Edition)
	if err != nil {
		return err
	}
	password, err := h.openSecret(account.Secret)
	if err != nil {
		return err
	}
	return smtpsend.Send(ctx, smtpsend.Credentials{
		Endpoint: server,
		Username: account.Username,
		Password: password,
	}, account.Username, []string{account.Username}, message)
}
