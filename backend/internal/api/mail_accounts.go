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
	"go.mongodb.org/mongo-driver/bson/primitive"
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

	if err := proveMailbox(ctx, *route.IMAP, address, password); err != nil {
		return models.MailAccount{}, err
	}

	sealed, err := h.sealSecret(password)
	if err != nil {
		return models.MailAccount{}, fmt.Errorf("seal mailbox secret: %w", err)
	}

	account, update := mailboxRow(userEmail, p.Key, address, route, sealed, time.Now())
	if _, err := h.db.MailAccounts().UpdateOne(ctx,
		bson.M{"userId": userEmail},
		update,
		options.Update().SetUpsert(true),
	); err != nil {
		return models.MailAccount{}, fmt.Errorf("store mail account: %w", err)
	}
	return account, nil
}

// proveMailbox opens, and closes, a session on endpoint with these
// credentials: the proof a connection is stored on.
func proveMailbox(ctx context.Context, endpoint provider.Endpoint, address, password string) error {
	client, err := imap.Connect(ctx, imap.Credentials{
		Endpoint: endpoint,
		Username: address,
		Password: password,
	})
	if err != nil {
		// A rejected password is the user's to fix and says so; anything else is
		// the server or the provider being unreachable, and saying "wrong
		// password" there would send them to regenerate a perfectly good one.
		if errors.Is(err, imap.ErrAuth) {
			return errMailboxRejected
		}
		return fmt.Errorf("%w: %v", errMailboxUnreachable, err)
	}
	client.Close()
	return nil
}

// connectedIMAPMailbox is the mailbox this account has connected over IMAP,
// or nil when it has none (no row, or a row of another transport). A store
// that cannot answer is an error, never "none": that would take a new
// password for a new mailbox.
func (h *Handler) connectedIMAPMailbox(ctx context.Context, userEmail string) (*models.MailAccount, error) {
	var account models.MailAccount
	err := h.db.MailAccounts().FindOne(ctx, bson.M{"userId": userEmail}).Decode(&account)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the connected mailbox: %w", err)
	}
	if account.Transport != string(provider.TransportIMAP) || account.Host == "" {
		return nil, nil
	}
	return &account, nil
}

// replacePassword gives the mailbox connected over IMAP a new app password:
// "Changer le mot de passe d'application" on /connect, or the answer to a
// password the provider now refuses. The new password is proved against the
// server the mailbox was connected on (its stored host, never one from the
// request, and not the catalog's again, which would make it a new
// connection), then only it is replaced: the row, its creation, its server and
// its sending server stay, and with them everything that belongs to that
// mailbox (the stored copy of its inbox, the proposals, the history and what
// "Annuler" can still bring back). A password the provider refuses replaces
// nothing.
func (h *Handler) replacePassword(ctx context.Context, stored models.MailAccount, password string) (models.MailAccount, error) {
	endpoint := provider.Endpoint{Host: stored.Host, Port: stored.Port, TLS: provider.TLSMode(stored.TLS)}
	if err := proveMailbox(ctx, endpoint, stored.Username, password); err != nil {
		return models.MailAccount{}, err
	}
	sealed, err := h.sealSecret(password)
	if err != nil {
		return models.MailAccount{}, fmt.Errorf("seal mailbox secret: %w", err)
	}
	now := time.Now()
	// Only the mailbox the password was proved on: one disconnected, or
	// replaced by another, while it was being tried matches nothing.
	filter := bson.M{"userId": stored.UserID, "username": stored.Username, "host": stored.Host}
	if oid, err := primitive.ObjectIDFromHex(stored.ID); err == nil {
		filter["_id"] = oid
	}
	res, err := h.db.MailAccounts().UpdateOne(ctx, filter,
		bson.M{"$set": bson.M{"secret": sealed, "updatedAt": now}},
	)
	if err != nil {
		return models.MailAccount{}, fmt.Errorf("store the new password: %w", err)
	}
	if res.MatchedCount == 0 {
		return models.MailAccount{}, errNoMailboxConnected
	}
	stored.UpdatedAt = now
	return stored, nil
}

