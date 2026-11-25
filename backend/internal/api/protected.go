package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"github.com/nohe-sohbi/mailsorter/backend/internal/protect"
	"github.com/nohe-sohbi/mailsorter/backend/internal/provider"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// errProtectedUnreadable is a protected list that could not be read.
var errProtectedUnreadable = errors.New("api: the protected senders could not be read")

// protectedUnreadableMessage is what a call that would move mail in bulk or
// on its own answers when the protected list cannot be read (503).
const protectedUnreadableMessage = "Vos expéditeurs protégés n'ont pas pu être lus : rien n'a été rangé, réessayez dans un instant."

// protectedValues returns the caller's protected entries (addresses and
// domains) made ready to read the mails of this call with (protect.Shield:
// each entry put in the form senders are compared in once, not once per
// mail), consulted before any automated or grouped destructive action.
//
// A list that cannot be read is an error (errProtectedUnreadable), never an
// empty shield: read as "nobody is protected", it let the selection, the AI,
// the rules, the auto-pilot and the sweeps archive or trash a protected
// sender's mail whenever the datastore stumbled. The caller moves nothing
// then (writeProtectedUnreadable).
func (h *Handler) protectedValues(ctx context.Context, userEmail string) (protect.Shield, error) {
	cursor, err := h.db.ProtectedSenders().Find(ctx, bson.M{"userId": userEmail})
	if err != nil {
		return protect.Shield{}, fmt.Errorf("%w: %w", errProtectedUnreadable, err)
	}
	defer cursor.Close(ctx)

	var rows []models.ProtectedSender
	if err := cursor.All(ctx, &rows); err != nil {
		return protect.Shield{}, fmt.Errorf("%w: %w", errProtectedUnreadable, err)
	}
	values := make([]string, 0, len(rows))
	for _, r := range rows {
		values = append(values, r.Value)
	}
	return protect.NewShield(values), nil
}

// readSender sets, on an email on its way to a screen, what the screens show
// for its sender, out of its From: the address read (protect.Address), as
// the reader shows it (protect.ShownAddress), and the label of the list
// (protect.Label).
func readSender(e *models.Email) {
	e.SenderAddress = protect.Address(e.From)
	e.SenderAddressShown = protect.ShownAddress(e.SenderAddress)
	e.SenderLabel = protect.Label(e.From)
}

// shownSenderAddress is what a sender card shows as the address of a From:
// the address read in the one form addresses are shown in
// (protect.ShownAddress), or the From's text, made visible, when it names no
// sender (protect.Label).
func shownSenderAddress(from string) string {
	if address := protect.Address(from); address != "" {
		return protect.ShownAddress(address)
	}
	return protect.Label(from)
}

// writeProtectedUnreadable answers a call that would have moved mail while
// the protected list could not be read: 503, nothing moved.
func writeProtectedUnreadable(w http.ResponseWriter, userEmail string, err error) {
	log.Printf("protected: nothing moved for %s: %v", userEmail, err)
	writeError(w, http.StatusServiceUnavailable, protectedUnreadableMessage)
}

// allows reports whether action may be applied to an email from `from` given the
// user's protected list. A small wrapper so callers read naturally.
func allows(action, from string, protectedList protect.Shield) bool {
	return protectedList.Allows(action, from)
}

// allowsOn is allows for a mailbox on this transport. Over IMAP a label is a
// folder, and filing a mail there takes it out of the inbox the way an
// archive does, so the shield reads it as one (protect.AllowedOnFolders).
func allowsOn(transport provider.Transport, action, from string, protectedList protect.Shield) bool {
	if transport == provider.TransportIMAP {
		return protectedList.AllowsOnFolders(action, from)
	}
	return protectedList.Allows(action, from)
}

