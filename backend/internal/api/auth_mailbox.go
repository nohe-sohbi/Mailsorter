package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Signing in with a mailbox, which is also how an account is created.
//
// Before this route the only way to obtain a session was Google's OAuth
// callback, which made the hosted edition impossible to enter: it cannot reach
// the Gmail API at all (see internal/provider on the 100-authorization cap), so
// its users had no first door.
//
// The decision that shapes everything here: connecting a mailbox IS
// registering. There is no separate sign-up, no password of ours to hash, no
// reset flow, no verification email and no SMTP to run, because Mailsorter
// never holds a credential it invented. The mail server is the authority: if it
// accepts the address and the app password, the person is who they say they
// are, and that is a stronger proof than a confirmation link. Signing in again
// later is the same request with the same body.
//
// What that buys, and what it costs:
//
//   - No account exists that its mailbox does not. There is nothing to recover,
//     because losing the app password means regenerating it at the provider,
//     which is where it was issued.
//   - Rotating the app password at the provider is a re-entry here, not a
//     support ticket: the next sign-in stores the new one.
//   - The address is the identity, exactly as it already was everywhere else
//     (X-User-Email is the userId).

// signInLimit is deliberately far below the global limiter.
//
// This route is the one place an unauthenticated caller makes the server try a
// password against somebody else's mail provider. At the global 20 r/s that is
// a credential-stuffing engine with our IP on the outside of it, and the
// provider would rightly start refusing us for every user. One attempt every
// three seconds, five in reserve, is generous for a person typing and useless
// for a machine guessing.
const (
	signInRatePerSec = 1.0 / 3.0
	signInBurst      = 5
)

// SignInWithMailbox proves an address and an app password against the provider,
// stores the connection, and hands back a session.
func (h *Handler) SignInWithMailbox(w http.ResponseWriter, r *http.Request) {
	var req models.ConnectMailboxRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	address := strings.ToLower(strings.TrimSpace(req.Address))
	if address == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "Adresse et mot de passe requis.")
		return
	}

	// Keyed on the address as well as the caller, so neither rotating addresses
	// from one machine nor hammering one address from many gets a free pass.
	limiter := h.signInLimiter()
	if !limiter.allow(clientKey(r)) || !limiter.allow("addr:"+address) {
		w.Header().Set("Retry-After", "3")
		writeError(w, http.StatusTooManyRequests,
			"Trop de tentatives. Patientez quelques instants.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), connectTimeout)
	defer cancel()

	// Fail closed on an address that already signs in through Google. Their
	// Gmail token is on file and a stored mailbox row would take precedence
	// over it (see transportFor), so this would silently move the account to
	// IMAP for whoever holds the app password. Switching is still possible, but
	// from inside the app, where proving control of the Google account came
	// first.
	googleLinked, err := h.hasGoogleCredentials(ctx, address)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Connexion impossible pour le moment.")
		return
	}
	if googleLinked {
		writeError(w, http.StatusConflict,
			"Cette adresse est déjà reliée à Google. Connectez-vous avec Google, "+
				"puis branchez la boîte depuis les réglages.")
		return
	}

	if _, err := h.connectAndStore(ctx, address, address, req.Password); err != nil {
		writeMailboxConnectError(w, err)
		return
	}

	// The account itself. It is an upsert because this one request is both the
	// first sign-up and every sign-in after it, and neither is allowed to
	// overwrite what the other put there.
	now := time.Now()
	if _, err := h.db.Users().UpdateOne(ctx,
		bson.M{"email": address},
		bson.M{
			"$set":         bson.M{"updatedAt": now},
			"$setOnInsert": bson.M{"email": address, "createdAt": now},
		},
		options.Update().SetUpsert(true),
	); err != nil {
		writeError(w, http.StatusInternalServerError, "Impossible de créer le compte.")
		return
	}

	writeJSON(w, http.StatusOK, models.TokenResponse{
		AccessToken: h.auth.IssueSession(address),
		UserEmail:   address,
	})
}

// hasGoogleCredentials reports whether this address already holds a stored
// Google grant.
//
// A missing user is not an error: that is the sign-up case, which is most of
// the traffic on this route. Anything else IS an error and the caller must fail
// on it, for the same reason transportFor refuses to guess: answering "no
// Google" on a datastore hiccup would let the guard above be walked past by
// retrying until Mongo blinked.
func (h *Handler) hasGoogleCredentials(ctx context.Context, address string) (bool, error) {
	var user models.User
	err := h.db.Users().FindOne(ctx, bson.M{"email": address},
		options.FindOne().SetProjection(bson.M{"refreshToken": 1, "accessToken": 1}),
	).Decode(&user)
	switch {
	case err == nil:
		return user.RefreshToken != "" || user.AccessToken != "", nil
	case errors.Is(err, mongo.ErrNoDocuments):
		return false, nil
	default:
		return false, err
	}
}
