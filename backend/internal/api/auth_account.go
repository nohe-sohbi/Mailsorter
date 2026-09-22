package api

import (
	"context"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/crypto/bcrypt"
)

// isValidEmail checks basic RFC 5322 address syntax and structure.
func isValidEmail(email string) bool {
	if len(email) < 3 || len(email) > 254 {
		return false
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return false
	}
	parts := strings.Split(email, "@")
	if len(parts) != 2 || !strings.Contains(parts[1], ".") {
		return false
	}
	return true
}

// Register creates a new independent MailSorter account with an email and password.
// This decouples account creation from mail provider linking.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req models.RegisterRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	password := req.Password

	if !isValidEmail(email) {
		writeError(w, http.StatusBadRequest, "Adresse email invalide.")
		return
	}

	if len(password) < 8 {
		writeError(w, http.StatusBadRequest, "Le mot de passe doit comporter au moins 8 caractères.")
		return
	}

	limiter := h.signInLimiter()
	if !limiter.allow(clientKey(r)) || !limiter.allow("reg:"+email) {
		w.Header().Set("Retry-After", "3")
		writeError(w, http.StatusTooManyRequests, "Trop de tentatives. Patientez quelques instants.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var existing models.User
	err := h.db.Users().FindOne(ctx, bson.M{"email": email}).Decode(&existing)
	if err == nil {
		writeError(w, http.StatusConflict, "Un compte existe déjà avec cette adresse email.")
		return
	} else if !errors.Is(err, mongo.ErrNoDocuments) {
		writeError(w, http.StatusInternalServerError, "Impossible de vérifier l'adresse email.")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Erreur lors de la sécurisation du mot de passe.")
		return
	}

	now := time.Now()
	user := models.User{
		Email:        email,
		PasswordHash: string(hash),
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if _, err := h.db.Users().InsertOne(ctx, user); err != nil {
		writeError(w, http.StatusInternalServerError, "Impossible de créer le compte.")
		return
	}

	sessionToken := h.auth.IssueSession(email)
	writeJSON(w, http.StatusCreated, models.TokenResponse{
		AccessToken: sessionToken,
		UserEmail:   email,
	})
}

// Login authenticates a MailSorter user with their email and password.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req models.LoginRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	email := strings.ToLower(strings.TrimSpace(req.Email))
	password := req.Password

	if email == "" || password == "" {
		writeError(w, http.StatusBadRequest, "Adresse email et mot de passe requis.")
		return
	}

	limiter := h.signInLimiter()
	if !limiter.allow(clientKey(r)) || !limiter.allow("login:"+email) {
		w.Header().Set("Retry-After", "3")
		writeError(w, http.StatusTooManyRequests, "Trop de tentatives. Patientez quelques instants.")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	var user models.User
	err := h.db.Users().FindOne(ctx, bson.M{"email": email}).Decode(&user)
	if errors.Is(err, mongo.ErrNoDocuments) {
		writeError(w, http.StatusUnauthorized, "Identifiants incorrects.")
		return
	} else if err != nil {
		writeError(w, http.StatusInternalServerError, "Erreur lors de la connexion.")
		return
	}

	if user.PasswordHash == "" {
		writeError(w, http.StatusBadRequest, "Ce compte utilise la connexion Google. Utilisez le bouton Continuer avec Google.")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		writeError(w, http.StatusUnauthorized, "Identifiants incorrects.")
		return
	}

	sessionToken := h.auth.IssueSession(email)
	writeJSON(w, http.StatusOK, models.TokenResponse{
		AccessToken: sessionToken,
		UserEmail:   email,
	})
}