// mailboxRow is what a connection proved over route becomes: the account
// connectAndStore answers with, and the upsert that writes it over the user's
// previous row, sealed password included. The sending server comes from the
// same route, so the recap leaves from the provider whose IMAP server was
// just proved. A route without one clears what a mailbox connected before may
// have left on the row, rather than keep another provider's server.
func mailboxRow(userEmail, providerKey, address string, route provider.Route, sealed string, now time.Time) (models.MailAccount, bson.M) {
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
	update := bson.M{
		"$set":         set,
		"$setOnInsert": bson.M{"userId": userEmail, "createdAt": now},
	}
	if route.SMTP != nil {
		account.SMTPHost = route.SMTP.Host
		account.SMTPPort = route.SMTP.Port
		account.SMTPTLS = string(route.SMTP.TLS)
		set["smtpHost"] = account.SMTPHost
		set["smtpPort"] = account.SMTPPort
		set["smtpTls"] = account.SMTPTLS
	} else {
		update["$unset"] = bson.M{"smtpHost": "", "smtpPort": "", "smtpTls": ""}
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
		// 401, as the credentials were just typed here; mailboxRejected
		// tells the SPA this is the provider refusing them, not a dead
		// Mailsorter session, which any other 401 is and which signs out.
		writeJSON(w, http.StatusUnauthorized, map[string]interface{}{
			"error": mailboxRejectedMessage, "status": http.StatusUnauthorized, "mailboxRejected": true,
		})
	case errors.Is(err, errMailboxUnreachable):
		writeError(w, http.StatusBadGateway, mailboxNoAnswerMessage)
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

	// Every attempt here is a LOGIN at the provider: throttled before it,
	// as the mailbox sign-in is.
	if !h.allowMailboxLogin(w, userEmail, address) {
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

// ChangeMailboxPassword gives the mailbox connected over IMAP a new app
// password (replacePassword): "Changer le mot de passe d'application" on
// /connect, and where "Mettre à jour" leads when the provider refuses the one
// on file. Connecting the address again through /api/mailbox/connect would
// re-derive the mailbox from the catalog; this keeps it, and can reach no
// other mailbox (the body has no address). Failures answer as the connect
// route does (writeMailboxConnectError), on the screen where the password was
// just typed: 401 refused, 502 no answer; 428 without a mailbox over IMAP.
func (h *Handler) ChangeMailboxPassword(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "Connexion requise.")
		return
	}
	var req models.MailboxPasswordRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Password == "" {
		writeError(w, http.StatusBadRequest, "Mot de passe requis.")
		return
	}
	// The same bound as the connect route, for the same reason.
	if len(req.Password) > 1024 {
		writeError(w, http.StatusBadRequest, "Mot de passe trop long.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), connectTimeout)
	defer cancel()

	transport, _, err := h.transportFor(ctx, userEmail)
	switch {
	case errors.Is(err, errNoMailboxConnected):
		writeError(w, http.StatusPreconditionRequired, noMailboxMessage)
		return
	case err != nil:
		log.Printf("mailbox password of %s: %v", userEmail, err)
		writeError(w, http.StatusInternalServerError, "Impossible d'enregistrer la connexion.")
		return
	case transport == provider.TransportGmailAPI:
		// Google holds no app password for Mailsorter: there is nothing
		// to change here.
		writeError(w, http.StatusUnprocessableEntity, googleMailboxPasswordMessage)
		return
	}
	stored, err := h.connectedIMAPMailbox(ctx, userEmail)
	if err != nil {
		log.Printf("mailbox password of %s: %v", userEmail, err)
		writeError(w, http.StatusInternalServerError, "Impossible d'enregistrer la connexion.")
		return
	}
	if stored == nil {
		writeError(w, http.StatusPreconditionRequired, noMailboxMessage)
		return
	}
	// Every attempt here is a LOGIN at the provider: throttled before it.
	if !h.allowMailboxLogin(w, userEmail, stored.Username) {
		return
	}
	account, err := h.replacePassword(ctx, *stored, req.Password)
	switch {
	case errors.Is(err, errNoMailboxConnected):
		// Disconnected, or replaced by another mailbox, while the password
		// was being tried: nothing was stored, and nothing is announced.
		writeError(w, http.StatusPreconditionRequired, noMailboxMessage)
	case err != nil:
		if !errors.Is(err, errMailboxRejected) {
			log.Printf("mailbox password of %s: %v", userEmail, err)
		}
		writeMailboxConnectError(w, err)
	default:
		writeJSON(w, http.StatusOK, account)
	}
}

// googleMailboxPasswordMessage is what PUT /api/mailbox/password answers a
// mailbox reached through the Gmail API.
const googleMailboxPasswordMessage = "Cette boîte est branchée par Google : son mot de passe ne se change pas ici."

// allowMailboxLogin throttles the routes of a signed-in person that try a
// password at their mail provider (connect, new password), before any LOGIN,
// with the mailbox sign-in's limiter and its rate (signInRatePerSec,
// signInBurst): keyed on the account and on the address tried, so neither
// changing the address nor using another account on the same address gets a
// fresh bucket. Over it, 429 with Retry-After, and the provider is not asked.
func (h *Handler) allowMailboxLogin(w http.ResponseWriter, userEmail, address string) bool {
	limiter := h.signInLimiter()
	if !limiter.allow("user:"+strings.ToLower(userEmail)) || !limiter.allow("addr:"+strings.ToLower(strings.TrimSpace(address))) {
		w.Header().Set("Retry-After", "3")
		writeError(w, http.StatusTooManyRequests, "Trop de tentatives. Patientez quelques instants.")
		return false
	}
	return true
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
