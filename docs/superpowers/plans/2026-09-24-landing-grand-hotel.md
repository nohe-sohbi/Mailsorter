# Landing "Grand Hôtel" et thème branchable : plan d'implémentation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Servir sur `/` une nouvelle landing "Grand Hôtel" (hôtel en coupe dessiné en SVG, démo de tri, couloir de portes, coffre, tableau à clés, tarifs, questions, façade de nuit), débranchable sans rebuild par `UI_THEME=classic`, avec la moodboard qui documente le système visuel.

**Architecture:** Le backend lit `UI_THEME` (validé au démarrage comme `EDITION`) et le renvoie dans `GET /api/config/status`. Le SPA choisit à l'exécution entre deux landings chargées à la demande (`React.lazy`) : `pages/HotelLanding.js` et l'ancienne `pages/Login.js`, qui partagent un hook d'authentification. Le thème vit dans une feuille confinée à `.theme-hotel` (`styles/hotel.css`), les illustrations sont des composants SVG dans `ui/hotel/`, les sections de la landing dans `components/landing/`.

**Tech Stack:** Go 1.21 (`net/http`, tests stdlib), React 18.2 + react-router 6 (CRA 5), CSS brut pour le thème + Tailwind 3.4 pour le reste, vérification navigateur par Playwright (`playwright-core` hors dépôt, Chrome système).

**Spec:** `docs/superpowers/specs/2026-09-24-landing-grand-hotel-design.md`. Maquettes validées : `docs/superpowers/specs/2026-09-24-landing-grand-hotel/landing-mockup.html` et `.../moodboard-mockup.html`. Les maquettes font foi pour le rendu ; la spec fait foi pour les textes.

## Global Constraints

- Travail dans le worktree `/home/user/preprod_projects/Mailsorter/.claude/worktrees/landing-grand-hotel`, branche `claude/landing-grand-hotel`. Ne jamais toucher au checkout principal (l'utilisateur y travaille sur `main`). Ne jamais pousser.
- Git : appeler `/usr/bin/git` (le hook RTK réécrit `git` d'une façon que l'isolation du worktree refuse). Une commande git par appel Bash, sans `&&` ni heredoc.
- Go : `export PATH="$HOME/go/bin:$PATH"` avant toute commande `go`. Le backend se teste avec `cd backend && go test -race ./...`.
- Aucune dépendance ajoutée au dépôt. Aucun framework de test frontend : la vérification frontend est `CI=false npm run build` puis les scénarios Playwright de `.superpowers/landing-check/` (répertoire ignoré par git).
- Ponctuation ASCII partout (code, commentaires, textes, commits) : pas de tiret long ou demi-cadratin, pas de caractère points de suspension, pas de guillemets courbes. Les lettres accentuées sont permises dans les textes affichés (en français).
- Textes affichés en français et au vouvoiement ; identifiants et commentaires en anglais. Ton : franc, avec de l'humour venu de la boîte mail, jamais de l'hôtel. Les verbes des boutons restent simples (Valider, Garder tel quel, Annuler, Se connecter).
- Classes CSS : `ht-` pour les briques du thème (`styles/hotel.css`), `hl-` pour la landing. Aucun nom générique : une feuille chargée par la landing reste chargée après navigation.
- Tokens uniquement sous `.theme-hotel` (et `.dark .theme-hotel` pour la nuit), jamais sur `:root`.
- `UI_THEME` : valeurs `hotel` (défaut) et `classic`, normalisées (minuscules, espaces retirés), toute autre valeur refusée au démarrage. Côté SPA, valeur absente ou inconnue : `classic`. Prévisualisation `?ui=hotel|classic` mémorisée en `sessionStorage` sous `mailsorter_ui_preview`, effacée par `?ui=default`.
- Commits : `type(scope): resume imperatif`, en minuscules, français sans accents, sans point final, terminés par la ligne `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Carte des fichiers

| Fichier | Tâche | Rôle |
|---|---|---|
| `backend/internal/config/config.go`, `config_test.go` | 1 | `UITheme`, `UI_THEME`, validation |
| `backend/internal/models/models.go` | 2 | champ `uiTheme` de `InstanceStatus` |
| `backend/internal/api/handlers.go`, `config_status_test.go`, `routes_integration_test.go` | 2 | variable `UITheme`, statut, tests |
| `backend/cmd/server/main.go` | 2 | fixe `api.UITheme` |
| `docker-compose.yml`, `.env.example`, `docs/API.md` | 2 | `UI_THEME` |
| `frontend/src/lib/uiTheme.js` | 3 | thème effectif et prévisualisation |
| `frontend/src/contexts/InstanceContext.js` | 3 | expose `uiTheme` |
| `frontend/src/App.js` | 3 | choix de la landing sur `/` |
| `frontend/src/pages/HotelLanding.js` | 3, 6 à 11 | la landing Grand Hôtel |
| `.superpowers/landing-check/*` (hors git) | 3 et suivantes | harnais et scénarios Playwright |
| `frontend/src/lib/useAuthForm.js` | 4 | authentification partagée |
| `frontend/src/pages/Login.js` | 4 | landing classique branchée sur le hook |
| `frontend/src/styles/hotel.css` | 5 | tokens, briques `ht-`, illustrations, mouvement |
| `frontend/src/ui/hotel/useHotelFonts.js` | 5 | polices du thème |
| `frontend/tailwind.config.js` | 5 | couleurs `hotel` |
| `docs/design/moodboard.html` | 5 | la moodboard |
| `frontend/src/ui/hotel/Emblem.js`, `ElevatorPanel.js`, `Plaque.js` | 6 | briques d'en-tête |
| `frontend/src/components/landing/landing.css`, `FloorHeading.js`, `HotelHeader.js/.css`, `ElevatorRail.js/.css`, `HotelFooter.js/.css`, `useActiveFloor.js`, `useUnreadCounter.js`, `scroll.js` | 6 | coquille de la landing |
| `frontend/src/components/landing/features.js` | 7 | `GMAIL_ONLY` et les promesses permises |
| `frontend/src/ui/hotel/HotelFacade.js` | 7 | façade de jour et de nuit |
| `frontend/src/components/landing/HallHero.js/.css`, `SignupCard.js/.css`, `NightExit.js/.css` | 7 | hall, fiche, sortie de nuit |
| `frontend/src/ui/hotel/Door.js` | 8 | porte et ses accessoires |
| `frontend/src/components/landing/FeatureCorridor.js/.css` | 8 | couloir des fonctions |
| `frontend/src/components/landing/TriageDemo.js/.css` | 9 | démo de tri |
| `frontend/src/ui/hotel/Vault.js`, `KeyTag.js` | 10 | coffre, clé |
| `frontend/src/components/landing/PrivacyVault.js/.css`, `ProviderBoard.js/.css` | 10 | confidentialité, boîtes compatibles |
| `frontend/src/components/landing/RateCard.js/.css`, `FaqBoard.js/.css` | 11 | tarifs, questions |
| `CLAUDE.md` | 2, 12 | documentation |

---

### Task 1: `UI_THEME` dans la configuration

**Files:**
- Modify: `backend/internal/config/config.go`
- Test: `backend/internal/config/config_test.go`

**Interfaces:**
- Produces: `type config.UITheme string`, constantes `config.UIThemeHotel = "hotel"`, `config.UIThemeClassic = "classic"`, champ `Config.UITheme`, et `Validate()` qui refuse toute autre valeur avec une erreur contenant `UI_THEME`.

- [ ] **Step 1: Écrire les tests qui échouent**

Ajouter à la fin de `backend/internal/config/config_test.go` :

```go
// The theme decides which landing every visitor gets, so a typo must stop the
// boot rather than silently serve the other one.
func TestValidateRejectsAnUnknownUITheme(t *testing.T) {
	base := func() *Config {
		return &Config{EncryptionKey: strings.Repeat("k", minEncryptionKeyLen), Edition: provider.EditionSelfHosted}
	}

	for _, good := range []UITheme{UIThemeHotel, UIThemeClassic} {
		c := base()
		c.UITheme = good
		if err := c.Validate(); err != nil {
			t.Errorf("Validate() with UI_THEME=%q = %v, want nil", good, err)
		}
	}

	for _, bad := range []UITheme{"", "Hotel", "grand-hotel", "clasic"} {
		c := base()
		c.UITheme = bad
		err := c.Validate()
		if err == nil {
			t.Errorf("Validate() with UI_THEME=%q = nil, want an error", bad)
			continue
		}
		if !strings.Contains(err.Error(), "UI_THEME") {
			t.Errorf("Validate() with UI_THEME=%q said %q, want it to name UI_THEME", bad, err)
		}
	}
}

func TestLoadUITheme(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want UITheme
	}{
		{"unset defaults to hotel", "", UIThemeHotel},
		{"hotel", "hotel", UIThemeHotel},
		{"classic", "classic", UIThemeClassic},
		{"case and spaces are normalized", "  Classic ", UIThemeClassic},
		{"an unknown value is kept for Validate to refuse", "retro", UITheme("retro")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("UI_THEME", tc.env)
			if got := Load().UITheme; got != tc.want {
				t.Errorf("Load().UITheme with UI_THEME=%q = %q, want %q", tc.env, got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Vérifier qu'ils échouent**

Run: `export PATH="$HOME/go/bin:$PATH"; cd backend && go test ./internal/config/ -run 'UITheme' -v`
Expected: échec de compilation, `undefined: UITheme` (et `UIThemeHotel`, `UIThemeClassic`).

- [ ] **Step 3: Implémenter**

Dans `backend/internal/config/config.go`, juste après le bloc `const minEncryptionKeyLen = 32` :

```go
// UITheme is which look the SPA wears. The server renders nothing with it: it
// reads it only so that a typo stops the boot instead of silently serving the
// other landing, and so the SPA can learn it from GET /api/config/status at
// boot. Switching needs a backend restart, never a frontend rebuild.
type UITheme string

const (
	// UIThemeHotel is the "Grand Hotel" landing and design system.
	UIThemeHotel UITheme = "hotel"
	// UIThemeClassic is the landing that predates it, kept so the opinionated
	// theme can be unplugged without touching the code.
	UIThemeClassic UITheme = "classic"
)
```

Dans la struct `Config`, après le champ `Edition` :

```go
	// UITheme picks the landing the SPA renders on /. See UITheme.
	UITheme UITheme
```

Dans `Load()`, après l'initialisation de `Edition` :

```go
		UITheme: UITheme(strings.ToLower(strings.TrimSpace(
			getEnv("UI_THEME", string(UIThemeHotel))))),
```

Dans `Validate()`, juste avant le `return nil` final :

```go
	// Like EDITION, an unrecognised theme must not fall back to a default: the
	// operator asked for something, and serving the other landing would look
	// like the switch simply does not work.
	switch c.UITheme {
	case UIThemeHotel, UIThemeClassic:
	default:
		return fmt.Errorf("UI_THEME is %q; use %q or %q", c.UITheme, UIThemeHotel, UIThemeClassic)
	}
```

Les deux tests existants construisent un `Config` à la main et doivent maintenant fournir un thème valide, sinon `Validate()` les fait échouer pour une autre raison que celle qu'ils testent. Dans `TestValidateEncryptionKey`, remplacer :

```go
			c := &Config{EncryptionKey: tt.key, Edition: provider.EditionSelfHosted}
```

par :

```go
			c := &Config{EncryptionKey: tt.key, Edition: provider.EditionSelfHosted, UITheme: UIThemeHotel}
```

Dans `TestValidateRejectsAnUnknownEdition`, remplacer :

```go
		return &Config{EncryptionKey: strings.Repeat("k", minEncryptionKeyLen)}
```

par :

```go
		return &Config{EncryptionKey: strings.Repeat("k", minEncryptionKeyLen), UITheme: UIThemeHotel}
```

- [ ] **Step 4: Vérifier que tout passe**

Run: `export PATH="$HOME/go/bin:$PATH"; cd backend && go test -race ./internal/config/ -v`
Expected: `PASS`, dont `TestValidateRejectsAnUnknownUITheme`, les 5 sous-tests de `TestLoadUITheme`, `TestValidateEncryptionKey` et `TestValidateRejectsAnUnknownEdition`.

- [ ] **Step 5: Commit**

```bash
/usr/bin/git add backend/internal/config/config.go backend/internal/config/config_test.go
/usr/bin/git commit -m "feat(config): ajouter UI_THEME pour choisir la landing" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: `uiTheme` dans `GET /api/config/status`, et la plomberie autour

**Files:**
- Modify: `backend/internal/models/models.go` (struct `InstanceStatus`)
- Modify: `backend/internal/api/handlers.go` (variables de package, `GetConfigStatus`)
- Modify: `backend/cmd/server/main.go:125`
- Create: `backend/internal/api/config_status_test.go`
- Modify: `backend/internal/api/routes_integration_test.go` (`TestConfigStatusIsPublicAndMinimal`)
- Modify: `docker-compose.yml`, `.env.example`, `docs/API.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: `config.UITheme`, `config.UIThemeHotel`, `config.UIThemeClassic` (Task 1).
- Produces: variable de package `api.UITheme config.UITheme` ; champ JSON `uiTheme` (string) dans la réponse de `GET /api/config/status`.

- [ ] **Step 1: Écrire le test qui échoue**

Créer `backend/internal/api/config_status_test.go` :

```go
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
```

Dans `backend/internal/api/routes_integration_test.go`, fonction `TestConfigStatusIsPublicAndMinimal` : remplacer

```go
	if len(body) != 4 {
		t.Errorf("status payload = %#v, want only isConfigured, mailboxSignIn, billingOn and edition", body)
	}
```

par

```go
	if len(body) != 5 {
		t.Errorf("status payload = %#v, want only isConfigured, mailboxSignIn, billingOn, edition and uiTheme", body)
	}
```

et ajouter, juste avant l'accolade fermante de la fonction (après le contrôle de `edition`) :

```go
	// The landing is chosen from this at boot; an empty string would read as
	// "unknown" and silently serve the classic landing.
	if theme, ok := body["uiTheme"].(string); !ok || theme == "" {
		t.Errorf("uiTheme = %#v, want a non-empty theme string", body["uiTheme"])
	}
```

Mettre à jour le commentaire au-dessus de la fonction : remplacer `Those four fields and nothing else` par `Those five fields and nothing else`, et `whether Pro can be bought yet, and` par `whether Pro can be bought yet, which landing to render, and`.

- [ ] **Step 2: Vérifier qu'il échoue**

Run: `export PATH="$HOME/go/bin:$PATH"; cd backend && go test ./internal/api/ -run 'ConfigStatus' -v`
Expected: échec de compilation, `undefined: UITheme`.

- [ ] **Step 3: Implémenter**

Dans `backend/internal/models/models.go`, struct `InstanceStatus`, après le champ `Edition` :

```go
	// UITheme is "hotel" or "classic", from UI_THEME. It picks which landing the
	// SPA renders on /, at runtime, so the opinionated theme can be unplugged by
	// changing one variable and restarting the backend, without a rebuild.
	UITheme string `json:"uiTheme"`
```

Dans `backend/internal/api/handlers.go`, ajouter `"github.com/nohe-sohbi/mailsorter/backend/internal/config"` au bloc d'imports (ordre alphabétique parmi les imports internes), puis juste après la déclaration `var Edition = provider.EditionSelfHosted` :

```go
// UITheme is the landing the SPA renders on /, set from configuration at
// startup (UI_THEME) and reported by GET /api/config/status.
var UITheme = config.UIThemeHotel
```

Dans `GetConfigStatus`, compléter le littéral :

```go
	status := models.InstanceStatus{
		IsConfigured:  h.gmailService.IsConfigured(),
		MailboxSignIn: mailboxSignInAvailable(),
		BillingOn:     h.billingEnabled(),
		Edition:       string(Edition),
		UITheme:       string(UITheme),
	}
```

Dans `backend/cmd/server/main.go`, juste après `api.Edition = cfg.Edition` :

```go
	api.UITheme = cfg.UITheme
```

- [ ] **Step 4: Vérifier que tout passe**

Run: `export PATH="$HOME/go/bin:$PATH"; cd backend && go vet ./... && go build ./... && go test -race ./...`
Expected: `ok` pour tous les packages, dont `internal/api` et `internal/config`.

- [ ] **Step 5: Plomberie et documentation**

`docker-compose.yml`, service `backend`, bloc `environment`, juste après la ligne `EDITION: ${EDITION:-self-hosted}` :

```yaml
      # Which landing the SPA renders on /: "hotel" (the Grand Hotel theme) or
      # "classic". Read at runtime, so switching needs a backend restart, not a
      # frontend rebuild.
      UI_THEME: ${UI_THEME:-hotel}
```

`.env.example`, juste après la ligne `EDITION=self-hosted` et sa ligne vide :

```
# Which landing the SPA renders on /. "hotel" is the Grand Hotel theme,
# "classic" the landing that predates it. Read by the backend and served by
# GET /api/config/status, so switching needs a backend restart, never a
# rebuild. Anything else stops the boot.
UI_THEME=hotel

```

`docs/API.md`, section `#### GET /api/config/status` :
- remplacer `is running. It is the only public route under` par `is running, and which landing to render. It is the only public route under` ;
- remplacer `nothing beyond these four fields.` par `nothing beyond these five fields.` ;
- remplacer l'exemple JSON par :

```json
{ "isConfigured": true, "mailboxSignIn": true, "billingOn": false, "edition": "self-hosted", "uiTheme": "hotel" }
```

- ajouter, après le paragraphe qui commence par `` `edition` is `self-hosted` or `hosted` `` :

```markdown
`uiTheme` is `hotel` or `classic`, from the `UI_THEME` environment variable
(default `hotel`). The SPA renders the Grand Hotel landing or the classic one on
`/`, chosen at runtime: switching needs a backend restart, never a rebuild. A
visitor can preview the other one for their tab with `?ui=hotel` or
`?ui=classic` (`?ui=default` clears it).
```

`CLAUDE.md`, table de la section "Configuration", ajouter une ligne juste après celle d'`EDITION` :

```markdown
| `UI_THEME` | optional (defaults to `hotel`) | `hotel` or `classic`: which landing `/` renders, read at runtime from `GET /api/config/status`, so switching is a backend restart, not a rebuild. Boot **refuses** anything else. `?ui=hotel\|classic` previews the other one for one tab |
```

- [ ] **Step 6: Commit**

```bash
/usr/bin/git add backend/internal/models/models.go backend/internal/api/handlers.go backend/internal/api/config_status_test.go backend/internal/api/routes_integration_test.go backend/cmd/server/main.go docker-compose.yml .env.example docs/API.md CLAUDE.md
/usr/bin/git commit -m "feat(api): exposer le theme de l'interface dans /api/config/status" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: le SPA choisit sa landing à l'exécution

**Files:**
- Create: `frontend/src/lib/uiTheme.js`
- Modify: `frontend/src/contexts/InstanceContext.js`
- Modify: `frontend/src/App.js`
- Create: `frontend/src/pages/HotelLanding.js` (coquille, complétée aux tâches 6 à 11)
- Create (hors git): `.superpowers/landing-check/package.json`, `check.mjs`, `scenarios/switch.mjs`, `test-pure.mjs`

**Interfaces:**
- Consumes: champ `uiTheme` de `/api/config/status` (Task 2).
- Produces:
  - `UI_THEMES` (`['hotel', 'classic']`), `serverUiTheme(value) -> 'hotel'|'classic'`, `resolveUiTheme(serverValue, search, remembered) -> 'hotel'|'classic'`, `effectiveUiTheme(serverValue, search) -> 'hotel'|'classic'` dans `lib/uiTheme.js` ;
  - `useInstance().uiTheme` (`'hotel'|'classic'`) ;
  - `pages/HotelLanding.js`, export par défaut, racine `<div className="theme-hotel hl-page" data-landing="hotel">` ;
  - le harnais `node check.mjs <scenario> [--theme=hotel|classic] [--width=N] [--dark] [--reduced] [--google=off] [--edition=hosted|self-hosted] [--billing=on]`, qui charge `scenarios/<scenario>.mjs` et lui passe `{ page, base, opt, expect, shot, calls, width }`.

- [ ] **Step 1: Écrire les vérifications de la logique pure**

Créer `.superpowers/landing-check/test-pure.mjs` :

```js
// Plain assertions on the pure modules of the landing. Run with Node 22+
// (ES module syntax detection): node test-pure.mjs
import assert from 'node:assert/strict';
import { resolveUiTheme, serverUiTheme } from '../../frontend/src/lib/uiTheme.js';

assert.equal(serverUiTheme('hotel'), 'hotel');
assert.equal(serverUiTheme('classic'), 'classic');
assert.equal(serverUiTheme(undefined), 'classic', 'a backend without the field predates the theme');
assert.equal(serverUiTheme('retro'), 'classic');

assert.equal(resolveUiTheme('hotel', '', null), 'hotel');
assert.equal(resolveUiTheme('hotel', '?ui=classic', null), 'classic', 'the query wins');
assert.equal(resolveUiTheme('hotel', '', 'classic'), 'classic', 'a remembered preview wins over the server');
assert.equal(resolveUiTheme('hotel', '?ui=default', 'classic'), 'hotel', '?ui=default drops the preview');
assert.equal(resolveUiTheme('classic', '?ui=bogus', null), 'classic', 'an unknown preview is ignored');
assert.equal(resolveUiTheme(undefined, '', null), 'classic');

console.log('uiTheme: ok');
```

Créer `.superpowers/landing-check/package.json` :

```json
{
  "name": "landing-check",
  "private": true,
  "type": "module",
  "dependencies": { "playwright-core": "^1.47.0" }
}
```

- [ ] **Step 2: Vérifier qu'elles échouent**

Run: `cd .superpowers/landing-check && node test-pure.mjs`
Expected: `Error [ERR_MODULE_NOT_FOUND]` sur `frontend/src/lib/uiTheme.js`.

- [ ] **Step 3: Implémenter `lib/uiTheme.js`**

Créer `frontend/src/lib/uiTheme.js` :

```js
// Which landing this browser renders on /.
//
// The server decides (UI_THEME, served by GET /api/config/status), so the
// opinionated Grand Hotel theme can be unplugged by changing one variable and
// restarting the backend, without a rebuild. `?ui=hotel` or `?ui=classic`
// overrides it for this browser tab only, to compare both on a live instance
// before flipping the switch for everyone; `?ui=default` drops the override.

export const UI_THEMES = ['hotel', 'classic'];

const PREVIEW_KEY = 'mailsorter_ui_preview';

// An absent or unknown server value falls back to classic: a backend that does
// not send the field predates the hotel theme, and classic is the landing it
// shipped with.
export function serverUiTheme(value) {
  return UI_THEMES.includes(value) ? value : 'classic';
}

// Pure: the query string wins, then a preview remembered for this tab, then
// the server.
export function resolveUiTheme(serverValue, search, remembered) {
  const asked = new URLSearchParams(search || '').get('ui');
  if (UI_THEMES.includes(asked)) return asked;
  if (asked !== 'default' && UI_THEMES.includes(remembered)) return remembered;
  return serverUiTheme(serverValue);
}

// Remembers or forgets the preview for this tab, then resolves. Storage can
// throw (private browsing, blocked site data): the preview is then simply not
// remembered, and the query still applies to this page.
export function effectiveUiTheme(serverValue, search) {
  const asked = new URLSearchParams(search || '').get('ui');
  let remembered = null;
  try {
    if (asked === 'default') sessionStorage.removeItem(PREVIEW_KEY);
    else if (UI_THEMES.includes(asked)) sessionStorage.setItem(PREVIEW_KEY, asked);
    remembered = sessionStorage.getItem(PREVIEW_KEY);
  } catch {
    remembered = null;
  }
  return resolveUiTheme(serverValue, search, remembered);
}
```

Run: `cd .superpowers/landing-check && node test-pure.mjs`
Expected: `uiTheme: ok`.

- [ ] **Step 4: Exposer `uiTheme` dans `InstanceContext`**

Dans `frontend/src/contexts/InstanceContext.js`, ajouter l'import en tête :

```js
import { serverUiTheme } from '../lib/uiTheme';
```

et, dans l'objet `value`, après la ligne `selfHosted: ...` :

```js
    // Which landing / renders, from UI_THEME. See lib/uiTheme.js, which also
    // applies the per-tab ?ui= preview on top of it.
    uiTheme: serverUiTheme(instance?.uiTheme),
```

- [ ] **Step 5: Choisir la landing dans `App.js`**

Dans `frontend/src/App.js` :
- remplacer `import React, { useEffect, useState } from 'react';` par `import React, { Suspense, lazy, useEffect, useMemo, useState } from 'react';` ;
- remplacer `import { BrowserRouter as Router, Routes, Route, Navigate, useNavigate } from 'react-router-dom';` par `import { BrowserRouter as Router, Routes, Route, Navigate, useLocation, useNavigate } from 'react-router-dom';` ;
- supprimer la ligne `import Login from './pages/Login';` ;
- ajouter `import { effectiveUiTheme } from './lib/uiTheme';` après `import { isAuthed } from './lib/session';` ;
- ajouter, juste avant `function RequireAuth(` :

```jsx
// Both landings are separate chunks: a visitor downloads only the one this
// instance serves, fonts included.
const ClassicLanding = lazy(() => import('./pages/Login'));
const HotelLanding = lazy(() => import('./pages/HotelLanding'));

// The landing on /, chosen at runtime from UI_THEME (see lib/uiTheme.js), so
// the Grand Hotel theme can be unplugged without a rebuild.
function Landing() {
  const { uiTheme } = useInstance();
  const { search } = useLocation();
  const theme = useMemo(() => effectiveUiTheme(uiTheme, search), [uiTheme, search]);
  const Page = theme === 'hotel' ? HotelLanding : ClassicLanding;
  return (
    <Suspense
      fallback={
        <BootScreen>
          <Spinner size={18} className="text-brand-600" />
        </BootScreen>
      }
    >
      <Page />
    </Suspense>
  );
}
```

- remplacer la route `<Route path="/" element={isUsable ? <Login /> : unconfigured()} />` par `<Route path="/" element={isUsable ? <Landing /> : unconfigured()} />`.

- [ ] **Step 6: Créer la coquille de `HotelLanding`**

Créer `frontend/src/pages/HotelLanding.js` :

```jsx
import React from 'react';

// The "Grand Hotel" landing, served on / when UI_THEME=hotel (lib/uiTheme.js).
// Filled section by section; see docs/superpowers/specs/2026-09-24-landing-grand-hotel-design.md.
export default function HotelLanding() {
  return (
    <div className="theme-hotel hl-page" data-landing="hotel">
      <h1>Mailsorter</h1>
    </div>
  );
}
```

- [ ] **Step 7: Écrire le harnais et le scénario de bascule**

Créer `.superpowers/landing-check/check.mjs` :

```js
// Landing checks: the production build in a real Chrome, with the API mocked.
//
// Lives under .superpowers/ (git-ignored) on purpose: the repo has no frontend
// test framework and this is a verification aid, not a suite. Usage, from this
// directory, after `CI=false npm run build` in frontend/:
//   node check.mjs <scenario> [--theme=hotel|classic] [--width=1440] [--dark]
//                  [--reduced] [--google=off] [--edition=hosted|self-hosted]
//                  [--billing=on]
import { chromium } from 'playwright-core';
import { spawn } from 'node:child_process';
import { mkdirSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import path from 'node:path';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(HERE, '../..');
const BUILD = path.join(REPO, 'frontend/build');
const PORT = Number(process.env.CHECK_PORT || 5055);
const SHOTS = path.join(HERE, 'shots');
mkdirSync(SHOTS, { recursive: true });

const [, , scenario, ...rest] = process.argv;
const opt = Object.fromEntries(
  rest.map((a) => {
    const [k, v] = a.replace(/^--/, '').split('=');
    return [k, v === undefined ? true : v];
  })
);
const width = Number(opt.width || 1440);

const STATUS = {
  isConfigured: opt.google !== 'off',
  mailboxSignIn: true,
  billingOn: opt.billing === 'on',
  edition: opt.edition || 'hosted',
  uiTheme: opt.theme || 'hotel',
};

const route = (transport) => ({ transport, auth: transport === 'gmail-api' ? 'oauth' : 'app-password', capabilities: {}, note: '' });
const PROVIDERS = {
  edition: STATUS.edition,
  providers: [
    { key: 'gmail', name: 'Gmail', routes: [route('gmail-api'), route('imap')] },
    { key: 'outlook', name: 'Outlook.com', routes: [route('imap')] },
    { key: 'orange', name: 'Orange', routes: [route('imap')] },
  ],
};

const failures = [];
const calls = [];
const expect = (ok, message) => {
  if (!ok) failures.push(message);
};

async function mockApi(page) {
  await page.route('**/api/**', async (r) => {
    const req = r.request();
    const url = new URL(req.url());
    const key = `${req.method()} ${url.pathname}`;
    calls.push({ key, body: req.postData() });
    const json = (status, body) => r.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
    switch (key) {
      case 'GET /api/config/status':
        return json(200, STATUS);
      case 'GET /api/providers':
        return json(200, PROVIDERS);
      case 'POST /api/auth/register':
      case 'POST /api/auth/login': {
        const body = JSON.parse(req.postData() || '{}');
        if (body.password === 'wrong-password') return json(401, { error: 'Identifiants invalides', status: 401 });
        return json(200, { userEmail: body.email, accessToken: 'test-token' });
      }
      case 'POST /api/waitlist':
        return json(200, { ok: true });
      default:
        return json(404, { error: 'not mocked', status: 404 });
    }
  });
}

async function waitUp(url) {
  for (let i = 0; i < 60; i += 1) {
    try {
      const res = await fetch(url);
      if (res.ok) return;
    } catch {
      /* not up yet */
    }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error(`static server did not answer on ${url}`);
}

