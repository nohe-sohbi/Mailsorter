package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/config"
)

func withUITheme(t *testing.T, theme config.UITheme, fn func()) {
	t.Helper()
	previous := UITheme
	UITheme = theme
	defer func() { UITheme = previous }()
	fn()
}

// The SPA picks its landing from this field at boot, before any login, so the
// theme can be switched by changing UI_THEME and restarting, without a rebuild.
func TestConfigStatusCarriesTheUITheme(t *testing.T) {
	for _, theme := range []config.UITheme{config.UIThemeHotel, config.UIThemeClassic} {
		withUITheme(t, theme, func() {
			rec := httptest.NewRecorder()
			newTestHandler(t).GetConfigStatus(rec, httptest.NewRequest(http.MethodGet, "/api/config/status", nil))

			var body map[string]interface{}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode /api/config/status: %v", err)
			}
			if body["uiTheme"] != string(theme) {
				t.Errorf("uiTheme = %#v with UI_THEME=%q, want %q", body["uiTheme"], theme, theme)
			}
		})
	}
}
