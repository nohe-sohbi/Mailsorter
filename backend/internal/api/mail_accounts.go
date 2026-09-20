package api

import (
	"context"
	"errors"
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

	// Resolved, not received. Detect always answers (it falls back to the
	// generic IMAP entry), so the failure below is about the EDITION rather
	// than about an unknown provider.
	p := provider.Detect(address)
	route, err := provider.Pick(p.Key, Edition)
	if err != nil || route.IMAP == nil {
		writeError(w, http.StatusUnprocessableEntity,
			"Cette boîte ne se connecte pas par IMAP sur cette instance.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), connectTimeout)
	defer cancel()

	client, err := imap.Connect(ctx, imap.Credentials{
		Endpoint: *route.IMAP,
		Username: address,
		Password: req.Password,
	})
	if err != nil {
		// A rejected password is the user's to fix and says so; anything else is
		// the server or the provider being unreachable, and saying "wrong
		// password" there would send them to regenerate a perfectly good one.
		if errors.Is(err, imap.ErrAuth) {
			writeError(w, http.StatusUnauthorized,
				"Identifiants refusés par le fournisseur. Vérifiez le mot de passe d'application.")
			return
		}
		writeError(w, http.StatusBadGateway, "Connexion au serveur de mail impossible.")
		return
	}
	client.Close()

	sealed, err := h.sealSecret(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Impossible d'enregistrer la connexion.")
		return
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
		UpdatedAt: now,
	}

	_, err = h.db.MailAccounts().UpdateOne(ctx,
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
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Impossible d'enregistrer la connexion.")
		return
	}

	account.CreatedAt = now
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
