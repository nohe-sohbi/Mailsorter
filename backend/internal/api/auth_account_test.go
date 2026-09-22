package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
)

func TestRegisterAndLoginValidation(t *testing.T) {
	h := newTestHandler(t)

	// Test invalid email
	{
		body, _ := json.Marshal(models.RegisterRequest{
			Email:    "notanemail",
			Password: "password123",
		})
		req := httptest.NewRequest("POST", "/api/auth/register", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		h.Register(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("Register with invalid email returned %d, want %d", rec.Code, http.StatusBadRequest)
		}
	}

	// Test short password (< 8 chars)
	{
		body, _ := json.Marshal(models.RegisterRequest{
			Email:    "test@example.com",
			Password: "short",
		})
		req := httptest.NewRequest("POST", "/api/auth/register", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		h.Register(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("Register with short password returned %d, want %d", rec.Code, http.StatusBadRequest)
		}
	}

	// Test login with empty fields
	{
		body, _ := json.Marshal(models.LoginRequest{
			Email:    "",
			Password: "",
		})
		req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		h.Login(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("Login with empty fields returned %d, want %d", rec.Code, http.StatusBadRequest)
		}
	}
}

func TestWriteAuthErrorPreconditionRequired(t *testing.T) {
	rec := httptest.NewRecorder()
	writeAuthError(rec, errNoMailboxConnected)
	if rec.Code != http.StatusPreconditionRequired {
		t.Errorf("writeAuthError(errNoMailboxConnected) = %d, want %d", rec.Code, http.StatusPreconditionRequired)
	}
}