const server = spawn('python3', ['-m', 'http.server', String(PORT), '--bind', '127.0.0.1', '--directory', BUILD], { stdio: 'ignore' });
let browser;
try {
  const base = `http://127.0.0.1:${PORT}`;
  await waitUp(`${base}/`);
  const { default: run } = await import(pathToFileURL(path.join(HERE, 'scenarios', `${scenario}.mjs`)).href);
  browser = await chromium.launch({ executablePath: process.env.CHROME || '/usr/bin/google-chrome', args: ['--no-sandbox'] });
  const context = await browser.newContext({
    viewport: { width, height: 900 },
    colorScheme: opt.dark ? 'dark' : 'light',
    reducedMotion: opt.reduced ? 'reduce' : 'no-preference',
  });
  const page = await context.newPage();
  const errors = [];
  // Only errors raised on the landing count: the routes a sign-in lands on
  // call endpoints this harness does not mock.
  const onLanding = () => new URL(page.url()).pathname === '/';
  page.on('pageerror', (e) => {
    if (onLanding()) errors.push(e.message);
  });
  page.on('console', (m) => {
    if (m.type() === 'error' && onLanding() && !/Failed to load resource/.test(m.text())) errors.push(m.text());
  });
  await mockApi(page);
  const shot = (name) => page.screenshot({ path: path.join(SHOTS, `${scenario}-${name}-${width}.png`), fullPage: true });
  await run({ page, base, opt, expect, shot, calls, width, repo: REPO });
  expect(errors.length === 0, `errors on the landing: ${errors.join(' | ')}`);
} catch (err) {
  failures.push(`scenario crashed: ${err.message}`);
} finally {
  if (browser) await browser.close();
  server.kill();
}

console.log(JSON.stringify({ scenario, width, theme: STATUS.uiTheme, failures }, null, 2));
process.exit(failures.length ? 1 : 0);
```

Créer `.superpowers/landing-check/scenarios/switch.mjs` :

```js
// UI_THEME decides the landing; ?ui= overrides it for this tab only.
export default async function switchScenario({ page, base, opt, expect, shot }) {
  const server = opt.theme || 'hotel';
  const other = server === 'hotel' ? 'classic' : 'hotel';
  const which = () =>
    page.evaluate(() => {
      if (document.querySelector('[data-landing="hotel"]')) return 'hotel';
      if (document.getElementById('auth-box')) return 'classic';
      return 'none';
    });
  const load = async (query = '') => {
    await page.goto(`${base}/${query}`);
    await page.waitForSelector('[data-landing="hotel"], #auth-box', { timeout: 10000 });
  };

  await load();
  expect((await which()) === server, `UI_THEME=${server} rendered ${await which()}`);
  await shot('server');

  await load(`?ui=${other}`);
  expect((await which()) === other, `?ui=${other} rendered ${await which()}`);

  await load();
  expect((await which()) === other, 'the preview was not remembered for the tab');

  await load('?ui=default');
  expect((await which()) === server, `?ui=default rendered ${await which()}, want the server's ${server}`);

  await load('?ui=bogus');
  expect((await which()) === server, `?ui=bogus rendered ${await which()}, want the server's ${server}`);
}
```

- [ ] **Step 8: Construire et vérifier dans le navigateur**

Run: `cd frontend && CI=false npm run build 2>&1 | tail -5`
Expected: `The build folder is ready to be deployed.`

Run: `cd .superpowers/landing-check && npm install --no-audit --no-fund && node check.mjs switch --theme=hotel && node check.mjs switch --theme=classic`
Expected: deux sorties JSON avec `"failures": []`, code de sortie 0.

- [ ] **Step 9: Commit**

```bash
/usr/bin/git add frontend/src/lib/uiTheme.js frontend/src/contexts/InstanceContext.js frontend/src/App.js frontend/src/pages/HotelLanding.js
/usr/bin/git commit -m "feat(frontend): choisir la landing au runtime selon UI_THEME" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: une seule logique d'authentification pour les deux landings

**Files:**
- Create: `frontend/src/lib/useAuthForm.js`
- Modify: `frontend/src/pages/Login.js` (composant `AuthBox` et imports)
- Create (hors git): `.superpowers/landing-check/scenarios/auth.mjs`, `scenarios/classic-shot.mjs`, `compare.py`

**Interfaces:**
- Produces: `useAuthForm(onSignedIn)` renvoie `{ mode, setMode, email, setEmail, password, setPassword, busy, googleBusy, error, submit, startGoogle }` ; `mode` vaut `'register'` ou `'login'` ; `setMode` est stable (`useCallback`) et efface l'erreur ; `submit(event)` appelle `onSignedIn('/connect')` après une inscription, `onSignedIn('/inbox')` après une connexion. Les champs gardent les ids `auth-email` et `auth-password` dans les deux landings, et chaque bascule de mode porte `data-auth-switch="login"` ou `"register"`.

- [ ] **Step 1: Capturer la landing classique AVANT le changement**

Créer `.superpowers/landing-check/scenarios/classic-shot.mjs` :

```js
// A fixed capture of the classic landing, to prove a refactor left it untouched.
export default async function classicShot({ page, base, opt }) {
  await page.goto(`${base}/`);
  await page.waitForSelector('#auth-box');
  await page.waitForTimeout(800);
  await page.screenshot({ path: `${opt.out}`, fullPage: true });
}
```

Créer `.superpowers/landing-check/compare.py` :

```python
"""Exit 0 when two screenshots are pixel-identical, 1 otherwise (prints the diff box)."""
import sys
from PIL import Image, ImageChops

a, b = Image.open(sys.argv[1]).convert("RGB"), Image.open(sys.argv[2]).convert("RGB")
if a.size != b.size:
    print(f"size differs: {a.size} vs {b.size}")
    sys.exit(1)
box = ImageChops.difference(a, b).getbbox()
print("identical" if box is None else f"differs in {box}")
sys.exit(0 if box is None else 1)
```

Run: `cd frontend && CI=false npm run build 2>&1 | tail -1 && cd ../.superpowers/landing-check && node check.mjs classic-shot --theme=classic --reduced --out=shots/classic-before.png`
Expected: `"failures": []` et le fichier `shots/classic-before.png`.

- [ ] **Step 2: Écrire le scénario d'authentification**

Créer `.superpowers/landing-check/scenarios/auth.mjs` :

```js
// Sign-up, refused sign-in and the redirect of a visitor already signed in.
// Both landings answer to the same selectors (#auth-email, #auth-password,
// [data-auth-switch]), so this runs against either theme.
export default async function auth({ page, base, expect, shot }) {
  const submit = 'form:has(#auth-email) button[type=submit]';

  await page.goto(`${base}/`);
  await page.waitForSelector('#auth-email');
  await page.fill('#auth-email', 'marie@exemple.fr');
  await page.fill('#auth-password', 'un-mot-de-passe');
  await page.click(submit);
  await page.waitForURL('**/connect', { timeout: 5000 }).catch(() => {});
  expect(page.url().endsWith('/connect'), `after sign-up the URL is ${page.url()}, want /connect`);
  expect((await page.evaluate(() => localStorage.getItem('accessToken'))) === 'test-token', 'sign-up did not store the session');

  await page.evaluate(() => localStorage.clear());
  await page.goto(`${base}/`);
  await page.waitForSelector('#auth-email');
  await page.click('[data-auth-switch="login"]');
  await page.fill('#auth-email', 'marie@exemple.fr');
  await page.fill('#auth-password', 'wrong-password');
  await page.click(submit);
  await page.waitForTimeout(600);
  expect((await page.content()).includes('Identifiants invalides'), 'a refused sign-in does not show the API error');
  await shot('login-error');

  await page.evaluate(() => {
    localStorage.setItem('userEmail', 'marie@exemple.fr');
    localStorage.setItem('accessToken', 'test-token');
    localStorage.setItem('hasMailbox', 'true');
  });
  await page.goto(`${base}/`);
  await page.waitForURL('**/inbox', { timeout: 5000 }).catch(() => {});
  expect(page.url().endsWith('/inbox'), `a signed-in visitor stays on ${page.url()}, want /inbox`);
}
```

Run: `cd .superpowers/landing-check && node check.mjs auth --theme=classic`
Expected: ÉCHEC, `failures` contient une erreur de sélecteur `[data-auth-switch="login"]` (la landing classique ne porte pas encore l'attribut).

- [ ] **Step 3: Écrire le hook**

Créer `frontend/src/lib/useAuthForm.js` :

```js
import { useCallback, useState } from 'react';
import { authService, apiError } from '../services/api';
import { track } from './analytics';

// Sign-up, sign-in and the Google hand-off, shared by both landings so a fix to
// one door is a fix to both. It owns the state and the side effects; each
// landing only decides how the form looks.
//
// onSignedIn receives where to go next: /connect after a sign-up (the account
// has no mailbox yet), /inbox after a sign-in (RequireMailbox sends it on to
// /connect if the account still has none).
export function useAuthForm(onSignedIn) {
  const [mode, setModeState] = useState('register');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [googleBusy, setGoogleBusy] = useState(false);
  const [error, setError] = useState('');

  const setMode = useCallback((next) => {
    setModeState(next);
    setError('');
  }, []);

  const submit = async (event) => {
    if (event) event.preventDefault();
    if (busy || googleBusy) return;
    setBusy(true);
    setError('');
    try {
      const call = mode === 'register' ? authService.register : authService.login;
      const { data } = await call(email.trim(), password);
      localStorage.setItem('userEmail', data.userEmail);
      localStorage.setItem('accessToken', data.accessToken);
      localStorage.removeItem('hasMailbox');
      track(mode === 'register' ? 'register_done' : 'login_done');
      onSignedIn(mode === 'register' ? '/connect' : '/inbox');
    } catch (err) {
      setError(apiError(err, 'Une erreur est survenue. Vérifiez vos identifiants.'));
      setBusy(false);
    }
  };

  const startGoogle = async () => {
    setGoogleBusy(true);
    setError('');
    try {
      const response = await authService.getAuthUrl();
      track('login_start', { provider: 'google' });
      window.location.href = response.data.authUrl;
    } catch {
      setError('Impossible de démarrer la connexion Google. Réessayez.');
      setGoogleBusy(false);
    }
  };

  return { mode, setMode, email, setEmail, password, setPassword, busy, googleBusy, error, submit, startGoogle };
}
```

- [ ] **Step 4: Brancher la landing classique sur le hook, rendu inchangé**

Dans `frontend/src/pages/Login.js` :
- remplacer `import { authService, configService, waitlistService, apiError } from '../services/api';` par `import { configService, waitlistService, apiError } from '../services/api';` ;
- ajouter `import { useAuthForm } from '../lib/useAuthForm';` après `import { isAuthed } from '../lib/session';` ;
- dans `function AuthBox({ isConfigured, onSignedIn })`, remplacer tout le bloc qui va de `const [mode, setMode] = useState('register');` jusqu'à la fin de `handleGoogleLogin` (l'accolade et le point-virgule qui ferment ce `const`) par :

```jsx
  const { mode, setMode, email, setEmail, password, setPassword, busy, googleBusy, error, submit, startGoogle } =
    useAuthForm(onSignedIn);
```

- dans le JSX d'`AuthBox` :
  - bouton "Créer un compte" du sélecteur : remplacer `onClick={() => { setMode('register'); setError(''); }}` par `onClick={() => setMode('register')}` et ajouter l'attribut `data-auth-switch="register"` ;
  - bouton "Se connecter" du sélecteur : remplacer `onClick={() => { setMode('login'); setError(''); }}` par `onClick={() => setMode('login')}` et ajouter l'attribut `data-auth-switch="login"` ;
  - remplacer `<form onSubmit={handleSubmit}` par `<form onSubmit={submit}` ;
  - remplacer `onClick={handleGoogleLogin}` par `onClick={startGoogle}`.

Rien d'autre ne change dans `Login.js`.

- [ ] **Step 5: Vérifier le comportement et le rendu**

Run: `cd frontend && CI=false npm run build 2>&1 | grep -E "Compiled|Failed|ready" ; cd ../.superpowers/landing-check && node check.mjs auth --theme=classic && node check.mjs classic-shot --theme=classic --reduced --out=shots/classic-after.png && python3 compare.py shots/classic-before.png shots/classic-after.png`
Expected: `"failures": []` pour `auth`, puis `identical`.

- [ ] **Step 6: Commit**

```bash
/usr/bin/git add frontend/src/lib/useAuthForm.js frontend/src/pages/Login.js
/usr/bin/git commit -m "refactor(frontend): partager la logique d'authentification entre les landings" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 5: les tokens du thème, les polices et la moodboard

**Files:**
- Create: `frontend/src/styles/hotel.css`
- Create: `frontend/src/ui/hotel/useHotelFonts.js`
- Modify: `frontend/tailwind.config.js` (`theme.extend.colors`, `theme.extend.fontFamily`)
- Create: `docs/design/moodboard.html` (généré depuis la maquette par un script, puis versionné)
- Create (hors git): `.superpowers/landing-check/port-moodboard.mjs`, `scenarios/moodboard.mjs`

**Interfaces:**
- Produces: classe racine `.theme-hotel` ; variables `--h-*` (table de la spec, section 5) ; classes `ht-disp`, `ht-btn`, `ht-btn-primary`, `ht-btn-secondary`, `ht-btn-gold`, `ht-btn-sm`, `ht-link`, `ht-input`, `ht-field`, `ht-label`, `ht-card`, `ht-tag` (+ `is-gold`, `is-teal`, `is-plain`), `ht-badge`, `ht-toast`, `ht-panel` (lien actif `a.is-on`), `ht-switch` (+ `is-on`), `ht-check` (+ `is-on`), `ht-plq`, `ht-prop` (+ `is-safe`), `ht-slab`, `ht-key`, `ht-key__hook`, `ht-key__str`, `ht-key__tag` (variable `--tilt`), `ht-facade` (+ `is-animated`), textes d'illustration `hf-*` ; hook `useHotelFonts()`.

- [ ] **Step 1: Écrire le scénario de la moodboard**

Créer `.superpowers/landing-check/scenarios/moodboard.mjs` :

```js
// The moodboard reads the real token file: primitives must render with the
// theme's values by day and by night, and nothing may still use the mockup's
// old h- class prefix.
export default async function moodboard({ page, repo, expect }) {
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  page.on('console', (m) => {
    if (m.type() === 'error') errors.push(m.text());
  });
  await page.goto(`file://${repo}/docs/design/moodboard.html`);
  await page.waitForTimeout(1500);
  const bg = () => page.evaluate(() => getComputedStyle(document.querySelector('.ht-btn-primary')).backgroundColor);
  expect((await bg()) === 'rgb(122, 46, 59)', `primary button by day is ${await bg()}, want rgb(122, 46, 59)`);
  await page.screenshot({ path: 'shots/moodboard-day.png', fullPage: true });
  await page.click('.seg button[data-theme="dark"]');
  await page.waitForTimeout(400);
  expect((await bg()) === 'rgb(168, 69, 90)', `primary button by night is ${await bg()}, want rgb(168, 69, 90)`);
  await page.screenshot({ path: 'shots/moodboard-night.png', fullPage: true });
  const old = await page.evaluate(() =>
    [...document.querySelectorAll('[class]')].flatMap((el) => [...el.classList]).filter((c) => /^h-/.test(c))
  );
  expect(old.length === 0, `classes still on the old h- prefix: ${[...new Set(old)].join(', ')}`);
  expect(errors.length === 0, `moodboard errors: ${errors.join(' | ')}`);
}
```

Run: `cd .superpowers/landing-check && node check.mjs moodboard`
Expected: ÉCHEC, la page `docs/design/moodboard.html` n'existe pas (`scenario crashed` ou bouton introuvable).

- [ ] **Step 2: Écrire `styles/hotel.css`**

Créer `frontend/src/styles/hotel.css` :

```css
/* Grand Hotel theme: tokens, primitives, illustration text and motion.
 *
 * Everything hangs off .theme-hotel, never :root, so the theme can be
 * unplugged (UI_THEME=classic) without leaving a rule behind: the classic
 * landing and today's dashboard never carry the class. Night values sit under
 * .dark .theme-hotel, driven by the light / dark / system switch that puts
 * .dark on <html> (ui/theme.js).
 *
 * Plain CSS on purpose, not Tailwind: a theme that can be switched off cannot
 * live in index.css, and @layer only works in the file holding the @tailwind
 * directives. Tailwind still does layout (see the `hotel` colours in
 * tailwind.config.js). Class prefixes: ht- for these primitives, hl- for the
 * landing. Never a generic name: a stylesheet loaded by the landing stays
 * loaded after navigation and would restyle the dashboard.
 *
 * The validated rendering of every class is docs/design/moodboard.html.
 */

.theme-hotel {
  --h-bg: #F6E3D9;
  --h-bg-alt: #FBF1E4;
  --h-surface: #FFFAF2;
  --h-sunk: #F7D6DC;
  --h-pink: #EFB7C3;
  --h-pile-1: #F8ECDC;
  --h-pile-2: #F1E1CC;
  --h-text: #2B1B1E;
  --h-muted: #6E4B52;
  --h-line: #2B1B1E;
  --h-hair: rgba(43, 27, 30, 0.16);
  --h-plum: #7A2E3B;
  --h-on-plum: #FBF1E4;
  --h-accent: #7A2E3B;
  --h-mustard: #E3A93B;
  --h-mustard-soft: #FBE8C4;
  --h-on-mustard: #2B1B1E;
  --h-teal: #2F6F73;
  --h-teal-soft: #D5E7E4;
  --h-teal-text: #2F6F73;
  --h-offset: #7A2E3B;
  --h-stripe: rgba(122, 46, 59, 0.045);
  --h-glow: rgba(255, 214, 120, 0.9);
  --h-font-display: 'Bodoni Moda', Didot, 'Times New Roman', serif;
  --h-font-ui: 'Jost', Futura, ui-sans-serif, system-ui, sans-serif;
  --h-ease: cubic-bezier(0.4, 0, 0.2, 1);

  font-family: var(--h-font-ui);
  color: var(--h-text);
  background: repeating-linear-gradient(90deg, var(--h-stripe) 0 24px, transparent 24px 48px), var(--h-bg);
  -webkit-font-smoothing: antialiased;
}

.dark .theme-hotel {
  --h-bg: #1C1220;
  --h-bg-alt: #221620;
  --h-surface: #2A1A24;
  --h-sunk: #33202C;
  --h-pink: #5A2A3A;
  --h-pile-1: #241620;
  --h-pile-2: #1F131B;
  --h-text: #F3E6DA;
  --h-muted: #BFA6A8;
  --h-line: #0B0609;
  --h-hair: rgba(243, 230, 218, 0.12);
  --h-plum: #A8455A;
  --h-on-plum: #FFF4E8;
  --h-accent: #EE9CB0;
  --h-mustard: #F4CF6B;
  --h-mustard-soft: #3A2A18;
  --h-on-mustard: #2B1B1E;
  --h-teal: #4F9A96;
  --h-teal-soft: #1E3533;
  --h-teal-text: #7CC4BF;
  --h-offset: #000000;
  --h-stripe: rgba(244, 207, 107, 0.03);
}

/* ---------- Type ---------- */

/* Bodoni Moda at its default optical size draws hairlines so thin that a 4 and
 * a hyphen vanish at display sizes. 28 keeps the contrast and the legibility. */
.ht-disp {
  font-family: var(--h-font-display);
  font-variation-settings: 'opsz' 28;
  font-weight: 400;
}

/* ---------- Buttons and links ---------- */

.ht-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: 9px;
  height: 44px;
  padding: 0 20px;
  border: 1.5px solid var(--h-line);
  background: var(--h-surface);
  color: var(--h-text);
  font-family: var(--h-font-ui);
  font-size: 12.5px;
  font-weight: 600;
  letter-spacing: 0.16em;
  text-transform: uppercase;
  cursor: pointer;
  transition: transform 0.12s, box-shadow 0.12s, background-color 0.2s;
}
.ht-btn:disabled { opacity: 0.6; cursor: not-allowed; }
.ht-btn-primary { background: var(--h-plum); color: var(--h-on-plum); box-shadow: 0 3px 0 var(--h-line); }
.ht-btn-primary:not(:disabled):hover { transform: translateY(-1px); box-shadow: 0 4px 0 var(--h-line); }
.ht-btn-primary:not(:disabled):active { transform: translateY(2px); box-shadow: 0 1px 0 var(--h-line); }
.ht-btn-secondary:not(:disabled):hover { background: var(--h-sunk); }
.ht-btn-gold { background: var(--h-mustard); color: var(--h-on-mustard); box-shadow: 0 3px 0 var(--h-line); }
.ht-btn-gold:not(:disabled):hover { transform: translateY(-1px); box-shadow: 0 4px 0 var(--h-line); }
.ht-btn-sm { height: 32px; padding: 0 12px; font-size: 10.5px; }

.ht-link {
  padding: 0;
  background: none;
  border-bottom: 1.5px solid currentColor;
  font-family: var(--h-font-display);
  font-style: italic;
  font-size: 16px;
  color: var(--h-accent);
  cursor: pointer;
}

.theme-hotel :focus-visible { outline: 3px solid var(--h-mustard); outline-offset: 2px; }

/* ---------- Fields ---------- */

.ht-input {
  width: 100%;
  height: 44px;
  padding: 0 12px;
  border: 1.5px solid var(--h-line);
  background: var(--h-surface);
  color: var(--h-text);
  font-size: 15px;
  outline: none;
}
.ht-input::placeholder { color: var(--h-muted); opacity: 0.7; }

/* The registration-card field: a dotted line to write on. */
.ht-field {
  display: block;
  width: 100%;
  height: 34px;
  margin-bottom: 12px;
  border: 0;
  border-bottom: 1.5px dotted var(--h-line);
  background: transparent;
  color: var(--h-text);
  font-family: var(--h-font-display);
  font-style: italic;
  font-size: 16px;
  outline: none;
}
.ht-field::placeholder { color: var(--h-muted); opacity: 0.75; }
.ht-field:focus { border-bottom-style: solid; border-bottom-color: var(--h-plum); }

