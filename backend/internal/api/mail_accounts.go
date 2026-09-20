package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/imap"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"go.mongodb.org/mongo-driver/bson"
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
	route, err := provider.Pick(p.Key, Edition)
	if err != nil || route.IMAP == nil {
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

	now := time.Now()
	account := models.MailAccount{
		UserID:    userEmail,
		Provider:  p.Key,
		Transport: string(route.Transport),
		Username:  address,
		Host:      route.IMAP.Host,
		Port:      route.IMAP.Port,
		TLS:       string(route.IMAP.TLS),
		CreatedAt: now,
		UpdatedAt: now,
	}

	if _, err := h.db.MailAccounts().UpdateOne(ctx,
		bson.M{"userId": userEmail},
		bson.M{
			"$set": bson.M{
				"provider":  account.Provider,
				"transport": account.Transport,
				"username":  account.Username,
				"secret":    sealed,
				"host":      account.Host,
				"port":      account.Port,
				"tls":       account.TLS,
				"updatedAt": now,
			},
			"$setOnInsert": bson.M{"userId": userEmail, "createdAt": now},
		},
		options.Update().SetUpsert(true),
	); err != nil {
		return models.MailAccount{}, fmt.Errorf("store mail account: %w", err)
	}
	return account, nil
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
	if err != nil {
		// Nothing connected is a normal state, not an error: it is what every
		// account looks like before the connect screen is used.
		writeJSON(w, http.StatusOK, map[string]interface{}{"mailbox": nil})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"mailbox": account})
}

// DisconnectMailbox forgets the connection, app password included.
//
// Deleting the row is the whole operation: the password only exists here, so
// this is also how a user revokes Mailsorter's access without going through
// their provider. Nothing is done to the mailbox itself.
func (h *Handler) DisconnectMailbox(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	res, err := h.db.MailAccounts().DeleteOne(ctx, bson.M{"userId": userEmail})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Impossible de déconnecter la boîte.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"disconnected": res.DeletedCount > 0})
}

// openMailbox opens the caller's stored mailbox for work.
//
// It is the IMAP counterpart of getUserToken, and the only way the rest of the
// app reaches a connection: nothing else reads the secret column. The caller
// closes the client, one connection per unit of work, because an IMAP session
// is stateful and cannot be shared between goroutines.
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