// GetProtected lists the caller's protected senders.
func (h *Handler) GetProtected(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	cursor, err := h.db.ProtectedSenders().Find(ctx, bson.M{"userId": userEmail},
		options.Find().SetSort(bson.M{"createdAt": -1}))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to load protected senders")
		return
	}
	defer cursor.Close(ctx)

	rows := make([]models.ProtectedSender, 0)
	if err := cursor.All(ctx, &rows); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to decode protected senders")
		return
	}
	for i := range rows {
		rows[i].ValueShown = protect.ShownEntry(rows[i].Value)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{"protected": rows})
}

// Maximum input bounds for protected sender entries to prevent resource exhaustion / DoS.
const (
	maxProtectedValueLength = 254 // RFC 5321 maximum address length
	maxProtectedNoteLength  = 500 // Upper bound for user note
)

// unreadableSenderProtectMessage is "Protéger" on a mail whose From names no
// sender (protect.Address): nothing can be protected from it.
const unreadableSenderProtectMessage = "L'adresse de cet expéditeur est illisible : il ne peut pas être protégé depuis ce mail."

// unprotectableDomainMessage is "Protéger" on a mail whose sender writes a
// domain IDNA refuses (protect.EntryFromMail): stored as written, it would
// show in the settings as what it is not.
const unprotectableDomainMessage = "Le domaine de cet expéditeur n'est pas un nom de domaine valide : il ne peut pas être protégé depuis ce mail."

// CreateProtected adds a sender (full address or whole domain) to the protected
// list. The value is normalized and classified server-side; duplicates are a
// no-op thanks to the unique {userId, value} index.
func (h *Handler) CreateProtected(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	var in models.ProtectedSenderInput
	if !decodeJSON(w, r, &in) {
		return
	}

	// Two ways in. From a mail, the sender's address exactly as the shield
	// will read the next mails (protect.EntryFromMail): retouched (an
	// apostrophe trimmed, a quote dropped), it protected nobody while the
	// screen said "protégé". Typed in the settings, a value normalized and
	// classified (protect.NormalizeEntry).
	var value, kind string
	if in.FromMail {
		value, kind = protect.EntryFromMail(in.Value), protect.KindAddress
		switch {
		case protect.Address(in.Value) == "":
			writeError(w, http.StatusBadRequest, unreadableSenderProtectMessage)
			return
		case value == "":
			writeError(w, http.StatusBadRequest, unprotectableDomainMessage)
			return
		}
	} else {
		value, kind = protect.NormalizeEntry(in.Value)
	}
	if value == "" || len(value) > maxProtectedValueLength {
		writeError(w, http.StatusBadRequest, "Adresse ou domaine invalide")
		return
	}
	if len(in.Note) > maxProtectedNoteLength {
		writeError(w, http.StatusBadRequest, "Note trop longue")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	now := time.Now()
	res, err := h.db.ProtectedSenders().UpdateOne(ctx,
		bson.M{"userId": userEmail, "value": value},
		bson.M{
			"$set":         bson.M{"kind": kind, "note": in.Note},
			"$setOnInsert": bson.M{"userId": userEmail, "value": value, "createdAt": now},
		},
		options.Update().SetUpsert(true),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Impossible d'enregistrer l'expéditeur protégé.")
		return
	}

	entry := models.ProtectedSender{UserID: userEmail, Value: value, Kind: kind, Note: in.Note, CreatedAt: now, ValueShown: protect.ShownEntry(value)}
	if oid, ok := res.UpsertedID.(primitive.ObjectID); ok {
		entry.ID = oid.Hex()
	}

	writeJSON(w, http.StatusCreated, entry)
}

// DeleteProtected removes a protected sender the caller owns.
func (h *Handler) DeleteProtected(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		writeError(w, http.StatusUnauthorized, "User email required")
		return
	}

	oid, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		writeError(w, http.StatusBadRequest, "Invalid id")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	res, err := h.db.ProtectedSenders().DeleteOne(ctx, bson.M{"_id": oid, "userId": userEmail})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to delete")
		return
	}
	if res.DeletedCount == 0 {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