.ht-label {
  display: block;
  margin-bottom: 2px;
  font-size: 9.5px;
  font-weight: 600;
  letter-spacing: 0.2em;
  text-transform: uppercase;
  color: var(--h-accent);
}

/* ---------- Surfaces ---------- */

.ht-card { background: var(--h-surface); border: 1.5px solid var(--h-line); box-shadow: 7px 7px 0 var(--h-offset); }

/* The slab between two floors. */
.ht-slab {
  height: 14px;
  background: #7A2E3B;
  border-top: 2px solid var(--h-line);
  border-bottom: 2px solid var(--h-line);
  box-shadow: inset 0 3px 0 #E3A93B;
}

/* ---------- Tags, badges, toast ---------- */

.ht-tag {
  position: relative;
  display: inline-flex;
  align-items: center;
  height: 28px;
  padding: 0 24px 0 10px;
  border: 1.5px solid var(--h-line);
  border-radius: 3px 14px 14px 3px;
  background: var(--h-pink);
  color: var(--h-text);
  font-size: 12px;
  font-weight: 600;
}
.ht-tag::after {
  content: '';
  position: absolute;
  right: 7px;
  top: 50%;
  width: 6px;
  height: 6px;
  margin-top: -3px;
  border-radius: 50%;
  background: var(--h-bg);
  border: 1.5px solid var(--h-line);
}
.ht-tag.is-gold { background: var(--h-mustard); color: var(--h-on-mustard); }
.ht-tag.is-teal { background: var(--h-teal); color: #fff; }
.ht-tag.is-plain { background: var(--h-surface); }

.ht-badge {
  display: inline-grid;
  place-items: center;
  min-width: 24px;
  height: 24px;
  padding: 0 6px;
  border-radius: 12px;
  background: var(--h-mustard);
  border: 1.5px solid var(--h-line);
  color: var(--h-on-mustard);
  font-size: 12px;
  font-weight: 600;
  transition: transform 0.2s;
}

/* Ink on paper by day, paper on ink by night: a dark toast on a dark page
 * would only be visible by its shadow. */
.ht-toast {
  display: inline-flex;
  align-items: center;
  gap: 16px;
  padding: 11px 16px 11px 18px;
  background: #2B1B1E;
  color: #FBF1E4;
  font-size: 14.5px;
  box-shadow: 0 10px 24px -10px rgba(0, 0, 0, 0.5);
}
.ht-toast button {
  color: #F4CF6B;
  border-bottom: 1.5px solid #F4CF6B;
  font-size: 12.5px;
  font-weight: 600;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}
.dark .theme-hotel .ht-toast { background: #F3E6DA; color: #2B1B1E; }
.dark .theme-hotel .ht-toast button { color: #7A2E3B; border-bottom-color: #7A2E3B; }

/* ---------- Elevator panel (the menu) ---------- */

.ht-panel {
  display: inline-flex;
  gap: 16px;
  padding: 7px 18px 5px;
  background: var(--h-plum);
  border: 2px solid var(--h-line);
  border-radius: 40px;
}
.ht-panel a { display: flex; flex-direction: column; align-items: center; gap: 3px; text-decoration: none; }
.ht-panel i {
  display: grid;
  place-items: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: #E3A93B;
  border: 1.5px solid #2B1B1E;
  color: #2B1B1E;
  font-style: normal;
  font-family: var(--h-font-display);
  font-weight: 700;
  font-size: 13px;
  transition: background-color 0.3s, box-shadow 0.3s;
}
.ht-panel a.is-on i { background: #FBF1E4; box-shadow: 0 0 0 3px rgba(251, 241, 228, 0.3), 0 0 14px 3px var(--h-glow); }
.ht-panel span { font-size: 8.5px; font-weight: 600; letter-spacing: 0.16em; text-transform: uppercase; color: #FBF1E4; }

/* ---------- Switch, checkbox ---------- */

.ht-switch {
  position: relative;
  flex-shrink: 0;
  width: 50px;
  height: 28px;
  border: 1.5px solid var(--h-line);
  border-radius: 14px;
  background: var(--h-sunk);
  transition: background-color 0.2s;
}
.ht-switch::after {
  content: '';
  position: absolute;
  top: 3px;
  left: 3px;
  width: 19px;
  height: 19px;
  border-radius: 50%;
  background: var(--h-surface);
  border: 1.5px solid var(--h-line);
  transition: transform 0.2s var(--h-ease);
}
.ht-switch.is-on { background: var(--h-mustard); }
.ht-switch.is-on::after { transform: translateX(21px); }

.ht-check {
  display: inline-grid;
  place-items: center;
  flex-shrink: 0;
  width: 18px;
  height: 18px;
  border: 1.5px solid var(--h-line);
  background: var(--h-surface);
}
.ht-check.is-on { background: var(--h-plum); }
.ht-check.is-on::after {
  content: '';
  width: 8px;
  height: 4px;
  border-left: 2px solid var(--h-on-plum);
  border-bottom: 2px solid var(--h-on-plum);
  transform: rotate(-45deg) translate(1px, -1px);
}

/* ---------- Numbered plaque, proposal ---------- */

.ht-plq { display: grid; grid-template-columns: 42px 1fr; gap: 14px; align-items: center; }
.ht-plq b {
  display: grid;
  place-items: center;
  width: 42px;
  height: 42px;
  border-radius: 50%;
  background: #E3A93B;
  border: 1.5px solid var(--h-line);
  color: #7A2E3B;
  font-family: var(--h-font-display);
  font-style: italic;
  font-size: 17px;
}
.ht-plq p { font-size: 15px; line-height: 1.45; color: var(--h-muted); }
.ht-plq strong { color: var(--h-text); font-weight: 600; }

.ht-prop {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 14px 16px;
  background: var(--h-mustard-soft);
  border: 1.5px solid var(--h-line);
}
.ht-prop svg { flex-shrink: 0; color: var(--h-accent); }
.ht-prop small {
  display: block;
  font-size: 10.5px;
  font-weight: 600;
  letter-spacing: 0.2em;
  text-transform: uppercase;
  color: var(--h-muted);
}
.ht-prop b {
  font-family: var(--h-font-display);
  font-variation-settings: 'opsz' 28;
  font-style: italic;
  font-weight: 500;
  font-size: 23px;
  color: var(--h-accent);
}
.ht-prop.is-safe { background: var(--h-teal-soft); }
.ht-prop.is-safe svg, .ht-prop.is-safe b { color: var(--h-teal-text); }

/* ---------- Key on its hook ---------- */

.ht-key { display: flex; flex-direction: column; align-items: center; }
.ht-key__hook { width: 12px; height: 12px; border-radius: 50%; background: #E3A93B; border: 1.5px solid #2B1B1E; }
.ht-key__str { width: 1.5px; height: 12px; background: #FBF1E4; opacity: 0.8; }
.ht-key__tag {
  flex-direction: column;
  align-items: flex-start;
  justify-content: center;
  height: auto;
  min-width: 128px;
  padding: 8px 26px 8px 12px;
  font-size: 13px;
  transform: rotate(var(--tilt, 0deg));
  transform-origin: 50% -30px;
  transition: transform 0.3s;
}
.ht-key:hover .ht-key__tag { transform: rotate(calc(var(--tilt, 0deg) * -2)); }
.ht-key__tag small {
  display: block;
  font-size: 9px;
  font-weight: 600;
  letter-spacing: 0.12em;
  text-transform: uppercase;
  color: #7A2E3B;
  opacity: 0.85;
}

/* ---------- Illustration text (SVG) ---------- */

.hf-sign { font: 600 12px var(--h-font-ui); letter-spacing: 7px; fill: #FBF1E4; text-anchor: middle; }
.hf-sign--lit { fill: #F4CF6B; }
.hf-num, .hf-dn { font: 700 9px var(--h-font-ui); fill: #2B1B1E; text-anchor: middle; }
.hf-lab { font: 600 7.4px var(--h-font-ui); letter-spacing: 1.2px; fill: #7A2E3B; text-anchor: middle; }
.hf-lab--wide { letter-spacing: 2px; }
.hf-tiny { font: 700 6px var(--h-font-ui); letter-spacing: 0.8px; fill: #FBF1E4; text-anchor: middle; }
.hf-tiny--lg { font-size: 7px; }
.hf-sg { font: 700 6.6px var(--h-font-ui); letter-spacing: 0.5px; text-anchor: middle; }
.hf-hg { font: 700 6.4px var(--h-font-ui); letter-spacing: 0.6px; fill: #7A2E3B; text-anchor: middle; }
.hf-hg--ink { fill: #2B1B1E; }
.hf-z { font: italic 600 13px var(--h-font-display); fill: #7A2E3B; }
.hf-z--sm { font-size: 10px; }
.hf-vault { font: 700 10px var(--h-font-ui); letter-spacing: 2.4px; fill: #2B1B1E; text-anchor: middle; }

/* ---------- Motion ---------- */

@keyframes ht-lift {
  0%, 18% { transform: translateY(0); }
  42%, 62% { transform: translateY(104px); }
  86%, 100% { transform: translateY(0); }
}
@keyframes ht-blink {
  0%, 40% { opacity: 0; }
  44%, 60% { opacity: 1; }
  64%, 100% { opacity: 0; }
}
.ht-facade.is-animated .hf-cab { animation: ht-lift 6.4s ease-in-out infinite; }
.ht-facade.is-animated .hf-lamp { animation: ht-blink 6.4s steps(1) infinite; }

@media (prefers-reduced-motion: reduce) {
  .ht-facade .hf-cab, .ht-facade .hf-lamp { animation: none; }
  .theme-hotel *, .theme-hotel *::before, .theme-hotel *::after {
    transition-duration: 0.01ms !important;
    animation-duration: 0.01ms !important;
    animation-iteration-count: 1 !important;
  }
}
```

- [ ] **Step 3: Écrire le hook des polices**

Créer `frontend/src/ui/hotel/useHotelFonts.js` :

```js
import { useEffect } from 'react';

// The theme's two families, requested only when a hotel screen mounts: the
// classic landing and today's dashboard never pay for them. A <link> rather
// than an @import in hotel.css, because CRA concatenates the chunk's CSS in an
// order we do not control, and an @import that is not first is dropped.
// index.html already preconnects to both Google Fonts hosts.
const HREF =
  'https://fonts.googleapis.com/css2?family=Bodoni+Moda:ital,opsz,wght@0,6..96,400..900;1,6..96,400..900&family=Jost:wght@300..700&display=swap';

export function useHotelFonts() {
  useEffect(() => {
    if (document.querySelector('link[data-hotel-fonts]')) return;
    const link = document.createElement('link');
    link.rel = 'stylesheet';
    link.href = HREF;
    link.dataset.hotelFonts = 'true';
    document.head.appendChild(link);
  }, []);
}
```

- [ ] **Step 4: Exposer les tokens à Tailwind**

Dans `frontend/tailwind.config.js`, dans `theme.extend.colors`, après la ligne `info: scale('info', [50, 100, 500, 600, 700]),` :

```js

        // Grand Hotel theme (src/styles/hotel.css). Plain CSS variables holding
        // hex values and set only under .theme-hotel, so opacity modifiers such
        // as /50 do not apply to them.
        hotel: {
          bg: 'var(--h-bg)',
          'bg-alt': 'var(--h-bg-alt)',
          surface: 'var(--h-surface)',
          sunk: 'var(--h-sunk)',
          pink: 'var(--h-pink)',
          text: 'var(--h-text)',
          muted: 'var(--h-muted)',
          line: 'var(--h-line)',
          plum: 'var(--h-plum)',
          accent: 'var(--h-accent)',
          mustard: 'var(--h-mustard)',
          teal: 'var(--h-teal)',
        },
```

et dans `theme.extend.fontFamily`, après la ligne `display: [...]` :

```js
        'hotel-display': ['var(--h-font-display)'],
        'hotel-ui': ['var(--h-font-ui)'],
```

- [ ] **Step 5: Générer la moodboard depuis la maquette validée**

Créer `.superpowers/landing-check/port-moodboard.mjs` :

```js
// Ports the validated moodboard mockup onto the real token file: its inline
// tokens and primitives are dropped in favour of frontend/src/styles/hotel.css,
// and its h- classes become the ht- ones that file defines.
// Usage, from this directory: node port-moodboard.mjs
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const REPO = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const SRC = path.join(REPO, 'docs/superpowers/specs/2026-09-24-landing-grand-hotel/moodboard-mockup.html');
const OUT = path.join(REPO, 'docs/design/moodboard.html');

let html = readFileSync(SRC, 'utf8');

function cut(from, to) {
  const a = html.indexOf(from);
  const b = html.indexOf(to, a);
  if (a < 0 || b < 0) throw new Error(`markers not found: ${from.trim()} / ${to.trim()}`);
  html = html.slice(0, a) + html.slice(b);
}

cut('  /* ===== Tokens: this block is what frontend/src/styles/hotel.css will hold ===== */', '  /* ===== Page ===== */');
cut('  /* ===== Components (future ui/hotel primitives) ===== */', '  .comp-grid {');

html = html.replace(/\bh-(btn|link|input|field|label|card|tag|badge|toast|panel|switch|check|plq|prop|slab)\b/g, 'ht-$1');
html = html
  .replace(/class="ht-tag gold"/g, 'class="ht-tag is-gold"')
  .replace(/class="ht-tag teal"/g, 'class="ht-tag is-teal"')
  .replace(/class="ht-tag plain"/g, 'class="ht-tag is-plain"')
  .replace(/class="ht-prop safe"/g, 'class="ht-prop is-safe"')
  .replace(/class="ht-switch on"/g, 'class="ht-switch is-on"')
  .replace(/class="ht-check on"/g, 'class="ht-check is-on"')
  .replace(/<a class="on"/g, '<a class="is-on"');

html = html.replace('<title>Mailsorter, système visuel</title>', '<title>Mailsorter, système visuel</title>\n<link rel="stylesheet" href="../../frontend/src/styles/hotel.css">');
html = html.replace('<body>', '<body class="theme-hotel">');
html = html.replace(
  '<style>\n',
  '<style>\n  /* Moodboard layout only. Tokens and ht- primitives come from\n     frontend/src/styles/hotel.css, the file the app ships, so this page cannot\n     drift from the code. */\n'
);

mkdirSync(path.dirname(OUT), { recursive: true });
writeFileSync(OUT, html);
console.log(`wrote ${path.relative(REPO, OUT)}`);
```

Run: `cd .superpowers/landing-check && node port-moodboard.mjs && node check.mjs moodboard`
Expected: `wrote docs/design/moodboard.html`, puis `"failures": []`.

- [ ] **Step 6: Comparer à la maquette**

Ouvrir `.superpowers/landing-check/shots/moodboard-day.png` et `moodboard-night.png` (outil Read), et les comparer à la maquette `docs/superpowers/specs/2026-09-24-landing-grand-hotel/moodboard-mockup.html` rendue de la même façon. Mêmes couleurs, mêmes boutons, même menu ascenseur, même maquette de dashboard, de jour comme de nuit. Un écart est un défaut de `hotel.css` : corriger la règle concernée, relancer `node check.mjs moodboard`, recomparer.

Run: `cd frontend && CI=false npm run build 2>&1 | grep -E "Compiled|Failed|ready"`
Expected: `The build folder is ready to be deployed.` (`hotel.css` n'est encore importé par aucun écran, le build doit rester vert).

- [ ] **Step 7: Commit**

```bash
/usr/bin/git add frontend/src/styles/hotel.css frontend/src/ui/hotel/useHotelFonts.js frontend/tailwind.config.js docs/design/moodboard.html
/usr/bin/git commit -m "feat(frontend): poser les tokens du theme grand hotel et la moodboard" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: la coquille de la landing (en-tête, ascenseur, pied de page)

**Files:**
- Create: `frontend/src/ui/hotel/Emblem.js`, `frontend/src/ui/hotel/ElevatorPanel.js`, `frontend/src/ui/hotel/Plaque.js`
- Create: `frontend/src/components/landing/landing.css`, `FloorHeading.js`, `scroll.js`, `useActiveFloor.js`, `useUnreadCounter.js`
- Create: `frontend/src/components/landing/HotelHeader.js`, `HotelHeader.css`, `ElevatorRail.js`, `ElevatorRail.css`, `HotelFooter.js`, `HotelFooter.css`
- Modify: `frontend/src/pages/HotelLanding.js` (remplacé en entier)
- Create (hors git): `.superpowers/landing-check/scenarios/shell.mjs`

**Interfaces:**
- Consumes: `useAuthForm` (Task 4), `useHotelFonts` et `hotel.css` (Task 5), `useInstance().selfHosted`, `useTheme()` et `THEMES` de `ui/theme.js`, `CONTACT_EMAIL` et `SOURCE_URL` de `components/PublicFooter.js`, `isAuthed` de `lib/session.js`.
- Produces:
  - `Emblem({ size })`, `ElevatorPanel({ items, active, label, onGo })` avec `items: [{ n, id, label }]`, `Plaque({ n, children })` ;
  - `FloorHeading({ n, kicker, id, lede, children })` ;
  - `scrollToId(id)`, `prefersReducedMotion()` dans `components/landing/scroll.js` ;
  - `useActiveFloor() -> string|null` (lit l'attribut `data-floor` des sections) ;
  - `UNREAD_START = 4212`, `useUnreadCounter(start) -> number`, `formatCount(n) -> string` dans `components/landing/useUnreadCounter.js` ;
  - `floorsFor(selfHosted) -> [{ n, id, label }]` exporté par `HotelHeader.js` ; ids de sections : `fonctionnement`, `confidentialite`, `tarifs`, `questions` ; le hall a l'id `hall` ;
  - dans `HotelLanding` : `openAuth(mode)` (bascule la fiche, remonte au hall, donne le focus à `#auth-email`), `floorNo` (id vers numéro d'étage), et un conteneur `<div className="hl-floors">` où les tâches suivantes insèrent leurs sections.

- [ ] **Step 1: Écrire le scénario de la coquille**

Créer `.superpowers/landing-check/scenarios/shell.mjs` :

```js
// Header, elevator menu, fonts and the day / night switch of the footer.
export default async function shell({ page, base, opt, expect, shot }) {
  await page.goto(`${base}/`);
  await page.waitForSelector('[data-landing="hotel"] .hl-top');
  const floors = await page.$$eval('.hl-top .ht-panel a', (as) => as.map((a) => a.textContent.trim()));
  const selfHosted = opt.edition === 'self-hosted';
  expect(floors.length === (selfHosted ? 3 : 4), `the panel has ${floors.length} floors: ${floors.join(', ')}`);
  expect(floors.some((f) => f.includes('Tarifs')) === !selfHosted, 'Tarifs must be absent exactly when self-hosted');
  expect(Boolean(await page.$('link[data-hotel-fonts]')), 'the hotel fonts were not requested');

  await page.click('.hl-walk__theme button:has-text("Nuit")');
  await page.waitForTimeout(300);
  expect(await page.evaluate(() => document.documentElement.classList.contains('dark')), 'the footer switch did not turn the night on');
  const night = await page.evaluate(() => getComputedStyle(document.querySelector('.theme-hotel')).backgroundColor);
  expect(night === 'rgb(28, 18, 32)', `night background is ${night}, want rgb(28, 18, 32)`);
  await shot('night');
  await page.click('.hl-walk__theme button:has-text("Jour")');
  await page.waitForTimeout(300);
  const day = await page.evaluate(() => getComputedStyle(document.querySelector('.theme-hotel')).backgroundColor);
  expect(day === 'rgb(246, 227, 217)', `day background is ${day}, want rgb(246, 227, 217)`);
  await shot('day');
}
```

Run: `cd .superpowers/landing-check && node check.mjs shell`
Expected: ÉCHEC, `.hl-top` introuvable (timeout).

- [ ] **Step 2: Les briques de `ui/hotel`**

Créer `frontend/src/ui/hotel/Emblem.js` :

```jsx
import React from 'react';

// The mark: an envelope under a pediment. Fixed colours, like every drawing of
// the theme: it reads the same by day and by night.
export default function Emblem({ size = 34 }) {
  return (
    <svg width={size} height={size} viewBox="0 0 34 34" aria-hidden="true">
      <polygon points="3,14 17,4 31,14" fill="#7A2E3B" stroke="#2B1B1E" strokeWidth="1.6" strokeLinejoin="round" />
      <rect x="5" y="14" width="24" height="16" fill="#FBF1E4" stroke="#2B1B1E" strokeWidth="1.6" />
      <path d="M5.5 14.5 L17 23 L28.5 14.5" fill="none" stroke="#2B1B1E" strokeWidth="1.6" />
      <circle cx="17" cy="11" r="2" fill="#E3A93B" stroke="#2B1B1E" />
    </svg>
  );
}
```

Créer `frontend/src/ui/hotel/ElevatorPanel.js` :

```jsx
import React from 'react';

// The elevator button panel: numbered brass buttons, the current floor lit.
// The landing's menu today, the dashboard's later.
export default function ElevatorPanel({ items, active, label = 'Sections', onGo }) {
  return (
    <nav className="ht-panel" aria-label={label}>
      {items.map((it) => (
        <a
          key={it.id}
          href={`#${it.id}`}
          className={it.id === active ? 'is-on' : undefined}
          aria-current={it.id === active ? 'location' : undefined}
          onClick={onGo ? (event) => onGo(event, it.id) : undefined}
        >
          <i aria-hidden="true">{it.n}</i>
          <span>{it.label}</span>
        </a>
      ))}
    </nav>
  );
}
```

Créer `frontend/src/ui/hotel/Plaque.js` :

```jsx
import React from 'react';

// A numbered brass plaque: one short line with its roman numeral.
export default function Plaque({ n, children }) {
  return (
    <div className="ht-plq">
      <b aria-hidden="true">{n}</b>
      <p>{children}</p>
    </div>
  );
}
```

- [ ] **Step 3: Les utilitaires de la landing**

Créer `frontend/src/components/landing/scroll.js` :

```js
export function prefersReducedMotion() {
  return typeof window !== 'undefined' && Boolean(window.matchMedia) && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

// Smooth unless the visitor asked for less motion. No hash is pushed: the
// landing is one page and a #floor in the URL would outlive the visit.
export function scrollToId(id) {
  const el = document.getElementById(id);
  if (el) el.scrollIntoView({ behavior: prefersReducedMotion() ? 'auto' : 'smooth', block: 'start' });
}
```

Créer `frontend/src/components/landing/useActiveFloor.js` :

```js
import { useEffect, useState } from 'react';

// Which floor the middle of the screen is on, read from the data-floor
// attribute of each section (an empty value for the hall and the night exit).
// It lights the matching button of the elevator panel.
export function useActiveFloor() {
  const [active, setActive] = useState(null);

  useEffect(() => {
    const sections = Array.from(document.querySelectorAll('[data-floor]'));
    if (!sections.length || typeof IntersectionObserver === 'undefined') return undefined;
    const io = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) setActive(entry.target.dataset.floor || null);
        });
      },
      { rootMargin: '-45% 0px -50% 0px' }
    );
    sections.forEach((s) => io.observe(s));
    return () => io.disconnect();
  }, []);

  return active;
}
```

Créer `frontend/src/components/landing/useUnreadCounter.js` :

```js
import { useEffect, useState } from 'react';
import { prefersReducedMotion } from './scroll';

export const UNREAD_START = 4212;

// The "4 212 non lus" figure. It ticks down by 1 to 3 every 3.2 s, in step with
// the elevator of the hall, and stands still for anyone who asked for less
// motion. Decorative: it is not announced to screen readers as it changes.
export function useUnreadCounter(start = UNREAD_START) {
  const [n, setN] = useState(start);

  useEffect(() => {
    if (prefersReducedMotion()) return undefined;
    const timer = setInterval(() => setN((v) => Math.max(0, v - 1 - Math.floor(Math.random() * 3))), 3200);
    return () => clearInterval(timer);
  }, []);

  return n;
}

// "4 212", with a no-break space so the figure never wraps in two.
export function formatCount(v) {
  return String(v).replace(/\B(?=(\d{3})+(?!\d))/g, ' ');
}
```

Créer `frontend/src/components/landing/FloorHeading.js` :

```jsx
import React from 'react';

// The heading of a floor: its number on a brass button, a kicker, the title
// (with an <em> for the italic accent) and an optional lede.
export default function FloorHeading({ n, kicker, id, lede, children }) {
  return (
    <div className="hl-fh">
      {kicker && (
        <span className="hl-fh__k">
          {n ? <b aria-hidden="true">{n}</b> : null}
          {kicker}
        </span>
      )}
      <h2 id={id} className="hl-fh__title">
        {children}
      </h2>
      {lede && <p className="hl-fh__lede">{lede}</p>}
    </div>
  );
}
```

Créer `frontend/src/components/landing/landing.css` :

```css
/* Layout shared by every section of the Grand Hotel landing. Section rules live
 * next to their component. Every class starts with hl-: a stylesheet loaded
 * by the landing stays loaded after navigating away, and a generic name would
 * then restyle the dashboard. */

.hl-page { min-height: 100vh; }
.hl-wrap { max-width: 1240px; margin: 0 auto; padding: 0 40px; }
.hl-section { padding: 80px 0 90px; }
.hl-section--alt { background: var(--h-bg-alt); }
.theme-hotel [data-floor] { scroll-margin-top: 80px; }

.hl-fh { text-align: center; }
.hl-fh__k {
  display: inline-flex;
  align-items: center;
  gap: 12px;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.3em;
  text-transform: uppercase;
  color: var(--h-accent);
}
.hl-fh__k b {
  display: grid;
  place-items: center;
  width: 30px;
  height: 30px;
  border-radius: 50%;
  background: #E3A93B;
  border: 1.5px solid var(--h-line);
  color: #2B1B1E;
  font-family: var(--h-font-display);
  font-size: 14px;
  letter-spacing: 0;
}
.hl-fh__title {
  margin-top: 14px;
  font-family: var(--h-font-display);
  font-variation-settings: 'opsz' 28;
  font-weight: 400;
  font-size: clamp(34px, 4.2vw, 54px);
  line-height: 1.04;
  letter-spacing: -0.01em;
}
.hl-fh__title em { color: var(--h-accent); font-weight: 500; }
.hl-fh__lede { max-width: 54ch; margin: 14px auto 0; font-size: 16.5px; line-height: 1.55; color: var(--h-muted); }

.hl-count { color: var(--h-accent); font-weight: 500; font-variant-numeric: lining-nums tabular-nums; }

@media (max-width: 640px) {
  .hl-wrap { padding: 0 16px; }
  .hl-section { padding: 56px 0 64px; }
}
```

- [ ] **Step 4: En-tête, rail et pied de page**

Créer `frontend/src/components/landing/HotelHeader.js` :

```jsx
import React from 'react';
import Emblem from '../../ui/hotel/Emblem';
import ElevatorPanel from '../../ui/hotel/ElevatorPanel';
import { scrollToId } from './scroll';
import './HotelHeader.css';

// The floors the header can take you to. Tarifs is absent when the instance
// bills nobody, like everywhere else in the SPA.
export function floorsFor(selfHosted) {
  return [
    { n: 1, id: 'fonctionnement', label: 'Fonctionnement' },
    { n: 2, id: 'confidentialite', label: 'Confidentialité' },
    ...(selfHosted ? [] : [{ n: 3, id: 'tarifs', label: 'Tarifs' }]),
    { n: selfHosted ? 3 : 4, id: 'questions', label: 'Questions' },
  ];
}

export default function HotelHeader({ floors, active, onSignIn }) {
  const go = (event, id) => {
    event.preventDefault();
    scrollToId(id);
  };
  return (
    <header className="hl-top">
      <div className="hl-wrap hl-top__row">
        <a className="hl-brand" href="#hall" onClick={(event) => go(event, 'hall')}>
          <Emblem size={34} />
          <b>Mailsorter</b>
        </a>
        <ElevatorPanel items={floors} active={active} onGo={go} />
        <button type="button" className="hl-top__login" onClick={onSignIn}>
          Se connecter
        </button>
      </div>
    </header>
  );
}
```

Créer `frontend/src/components/landing/HotelHeader.css` :

```css
.hl-top {
  position: sticky;
  top: 0;
  z-index: 20;
  background: repeating-linear-gradient(90deg, var(--h-stripe) 0 24px, transparent 24px 48px), var(--h-bg);
  border-bottom: 2px solid var(--h-line);
}
.hl-top__row { display: grid; grid-template-columns: 1fr auto 1fr; align-items: center; height: 76px; }
.hl-brand { display: inline-flex; align-items: center; gap: 10px; justify-self: start; color: var(--h-text); }
.hl-brand b { font-family: var(--h-font-display); font-style: italic; font-weight: 500; font-size: 26px; line-height: 1; }
.hl-top__login {
  justify-self: end;
  color: var(--h-text);
  border-bottom: 1.5px solid var(--h-line);
  font-family: var(--h-font-display);
  font-style: italic;
  font-size: 16px;
}
@media (max-width: 1000px) {
  .hl-top__row { grid-template-columns: auto 1fr; height: 64px; }
  .hl-top .ht-panel { display: none; }
}
```

Créer `frontend/src/components/landing/ElevatorRail.js` :

```jsx
import React, { useEffect, useRef } from 'react';
import './ElevatorRail.css';

// A thin shaft down the left edge on wide screens; the cabin follows the scroll.
export default function ElevatorRail() {
  const cab = useRef(null);

  useEffect(() => {
    const el = cab.current;
    if (!el) return undefined;
    const move = () => {
      const rail = el.parentElement;
      const max = document.documentElement.scrollHeight - window.innerHeight;
      const progress = max > 0 ? window.scrollY / max : 0;
      el.style.transform = `translateY(${Math.round(progress * (rail.clientHeight - el.offsetHeight))}px)`;
    };
    move();
    window.addEventListener('scroll', move, { passive: true });
    window.addEventListener('resize', move);
    return () => {
      window.removeEventListener('scroll', move);
      window.removeEventListener('resize', move);
    };
  }, []);

  return (
    <div className="hl-rail" aria-hidden="true">
      <div className="hl-rail__cab" ref={cab} />
    </div>
  );
}
```

Créer `frontend/src/components/landing/ElevatorRail.css` :

```css
.hl-rail { position: fixed; left: 18px; top: 110px; bottom: 40px; z-index: 15; width: 16px; }
.hl-rail::before { content: ''; position: absolute; left: 7px; top: 0; bottom: 0; width: 2px; background: var(--h-line); opacity: 0.35; }
.hl-rail__cab {
  position: absolute;
  left: 0;
  top: 0;
  width: 16px;
  height: 22px;
  background: #E3A93B;
  border: 1.5px solid #2B1B1E;
  border-radius: 2px;
  transition: transform 0.15s linear;
}
.hl-rail__cab::after {
  content: '';
  position: absolute;
  inset: 4px 3px;
  background: repeating-linear-gradient(90deg, #2B1B1E 0 1px, transparent 1px 3px);
  opacity: 0.5;
}
@media (max-width: 1340px) { .hl-rail { display: none; } }
```

Créer `frontend/src/components/landing/HotelFooter.js` :

```jsx
import React from 'react';
import { Link } from 'react-router-dom';
import { THEMES, useTheme } from '../../ui/theme';
import { CONTACT_EMAIL, SOURCE_URL } from '../PublicFooter';
import './HotelFooter.css';

const LABELS = { light: 'Jour', dark: 'Nuit', system: 'Auto' };

// The pavement under the hotel: the legal links every public page must carry
// (Google's OAuth review asks for the privacy policy from the home page), and
// the day / night / auto switch, which is the app's own theme setting.
export default function HotelFooter() {
  const { theme, setTheme } = useTheme();
  return (
    <footer className="hl-walk">
      <div className="hl-wrap hl-walk__row">
        <nav className="hl-walk__links" aria-label="Liens utiles">
          <Link to="/confidentialite">Confidentialité</Link>
          <Link to="/conditions">Conditions</Link>
          <a href={`mailto:${CONTACT_EMAIL}`}>Contact</a>
          <a href={SOURCE_URL} target="_blank" rel="noreferrer">
            Code source
          </a>
        </nav>
        <div className="hl-walk__theme" role="group" aria-label="Thème">
          {THEMES.map((t) => (
            <button key={t} type="button" aria-pressed={theme === t} className={theme === t ? 'is-on' : undefined} onClick={() => setTheme(t)}>
              {LABELS[t]}
            </button>
          ))}
        </div>
      </div>
    </footer>
  );
}
```

Créer `frontend/src/components/landing/HotelFooter.css` :

```css
.hl-walk { background: #7A2E3B; border-top: 2px solid #000; color: #FBF1E4; }
.hl-walk__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 20px;
  min-height: 64px;
  padding-top: 12px;
  padding-bottom: 12px;
  font-size: 13.5px;
}
.hl-walk__links { display: flex; flex-wrap: wrap; gap: 24px; }
.hl-walk__links a:hover { text-decoration: underline; }
.hl-walk__theme { display: inline-flex; overflow: hidden; border: 1.5px solid #FBF1E4; border-radius: 20px; }
.hl-walk__theme button {
  padding: 5px 12px;
  color: #FBF1E4;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}
.hl-walk__theme button.is-on { background: #FBF1E4; color: #7A2E3B; }
```

- [ ] **Step 5: Monter la coquille dans `HotelLanding`**

Remplacer tout le contenu de `frontend/src/pages/HotelLanding.js` par :

```jsx
import React, { useCallback, useEffect, useMemo } from 'react';
import { useNavigate } from 'react-router-dom';
import { useInstance } from '../contexts/InstanceContext';
import { isAuthed } from '../lib/session';
import { useAuthForm } from '../lib/useAuthForm';
import { useHotelFonts } from '../ui/hotel/useHotelFonts';
import HotelHeader, { floorsFor } from '../components/landing/HotelHeader';
import ElevatorRail from '../components/landing/ElevatorRail';
import HotelFooter from '../components/landing/HotelFooter';
import { useActiveFloor } from '../components/landing/useActiveFloor';
import { scrollToId } from '../components/landing/scroll';
import '../styles/hotel.css';
import '../components/landing/landing.css';

// The "Grand Hotel" landing, served on / when UI_THEME=hotel (lib/uiTheme.js).
// It owns the auth form state so the header, the hall, the demo and the night
// exit all open the same form, in the right mode.
export default function HotelLanding() {
  const navigate = useNavigate();
  const { selfHosted } = useInstance();
  useHotelFonts();
  const auth = useAuthForm((to) => navigate(to));
  const floors = useMemo(() => floorsFor(selfHosted), [selfHosted]);
  const active = useActiveFloor();

  // Same rule as the classic landing: someone already signed in has nothing
  // to do here.
  useEffect(() => {
    if (isAuthed()) navigate(localStorage.getItem('hasMailbox') ? '/inbox' : '/connect');
  }, [navigate]);

  const { setMode } = auth;
  const openAuth = useCallback(
    (mode) => {
      setMode(mode);
      scrollToId('hall');
      window.setTimeout(() => {
        const field = document.getElementById('auth-email');
        if (field) field.focus({ preventScroll: true });
      }, 350);
    },
    [setMode]
  );

  return (
    <div className="theme-hotel hl-page" data-landing="hotel">
      <HotelHeader floors={floors} active={active} onSignIn={() => openAuth('login')} />
      <ElevatorRail />
      <div className="hl-floors" />
      <HotelFooter />
    </div>
  );
}
```

- [ ] **Step 6: Vérifier**

Run: `cd frontend && CI=false npm run build 2>&1 | grep -E "Compiled|Failed|ready" ; cd ../.superpowers/landing-check && node check.mjs shell && node check.mjs shell --edition=self-hosted && node check.mjs switch`
Expected: build prêt ; trois sorties `"failures": []`.

- [ ] **Step 7: Commit**

```bash
/usr/bin/git add frontend/src/ui/hotel/Emblem.js frontend/src/ui/hotel/ElevatorPanel.js frontend/src/ui/hotel/Plaque.js frontend/src/components/landing frontend/src/pages/HotelLanding.js
/usr/bin/git commit -m "feat(frontend): monter la coquille de la landing grand hotel" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 7: le hall, la fiche d'inscription et la sortie de nuit

**Files:**
- Create: `frontend/src/components/landing/features.js`
- Create: `frontend/src/ui/hotel/HotelFacade.js`
- Create: `frontend/src/components/landing/HallHero.js`, `HallHero.css`, `SignupCard.js`, `SignupCard.css`, `NightExit.js`, `NightExit.css`
- Modify: `frontend/src/pages/HotelLanding.js`
- Modify (hors git): `.superpowers/landing-check/test-pure.mjs` ; Create: `scenarios/hall.mjs`

**Interfaces:**
- Consumes: `useAuthForm` (retour décrit en Task 4), `Plaque`, `FloorHeading`, `formatCount`, `useUnreadCounter`, `scrollToId` (Task 6), `useTheme().isDark`, `Spinner` (`{ size, className }`), icône `Google` (`{ size }`) de `ui/icons.js`.
- Produces:
  - `features.js` : `GMAIL_ONLY` (objet `{ undo, rules, snooze, unsubscribe, digest }` vers libellé avec article), `isGmailOnly(feature) -> bool`, `canPromise(feature, isConfigured) -> bool`, `joinFr(items) -> string`, `imapLegend() -> string`, `outlookAnswer() -> string` ;
  - `HotelFacade({ night, animated, className, title })` : SVG de jour animé (`ht-facade is-animated`) ou de nuit ;
  - `HallHero({ count, auth, isConfigured, night })` (section `id="hall"`), `SignupCard({ auth, isConfigured })` (formulaire `id="auth-card"`, champs `#auth-email` et `#auth-password`, bascule `[data-auth-switch]`), `NightExit({ count, onStart, onSignIn })`.

- [ ] **Step 1: Écrire les vérifications de `features.js`**

Ajouter à la fin de `.superpowers/landing-check/test-pure.mjs`, après la ligne `console.log('uiTheme: ok');` :

```js
import { GMAIL_ONLY, canPromise, imapLegend, isGmailOnly, joinFr, outlookAnswer } from '../../frontend/src/components/landing/features.js';

assert.equal(joinFr([]), '');
assert.equal(joinFr(['a']), 'a');
assert.equal(joinFr(['a', 'b']), 'a et b');
assert.equal(joinFr(['a', 'b', 'c']), 'a, b et c');
assert.ok(isGmailOnly('undo') && isGmailOnly('snooze') && !isGmailOnly('protected'));
assert.equal(canPromise('undo', true), true, 'with Google everything may be promised');
assert.equal(canPromise('undo', false), false, 'without Google, a Gmail-only feature may not');
assert.equal(canPromise('protected', false), true, 'what works over IMAP may always be promised');
assert.equal(imapLegend(), "Lecture et tri par IA. L'annulation, les règles, le report, le désabonnement et le récap arrivent.");
assert.ok(outlookAnswer().startsWith("Oui pour lire vos e-mails, les faire trier par l'IA et agir dessus. L'annulation"));
assert.ok(outlookAnswer().endsWith('sont pour l\'instant réservés à Gmail. Ça arrive.'));
assert.equal(Object.keys(GMAIL_ONLY).length, 5);
console.log('features: ok');
```

Puis remonter la ligne `import { GMAIL_ONLY, ... }` en tête du fichier, sous le premier `import` : c'est la convention du fichier, et les `import` d'un module ES sont de toute façon évalués avant le code.

Run: `cd .superpowers/landing-check && node test-pure.mjs`
Expected: `ERR_MODULE_NOT_FOUND` sur `components/landing/features.js`.

- [ ] **Step 2: Écrire `features.js`**

Créer `frontend/src/components/landing/features.js` :

```js
// What the landing may promise, in one place.
//
// GMAIL_ONLY lists the features still reachable only through the Gmail API:
// over IMAP they answer 501 (see gmailClientFor in backend/internal/api), so the
// landing must not promise them to someone who can only connect a mailbox.
// Porting a feature to IMAP is deleting its line here: the corridor badges, the
// key-board legend, the hall, the demo and the "Outlook" answer all follow.
// Each label carries its article, because it is used inside sentences.
export const GMAIL_ONLY = {
  undo: "l'annulation",
  rules: 'les règles',
  snooze: 'le report',
  unsubscribe: 'le désabonnement',
  digest: 'le récap',
};

export function isGmailOnly(feature) {
  return Object.prototype.hasOwnProperty.call(GMAIL_ONLY, feature);
}

// Without Google credentials nobody on this instance can reach the Gmail API,
// so a Gmail-only feature is a promise nobody here could keep.
export function canPromise(feature, isConfigured) {
  return Boolean(isConfigured) || !isGmailOnly(feature);
}

// "a", "a et b", "a, b et c".
export function joinFr(items) {
  if (items.length <= 1) return items.join('');
  return `${items.slice(0, -1).join(', ')} et ${items[items.length - 1]}`;
}

const capitalize = (s) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s);

// The legend of the pink keys: what a mailbox connected over IMAP gets today.
export function imapLegend() {
  const missing = Object.values(GMAIL_ONLY);
  if (!missing.length) return 'Toutes les fonctions.';
  return `Lecture et tri par IA. ${capitalize(joinFr(missing))} ${missing.length > 1 ? 'arrivent' : 'arrive'}.`;
}

// The answer to "Ça marche avec Outlook ?".
export function outlookAnswer() {
  const missing = Object.values(GMAIL_ONLY);
  if (!missing.length) return 'Oui, avec toutes les fonctions.';
  const verb = missing.length > 1 ? "sont pour l'instant réservés" : "est pour l'instant réservé";
  return `Oui pour lire vos e-mails, les faire trier par l'IA et agir dessus. ${capitalize(joinFr(missing))} ${verb} à Gmail. Ça arrive.`;
}
```

Run: `cd .superpowers/landing-check && node test-pure.mjs`
Expected: `uiTheme: ok` puis `features: ok`.

- [ ] **Step 3: Écrire le scénario du hall**

Créer `.superpowers/landing-check/scenarios/hall.mjs` :

```js
// The hall: the unread figure, the hotel, the three lines, the form, and the
// header's "Se connecter" opening the form in sign-in mode.
export default async function hall({ page, base, opt, expect, shot }) {
  await page.goto(`${base}/`);
  await page.waitForSelector('.hl-hall .ht-facade');
  const read = () => page.$eval('.hl-hall .hl-count', (el) => Number(el.textContent.replace(/\D/g, '')));
  const first = await read();
  expect(first === 4212, `the hall opens on ${first} unread, want 4212`);
  await page.waitForTimeout(3600);
  const later = await read();
  if (opt.reduced) expect(later === first, `with reduced motion the figure moved from ${first} to ${later}`);
  else expect(later < first, `the figure did not tick down (${first} then ${later})`);

  const google = opt.google !== 'off';
  const lines = await page.$$eval('.hl-hall .ht-plq p', (ps) => ps.map((p) => p.textContent));
  expect(lines.length === 3, `the hall has ${lines.length} lines, want 3`);
  expect(lines[2].includes("Tout s'annule") === google, `third line "${lines[2]}" with google=${google}`);
  expect(lines[1].includes('reporter') === google, `second line "${lines[1]}" with google=${google}`);
  expect(Boolean(await page.$('.hl-fiche__google')) === google, 'the Google button must show exactly when Google is configured');
  expect(Boolean(await page.$('.hl-exit .hl-count')), 'the night exit has no figure');

  await page.click('.hl-top__login');
  await page.waitForTimeout(800);
  const title = await page.$eval('.hl-fiche__top h2', (el) => el.textContent);
  expect(title === 'Se connecter', `"Se connecter" left the form on "${title}"`);
  const focused = await page.evaluate(() => document.activeElement && document.activeElement.id);
  expect(focused === 'auth-email', `after "Se connecter" the focus is on "${focused}", want auth-email`);
  await shot('hall');
}
```

Run: `cd .superpowers/landing-check && node check.mjs hall`
Expected: ÉCHEC, `.hl-hall .ht-facade` introuvable.

- [ ] **Step 4: Écrire la façade**

Créer `frontend/src/ui/hotel/HotelFacade.js` :

```jsx
import React, { useId } from 'react';

// The hotel in cross-section: each room is a category of mail, the elevator
// carries an envelope up and down, the concierge waits at the desk. The night
// version is the same building, windows lit. Colours are fixed on purpose:
// the drawing is the same object by day and by night, only the page around it
// changes. Text styles (hf-*) and the elevator motion live in styles/hotel.css.
const INK = '#2B1B1E';
const PLUM = '#7A2E3B';
const MUST = '#E3A93B';
const TEAL = '#2F6F73';
const CREAM = '#FBF1E4';
const PINK = '#EFB7C3';
const PINK2 = '#F7D6DC';
const CARPET = '#B8586B';
const KRAFT = '#C99A62';
const PAPER = '#FFFAF2';

function Env({ id, x, y, w = 20, h = 14, rotate }) {
  return <use href={`#${id}`} x={x} y={y} width={w} height={h} transform={rotate ? `rotate(${rotate})` : undefined} />;
}

// A room at (x, y), 80 x 92: wallpaper, carpet, brass number plate, label.
function Room({ x, y, n, label, wp, vip = false, children }) {
  const wide = n.length > 3;
  return (
    <g>
      <rect x={x} y={y} width="80" height="92" fill={vip ? PINK2 : `url(#${wp})`} stroke={INK} strokeWidth="1.5" />
      <rect x={x} y={y + 84} width="80" height="8" fill={vip ? PLUM : CARPET} />
      <rect x={x + (wide ? 20 : 23)} y={y + 6} width={wide ? 40 : 34} height="13" rx="2" fill={MUST} stroke={INK} />
      <text x={x + 40} y={y + 15.6} className="hf-num">
        {n}
      </text>
      <text x={x + 40} y={y + 32} className="hf-lab">
        {label}
      </text>
      {children}
    </g>
  );
}

function DayFacade({ uid, title, className }) {
  const wp = `hwp-${uid}`;
  const aw = `haw-${uid}`;
  const ck = `hck-${uid}`;
  const env = `henv-${uid}`;
  return (
    <svg className={className} viewBox="0 0 600 530" role="img" aria-label={title}>
      <defs>
        <pattern id={wp} width="8" height="8" patternUnits="userSpaceOnUse">
          <rect width="8" height="8" fill="#FBEADF" />
          <circle cx="4" cy="4" r="1" fill="#EBB0BD" />
        </pattern>
        <pattern id={aw} width="12" height="12" patternUnits="userSpaceOnUse">
          <rect width="6" height="12" fill={PLUM} />
          <rect x="6" width="6" height="12" fill={CREAM} />
        </pattern>
        <pattern id={ck} width="16" height="16" patternUnits="userSpaceOnUse">
          <rect width="16" height="16" fill={CREAM} />
          <rect width="8" height="8" fill={PLUM} />
          <rect x="8" y="8" width="8" height="8" fill={PLUM} />
        </pattern>
        <symbol id={env} viewBox="0 0 20 14">
          <rect x=".7" y=".7" width="18.6" height="12.6" fill={PAPER} stroke={INK} strokeWidth="1.2" />
          <path d="M1 1.2l9 6.6 9-6.6" fill="none" stroke={INK} strokeWidth="1.2" />
        </symbol>
      </defs>

      <g fill={MUST} stroke={INK} strokeWidth="1">
        <polygon points="36,70 40,62 44,70 40,78" />
        <polygon points="560,86 564,78 568,86 564,94" />
        <polygon points="22,200 25,194 28,200 25,206" />
        <polygon points="576,230 579,224 582,230 579,236" />
      </g>
      <g stroke={INK} strokeWidth="1.5">
        <line x1="88" y1="122" x2="88" y2="74" />
        <polygon points="88,74 110,81 88,88" fill={MUST} />
        <line x1="512" y1="122" x2="512" y2="74" />
        <polygon points="512,74 490,81 512,88" fill={MUST} />
      </g>
      <polygon points="66,124 300,38 534,124" fill={PLUM} stroke={INK} strokeWidth="2" />
      <polygon points="112,116 300,54 488,116" fill={PINK2} stroke={INK} strokeWidth="1.2" />
      <circle cx="300" cy="92" r="18" fill={CREAM} stroke={INK} strokeWidth="2" />
      <g stroke={INK} strokeWidth="1.4" strokeLinecap="round">
        <line x1="300" y1="92" x2="300" y2="80" />
        <line x1="300" y1="92" x2="309" y2="96" />
        <line x1="300" y1="76" x2="300" y2="78" />
        <line x1="316" y1="92" x2="314" y2="92" />
        <line x1="300" y1="108" x2="300" y2="106" />
        <line x1="284" y1="92" x2="286" y2="92" />
      </g>
      <rect x="58" y="122" width="484" height="12" fill={CREAM} stroke={INK} strokeWidth="2" />
      <rect x="80" y="134" width="440" height="366" fill={PINK} stroke={INK} strokeWidth="2" />
      <rect x="128" y="143" width="344" height="26" fill={PLUM} stroke={INK} strokeWidth="1.5" />
      <text x="300" y="160.5" className="hf-sign">
        MAILSORTER
      </text>

      <Room x={96} y={180} n="201" label="NEWSLETTERS" wp={wp}>
        <Env id={env} x={100} y={248} />
        <Env id={env} x={118} y={249} rotate="4 128 256" />
        <Env id={env} x={108} y={236} rotate="-6 118 243" />
        <Env id={env} x={126} y={236} rotate="5 136 243" />
        <Env id={env} x={116} y={223} rotate="-3 126 230" />
        <g transform="rotate(-9 160 244)">
          <rect x="146" y="238" width="28" height="11" fill={PLUM} stroke={INK} />
          <text x="160" y="245.8" className="hf-tiny">
            COMPLET
          </text>
        </g>
        <line x1="160" y1="249" x2="160" y2="264" stroke={INK} strokeWidth="1.2" />
      </Room>
      <Room x={184} y={180} n="202" label="PROMOS" wp={wp}>
        <Env id={env} x={192} y={244} rotate="-16 202 251" />
        <Env id={env} x={222} y={246} rotate="12 232 253" />
        <Env id={env} x={207} y={228} rotate="6 217 235" />
        <g fill={MUST} stroke={INK} strokeWidth=".6">
          <rect x="194" y="222" width="4" height="4" transform="rotate(20 196 224)" />
          <rect x="248" y="230" width="4" height="4" transform="rotate(-25 250 232)" />
          <rect x="240" y="220" width="3" height="3" />
        </g>
        <g fill={TEAL}>
          <circle cx="200" cy="236" r="1.8" />
          <circle cx="252" cy="246" r="1.8" />
          <circle cx="236" cy="226" r="1.5" />
        </g>
      </Room>
      <Room x={336} y={180} n="203" label="TRAVAIL" wp={wp}>
        <rect x="348" y="246" width="54" height="6" fill={PLUM} stroke={INK} />
        <g stroke={INK} strokeWidth="1.4">
          <line x1="352" y1="252" x2="352" y2="264" />
          <line x1="398" y1="252" x2="398" y2="264" />
          <line x1="392" y1="246" x2="392" y2="228" />
        </g>
        <polygon points="384,230 400,230 395,221 389,221" fill={MUST} stroke={INK} />
        <Env id={env} x={356} y={232} />
      </Room>
      <Room x={424} y={180} n="204" label="VOYAGES" wp={wp}>
        <path d="M456 234 v-6 h16 v6" fill="none" stroke={INK} strokeWidth="1.6" />
        <rect x="444" y="234" width="40" height="28" rx="3" fill={TEAL} stroke={INK} strokeWidth="1.4" />
        <circle cx="454" cy="246" r="5" fill={MUST} stroke={INK} strokeWidth=".8" />
        <circle cx="472" cy="252" r="4" fill={PINK} stroke={INK} strokeWidth=".8" />
        <rect x="466" y="238" width="12" height="7" fill={CREAM} stroke={INK} strokeWidth=".8" transform="rotate(8 472 241)" />
      </Room>

      <Room x={96} y={284} n="101" label="FACTURES" wp={wp}>
        <Env id={env} x={106} y={352} w={22} h={15} />
        <Env id={env} x={106} y={343} w={22} h={15} />
        <Env id={env} x={106} y={334} w={22} h={15} />
        <rect x="140" y="336" width="26" height="31" fill="#4A3A3E" stroke={INK} strokeWidth="1.4" />
        <circle cx="153" cy="351" r="6" fill={MUST} stroke={INK} />
        <line x1="153" y1="351" x2="156" y2="347" stroke={INK} strokeWidth="1.2" />
      </Room>
      <Room x={184} y={284} n="102" label="COLIS" wp={wp}>
        <g stroke={INK} strokeWidth="1.3" fill={KRAFT}>
          <rect x="194" y="342" width="26" height="26" />
          <rect x="222" y="348" width="22" height="20" />
          <rect x="204" y="324" width="22" height="18" />
        </g>
        <g stroke={INK} strokeWidth=".9" opacity=".7">
          <line x1="207" y1="342" x2="207" y2="368" />
          <line x1="233" y1="348" x2="233" y2="368" />
          <line x1="215" y1="324" x2="215" y2="342" />
        </g>
      </Room>
      <Room x={336} y={284} n="103" label="PERSO" wp={wp}>
        <Env id={env} x={352} y={348} w={22} h={15} />
        <path d="M363 336 c-3 -4.5 -9 -1 -5.5 3.5 l5.5 5.5 l5.5 -5.5 c3.5 -4.5 -2.5 -8 -5.5 -3.5z" fill={CARPET} stroke={INK} strokeWidth=".9" />
        <path d="M392 368 l2 -14 h12 l2 14z" fill={TEAL} stroke={INK} strokeWidth="1.2" />
        <path d="M400 354 q-2 -10 -8 -14 M400 354 q2 -12 8 -16" fill="none" stroke={INK} strokeWidth="1.1" />
        <circle cx="392" cy="339" r="4" fill={MUST} stroke={INK} strokeWidth=".9" />
        <circle cx="408" cy="337" r="4" fill={PINK} stroke={INK} strokeWidth=".9" />
      </Room>
      <Room x={424} y={284} n="SUITE" label="VIP" wp={wp} vip>
        <polygon points="464,322 466.5,328 473,328 468,332 470,338 464,334.5 458,338 460,332 455,328 461.5,328" fill={MUST} stroke={INK} strokeWidth=".8" />
        <Env id={env} x={453} y={340} w={22} h={15} />
        <g stroke={INK} strokeWidth="1.2" fill={MUST}>
          <rect x="436" y="346" width="5" height="22" />
          <rect x="487" y="346" width="5" height="22" />
        </g>
        <path d="M440 350 Q464 366 488 350" fill="none" stroke={PLUM} strokeWidth="3.2" strokeLinecap="round" />
      </Room>
      {/* Lit when the elevator stops at floor 1 (ht-blink, in step with ht-lift). */}
      <rect className="hf-lamp" x="96" y="284" width="80" height="92" fill="#FFE39A" opacity="0" style={{ mixBlendMode: 'multiply' }} />

      <rect x="276" y="176" width="48" height="204" fill={TEAL} stroke={INK} strokeWidth="1.5" />
      <g stroke={INK} strokeWidth="1.2" opacity=".8">
        <line x1="292" y1="176" x2="292" y2="380" />
        <line x1="308" y1="176" x2="308" y2="380" />
      </g>
      <g className="hf-cab">
        <rect x="282" y="212" width="36" height="54" fill={MUST} stroke={INK} strokeWidth="1.5" />
        <Env id={env} x={290} y={242} />
        <g stroke={INK} strokeWidth=".8" opacity=".75">
          <line x1="288" y1="216" x2="288" y2="262" />
          <line x1="294" y1="216" x2="294" y2="262" />
          <line x1="300" y1="216" x2="300" y2="262" />
          <line x1="306" y1="216" x2="306" y2="262" />
          <line x1="312" y1="216" x2="312" y2="262" />
        </g>
      </g>

      <rect x="96" y="388" width="408" height="100" fill={PINK2} stroke={INK} strokeWidth="1.5" />
      <rect x="96" y="486" width="408" height="14" fill={`url(#${ck})`} stroke={INK} strokeWidth="1.2" />
      <path d="M272 486 V446 A28 28 0 0 1 328 446 V486 Z" fill={TEAL} stroke={INK} strokeWidth="1.5" />
      <g stroke="#9CC9C3" strokeWidth="1.4">
        <line x1="300" y1="420" x2="300" y2="486" />
        <line x1="284" y1="440" x2="316" y2="480" />
      </g>
      <rect x="262" y="398" width="76" height="12" fill={`url(#${aw})`} stroke={INK} strokeWidth="1.2" />
      <g fill={PLUM} stroke={INK} strokeWidth=".8">
        {[266.5, 275.5, 284.5, 293.5, 302.5, 311.5, 320.5, 329.5, 333.5].map((cx) => (
          <circle key={cx} cx={cx} cy="411" r="4.5" />
        ))}
      </g>
      <g stroke={INK} strokeWidth="1.2">
        <path d="M244 486 l3 -16 h14 l3 16z" fill={MUST} />
        <circle cx="254" cy="460" r="11" fill="#4F8E88" />
        <path d="M336 486 l3 -16 h14 l3 16z" fill={MUST} />
        <circle cx="346" cy="460" r="11" fill="#4F8E88" />
      </g>
      <polygon points="160,452 190,452 187,428 163,428" fill={PLUM} stroke={INK} strokeWidth="1.2" />
      <polygon points="170,428 180,428 175,436" fill={CREAM} stroke={INK} strokeWidth=".8" />
      <g fill={MUST}>
        <circle cx="175" cy="441" r="1.6" />
        <circle cx="175" cy="447" r="1.6" />
      </g>
      <circle cx="175" cy="419" r="8.5" fill="#F2C7A6" stroke={INK} strokeWidth="1.2" />
      <rect x="168" y="403.5" width="14" height="8" fill={PLUM} stroke={INK} strokeWidth="1" />
      <rect x="168" y="409" width="14" height="2" fill={MUST} />
      <g fill={INK}>
        <circle cx="172" cy="418" r="1" />
        <circle cx="178" cy="418" r="1" />
      </g>
      <path d="M171 423 q4 3 8 0" fill="none" stroke={INK} strokeWidth="1" />
      <rect x="118" y="452" width="116" height="34" fill={MUST} stroke={INK} strokeWidth="1.5" />
      <rect x="124" y="458" width="104" height="22" fill="none" stroke={INK} strokeWidth=".9" />
      <text x="176" y="472" className="hf-lab hf-lab--wide">
        RÉCEPTION
      </text>
      <path d="M206 452 a9 8 0 0 1 18 0 z" fill="#F4CF6B" stroke={INK} strokeWidth="1.2" />
      <circle cx="215" cy="443" r="2" fill={INK} />
      <rect x="386" y="398" width="72" height="15" fill={PLUM} stroke={INK} />
      <text x="422" y="408.5" className="hf-tiny hf-tiny--lg">
        ARRIVÉES
      </text>
      <path d="M394 476 V438 Q422 420 450 438 V476" fill="none" stroke={MUST} strokeWidth="3.5" />
      <path d="M394 476 V438 Q422 420 450 438 V476" fill="none" stroke={INK} strokeWidth=".8" />
      <rect x="388" y="474" width="68" height="5" fill={MUST} stroke={INK} />
      <circle cx="396" cy="483" r="4" fill={INK} />
      <circle cx="448" cy="483" r="4" fill={INK} />
      <rect x="400" y="454" width="22" height="20" fill={KRAFT} stroke={INK} strokeWidth="1.2" />
      <rect x="424" y="452" width="20" height="22" rx="2" fill={TEAL} stroke={INK} strokeWidth="1.2" />
      <Env id={env} x={404} y={440} rotate="-8 414 447" />
      <Env id={env} x={424} y={438} rotate="6 434 445" />
      <rect x="36" y="500" width="528" height="10" fill={PLUM} stroke={INK} strokeWidth="1.5" />
    </svg>
  );
}

function NightFacade({ uid, title, className }) {
  const glow = `hglow-${uid}`;
  const LIT = '#F4CF6B';
  const OFF = '#2A1520';
  const EDGE = '#0E080C';
  const ROOF = '#3A1D28';
  const windows = [
    [104, 176, true], [160, 176, false], [216, 176, true], [348, 176, true], [404, 176, true], [460, 176, false],
    [104, 244, false], [160, 244, true], [216, 244, true], [348, 244, false], [404, 244, true], [460, 244, true],
  ];
  return (
    <svg className={className} viewBox="0 0 600 400" role="img" aria-label={title}>
      <defs>
        <filter id={glow} x="-50%" y="-50%" width="200%" height="200%">
          <feGaussianBlur stdDeviation="4" result="b" />
          <feMerge>
            <feMergeNode in="b" />
            <feMergeNode in="SourceGraphic" />
          </feMerge>
        </filter>
      </defs>
      <circle cx="530" cy="60" r="26" fill="#F4E3B5" />
      <circle cx="520" cy="54" r="5" fill="#E6D19C" />
      <circle cx="538" cy="70" r="3.5" fill="#E6D19C" />
      <polygon points="70,112 300,30 530,112" fill={ROOF} stroke={EDGE} strokeWidth="2" />
      <circle cx="300" cy="80" r="16" fill={LIT} filter={`url(#${glow})`} />
      <g stroke={ROOF} strokeWidth="1.6" strokeLinecap="round">
        <line x1="300" y1="80" x2="300" y2="70" />
        <line x1="300" y1="80" x2="307" y2="84" />
      </g>
      <rect x="62" y="110" width="476" height="12" fill={OFF} stroke={EDGE} strokeWidth="2" />
      <rect x="80" y="122" width="440" height="266" fill="#4A2433" stroke={EDGE} strokeWidth="2" />
      <rect x="128" y="132" width="344" height="26" fill={OFF} stroke={EDGE} />
      <text x="300" y="150" className="hf-sign hf-sign--lit" filter={`url(#${glow})`}>
        MAILSORTER
      </text>
      <g stroke={EDGE} strokeWidth="1.5">
        {windows.map(([x, y, lit]) => (
          <rect key={`${x}-${y}`} x={x} y={y} width="36" height="46" fill={lit ? LIT : OFF} filter={lit ? `url(#${glow})` : undefined} />
        ))}
        <rect x="276" y="170" width="48" height="126" fill={TEAL} />
      </g>
      <g stroke={ROOF} strokeWidth="1.2" opacity=".6">
        {windows.filter(([, , lit]) => lit).map(([x, y]) => (
          <line key={`m-${x}-${y}`} x1={x + 18} y1={y} x2={x + 18} y2={y + 46} />
        ))}
      </g>
      <rect x="284" y="210" width="32" height="40" fill={LIT} stroke={EDGE} filter={`url(#${glow})`} />
      <path d="M272 388 V350 A28 28 0 0 1 328 350 V388 Z" fill={LIT} stroke={EDGE} strokeWidth="1.5" filter={`url(#${glow})`} />
      <rect x="262" y="304" width="76" height="12" fill={PLUM} stroke={EDGE} />
      <g fill="#1F3A38" stroke={EDGE} strokeWidth="1.2">
        <circle cx="246" cy="364" r="12" />
        <circle cx="354" cy="364" r="12" />
      </g>
      <rect x="36" y="388" width="528" height="12" fill={PLUM} stroke={EDGE} strokeWidth="1.5" />
    </svg>
  );
}

export default function HotelFacade({
  night = false,
  animated = true,
  className = '',
  title = "Un hôtel en coupe, dont chaque chambre est une catégorie d'e-mails",
}) {
  // Pattern and filter ids must be unique per drawing: the page can hold the
  // day and the night facade at once.
  const uid = useId().replace(/:/g, '');
  const cls = ['ht-facade', animated && !night ? 'is-animated' : '', className].filter(Boolean).join(' ');
  return night ? <NightFacade uid={uid} title={title} className={cls} /> : <DayFacade uid={uid} title={title} className={cls} />;
}
```

- [ ] **Step 5: Écrire la fiche**

Créer `frontend/src/components/landing/SignupCard.js` :

```jsx
import React from 'react';
import Spinner from '../../ui/Spinner';
import { Google } from '../../ui/icons';
import './SignupCard.css';

// The hall's registration card. State and side effects come from useAuthForm,
// shared with the classic landing; this component only decides how it looks.
// Field ids and the [data-auth-switch] toggle match the classic form's, so
// both landings answer to the same selectors.
export default function SignupCard({ auth, isConfigured }) {
  const register = auth.mode === 'register';
  const locked = auth.busy || auth.googleBusy;
  return (
    <form className="hl-fiche" id="auth-card" onSubmit={auth.submit}>
      <div className="hl-fiche__top">
        <h2>{register ? 'Créer un compte' : 'Se connecter'}</h2>
        {register && <span>gratuit</span>}
      </div>
      <label className="ht-label" htmlFor="auth-email">
        Adresse e-mail
      </label>
      <input
        id="auth-email"
        className="ht-field"
        type="email"
        required
        autoComplete="email"
        value={auth.email}
        onChange={(e) => auth.setEmail(e.target.value)}
        placeholder="vous@exemple.fr"
      />
      <label className="ht-label" htmlFor="auth-password">
        Mot de passe
      </label>
      <input
        id="auth-password"
        className="ht-field"
        type="password"
        required
        minLength={register ? 8 : undefined}
        autoComplete={register ? 'new-password' : 'current-password'}
        value={auth.password}
        onChange={(e) => auth.setPassword(e.target.value)}
        placeholder={register ? '8 caractères minimum' : 'Votre mot de passe'}
      />
      <button type="submit" className="ht-btn ht-btn-primary hl-fiche__go" disabled={locked}>
        {auth.busy ? <Spinner size={16} /> : null}
        {auth.busy ? 'Un instant' : register ? 'Faire le tri' : 'Entrer'}
      </button>
      {isConfigured && (
        <button type="button" className="ht-btn hl-fiche__google" onClick={auth.startGoogle} disabled={locked}>
          {auth.googleBusy ? <Spinner size={16} /> : <Google size={18} />}
          Continuer avec Google
        </button>
      )}
      {auth.error && (
        <p className="hl-fiche__error" role="alert">
          {auth.error}
        </p>
      )}
      <p className="hl-fiche__alt">
        {register ? 'Déjà un compte ?' : 'Pas encore de compte ?'}{' '}
        <button type="button" data-auth-switch={register ? 'login' : 'register'} onClick={() => auth.setMode(register ? 'login' : 'register')}>
          {register ? 'Se connecter' : 'Créer un compte'}
        </button>
      </p>
      <p className="hl-fiche__fine">Gratuit jusqu'à 200 tris par mois. Sans carte bancaire.</p>
    </form>
  );
}
```

Créer `frontend/src/components/landing/SignupCard.css` :

```css
.hl-fiche {
  padding: 18px 20px 20px;
  background: var(--h-surface);
  border: 1.5px solid var(--h-line);
  box-shadow: 7px 7px 0 var(--h-offset);
  transform: rotate(1.2deg);
  scroll-margin-top: 100px;
}
.hl-fiche__top {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  margin-bottom: 14px;
  padding-bottom: 8px;
  border-bottom: 3px double var(--h-line);
}
.hl-fiche__top h2 { font-size: 11px; font-weight: 600; letter-spacing: 0.24em; text-transform: uppercase; }
.hl-fiche__top span { font-family: var(--h-font-display); font-style: italic; font-size: 14px; color: var(--h-accent); }
.hl-fiche__go { width: 100%; height: 48px; margin-top: 4px; }
.hl-fiche__google {
  width: 100%;
  margin-top: 8px;
  background: #fff;
  color: #2B1B1E;
  font-size: 14px;
  font-weight: 500;
  letter-spacing: 0;
  text-transform: none;
}
.hl-fiche__error {
  margin-top: 10px;
  padding: 7px 10px;
  background: var(--h-sunk);
  border-left: 3px solid var(--h-plum);
  font-size: 13.5px;
  line-height: 1.4;
}
.hl-fiche__alt { margin-top: 12px; text-align: center; font-size: 14px; }
.hl-fiche__alt button {
  color: var(--h-accent);
  border-bottom: 1px solid currentColor;
  font-family: var(--h-font-display);
  font-style: italic;
  font-size: 15px;
}
.hl-fiche__fine { margin-top: 10px; text-align: center; font-size: 12px; color: var(--h-muted); }
@media (max-width: 1000px) {
  .hl-fiche { width: 100%; max-width: 440px; margin: 0 auto; transform: none; }
}
```

- [ ] **Step 6: Écrire le hall et la sortie de nuit**

Créer `frontend/src/components/landing/HallHero.js` :

```jsx
import React from 'react';
import HotelFacade from '../../ui/hotel/HotelFacade';
import Plaque from '../../ui/hotel/Plaque';
import SignupCard from './SignupCard';
import { canPromise } from './features';
import { formatCount } from './useUnreadCounter';
import './HallHero.css';

// The hall: the unread figure, the hotel in cross-section, three plain lines on
// what happens, and the form. The lines only promise what this instance can do
// (features.js): without Google there is no undo and no snooze to offer.
export default function HallHero({ count, auth, isConfigured, night }) {
  const snooze = canPromise('snooze', isConfigured);
  const undo = canPromise('undo', isConfigured);
  return (
    <section className="hl-hall" id="hall" data-floor="">
      <div className="hl-wrap">
        <h1 className="hl-h1">
          <span className="hl-h1__big">
            <span className="hl-count">{formatCount(count)}</span> non lus ?
          </span>
          <span className="hl-h1__sub">
            <em>On s'en occupe.</em> Vous validez.
          </span>
        </h1>
        <div className="hl-tri">
          <div className="hl-tri__lines">
            <Plaque n="I">
              <strong>Il lit</strong> l'expéditeur, l'objet et le début du message. Pas plus.
            </Plaque>
            <Plaque n="II">
              <strong>Il propose</strong> : {snooze ? 'archiver, classer, reporter' : 'archiver ou classer'}. Vous dites oui ou non.
            </Plaque>
            {undo ? (
              <Plaque n="III">
                <strong>Tout s'annule</strong> en un clic. Même à 2 h du matin.
              </Plaque>
            ) : (
              <Plaque n="III">
                <strong>Rien ne bouge</strong> sans votre accord. Même à 2 h du matin.
              </Plaque>
            )}
          </div>
          <HotelFacade night={night} className="hl-tri__hotel" />
          <SignupCard auth={auth} isConfigured={isConfigured} />
        </div>
      </div>
      <div className="ht-slab" aria-hidden="true" />
    </section>
  );
}
```

Créer `frontend/src/components/landing/HallHero.css` :

```css
.hl-hall { padding-top: 40px; }
.hl-h1 { text-align: center; font-family: var(--h-font-display); font-variation-settings: 'opsz' 28; }
.hl-h1__big { display: block; font-weight: 400; font-size: clamp(52px, 7.4vw, 104px); line-height: 0.98; letter-spacing: -0.02em; }
.hl-h1__sub { display: block; margin-top: 8px; font-style: italic; font-weight: 400; font-size: clamp(24px, 3vw, 40px); line-height: 1.15; }
.hl-h1__sub em { color: var(--h-accent); }
.hl-tri { display: grid; grid-template-columns: 1fr minmax(0, 600px) 1fr; gap: 34px; align-items: center; margin-top: 18px; }
.hl-tri__lines .ht-plq + .ht-plq { margin-top: 16px; }
.hl-tri__hotel { display: block; width: 100%; height: auto; align-self: end; }
@media (max-width: 1000px) {
  .hl-tri { grid-template-columns: 1fr; gap: 28px; }
  .hl-tri__hotel { order: 1; max-width: 560px; margin: 0 auto; }
  .hl-fiche { order: 2; }
  .hl-tri__lines { order: 3; max-width: 440px; margin: 0 auto 32px; }
}
```

Créer `frontend/src/components/landing/NightExit.js` :

```jsx
import React from 'react';
import HotelFacade from '../../ui/hotel/HotelFacade';
import { formatCount } from './useUnreadCounter';
import './NightExit.css';

// The last call, at night whatever the theme: the same figure as the hall, and
// both ways back up to the form.
export default function NightExit({ count, onStart, onSignIn }) {
  return (
    <section className="hl-exit" data-floor="" aria-labelledby="hl-exit-title">
      <div className="hl-wrap">
        <div className="hl-fh">
          <span className="hl-fh__k">Il est tard</span>
          <h2 id="hl-exit-title" className="hl-fh__title">
            Toujours <span className="hl-count">{formatCount(count)}</span> non lus ?
          </h2>
        </div>
        <div className="hl-exit__cta">
          <button type="button" className="ht-btn ht-btn-gold" onClick={onStart}>
            Faire le tri
          </button>
          <button type="button" className="hl-exit__link" onClick={onSignIn}>
            Se connecter
          </button>
        </div>
        <HotelFacade night className="hl-exit__facade" title="L'hôtel la nuit, fenêtres allumées" />
      </div>
    </section>
  );
}
```

Créer `frontend/src/components/landing/NightExit.css` :

```css
.hl-exit { position: relative; overflow: hidden; padding-top: 90px; background: #1C1220; color: #FBF1E4; }
.hl-exit::before {
  content: '';
  position: absolute;
  inset: 0;
  opacity: 0.8;
  background-image:
    radial-gradient(1.4px 1.4px at 10% 20%, #F4E3B5 50%, transparent 51%),
    radial-gradient(1.2px 1.2px at 30% 12%, #F4E3B5 50%, transparent 51%),
    radial-gradient(1.6px 1.6px at 70% 18%, #F4E3B5 50%, transparent 51%),
    radial-gradient(1.2px 1.2px at 86% 30%, #F4E3B5 50%, transparent 51%),
    radial-gradient(1.3px 1.3px at 52% 8%, #F4E3B5 50%, transparent 51%),
    radial-gradient(1.1px 1.1px at 18% 42%, #F4E3B5 50%, transparent 51%),
    radial-gradient(1.2px 1.2px at 92% 10%, #F4E3B5 50%, transparent 51%);
}
.hl-exit .hl-wrap { position: relative; }
.hl-exit .hl-fh__k { color: #F4CF6B; }
.hl-exit .hl-fh__title { color: #FBF1E4; font-size: clamp(38px, 5vw, 68px); }
.hl-exit .hl-count { color: #F4CF6B; }
.hl-exit__cta { display: flex; flex-wrap: wrap; justify-content: center; align-items: center; gap: 22px; margin-top: 26px; }
.hl-exit__cta .ht-btn-gold { height: 50px; padding: 0 30px; background: #E3A93B; color: #2B1B1E; box-shadow: 0 3px 0 #000; }
.hl-exit__link { color: #FBF1E4; border-bottom: 1.5px solid #F4CF6B; font-family: var(--h-font-display); font-style: italic; font-size: 17px; }
.hl-exit__facade { display: block; width: 100%; max-width: 640px; height: auto; margin: 40px auto 0; }
```

- [ ] **Step 7: Les monter dans `HotelLanding`**

Dans `frontend/src/pages/HotelLanding.js` :
- remplacer `import { useInstance } from '../contexts/InstanceContext';` par :

```jsx
import { useInstance } from '../contexts/InstanceContext';
import { useTheme } from '../ui/theme';
```

- après `import { scrollToId } from '../components/landing/scroll';`, ajouter :

```jsx
import { useUnreadCounter } from '../components/landing/useUnreadCounter';
import HallHero from '../components/landing/HallHero';
import NightExit from '../components/landing/NightExit';
```

- remplacer `const { selfHosted } = useInstance();` par :

```jsx
  const { isConfigured, selfHosted } = useInstance();
  const { isDark } = useTheme();
  const count = useUnreadCounter();
```

- remplacer `<div className="hl-floors" />` par :

```jsx
      <div className="hl-floors">
        <HallHero count={count} auth={auth} isConfigured={isConfigured} night={isDark} />
        <NightExit count={count} onStart={() => openAuth('register')} onSignIn={() => openAuth('login')} />
      </div>
```

- [ ] **Step 8: Vérifier**

Run: `cd frontend && CI=false npm run build 2>&1 | grep -E "Compiled|Failed|ready" ; cd ../.superpowers/landing-check && node check.mjs hall && node check.mjs hall --google=off && node check.mjs hall --reduced && node check.mjs auth --theme=hotel && node check.mjs auth --theme=classic`
Expected: build prêt ; cinq sorties `"failures": []`.

Ouvrir `shots/hall-hall-1440.png` (outil Read) et le comparer au hall de `docs/superpowers/specs/2026-09-24-landing-grand-hotel/landing-mockup.html` : même titre, même hôtel, même fiche inclinée, mêmes plaques. Corriger tout écart dans les fichiers de cette tâche avant de commiter.

- [ ] **Step 9: Commit**

```bash
/usr/bin/git add frontend/src/components/landing frontend/src/ui/hotel/HotelFacade.js frontend/src/pages/HotelLanding.js
/usr/bin/git commit -m "feat(frontend): ajouter le hall, la fiche et la sortie de nuit" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: les portes et le couloir des fonctions

**Files:**
- Create: `frontend/src/ui/hotel/Door.js`
- Create: `frontend/src/components/landing/FeatureCorridor.js`, `FeatureCorridor.css`
- Modify: `frontend/src/pages/HotelLanding.js`
- Create (hors git): `.superpowers/landing-check/scenarios/corridor.mjs`

**Interfaces:**
- Consumes: `canPromise`, `isGmailOnly` (Task 7), `FloorHeading` (Task 6).
- Produces: `Door({ color, number, prop, crop, className, title })` avec `prop` parmi `signpost`, `alarm`, `rope`, `dnd`, `newspaper`, `wallclock`, et `crop` qui recadre sur la seule porte (`viewBox="58 18 124 214"`) ; `FeatureCorridor({ isConfigured })`, une section `data-floor="fonctionnement"` sans id propre (l'ancre `#fonctionnement` est la démo, Task 9).

- [ ] **Step 1: Écrire le scénario du couloir**

Créer `.superpowers/landing-check/scenarios/corridor.mjs` :

```js
// Six doors with Google, only the two that work over IMAP without it, and a
// Gmail badge on exactly the Gmail-only ones.
export default async function corridor({ page, base, opt, expect, shot }) {
  await page.goto(`${base}/`);
  await page.waitForSelector('.hl-corr');
  const rooms = await page.$$eval('.hl-room', (els) =>
    els.map((el) => ({ title: el.querySelector('h3').textContent, gmail: Boolean(el.querySelector('.ht-tag')) }))
  );
  if (opt.google === 'off') {
    expect(rooms.length === 2, `without Google the corridor shows ${rooms.length} rooms, want 2`);
    expect(rooms.every((r) => !r.gmail), 'a Gmail badge shows on an instance without Google');
  } else {
    expect(rooms.length === 6, `the corridor shows ${rooms.length} rooms, want 6`);
    const badges = rooms.filter((r) => r.gmail).length;
    expect(badges === 4, `want 4 Gmail badges, got ${badges}`);
  }
  await page.$eval('.hl-corr', (el) => el.scrollIntoView());
  await shot('corridor');
}
```

Run: `cd .superpowers/landing-check && node check.mjs corridor`
Expected: ÉCHEC, `.hl-corr` introuvable.

- [ ] **Step 2: Écrire la porte**

Créer `frontend/src/ui/hotel/Door.js` :

```jsx
import React from 'react';

// A hotel door drawn at 240 x 230, the object beside it telling what it stands
// for. `crop` frames the door alone (the demo's small doors). Fixed colours:
// the drawing reads the same by day and by night.
const INK = '#2B1B1E';
const PLUM = '#7A2E3B';
const MUST = '#E3A93B';
const CREAM = '#FBF1E4';
const TEAL = '#2F6F73';

function Signpost() {
  return (
    <g>
      <rect x="206" y="98" width="5" height="132" fill={PLUM} stroke={INK} />
      <polygon points="180,104 226,104 236,112 226,120 180,120" fill={MUST} stroke={INK} />
      <text x="206" y="114.6" className="hf-sg" fill={INK}>
        FACTURES
      </text>
      <polygon points="188,128 232,128 232,144 188,144 178,136" fill={CREAM} stroke={INK} />
      <text x="207" y="138.6" className="hf-sg" fill={INK}>
        PROMOS
      </text>
      <polygon points="180,152 226,152 236,160 226,168 180,168" fill={PLUM} stroke={INK} />
      <text x="206" y="162.6" className="hf-sg" fill={CREAM}>
        COLIS
      </text>
    </g>
  );
}

function Alarm() {
  return (
    <g>
      <ellipse cx="34" cy="178" rx="24" ry="5" fill={PLUM} stroke={INK} strokeWidth="1.3" />
      <line x1="34" y1="182" x2="34" y2="224" stroke={INK} strokeWidth="2.4" />
      <ellipse cx="34" cy="226" rx="14" ry="3" fill={PLUM} stroke={INK} />
      <circle cx="23" cy="146" r="5.5" fill={MUST} stroke={INK} />
      <circle cx="45" cy="146" r="5.5" fill={MUST} stroke={INK} />
      <circle cx="34" cy="159" r="15" fill={CREAM} stroke={INK} strokeWidth="2" />
      <g stroke={INK} strokeWidth="1.6" strokeLinecap="round">
        <line x1="34" y1="159" x2="34" y2="149" />
        <line x1="34" y1="159" x2="41" y2="162" />
        <line x1="26" y1="173" x2="23" y2="177" />
        <line x1="42" y1="173" x2="45" y2="177" />
      </g>
      <text x="44" y="134" className="hf-z">
        z
      </text>
      <text x="51" y="123" className="hf-z hf-z--sm">
        z
      </text>
    </g>
  );
}

function Rope() {
  return (
    <g>
      <polygon points="120,82 124,92 135,92 126,98 129,108 120,102 111,108 114,98 105,92 116,92" fill={MUST} stroke={INK} />
      <g stroke={INK} strokeWidth="1.3" fill={MUST}>
        <rect x="36" y="182" width="7" height="48" />
        <rect x="197" y="182" width="7" height="48" />
        <circle cx="39.5" cy="179" r="5.5" />
        <circle cx="200.5" cy="179" r="5.5" />
      </g>
      <path d="M43 190 Q120 224 197 190" fill="none" stroke={PLUM} strokeWidth="6" strokeLinecap="round" />
      <path d="M43 190 Q120 224 197 190" fill="none" stroke={INK} strokeWidth="1" opacity=".5" />
    </g>
  );
}

function DoNotDisturb({ color }) {
  return (
    <g>
      <g transform="rotate(-7 152 126)">
        <rect x="134" y="118" width="38" height="76" rx="4" fill={CREAM} stroke={INK} strokeWidth="1.4" />
        <circle cx="152" cy="130" r="6.5" fill={color} stroke={INK} strokeWidth="1.2" />
        <text x="153" y="153" className="hf-hg">
          NE PAS
        </text>
        <text x="153" y="162" className="hf-hg">
          DÉRANGER
        </text>
        <line x1="142" y1="170" x2="164" y2="170" stroke={MUST} strokeWidth="2" />
      </g>
      <circle cx="152" cy="126" r="3.5" fill={MUST} stroke={INK} />
    </g>
  );
}

function Newspaper() {
  return (
    <g>
      <g transform="rotate(-5 120 219)">
        <rect x="88" y="208" width="64" height="20" fill={CREAM} stroke={INK} strokeWidth="1.3" />
        <text x="120" y="217.5" className="hf-hg hf-hg--ink">
          LE RÉCAP
        </text>
        <line x1="94" y1="222" x2="146" y2="222" stroke={INK} strokeWidth=".7" opacity=".5" />
        <line x1="94" y1="225" x2="136" y2="225" stroke={INK} strokeWidth=".7" opacity=".5" />
      </g>
      <ellipse cx="200" cy="226" rx="17" ry="3.5" fill={CREAM} stroke={INK} strokeWidth="1.2" />
      <path d="M190 206 h20 v10 a10 9 0 0 1 -20 0z" fill={CREAM} stroke={INK} strokeWidth="1.3" />
      <path d="M210 209 a5 5 0 0 1 0 9" fill="none" stroke={INK} strokeWidth="1.3" />
      <path d="M196 200 q-3 -5 0 -9 M203 200 q-3 -5 0 -9" fill="none" stroke={INK} strokeWidth="1" opacity=".55" />
    </g>
  );
}

function WallClock() {
  return (
    <g>
      <circle cx="32" cy="72" r="22" fill={CREAM} stroke={MUST} strokeWidth="4" />
      <circle cx="32" cy="72" r="22" fill="none" stroke={INK} strokeWidth="1.2" />
      <path d="M32 72 L32 52 A20 20 0 0 1 32 92 Z" fill="#EFB7C3" opacity=".8" />
      <g stroke={INK} strokeWidth="1.8" strokeLinecap="round">
        <line x1="32" y1="72" x2="32" y2="56" />
        <line x1="32" y1="72" x2="32" y2="88" />
      </g>
      <circle cx="32" cy="72" r="2.2" fill={INK} />
      <rect x="8" y="102" width="48" height="16" fill={PLUM} stroke={INK} />
      <text x="32" y="113" className="hf-sg" fill={CREAM}>
        30 MIN
      </text>
    </g>
  );
}

const PROPS = { signpost: Signpost, alarm: Alarm, rope: Rope, dnd: DoNotDisturb, newspaper: Newspaper, wallclock: WallClock };

export default function Door({ color = TEAL, number, prop, crop = false, className, title }) {
  const Prop = prop ? PROPS[prop] : null;
  return (
    <svg
      className={className}
      viewBox={crop ? '58 18 124 214' : '0 0 240 230'}
      role={title ? 'img' : undefined}
      aria-label={title}
      aria-hidden={title ? undefined : 'true'}
    >
      <rect x="62" y="22" width="116" height="208" fill={CREAM} stroke={INK} strokeWidth="2" />
      <rect x="68" y="28" width="104" height="202" fill="none" stroke={INK} strokeWidth="1" />
      <rect x="74" y="34" width="92" height="196" fill={color} stroke={INK} strokeWidth="1.6" />
      <rect x="84" y="46" width="72" height="72" fill="none" stroke={INK} strokeOpacity=".45" strokeWidth="1.5" />
      <rect x="84" y="128" width="72" height="90" fill="none" stroke={INK} strokeOpacity=".45" strokeWidth="1.5" />
      <rect x="148" y="118" width="8" height="18" rx="2" fill={MUST} stroke={INK} />
      <circle cx="152" cy="126" r="3.5" fill={MUST} stroke={INK} />
      {number && (
        <>
          <rect x="104" y="56" width="32" height="15" rx="2" fill={MUST} stroke={INK} />
          <text x="120" y="67" className="hf-dn">
            {number}
          </text>
        </>
      )}
      {Prop && <Prop color={color} />}
    </svg>
  );
}
```

- [ ] **Step 3: Écrire le couloir**

Créer `frontend/src/components/landing/FeatureCorridor.js` :

```jsx
import React from 'react';
import Door from '../../ui/hotel/Door';
import FloorHeading from './FloorHeading';
import { canPromise, isGmailOnly } from './features';
import './FeatureCorridor.css';

// What runs while the visitor does something else, one door per feature.
// A door is shown only when this instance can keep its promise, and carries a
// "Gmail" badge when an IMAP mailbox would not get it (features.js).
const ROOMS = [
  {
    feature: 'rules',
    color: '#2F6F73',
    number: '301',
    prop: 'signpost',
    title: "Les évidences n'ont pas besoin d'IA.",
    text: 'Vos règles passent avant le modèle. Plus rapide, et ça ne touche pas à votre quota.',
  },
  {
    feature: 'snooze',
    color: '#7A2E3B',
    number: '302',
    prop: 'alarm',
    title: 'Pas maintenant.',
    text: "Un e-mail disparaît et revient ce soir, demain ou ce week-end. Comme si vous l'aviez reçu à ce moment-là.",
  },
  {
    feature: 'protected',
    color: '#E48FA5',
    number: 'VIP',
    prop: 'rope',
    title: 'Votre mère ne sera jamais archivée.',
    text: 'Les expéditeurs protégés échappent à tout tri automatique. Même quand ils écrivent en majuscules.',
  },
  {
    feature: 'unsubscribe',
    color: '#7A2E3B',
    number: '304',
    prop: 'dnd',
    title: 'Désabonnez-vous en un clic.',
    text: 'Sans chercher le lien minuscule en gris clair tout en bas du mail.',
  },
  {
    feature: 'digest',
    color: '#2F6F73',
    number: '305',
    prop: 'newspaper',
    title: 'Un récap par jour. Pas un de plus.',
    text: 'Chaque matin, si vous le voulez, un e-mail résume ce qui a été trié ces sept derniers jours.',
  },
  {
    feature: 'autosync',
    color: '#7A2E3B',
    number: '306',
    prop: 'wallclock',
    title: 'Il repasse toutes les 30 minutes.',
    text: "Activez-le une fois : les nouveaux e-mails sont relevés sans que vous ayez à ouvrir l'app. Ni à y penser.",
  },
];

export default function FeatureCorridor({ isConfigured }) {
  const rooms = ROOMS.filter((r) => canPromise(r.feature, isConfigured));
  return (
    <section className="hl-section hl-section--alt hl-corridor" data-floor="fonctionnement" aria-labelledby="hl-corridor-title">
      <div className="hl-wrap">
        <FloorHeading id="hl-corridor-title">
          Et pendant que vous faites <em>autre chose.</em>
        </FloorHeading>
        <div className="hl-corr">
          {rooms.map((r) => (
            <article key={r.feature} className="hl-room">
              <div className="hl-room__wall">
                <Door color={r.color} number={r.number} prop={r.prop} />
              </div>
              <div className="hl-room__floor" aria-hidden="true" />
              <div className="hl-room__txt">
                <h3>{r.title}</h3>
                <p>{r.text}</p>
                {isGmailOnly(r.feature) && <span className="ht-tag is-gold">Gmail</span>}
              </div>
            </article>
          ))}
        </div>
      </div>
    </section>
  );
}
```

Créer `frontend/src/components/landing/FeatureCorridor.css` :

```css
.hl-corridor { padding-top: 0; }
.hl-corr {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  margin-top: 44px;
  background: var(--h-surface);
  border: 2px solid var(--h-line);
}
.hl-room { display: flex; flex-direction: column; border-right: 1px solid var(--h-hair); border-bottom: 2px solid var(--h-line); }
.hl-room:nth-child(3n) { border-right: 0; }
.hl-room__wall {
  position: relative;
  padding: 22px 10px 0;
  background-color: #F7D6DC;
  background-image: radial-gradient(circle, #E9A3B3 1.1px, transparent 1.3px);
  background-size: 12px 12px;
}
.hl-room__wall::after {
  content: '';
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 64px;
  background: #EFB7C3;
  border-top: 2px solid #2B1B1E;
}
.hl-room__wall svg { position: relative; z-index: 1; display: block; width: 100%; height: auto; }
.hl-room__floor {
  height: 14px;
  background: repeating-linear-gradient(90deg, #7A2E3B 0 16px, #FBF1E4 16px 32px);
  border-top: 2px solid var(--h-line);
  border-bottom: 2px solid var(--h-line);
}
.hl-room__txt { flex: 1; padding: 18px 26px 26px; }
.hl-room__txt h3 { font-family: var(--h-font-display); font-variation-settings: 'opsz' 28; font-weight: 400; font-size: 21px; line-height: 1.15; }
.hl-room__txt p { margin-top: 6px; font-size: 14.5px; line-height: 1.5; color: var(--h-muted); }
.hl-room__txt .ht-tag { height: 22px; margin-top: 10px; font-size: 9.5px; letter-spacing: 0.16em; text-transform: uppercase; }
@media (max-width: 1000px) {
  .hl-corr { grid-template-columns: repeat(2, 1fr); }
  .hl-room:nth-child(3n) { border-right: 1px solid var(--h-hair); }
  .hl-room:nth-child(2n) { border-right: 0; }
}
@media (max-width: 640px) {
  .hl-room__txt { padding: 14px 14px 18px; }
  .hl-room__txt h3 { font-size: 17px; }
  .hl-room__txt p { font-size: 13.5px; }
}
```

- [ ] **Step 4: Le monter dans `HotelLanding`**

Dans `frontend/src/pages/HotelLanding.js`, ajouter l'import `import FeatureCorridor from '../components/landing/FeatureCorridor';` après celui de `HallHero`, et insérer `<FeatureCorridor isConfigured={isConfigured} />` juste après la ligne `<HallHero ... />`.

- [ ] **Step 5: Vérifier**

Run: `cd frontend && CI=false npm run build 2>&1 | grep -E "Compiled|Failed|ready" ; cd ../.superpowers/landing-check && node check.mjs corridor && node check.mjs corridor --google=off && node check.mjs corridor --width=375`
Expected: build prêt ; trois sorties `"failures": []`.

Ouvrir `shots/corridor-corridor-1440.png` et `shots/corridor-corridor-375.png` et les comparer au couloir de la maquette : mêmes portes, mêmes accessoires, textes lisibles sur deux colonnes à 375 px.

- [ ] **Step 6: Commit**

```bash
/usr/bin/git add frontend/src/ui/hotel/Door.js frontend/src/components/landing/FeatureCorridor.js frontend/src/components/landing/FeatureCorridor.css frontend/src/pages/HotelLanding.js
/usr/bin/git commit -m "feat(frontend): ajouter le couloir des fonctions" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: la démo de tri, un mail à la fois

**Files:**
- Create: `frontend/src/components/landing/TriageDemo.js`, `TriageDemo.css`
- Modify: `frontend/src/pages/HotelLanding.js`
- Create (hors git): `.superpowers/landing-check/scenarios/demo.mjs`

**Interfaces:**
- Consumes: `Door` (`crop`), `canPromise`, `FloorHeading`, `formatCount`, `prefersReducedMotion`, `track(event, props)` de `lib/analytics.js`.
- Produces: `TriageDemo({ count, isConfigured, floorNo, onStart })`, section `id="fonctionnement"` `data-floor="fonctionnement"` ; cartes `.hl-mail[data-depth="0|1|2|far|gone"]`, portes `.hl-md[data-to="archives|factures|desabo|later"]` avec leur `.ht-badge`, notification `.hl-toast` ; événement analytics `landing_demo` avec `{ action: 'validate' | 'skip' | 'undo' }`.

- [ ] **Step 1: Écrire le scénario de la démo**

Créer `.superpowers/landing-check/scenarios/demo.mjs` :

```js
// The pile: the next mail is already there, a validated card reaches its door,
// Annuler brings it back (only where undo can be promised), and the pile can
// be emptied and restarted.
export default async function demo({ page, base, opt, expect, shot }) {
  await page.goto(`${base}/`);
  await page.waitForSelector('.hl-deck');
  await page.$eval('#fonctionnement', (el) => el.scrollIntoView());
  const front = '.hl-mail[data-depth="0"]';
  const subject = () => page.$eval(`${front} .hl-mail__subj`, (el) => el.textContent);
  const count = (to) => page.$eval(`.hl-md[data-to="${to}"] .ht-badge`, (el) => Number(el.textContent));
  const withUndo = opt.google !== 'off';

  const lede = await page.$eval('#fonctionnement .hl-fh__lede', (el) => el.textContent);
  expect(lede.includes('Annuler est juste là') === withUndo, `lede "${lede}" with google=${withUndo}`);
  expect((await subject()) === 'La lettre du matin', `the pile opens on "${await subject()}"`);
  const behind = await page.$$eval('.hl-mail[data-depth="1"], .hl-mail[data-depth="2"]', (els) => els.length);
  expect(behind === 2, `${behind} mails wait behind the first, want 2`);

  await page.click(`${front} button:has-text("Valider")`);
  await page.waitForTimeout(900);
  expect((await count('archives')) === 1, 'validating Le Monde did not reach the Archives door');
  const toast = await page.$eval('.hl-toast', (el) => el.textContent);
  expect(toast.includes('Archivé.'), `toast reads "${toast}"`);
  expect(toast.includes('Annuler') === withUndo, `the toast offers Annuler: ${toast.includes('Annuler')}, want ${withUndo}`);
  await shot('validated');

  if (withUndo) {
    await page.click('.hl-toast button');
    await page.waitForTimeout(900);
    expect((await count('archives')) === 0, 'undo did not take the mail back out of Archives');
    expect((await subject()) === 'La lettre du matin', 'undo did not put Le Monde back on top');
  }

  for (let i = 0; i < 8; i += 1) {
    const button = await page.$(`${front} button:has-text("Valider"), ${front} button:has-text("Suivant")`);
    if (!button) break;
    await button.click();
    await page.waitForTimeout(700);
  }
  const done = await page.$eval(front, (el) => el.textContent);
  expect(done.includes('Pile vide'), `after the pile the front card reads "${done.slice(0, 60)}"`);
  await shot('empty');

  await page.click(`${front} button:has-text("Recommencer")`);
  await page.waitForTimeout(700);
  expect((await subject()) === 'La lettre du matin', 'restart did not bring the pile back');
  expect((await count('archives')) === 0, 'restart did not reset the doors');
}
```

Run: `cd .superpowers/landing-check && node check.mjs demo`
Expected: ÉCHEC, `.hl-deck` introuvable.

- [ ] **Step 2: Écrire la démo**

Créer `frontend/src/components/landing/TriageDemo.js` :

```jsx
import React, { useEffect, useRef, useState } from 'react';
import Door from '../../ui/hotel/Door';
import FloorHeading from './FloorHeading';
import { canPromise } from './features';
import { formatCount } from './useUnreadCounter';
import { prefersReducedMotion } from './scroll';
import { track } from '../../lib/analytics';
import './TriageDemo.css';

// One mail at a time, like the step-by-step triage of the inbox: a real pile
// where the next mail is already visible, a validated card that flies into its
// door, and an Annuler that brings it back out. Made-up mails, no network. A
// mail whose action this instance cannot perform is left out (features.js).
const MAILS = [
  { from: 'Le Monde', av: 'LM', time: '07:02', subj: 'La lettre du matin', snip: 'Les cinq informations à retenir ce jeudi, et un éditorial que vous ne lirez pas non plus.', act: 'Archiver', to: 'archives', done: 'Archivé.' },
  { from: 'Free Mobile', av: 'FM', time: '08:15', subj: 'Votre facture de septembre est disponible', snip: 'Montant : 19,99 €. Prélèvement le 5 octobre.', act: 'Ranger dans Factures', to: 'factures', done: 'Rangé dans Factures.' },
  { from: 'Zalando', av: 'Z', time: '09:30', subj: '-40 % ce week-end seulement', snip: "Comme le week-end dernier. Et celui d'avant.", act: 'Se désabonner', to: 'desabo', done: 'Désabonné.', feature: 'unsubscribe' },
  { from: 'SNCF Connect', av: 'SN', time: '10:11', subj: 'Votre billet Paris - Lyon du 2 octobre', snip: 'Voiture 14, place 62. Départ 08:04, gare de Lyon.', act: 'Reporter à jeudi, 8 h', to: 'later', done: 'Reporté à jeudi, 8 h.', feature: 'snooze' },
  { from: 'Maman', av: 'M', time: '11:48', subj: 'Pour dimanche, on dit midi ?', snip: 'Et tu ramènes le dessert. Pas comme la dernière fois.', safe: true },
];

const DOORS = [
  { to: 'archives', label: 'Archives', color: '#2F6F73' },
  { to: 'factures', label: 'Factures', color: '#7A2E3B' },
  { to: 'desabo', label: 'Désabonnements', color: '#E48FA5', feature: 'unsubscribe' },
  { to: 'later', label: 'Plus tard', color: '#C98B22', feature: 'snooze' },
];

const EMPTY = { archives: 0, factures: 0, desabo: 0, later: 0 };

const Star = () => (
  <svg width="30" height="30" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M12 3l2.2 5.6L20 9.5l-4.4 3.8 1.4 5.9L12 16.2 7 19.2l1.4-5.9L4 9.5l5.8-.9z" />
  </svg>
);

const Shield = () => (
  <svg width="30" height="30" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
    <path d="M12 3l8 3v6c0 4.5-3.4 8-8 9-4.6-1-8-4.5-8-9V6z" />
    <path d="M8.5 12l2.5 2.5 4.5-5" />
  </svg>
);

export default function TriageDemo({ count, isConfigured, floorNo, onStart }) {
  const canUndo = canPromise('undo', isConfigured);
  const mails = MAILS.filter((m) => !m.feature || canPromise(m.feature, isConfigured));
  const doors = DOORS.filter((d) => !d.feature || canPromise(d.feature, isConfigured));

  const [out, setOut] = useState({}); // card index -> transform it left with
  const [log, setLog] = useState([]); // [{ i, to }], most recent last
  const [counts, setCounts] = useState(EMPTY);
  const [bumped, setBumped] = useState(null);
  const [toast, setToast] = useState(null); // { msg, undoable }
  const cardRefs = useRef([]);
  const doorRefs = useRef({});
  const timers = useRef([]);

  useEffect(() => () => timers.current.forEach(clearTimeout), []);
  useEffect(() => {
    if (!toast) return undefined;
    const timer = setTimeout(() => setToast(null), 5000);
    return () => clearTimeout(timer);
  }, [toast]);

  const later = (fn, ms) => {
    timers.current.push(setTimeout(fn, ms));
  };

  // The pile is every card not yet sent away, the end card last.
  const gone = new Set(log.map((l) => l.i));
  const pile = [...mails.keys(), mails.length].filter((i) => !gone.has(i));
  const depth = (i) => {
    const d = pile.indexOf(i);
    if (d < 0) return 'gone';
    return d > 2 ? 'far' : String(d);
  };

  const bump = (to, delta) => {
    setCounts((c) => ({ ...c, [to]: Math.max(0, c[to] + delta) }));
    setBumped(to);
    later(() => setBumped(null), 450);
  };

  const leave = (i, transform, to) => {
    setOut((o) => ({ ...o, [i]: transform }));
    setLog((l) => [...l, { i, to }]);
  };

  const validate = (i) => {
    const m = mails[i];
    const card = cardRefs.current[i];
    const door = doorRefs.current[m.to];
    let transform = 'translateY(120px) scale(.1)';
    if (card && door) {
      const a = card.getBoundingClientRect();
      const b = door.getBoundingClientRect();
      const dx = b.left + b.width / 2 - (a.left + a.width / 2);
      const dy = b.top + b.height / 3 - a.top;
      transform = `translate(${dx}px, ${dy}px) scale(.08) rotate(8deg)`;
    }
    leave(i, transform, m.to);
    later(() => {
      bump(m.to, 1);
      setToast({ msg: m.done, undoable: canUndo });
    }, prefersReducedMotion() ? 0 : 480);
    track('landing_demo', { action: 'validate' });
  };

  const keep = (i) => {
    leave(i, 'translateX(-160px) rotate(-8deg)', null);
    later(() => setToast({ msg: 'Gardé dans la boîte de réception.', undoable: canUndo }), 250);
    track('landing_demo', { action: 'skip' });
  };

  // Maman's card: nothing happened to her, so there is nothing to undo either.
  const next = (i) => {
    leave(i, 'translateY(40px) scale(.96)', null);
    setToast(null);
  };

  const undo = () => {
    const last = log[log.length - 1];
    if (!last) return;
    setLog((l) => l.slice(0, -1));
    setOut((o) => {
      const rest = { ...o };
      delete rest[last.i];
      return rest;
    });
    if (last.to) bump(last.to, -1);
    setToast({ msg: "Annulé. Comme si de rien n'était.", undoable: false });
    track('landing_demo', { action: 'undo' });
  };

  const restart = () => {
    setLog([]);
    setOut({});
    setCounts(EMPTY);
    setToast(null);
  };

  const cardProps = (i) => {
    const d = depth(i);
    return {
      ref: (el) => {
        cardRefs.current[i] = el;
      },
      className: `hl-mail${d === 'gone' ? ' is-gone' : ''}`,
      'data-depth': d,
      style: out[i] ? { transform: out[i], opacity: 0 } : undefined,
      inert: d !== '0' ? '' : undefined,
      'aria-hidden': d !== '0' ? 'true' : undefined,
    };
  };

  return (
    <section className="hl-section hl-section--alt" id="fonctionnement" data-floor="fonctionnement" aria-labelledby="hl-demo-title">
      <div className="hl-wrap">
        <FloorHeading
          n={floorNo}
          kicker="Fonctionnement"
          id="hl-demo-title"
          lede={`Pour chaque e-mail, Mailsorter vous dit ce qu'il en ferait. Vous validez ou vous passez.${canUndo ? " Et si vous changez d'avis, Annuler est juste là." : ''}`}
        >
          Un mail, une question, <em>un clic.</em>
        </FloorHeading>
        <div className="hl-stage">
          <p className="hl-try">Démo interactive</p>
          <div className="hl-deck">
            {mails.map((m, i) => (
              <article key={m.from} {...cardProps(i)}>
                <header className="hl-mail__head">
                  <span className="hl-mail__av">{m.av}</span>
                  <div className="hl-mail__who">
                    <b>{m.from}</b>
                    <span>Aujourd'hui, {m.time}</span>
                  </div>
                  <span className="hl-mail__left">{mails.length - i} dans la pile</span>
                </header>
                <h3 className="hl-mail__subj">{m.subj}</h3>
                <p className="hl-mail__snip">{m.snip}</p>
                {m.safe ? (
                  <>
                    <div className="ht-prop is-safe">
                      <Shield />
                      <div>
                        <small>Expéditrice protégée</small>
                        <b>Mailsorter n'y touche pas.</b>
                      </div>
                    </div>
                    <div className="hl-mail__acts">
                      <button type="button" className="ht-btn ht-btn-primary" onClick={() => next(i)}>
                        Suivant
                      </button>
                    </div>
                  </>
                ) : (
                  <>
                    <div className="ht-prop">
                      <Star />
                      <div>
                        <small>Mailsorter propose</small>
                        <b>{m.act}</b>
                      </div>
                    </div>
                    <div className="hl-mail__acts">
                      <button type="button" className="ht-btn ht-btn-primary" onClick={() => validate(i)}>
                        Valider
                      </button>
                      <button type="button" className="ht-btn ht-btn-secondary" onClick={() => keep(i)}>
                        Garder tel quel
                      </button>
                    </div>
                  </>
                )}
              </article>
            ))}
            <article {...cardProps(mails.length)} className={`${cardProps(mails.length).className} hl-mail--done`}>
              <h3 className="hl-mail__subj">
                Pile vide. Plus que <span className="hl-count">{formatCount(count)}</span> chez vous.
              </h3>
              <p className="hl-mail__snip">Ceux-là, on ne peut pas les trier sans vous.</p>
              <div className="hl-mail__acts">
                <button type="button" className="ht-btn ht-btn-primary" onClick={onStart}>
                  Faire le tri
                </button>
              </div>
              <button type="button" className="hl-mail__again" onClick={restart}>
                Recommencer la démo
              </button>
            </article>
            <div className={`ht-toast hl-toast${toast ? ' is-on' : ''}`} role="status" aria-live="polite">
              {toast && (
                <>
                  {toast.msg}
                  {toast.undoable && (
                    <button type="button" onClick={undo}>
                      Annuler
                    </button>
                  )}
                </>
              )}
            </div>
          </div>
          <div className="hl-mdoors">
            {doors.map((d) => (
              <div
                key={d.to}
                className={`hl-md${bumped === d.to ? ' is-bumped' : ''}`}
                data-to={d.to}
                ref={(el) => {
                  doorRefs.current[d.to] = el;
                }}
              >
                <span className="ht-badge hl-md__badge">{counts[d.to]}</span>
                <Door color={d.color} crop className="hl-md__door" />
                <b>{d.label}</b>
              </div>
            ))}
          </div>
        </div>
      </div>
    </section>
  );
}
```

Créer `frontend/src/components/landing/TriageDemo.css` :

```css
.hl-stage { max-width: 760px; margin: 40px auto 0; }
.hl-try { margin-bottom: 18px; text-align: center; font-family: var(--h-font-display); font-style: italic; font-size: 17px; color: var(--h-accent); }
.hl-try::before, .hl-try::after {
  content: '';
  display: inline-block;
  width: 7px;
  height: 7px;
  margin: 0 12px 3px;
  background: #E3A93B;
  border: 1.2px solid var(--h-line);
  transform: rotate(45deg);
}
.hl-deck { position: relative; display: grid; max-width: 600px; margin: 0 auto; padding-top: 34px; }
.hl-mail {
  grid-area: 1 / 1;
  align-self: start;
  position: relative;
  padding: 24px 28px 26px;
  background: var(--h-surface);
  border: 1.5px solid var(--h-line);
  transform-origin: 50% 0;
  transition: transform 0.5s var(--h-ease), opacity 0.5s, background-color 0.5s, box-shadow 0.5s, visibility 0s;
}
.hl-mail[data-depth='0'] { z-index: 5; box-shadow: 8px 8px 0 var(--h-offset); }
.hl-mail[data-depth='1'] { z-index: 4; transform: translateY(-17px) scale(0.95); background-color: var(--h-pile-1); }
.hl-mail[data-depth='2'] { z-index: 3; transform: translateY(-32px) scale(0.9); background-color: var(--h-pile-2); }
.hl-mail[data-depth='far'] { z-index: 1; transform: translateY(-32px) scale(0.9); opacity: 0; }
.hl-mail[data-depth='1'] > *, .hl-mail[data-depth='2'] > * { opacity: 0.55; transition: opacity 0.5s; }
.hl-mail.is-gone {
  z-index: 6;
  visibility: hidden;
  opacity: 0;
  transition: transform 0.5s cubic-bezier(0.5, 0, 0.3, 1), opacity 0.5s, visibility 0s linear 0.5s;
}
.hl-mail__head { display: flex; align-items: center; gap: 12px; }
.hl-mail__av {
  display: grid;
  place-items: center;
  flex-shrink: 0;
  width: 42px;
  height: 42px;
  border-radius: 50%;
  background: #EFB7C3;
  border: 1.5px solid var(--h-line);
  color: #2B1B1E;
  font-weight: 600;
  font-size: 14px;
}
.hl-mail__who { flex: 1; min-width: 0; }
.hl-mail__who b { display: block; font-size: 17px; font-weight: 600; }
.hl-mail__who span { font-size: 13px; color: var(--h-muted); }
.hl-mail__left { font-size: 11px; font-weight: 600; letter-spacing: 0.14em; text-transform: uppercase; color: var(--h-muted); }
.hl-mail__subj { margin-top: 18px; font-family: var(--h-font-display); font-variation-settings: 'opsz' 28; font-weight: 400; font-size: 29px; line-height: 1.15; }
.hl-mail__snip { margin-top: 8px; font-size: 16px; line-height: 1.55; color: var(--h-muted); }
.hl-mail .ht-prop { margin-top: 22px; }
.hl-mail__acts { display: flex; gap: 10px; margin-top: 16px; }
.hl-mail__acts .ht-btn-primary { flex: 1; height: 50px; }
.hl-mail__acts .ht-btn-secondary { flex: 0 0 auto; height: 50px; }
.hl-mail--done { padding: 34px 28px; text-align: center; }
.hl-mail--done .hl-mail__subj { margin-top: 0; font-size: 27px; }
.hl-mail--done .hl-mail__acts { justify-content: center; }
.hl-mail--done .hl-mail__acts .ht-btn-primary { flex: 0 0 auto; padding: 0 30px; }
.hl-mail__again { margin-top: 14px; color: var(--h-accent); border-bottom: 1px solid currentColor; font-family: var(--h-font-display); font-style: italic; font-size: 15px; }
.hl-toast {
  position: absolute;
  left: 50%;
  bottom: -26px;
  z-index: 7;
  white-space: nowrap;
  opacity: 0;
  pointer-events: none;
  transform: translate(-50%, 10px);
  transition: opacity 0.25s, transform 0.25s;
}
.hl-toast.is-on { opacity: 1; pointer-events: auto; transform: translate(-50%, 0); }
.hl-mdoors {
  display: flex;
  justify-content: center;
  gap: 44px;
  margin-top: 64px;
  padding-bottom: 6px;
  border-bottom: 10px solid #7A2E3B;
  box-shadow: 0 2px 0 var(--h-line);
}
.hl-md { position: relative; display: flex; flex-direction: column; align-items: center; gap: 8px; }
.hl-md__door { width: 62px; height: auto; }
.hl-md b { font-size: 11px; font-weight: 600; letter-spacing: 0.14em; text-transform: uppercase; }
.hl-md__badge { position: absolute; top: -10px; left: calc(50% + 18px); z-index: 1; }
.hl-md.is-bumped .hl-md__badge { transform: scale(1.35); }
.hl-md.is-bumped .hl-md__door { filter: drop-shadow(0 0 10px rgba(244, 207, 107, 0.95)); }
@media (max-width: 640px) {
  .hl-mail { padding: 20px 18px 22px; }
  .hl-mail__subj { font-size: 24px; }
  .hl-mail__acts { flex-direction: column; }
  .hl-mail__acts .ht-btn-secondary { flex: 1; }
  .hl-toast { white-space: normal; width: max-content; max-width: 92%; }
  .hl-mdoors { gap: 16px; }
  .hl-md__door { width: 44px; }
  .hl-md b { font-size: 9px; letter-spacing: 0.08em; }
}
```

- [ ] **Step 3: La monter dans `HotelLanding`**

Dans `frontend/src/pages/HotelLanding.js` :
- ajouter `import TriageDemo from '../components/landing/TriageDemo';` après l'import de `FeatureCorridor` ;
- après `const floors = useMemo(() => floorsFor(selfHosted), [selfHosted]);`, ajouter :

```jsx
  const floorNo = useMemo(() => Object.fromEntries(floors.map((f) => [f.id, f.n])), [floors]);
```

- insérer, entre `<HallHero ... />` et `<FeatureCorridor ... />` :

```jsx
        <TriageDemo count={count} isConfigured={isConfigured} floorNo={floorNo.fonctionnement} onStart={() => openAuth('register')} />
```

- [ ] **Step 4: Vérifier**

Run: `cd frontend && CI=false npm run build 2>&1 | grep -E "Compiled|Failed|ready" ; cd ../.superpowers/landing-check && node check.mjs demo && node check.mjs demo --google=off && node check.mjs demo --width=375 && node check.mjs demo --reduced`
Expected: build prêt ; quatre sorties `"failures": []`.

Ouvrir `shots/demo-validated-1440.png` et `shots/demo-validated-375.png` : la carte suivante est au premier plan, la notification est lisible, les portes sont sous la pile.

- [ ] **Step 5: Commit**

```bash
/usr/bin/git add frontend/src/components/landing/TriageDemo.js frontend/src/components/landing/TriageDemo.css frontend/src/pages/HotelLanding.js
/usr/bin/git commit -m "feat(frontend): ajouter la demo de tri pas a pas" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---
### Task 10: le coffre-fort et le tableau à clés

**Files:**
- Create: `frontend/src/ui/hotel/Vault.js`, `frontend/src/ui/hotel/KeyTag.js`
- Create: `frontend/src/components/landing/PrivacyVault.js`, `PrivacyVault.css`, `ProviderBoard.js`, `ProviderBoard.css`
- Modify: `frontend/src/pages/HotelLanding.js`
- Create (hors git): `.superpowers/landing-check/scenarios/providers.mjs`

**Interfaces:**
- Consumes: `configService.getProviders()` (réponse `{ edition, providers: [{ key, name, routes: [{ transport, ... }] }] }`), `canPromise`, `imapLegend` (Task 7), `FloorHeading`.
- Produces: `Vault({ className })`, `KeyTag({ name, note, gold, tilt })` ; `PrivacyVault({ floorNo, isConfigured })` (section `id="confidentialite"`), `ProviderBoard({ isConfigured })` (section `data-floor="confidentialite"`, clés `.ht-key__tag`, dorées avec `.is-gold`, légende `.hl-legend`).

- [ ] **Step 1: Écrire le scénario**

Créer `.superpowers/landing-check/scenarios/providers.mjs` :

```js
// The key board renders the catalog the server returns, gold only for a Gmail
// API route on an instance with Google; the vault links to the privacy policy.
export default async function providers({ page, base, opt, expect, shot }) {
  await page.goto(`${base}/`);
  await page.waitForSelector('.hl-board .ht-key');
  const tags = await page.$$eval('.hl-board .ht-key__tag', (els) => els.map((el) => ({ text: el.textContent, gold: el.classList.contains('is-gold') })));
  expect(tags.length === 3, `the board shows ${tags.length} keys, want the 3 mocked providers`);
  const gold = tags.filter((t) => t.gold).map((t) => t.text);
  if (opt.google === 'off') expect(gold.length === 0, `gold keys without Google: ${gold.join(', ')}`);
  else expect(gold.length === 1 && gold[0].includes('Gmail'), `gold keys: ${gold.join(', ')}, want only Gmail`);
  const legend = await page.$eval('.hl-legend', (el) => el.textContent);
  expect(legend.includes('Lecture et tri par IA'), `legend reads "${legend}"`);
  const link = await page.$eval('.hl-vault a', (a) => a.getAttribute('href'));
  expect(link === '/confidentialite', `the privacy link points to ${link}`);
  expect(Boolean(await page.$('.hl-vault svg[role="img"]')), 'the vault drawing is missing');
  await page.$eval('#confidentialite', (el) => el.scrollIntoView());
  await shot('privacy');
}
```

Run: `cd .superpowers/landing-check && node check.mjs providers`
Expected: ÉCHEC, `.hl-board .ht-key` introuvable.

- [ ] **Step 2: Le coffre et la clé**

Créer `frontend/src/ui/hotel/Vault.js` :

```jsx
import React from 'react';

// The safe: what is kept is kept locked. The plate says how (AES-256-GCM at
// rest, see backend/internal/crypto).
const INK = '#2B1B1E';
const MUST = '#E3A93B';
const BOLTS = [[180, 63], [263, 97], [297, 180], [263, 263], [180, 297], [97, 263], [63, 180], [97, 97]];
const TICKS = [[180, 150, 180, 156], [210, 180, 204, 180], [180, 210, 180, 204], [150, 180, 156, 180], [201, 159, 197, 163], [201, 201, 197, 197], [159, 201, 163, 197], [159, 159, 163, 163]];

export default function Vault({ className }) {
  return (
    <svg className={className} viewBox="0 0 360 360" role="img" aria-label="Un coffre-fort, fermé">
      <rect x="14" y="14" width="332" height="332" rx="22" fill="#2F6F73" stroke={INK} strokeWidth="3" />
      <rect x="28" y="28" width="304" height="304" rx="14" fill="none" stroke="#1F4F4F" strokeWidth="2" />
      <g fill={MUST} stroke={INK}>
        {[[42, 42], [318, 42], [42, 318], [318, 318], [180, 38], [180, 322]].map(([cx, cy]) => (
          <circle key={`r-${cx}-${cy}`} cx={cx} cy={cy} r="4" />
        ))}
      </g>
      <g fill={MUST} stroke={INK} strokeWidth="1.5">
        <rect x="20" y="96" width="18" height="44" rx="3" />
        <rect x="20" y="220" width="18" height="44" rx="3" />
      </g>
      <circle cx="180" cy="180" r="124" fill="#3E8580" stroke={INK} strokeWidth="3" />
      <circle cx="180" cy="180" r="110" fill="none" stroke={MUST} strokeWidth="4" />
      <circle cx="180" cy="180" r="98" fill="#2F6F73" stroke={INK} strokeWidth="1.5" />
      <g fill={MUST} stroke={INK} strokeWidth="1.2">
        {BOLTS.map(([cx, cy]) => (
          <circle key={`b-${cx}-${cy}`} cx={cx} cy={cy} r="6" />
        ))}
      </g>
      <circle cx="180" cy="180" r="62" fill="none" stroke={MUST} strokeWidth="6" />
      <circle cx="180" cy="180" r="62" fill="none" stroke={INK} strokeWidth="1" />
      <g stroke={MUST} strokeWidth="6" strokeLinecap="round">
        <line x1="180" y1="180" x2="180" y2="112" />
        <line x1="180" y1="180" x2="239" y2="214" />
        <line x1="180" y1="180" x2="121" y2="214" />
      </g>
      <g fill={MUST} stroke={INK} strokeWidth="1.3">
        <circle cx="180" cy="108" r="9" />
        <circle cx="243" cy="216" r="9" />
        <circle cx="117" cy="216" r="9" />
      </g>
      <circle cx="180" cy="180" r="32" fill={MUST} stroke={INK} strokeWidth="2" />
      <g stroke={INK} strokeWidth="1.2">
        {TICKS.map(([x1, y1, x2, y2]) => (
          <line key={`t-${x1}-${y1}`} x1={x1} y1={y1} x2={x2} y2={y2} />
        ))}
      </g>
      <circle cx="180" cy="180" r="10" fill="#7A2E3B" stroke={INK} strokeWidth="1.5" />
      <polygon points="180,140 175,132 185,132" fill="#7A2E3B" stroke={INK} />
      <rect x="132" y="312" width="96" height="20" rx="2" fill={MUST} stroke={INK} strokeWidth="1.3" />
      <text x="180" y="326" className="hf-vault">
        AES-256
      </text>
    </svg>
  );
}
```

Créer `frontend/src/ui/hotel/KeyTag.js` :

```jsx
import React from 'react';

// A key on its hook, the provider's name on the tag. Gold for a mailbox that
// gets every feature, pink otherwise. `tilt` (degrees) keeps a board of keys
// from looking like a spreadsheet.
export default function KeyTag({ name, note, gold = false, tilt = 0 }) {
  return (
    <div className="ht-key">
      <span className="ht-key__hook" aria-hidden="true" />
      <span className="ht-key__str" aria-hidden="true" />
      <span className={`ht-tag ht-key__tag${gold ? ' is-gold' : ''}`} style={{ '--tilt': `${tilt}deg` }}>
        {note && <small>{note}</small>}
        {name}
      </span>
    </div>
  );
}
```

- [ ] **Step 3: Les deux sections**

Créer `frontend/src/components/landing/PrivacyVault.js` :

```jsx
import React from 'react';
import { Link } from 'react-router-dom';
import Vault from '../../ui/hotel/Vault';
import FloorHeading from './FloorHeading';
import { canPromise } from './features';
import './PrivacyVault.css';

// Three facts, each checked against the code: the model only sees the sender,
// the subject and at most 200 characters (internal/ai/mistral.go); credentials
// are sealed at rest (api/tokens.go); a rule the user wrote can trash, hence
// "sans votre accord" rather than "sans vous demander".
export default function PrivacyVault({ floorNo, isConfigured }) {
  const undo = canPromise('undo', isConfigured);
  return (
    <section className="hl-section" id="confidentialite" data-floor="confidentialite" aria-labelledby="hl-privacy-title">
      <div className="hl-wrap">
        <FloorHeading n={floorNo} kicker="Confidentialité" id="hl-privacy-title">
          Ce qu'on lit. Ce qu'on garde.
          <br />
          <em>Ce qu'on ne fait jamais.</em>
        </FloorHeading>
        <div className="hl-vault">
          <Vault className="hl-vault__art" />
          <div>
            <div className="hl-vplq">
              <b aria-hidden="true">I</b>
              <h3>On lit</h3>
              <p>L'expéditeur, l'objet et au plus 200 caractères du message. Jamais le message entier, jamais les pièces jointes.</p>
            </div>
            <div className="hl-vplq">
              <b aria-hidden="true">II</b>
              <h3>On garde</h3>
              <p>
                Vos accès, chiffrés. L'historique de vos actions{undo ? ', pour pouvoir les annuler' : ''}. Vous pouvez tout exporter, ou tout
                supprimer, quand vous voulez.
              </p>
            </div>
            <div className="hl-vplq">
              <b aria-hidden="true">III</b>
              <h3>On ne fait jamais</h3>
              <p>Revendre quoi que ce soit. Supprimer un e-mail sans votre accord. Et le code est public : vous pouvez vérifier.</p>
            </div>
            <Link className="ht-link" to="/confidentialite">
              Lire la politique de confidentialité
            </Link>
          </div>
        </div>
      </div>
    </section>
  );
}
```

Créer `frontend/src/components/landing/PrivacyVault.css` :

```css
.hl-vault { display: grid; grid-template-columns: 380px 1fr; gap: 56px; align-items: center; margin-top: 48px; }
.hl-vault__art { display: block; width: 100%; height: auto; }
.hl-vplq {
  position: relative;
  margin-bottom: 16px;
  padding: 18px 22px 20px 64px;
  background: var(--h-surface);
  border: 1.5px solid var(--h-line);
  box-shadow: 5px 5px 0 var(--h-hair);
}
.hl-vplq b {
  position: absolute;
  left: 16px;
  top: 18px;
  display: grid;
  place-items: center;
  width: 32px;
  height: 32px;
  border-radius: 50%;
  background: #E3A93B;
  border: 1.5px solid var(--h-line);
  color: #7A2E3B;
  font-family: var(--h-font-display);
  font-style: italic;
  font-size: 14px;
}
.hl-vplq h3 { font-family: var(--h-font-display); font-variation-settings: 'opsz' 28; font-weight: 400; font-size: 22px; }
.hl-vplq p { margin-top: 4px; font-size: 15px; line-height: 1.5; color: var(--h-muted); }
@media (max-width: 1000px) {
  .hl-vault { grid-template-columns: 1fr; gap: 32px; }
  .hl-vault__art { max-width: 280px; margin: 0 auto; }
}
```

Créer `frontend/src/components/landing/ProviderBoard.js` :

```jsx
import React, { useEffect, useState } from 'react';
import { configService } from '../../services/api';
import KeyTag from '../../ui/hotel/KeyTag';
import FloorHeading from './FloorHeading';
import { imapLegend } from './features';
import './ProviderBoard.css';

const TILTS = [-3, 2, -1, 3, -2, 2, -3, 1, -2, 3, -1, 2, -3, 1, -2];

// The key board: the mailbox catalog for this edition, as GET /api/providers
// returns it. Nothing is written in here: a provider on the board is one the
// server can reach. A key is gold when it reaches the Gmail API on an instance
// configured for Google, which is the only route with every feature today.
export default function ProviderBoard({ isConfigured }) {
  const [providers, setProviders] = useState(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    configService
      .getProviders()
      .then(({ data }) => {
        if (!cancelled) setProviders(data.providers || []);
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const keys = (providers || []).map((p, i) => {
    const routes = p.routes || [];
    const gold = Boolean(isConfigured) && routes.some((r) => r.transport === 'gmail-api');
    const imap = routes.some((r) => r.transport === 'imap');
    return { key: p.key, name: p.name, gold, note: gold ? 'via Google' : imap ? 'IMAP' : '', tilt: TILTS[i % TILTS.length] };
  });

  return (
    <section className="hl-section hl-section--alt" data-floor="confidentialite" aria-labelledby="hl-providers-title">
      <div className="hl-wrap">
        <FloorHeading id="hl-providers-title" lede="Même avec l'adresse Orange que vous avez depuis 2004.">
          Ça marche avec <em>votre boîte.</em>
        </FloorHeading>
        <div className="hl-board" aria-busy={providers === null && !failed}>
          {failed && <p className="hl-board__note">La liste des boîtes n'a pas pu être chargée. Rechargez la page pour la voir.</p>}
          {keys.map((k) => (
            <KeyTag key={k.key} name={k.name} note={k.note} gold={k.gold} tilt={k.tilt} />
          ))}
        </div>
        <p className="hl-legend">
          {keys.some((k) => k.gold) && (
            <span>
              <i className="is-gold" aria-hidden="true" />
              Toutes les fonctions
            </span>
          )}
          <span>
            <i aria-hidden="true" />
            {imapLegend()}
          </span>
        </p>
      </div>
    </section>
  );
}
```

Créer `frontend/src/components/landing/ProviderBoard.css` :

```css
.hl-board {
  display: grid;
  grid-template-columns: repeat(5, 1fr);
  gap: 26px 10px;
  max-width: 1000px;
  min-height: 120px;
  margin: 44px auto 0;
  padding: 26px 26px 30px;
  background: #6A2733;
  border: 12px solid #4A1A24;
  border-radius: 6px;
  box-shadow: inset 0 0 0 2px #2B1B1E, 0 0 0 2px #2B1B1E, 0 30px 50px -30px rgba(43, 27, 30, 0.7);
}
.hl-board .ht-key__tag { background: #EFB7C3; color: #2B1B1E; }
.hl-board .ht-key__tag.is-gold { background: #E3A93B; }
.hl-board .ht-key__tag::after { background: #6A2733; }
.hl-board__note { grid-column: 1 / -1; align-self: center; text-align: center; color: #FBF1E4; font-family: var(--h-font-display); font-style: italic; font-size: 17px; }
.hl-legend { display: flex; flex-wrap: wrap; justify-content: center; gap: 12px 28px; margin-top: 22px; font-size: 14px; color: var(--h-muted); }
.hl-legend span { display: inline-flex; align-items: center; gap: 8px; }
.hl-legend i { width: 26px; height: 14px; border: 1.5px solid var(--h-line); border-radius: 2px 8px 8px 2px; background: #EFB7C3; }
.hl-legend i.is-gold { background: #E3A93B; }
@media (max-width: 1000px) { .hl-board { grid-template-columns: repeat(3, 1fr); } }
@media (max-width: 640px) {
  .hl-board { grid-template-columns: 1fr 1fr; padding: 18px 12px 22px; border-width: 8px; }
  .hl-board .ht-key__tag { width: 100%; min-width: 0; }
}
```

- [ ] **Step 4: Les monter dans `HotelLanding`**

Dans `frontend/src/pages/HotelLanding.js`, ajouter après l'import de `TriageDemo` :

```jsx
import PrivacyVault from '../components/landing/PrivacyVault';
import ProviderBoard from '../components/landing/ProviderBoard';
```

et insérer après `<FeatureCorridor ... />` :

```jsx
        <PrivacyVault floorNo={floorNo.confidentialite} isConfigured={isConfigured} />
        <ProviderBoard isConfigured={isConfigured} />
```

- [ ] **Step 5: Vérifier**

Run: `cd frontend && CI=false npm run build 2>&1 | grep -E "Compiled|Failed|ready" ; cd ../.superpowers/landing-check && node check.mjs providers && node check.mjs providers --google=off && node check.mjs providers --width=375`
Expected: build prêt ; trois sorties `"failures": []`.

Ouvrir `shots/providers-privacy-1440.png` et `-375.png` : coffre, trois plaques, tableau à clés, légende, conformes à la maquette.

- [ ] **Step 6: Commit**

```bash
/usr/bin/git add frontend/src/ui/hotel/Vault.js frontend/src/ui/hotel/KeyTag.js frontend/src/components/landing/PrivacyVault.js frontend/src/components/landing/PrivacyVault.css frontend/src/components/landing/ProviderBoard.js frontend/src/components/landing/ProviderBoard.css frontend/src/pages/HotelLanding.js
/usr/bin/git commit -m "feat(frontend): ajouter le coffre et le tableau des boites" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 11: la carte des tarifs et le tableau à lettres

**Files:**
- Create: `frontend/src/components/landing/RateCard.js`, `RateCard.css`, `FaqBoard.js`, `FaqBoard.css`
- Modify: `frontend/src/pages/HotelLanding.js`
- Create (hors git): `.superpowers/landing-check/scenarios/rates-faq.mjs`

**Interfaces:**
- Consumes: `waitlistService.join(email, source)`, `apiError`, `hasJoinedWaitlist`, `rememberWaitlistJoin`, `forgetWaitlistJoin`, `waitlistEmail` de `lib/waitlist.js`, `track`, `canPromise`, `outlookAnswer`, `FloorHeading`.
- Produces: `RateCard({ floorNo, billingOn })` (section `id="tarifs"`, rendue seulement hors self-hosted), `FaqBoard({ floorNo, isConfigured, selfHosted })` (section `id="questions"`, réponse `#hl-faq-answer`).

- [ ] **Step 1: Écrire le scénario**

Créer `.superpowers/landing-check/scenarios/rates-faq.mjs` :

```js
// Tarifs exists only where someone bills; the waitlist joins with the
// "landing" source; with billing on it points to /pricing; the letter board
// answers the Outlook question from GMAIL_ONLY.
export default async function ratesFaq({ page, base, opt, expect, calls, shot }) {
  await page.goto(`${base}/`);
  await page.waitForSelector('.hl-lb');
  const rates = await page.$('#tarifs');
  const selfHosted = opt.edition === 'self-hosted';
  expect(Boolean(rates) === !selfHosted, `Tarifs present: ${Boolean(rates)} on edition ${opt.edition || 'hosted'}`);
  if (rates && opt.billing !== 'on') {
    await page.fill('#tarifs input[type=email]', 'marie@exemple.fr');
    await page.click('#tarifs button[type=submit]');
    await page.waitForTimeout(700);
    const call = calls.find((c) => c.key === 'POST /api/waitlist');
    expect(Boolean(call) && JSON.parse(call.body).source === 'landing', 'the waitlist was not joined with source "landing"');
    const text = await page.$eval('#tarifs', (el) => el.textContent);
    expect(text.includes("C'est noté"), 'no confirmation after joining the waitlist');
  }
  if (rates && opt.billing === 'on') {
    expect(Boolean(await page.$('#tarifs a[href="/pricing"]')), 'with billing on, Tarifs does not link to /pricing');
  }
  await page.click('.hl-lb button:has-text("Outlook")');
  const answer = await page.$eval('#hl-faq-answer', (el) => el.textContent);
  expect(answer.includes("L'annulation"), `the Outlook answer reads "${answer}"`);
  await page.$eval('#questions', (el) => el.scrollIntoView());
  await shot('faq');
}
```

Run: `cd .superpowers/landing-check && node check.mjs rates-faq`
Expected: ÉCHEC, `.hl-lb` introuvable.

- [ ] **Step 2: La carte des tarifs**

Créer `frontend/src/components/landing/RateCard.js` :

```jsx
import React, { useState } from 'react';
import { Link } from 'react-router-dom';
import { waitlistService, apiError } from '../../services/api';
import { forgetWaitlistJoin, hasJoinedWaitlist, rememberWaitlistJoin, waitlistEmail } from '../../lib/waitlist';
import { track } from '../../lib/analytics';
import FloorHeading from './FloorHeading';
import './RateCard.css';

// The rate card on the wall. With paid checkout closed it keeps the Pro
// waitlist (source "landing", remembered by lib/waitlist.js like on the
// pricing page); with it open it points to /pricing. Never rendered on a
// self-hosted instance, which bills nobody.
export default function RateCard({ floorNo, billingOn }) {
  const [email, setEmail] = useState(waitlistEmail);
  const [joined, setJoined] = useState(hasJoinedWaitlist);
  const [joining, setJoining] = useState(false);
  const [error, setError] = useState('');

  const join = async (event) => {
    event.preventDefault();
    const address = email.trim();
    if (!address || joining) return;
    setJoining(true);
    setError('');
    try {
      await waitlistService.join(address, 'landing');
      rememberWaitlistJoin(address);
      setJoined(true);
      // The source only: the address itself must never reach analytics.
      track('waitlist_join', { source: 'landing' });
    } catch (err) {
      setError(apiError(err, 'Inscription impossible pour le moment. Réessayez.'));
    } finally {
      setJoining(false);
    }
  };

  const reset = () => {
    forgetWaitlistJoin();
    setJoined(false);
    setError('');
  };

  const lines = [
    ['Gratuit', '200 tris par mois'],
    ['Toutes les fonctions', 'incluses'],
    ['Carte bancaire', 'jamais demandée'],
  ];

  return (
    <section className="hl-section" id="tarifs" data-floor="tarifs" aria-labelledby="hl-rates-title">
      <div className="hl-wrap">
        <FloorHeading n={floorNo} kicker="Tarifs" id="hl-rates-title">
          Gratuit jusqu'à <em>200 tris par mois.</em>
        </FloorHeading>
        <div className="hl-rate">
          <h3>Tarifs</h3>
          <div className="hl-rate__orn" aria-hidden="true">
            <i />
            <i />
            <i />
          </div>
          <dl>
            {lines.map(([label, value]) => (
              <div key={label} className="hl-rate__line">
                <dt>{label}</dt>
                <span className="hl-rate__dots" aria-hidden="true" />
                <dd>{value}</dd>
              </div>
            ))}
            <div className="hl-rate__line">
              <dt>Pro</dt>
              <span className="hl-rate__dots" aria-hidden="true" />
              <dd>
                <em>{billingOn ? 'illimité' : 'illimité, bientôt'}</em>
              </dd>
            </div>
          </dl>
          {billingOn ? (
            <div className="hl-rate__wl">
              <Link className="ht-btn ht-btn-primary" to="/pricing">
                Voir l'offre Pro
              </Link>
            </div>
          ) : joined ? (
            <div className="hl-rate__wl">
              <p role="status">C'est noté : on vous écrit une seule fois, le jour où Pro ouvre.</p>
              <button type="button" className="ht-link" onClick={reset}>
                Changer d'adresse
              </button>
            </div>
          ) : (
            <form className="hl-rate__wl" onSubmit={join}>
              <p>Laissez votre e-mail, on vous écrit une fois : le jour où Pro ouvre.</p>
              <div className="hl-rate__row">
                <input
                  className="ht-input"
                  type="email"
                  required
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="vous@exemple.fr"
                  aria-label="Votre adresse e-mail"
                />
                <button type="submit" className="ht-btn ht-btn-primary" disabled={joining}>
                  Prévenez-moi
                </button>
              </div>
              {error && (
                <p className="hl-rate__error" role="alert">
                  {error}
                </p>
              )}
            </form>
          )}
        </div>
      </div>
    </section>
  );
}
```

Créer `frontend/src/components/landing/RateCard.css` :

```css
.hl-rate {
  max-width: 600px;
  margin: 44px auto 0;
  padding: 30px 38px 32px;
  background: var(--h-surface);
  border: 14px solid #E3A93B;
  box-shadow: inset 0 0 0 2px var(--h-line), 0 0 0 2px var(--h-line), 10px 10px 0 var(--h-offset);
}
.hl-rate h3 { text-align: center; font-family: var(--h-font-display); font-style: italic; font-weight: 400; font-size: 32px; }
.hl-rate__orn { display: flex; justify-content: center; gap: 8px; margin: 8px 0 18px; }
.hl-rate__orn i { width: 7px; height: 7px; background: #E3A93B; border: 1.2px solid var(--h-line); transform: rotate(45deg); }
.hl-rate__line { display: flex; align-items: baseline; padding: 8px 0; font-size: 17px; }
.hl-rate__line dt { font-family: var(--h-font-display); font-variation-settings: 'opsz' 28; font-weight: 500; font-size: 20px; }
.hl-rate__line dd em { font-family: var(--h-font-display); color: var(--h-accent); }
.hl-rate__dots { flex: 1; margin: 0 10px; border-bottom: 2px dotted var(--h-line); opacity: 0.6; transform: translateY(-5px); }
.hl-rate__wl { margin-top: 22px; padding-top: 18px; border-top: 3px double var(--h-line); text-align: center; }
.hl-rate__wl p { font-size: 14.5px; color: var(--h-muted); }
.hl-rate__wl .ht-link { margin-top: 10px; }
.hl-rate__row { display: flex; gap: 8px; margin-top: 12px; }
.hl-rate__row .ht-btn { flex-shrink: 0; }
.hl-rate__error { margin-top: 10px; font-size: 13.5px; color: var(--h-accent); }
@media (max-width: 640px) {
  .hl-rate { padding: 24px 18px 26px; border-width: 10px; }
  .hl-rate__row { flex-direction: column; }
  .hl-rate__line { font-size: 15px; }
  .hl-rate__line dt { font-size: 17px; }
}
```

- [ ] **Step 3: Le tableau à lettres**

Créer `frontend/src/components/landing/FaqBoard.js` :

```jsx
import React, { useState } from 'react';
import FloorHeading from './FloorHeading';
import { canPromise, outlookAnswer } from './features';
import './FaqBoard.css';

// The felt letter board of a hotel lobby: the questions on the board, the
// answer beside it. Every answer holds on this instance (features.js): no undo
// promised without Google, no Pro on a self-hosted instance.
export default function FaqBoard({ floorNo, isConfigured, selfHosted }) {
  const undo = canPromise('undo', isConfigured);
  const rules = canPromise('rules', isConfigured);
  const qa = [
    [
      'Vous lisez mes mails ?',
      "Nous, non. Le modèle voit l'expéditeur, l'objet et au plus 200 caractères, le temps de proposer un tri. Il ne voit jamais le message entier ni les pièces jointes.",
    ],
    [
      "Et si l'IA se trompe ?",
      undo
        ? "Vous dites non. Et si vous aviez déjà dit oui, vous annulez depuis l'historique. Rien n'est supprimé sans votre accord."
        : "Vous dites non. Rien ne bouge sans votre accord, et rien n'est supprimé sans lui.",
    ],
    ['Ça marche avec Outlook ?', outlookAnswer()],
    [
      'Je peux tout supprimer ?',
      'Oui. Vous exportez tout, puis vous supprimez votre compte depuis la page Compte. Vos accès et votre historique partent avec.',
    ],
    [
      "Pourquoi c'est gratuit ?",
      selfHosted
        ? "Parce que c'est votre instance : vous l'hébergez, personne ne vous facture."
        : `Jusqu'à 200 tris par mois, ça nous coûte peu. ${rules ? 'Les règles et les tris' : 'Les tris'} déjà connus ne comptent même pas. Au-delà, il y aura Pro.`,
    ],
  ];
  const [open, setOpen] = useState(0);

  return (
    <section className="hl-section hl-section--alt" id="questions" data-floor="questions" aria-labelledby="hl-faq-title">
      <div className="hl-wrap">
        <FloorHeading n={floorNo} kicker="Questions" id="hl-faq-title">
          Les questions qu'on nous pose <em>vraiment.</em>
        </FloorHeading>
        <div className="hl-qa">
          <div className="hl-lb">
            <p className="hl-lb__hd" aria-hidden="true">
              QUESTIONS
            </p>
            {qa.map(([question], i) => (
              <button key={question} type="button" aria-pressed={open === i} aria-controls="hl-faq-answer" onClick={() => setOpen(i)}>
                {question}
              </button>
            ))}
          </div>
          <div className="hl-ans" id="hl-faq-answer" aria-live="polite">
            <h3>{qa[open][0]}</h3>
            <p>{qa[open][1]}</p>
          </div>
        </div>
      </div>
    </section>
  );
}
```

Créer `frontend/src/components/landing/FaqBoard.css` :

```css
.hl-qa { display: grid; grid-template-columns: 1.1fr 1fr; gap: 40px; align-items: start; margin-top: 44px; }
.hl-lb {
  padding: 22px 26px;
  background-color: #1E1A1C;
  background-image: repeating-linear-gradient(0deg, rgba(255, 255, 255, 0.04) 0 2px, transparent 2px 13px);
  border: 16px solid #8A5A3C;
  border-radius: 6px;
  box-shadow: inset 0 0 0 2px #000, 0 0 0 2px var(--h-line), 0 30px 50px -30px rgba(43, 27, 30, 0.7);
}
.hl-lb__hd { margin-bottom: 12px; text-align: center; font-size: 13px; font-weight: 600; letter-spacing: 0.5em; color: #E3A93B; }
.hl-lb button {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 100%;
  padding: 10px 0;
  text-align: left;
  color: #F5EFE4;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  font-size: 15px;
  font-weight: 600;
  letter-spacing: 0.16em;
  text-transform: uppercase;
}
.hl-lb button::before { content: ''; flex-shrink: 0; width: 8px; height: 8px; border-radius: 50%; border: 1.5px solid rgba(255, 255, 255, 0.35); }
.hl-lb button[aria-pressed='true'] { color: #F4CF6B; }
.hl-lb button[aria-pressed='true']::before { background: #F4CF6B; border-color: #F4CF6B; box-shadow: 0 0 8px #F4CF6B; }
.hl-ans { position: sticky; top: 110px; padding: 26px 28px; background: var(--h-bg); border: 1.5px solid var(--h-line); box-shadow: 7px 7px 0 var(--h-offset); }
.hl-ans h3 { font-family: var(--h-font-display); font-variation-settings: 'opsz' 28; font-weight: 400; font-size: 28px; line-height: 1.12; }
.hl-ans p { margin-top: 12px; font-size: 16px; line-height: 1.6; color: var(--h-muted); }
@media (max-width: 1000px) {
  .hl-qa { grid-template-columns: 1fr; }
  .hl-ans { position: static; }
}
@media (max-width: 640px) {
  .hl-lb { padding: 16px 14px; border-width: 10px; }
  .hl-lb button { font-size: 12.5px; letter-spacing: 0.08em; }
}
```

- [ ] **Step 4: Les monter dans `HotelLanding`**

Dans `frontend/src/pages/HotelLanding.js` :
- ajouter après l'import de `ProviderBoard` :

```jsx
import RateCard from '../components/landing/RateCard';
import FaqBoard from '../components/landing/FaqBoard';
```

- remplacer `const { isConfigured, selfHosted } = useInstance();` par `const { isConfigured, selfHosted, billingOn } = useInstance();` ;
- insérer après `<ProviderBoard ... />` :

```jsx
        {!selfHosted && <RateCard floorNo={floorNo.tarifs} billingOn={billingOn} />}
        <FaqBoard floorNo={floorNo.questions} isConfigured={isConfigured} selfHosted={selfHosted} />
```

- [ ] **Step 5: Vérifier**

Run: `cd frontend && CI=false npm run build 2>&1 | grep -E "Compiled|Failed|ready" ; cd ../.superpowers/landing-check && node check.mjs rates-faq && node check.mjs rates-faq --billing=on && node check.mjs rates-faq --edition=self-hosted && node check.mjs rates-faq --width=375`
Expected: build prêt ; quatre sorties `"failures": []`.

- [ ] **Step 6: Commit**

```bash
/usr/bin/git add frontend/src/components/landing/RateCard.js frontend/src/components/landing/RateCard.css frontend/src/components/landing/FaqBoard.js frontend/src/components/landing/FaqBoard.css frontend/src/pages/HotelLanding.js
/usr/bin/git commit -m "feat(frontend): ajouter les tarifs et les questions" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 12: la page entière, sur tous les écrans, et la documentation

**Files:**
- Create (hors git): `.superpowers/landing-check/scenarios/tour.mjs`
- Modify: tout fichier de `frontend/src/components/landing/` ou `frontend/src/styles/hotel.css` qu'un défaut constaté ici oblige à corriger
- Modify: `CLAUDE.md`

**Interfaces:**
- Consumes: toute la landing (tâches 3 à 11).
- Produces: rien de nouveau ; la preuve que la page tient de 375 à 1440 px, de jour et de nuit, au clavier et sans animation, et une documentation à jour.

- [ ] **Step 1: Écrire le scénario de visite**

Créer `.superpowers/landing-check/scenarios/tour.mjs` :

```js
// The whole page at one width: no horizontal scroll, every floor present, the
// elevator panel lighting the floor it goes to (wide screens), the form
// reachable from the keyboard with a visible focus ring.
export default async function tour({ page, base, width, opt, expect, shot }) {
  await page.goto(`${base}/`);
  await page.waitForSelector('.hl-exit');
  await page.waitForTimeout(1000);
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
  expect(overflow <= 0, `horizontal overflow of ${overflow}px at ${width}px`);
  for (const id of ['hall', 'fonctionnement', 'confidentialite', 'questions']) {
    expect(Boolean(await page.$(`#${id}`)), `section #${id} is missing`);
  }
  if (width > 1000) {
    await page.click('.hl-top .ht-panel a:has-text("Questions")');
    await page.waitForTimeout(1400);
    const lit = await page.$eval('.hl-top .ht-panel a.is-on', (a) => a.textContent).catch(() => '');
    expect(lit.includes('Questions'), `after going to Questions the lit floor is "${lit}"`);
  }
  await page.evaluate(() => window.scrollTo(0, 0));
  let reached = false;
  for (let i = 0; i < 20 && !reached; i += 1) {
    await page.keyboard.press('Tab');
    reached = await page.evaluate(() => document.activeElement && document.activeElement.id === 'auth-email');
  }
  expect(reached, 'the e-mail field cannot be reached with Tab within 20 presses');
  const ring = await page.$eval('#auth-email', (el) => getComputedStyle(el).outlineStyle);
  expect(ring !== 'none', `the focused e-mail field shows no outline (${ring})`);
  await shot(opt.dark ? 'night' : 'day');
}
```

- [ ] **Step 2: Lancer la visite sur toutes les largeurs**

Run: `cd .superpowers/landing-check && for w in 375 768 1440; do node check.mjs tour --width=$w || echo "FAILED at $w"; node check.mjs tour --width=$w --dark || echo "FAILED at $w night"; done`
Expected: six sorties `"failures": []`, aucune ligne `FAILED`.

Ouvrir les six captures `shots/tour-day-*.png` et `shots/tour-night-*.png` (outil Read). Pour chaque largeur, vérifier : aucun texte coupé ni superposé, l'hôtel lisible, la fiche dans l'ordre titre, hôtel, fiche, plaques sur mobile, les portes et les clés sur deux colonnes à 375 px, la nuit sans texte sombre sur fond sombre. Chaque défaut se corrige dans le fichier CSS de la section concernée, puis on relance la largeur touchée jusqu'à ce qu'elle soit propre.

- [ ] **Step 3: Relancer toute la batterie**

Run:
```bash
cd frontend && CI=false npm run build 2>&1 | grep -E "Compiled|Failed|ready"
cd ../.superpowers/landing-check && node test-pure.mjs
for s in "switch --theme=hotel" "switch --theme=classic" "auth --theme=hotel" "auth --theme=classic" "shell" "shell --edition=self-hosted" "hall" "hall --google=off" "hall --reduced" "corridor" "corridor --google=off" "demo" "demo --google=off" "demo --reduced" "providers" "providers --google=off" "rates-faq" "rates-faq --billing=on" "rates-faq --edition=self-hosted" "moodboard"; do node check.mjs $s > shots/last.json || { echo "FAILED: $s"; cat shots/last.json; }; done; echo "battery done"
node check.mjs classic-shot --theme=classic --reduced --out=shots/classic-final.png && python3 compare.py shots/classic-before.png shots/classic-final.png
```
Expected: build prêt, `uiTheme: ok` et `features: ok`, aucune ligne `FAILED`, `battery done`, puis `identical` (la landing classique n'a pas bougé depuis le début).

- [ ] **Step 4: Le backend, une dernière fois**

Run: `export PATH="$HOME/go/bin:$PATH"; cd backend && go vet ./... && go build ./... && go test -race ./... 2>&1 | tail -30`
Expected: `ok` partout, aucune ligne `FAIL`.

- [ ] **Step 5: Mettre `CLAUDE.md` à jour**

Lire `CLAUDE.md` avant d'éditer : `main` a pu bouger. Puis :

1. Section "Project Structure", insérer juste avant la ligne `  src/services/api.js    every HTTP call in the app, grouped by service object` :

```
  src/styles/hotel.css   the Grand Hotel theme (UI_THEME=hotel): tokens under
                         .theme-hotel, day and night, ht- primitives, motion
  src/ui/hotel/          the theme's drawings and bricks: HotelFacade, Door, Vault,
                         KeyTag, ElevatorPanel, Plaque, Emblem, useHotelFonts
  src/components/landing/ the Grand Hotel landing's sections (hl- classes) and
                         features.js, what the landing may promise (GMAIL_ONLY)
```

et remplacer `docs/                    ARCHITECTURE.md, API.md, ROADMAP.md, assets/` par `docs/                    ARCHITECTURE.md, API.md, ROADMAP.md, design/moodboard.html, assets/`.

2. Table des routes, ligne de `/` : remplacer le début `` | `/` | `pages/Login.js` | Marketing landing `` par :

```
| `/` | `pages/HotelLanding.js` or `pages/Login.js` | Chosen at runtime by `UI_THEME` (`lib/uiTheme.js`; `?ui=hotel\|classic` previews it for one tab): the Grand Hotel landing or the classic one. Both share the auth logic (`lib/useAuthForm.js`) and the doors below. Marketing landing
```

3. Section "Data access and state", dans la parenthèse qui liste ce que renvoie `useInstance()` (`` `loading`, `error`, `reload`, `isConfigured`, `billingOn`, `edition`, `selfHosted` ``), ajouter `` `uiTheme` `` à la fin.

4. Section "Design system", ajouter en dernier point :

```markdown
- **The Grand Hotel theme is a second, switchable design system** (`UI_THEME`, see
  Configuration). Its tokens live in `src/styles/hotel.css` under `.theme-hotel`
  (night under `.dark .theme-hotel`), never on `:root`, so `classic` leaves no trace.
  It is plain CSS on purpose, the one exception to "Tailwind only": a theme that can
  be switched off cannot live in `index.css`. Classes are `ht-` (primitives) and
  `hl-` (landing), never a generic name, because a CSS chunk stays loaded after
  navigation and would restyle the dashboard. Illustrations are hand-drawn SVG
  components in `src/ui/hotel/` with fixed colours. Bodoni Moda is always set at
  `font-variation-settings: 'opsz' 28`, or its hairlines swallow a 4 and a hyphen.
  The reference rendering is `docs/design/moodboard.html`, which loads the real
  `hotel.css`: open it before touching the theme.
```

5. Section "Known Gotchas", paragraphe qui commence par `- **Most of the app is still Gmail-only` : remplacer la liste de ce qui refuse (de `Everything else (rules, AI, snooze,` jusqu'à `12 call sites left.`) par :

```
The AI analysis has moved to sessions since. Everything else (rules, snooze,
  unsubscribe, attachments, labels, undo, batch, digest) refuses: 12 call sites left.
```

et ajouter à la fin du paragraphe :

```
  Porting one also means deleting its line in `GMAIL_ONLY`
  (`frontend/src/components/landing/features.js`): the Grand Hotel landing reads it
  to decide what it may promise, and keeps promising nothing it cannot deliver.
```

Vérifier qu'aucun caractère interdit n'est entré : `grep -nP "[\x{2013}\x{2014}\x{2026}\x{2018}\x{2019}\x{201C}\x{201D}]" CLAUDE.md docs/design/moodboard.html frontend/src/styles/hotel.css frontend/src/components/landing/*.js frontend/src/ui/hotel/*.js frontend/src/lib/uiTheme.js frontend/src/lib/useAuthForm.js` doit ne rien afficher.

- [ ] **Step 6: Commit**

```bash
/usr/bin/git add CLAUDE.md frontend/src
/usr/bin/git commit -m "docs: documenter le theme grand hotel et ajuster la landing sur tous les ecrans" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

- [ ] **Step 7: Rendre la main**

Ne pas pousser. Utiliser le skill `superpowers:finishing-a-development-branch` pour proposer la suite (PR vers `main`), en montrant `/usr/bin/git log --oneline main..claude/landing-grand-hotel` et le rappel de déploiement : `UI_THEME` absent vaut `hotel` ; pour garder l'ancienne landing en production après la fusion, régler `UI_THEME=classic` dans Dokploy avant de fusionner.
