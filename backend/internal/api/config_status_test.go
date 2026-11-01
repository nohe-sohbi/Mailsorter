package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/nohe-sohbi/mailsorter/backend/internal/config"
	"github.com/nohe-sohbi/mailsorter/backend/internal/digest"
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

// The digest is the one thing the server draws itself, and it follows the
// same switch as the SPA: a hotel instance mails a hotel recap.
func TestDigestStyleFollowsTheUITheme(t *testing.T) {
	cases := []struct {
		theme config.UITheme
		want  digest.Style
	}{
		{config.UIThemeHotel, digest.StyleHotel},
		{config.UIThemeClassic, digest.StyleClassic},
	}
	for _, tc := range cases {
		withUITheme(t, tc.theme, func() {
			if got := digestStyle(); got != tc.want {
				t.Errorf("digestStyle() with UI_THEME=%q = %q, want %q", tc.theme, got, tc.want)
			}
		})
	}
}
