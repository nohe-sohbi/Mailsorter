# 🗂️ Backlog d'audit — Mailsorter

> Audit réalisé sur la branche `claude/repo-audit-execution-sdy2q4`.
> **État de départ sain** : `go build`, `go vet`, `go test -race ./...` tout vert ;
> `npm run build` compile sans erreur ; le contrat frontend↔backend est complet
> (les ~45 appels d'`api.js` mappent tous une route réelle). Les 12 phases de la
> roadmap sont livrées et branchées. Le précédent sprint d'audit (PR #18/#19) est
> intégralement shippé.
>
> Cet audit repart de zéro et remonte **des défauts de correction réels** (surtout
> côté moteur Gmail : décodage du corps, parsing de date), plus du **code mort** et
> des **incohérences doc**. Aucun chantier structurel, aucune feature essentielle
> manquante.

Légende : priorité **P0** bloquant · **P1** essentiel · **P2** confort — effort **S/M/L**.

---

## 🔴 À réparer

- [x] **B1 — Les règles sur le champ `body` ne matchent jamais (corps stocké en base64url non décodé)** · **P1 · S**
  `backend/internal/gmail/gmail.go:338-352` (`GetEmailBody`) renvoie
  `Payload.Body.Data` / `Parts[].Body.Data` **verbatim**. L'API Gmail livre ces
  champs **encodés base64url**, et rien ne les décode (aucun `base64.Decode` dans
  le package). Cette valeur brute est stockée comme `Email.Body` (`handlers.go:373`)
  puis **comparée** par les règles déterministes (`rules.go:244,332` →
  `rules/rules.go:110-111` fait `strings.Contains(strings.ToLower(body), terme)`).
  `body` est un champ de condition **annoncé et validé** (`models.go:138`,
  `rules.go:60`). Conséquence : une règle « *body contient `unsubscribe`* » compare
  le terme en clair à du charabia base64 → **la règle ne se déclenche jamais**
  (match par accident seulement). Les champs `from`/`subject`/`snippet` marchent car
  déjà en clair — seul `body` est cassé, donc échec silencieux et non-évident.
  **Fix** : décoder base64url dans `GetEmailBody`.

- [x] **B2 — Le parsing du header `Date` rejette des formats RFC 5322 valides et avale l'erreur → `ReceivedDate` à zéro** · **P1 · S**
  `backend/internal/gmail/gmail.go:332` : `date, _ = time.Parse(time.RFC1123Z, header.Value)`.
  L'erreur est jetée, et `RFC1123Z` échoue sur des formes légales courantes (jour à
  un chiffre `Tue, 5 Nov…`, fuseau nommé `… GMT`, sans jour de semaine `15 Nov…`) →
  `time.Time` zéro. Or Gmail fournit `Message.InternalDate` (epoch ms, canonique),
  **ignoré**. Conséquence : les règles temporelles `olderThan`/`newerThan` bailent
  sur date zéro (`rules/rules.go:171-173`) → ces emails **échappent** aux règles
  d'âge ; et `lastEmail`/`lastReceived` des vues Expéditeurs/Désabonnements
  (`ai_handlers.go:557`, `unsubscribe.go:164`) tombent à epoch-zéro → tri et
  affichage « dernier reçu » corrompus. **Fix** : parser plusieurs layouts +
  fallback sur `InternalDate`.

- [x] **B3 — Le récap hebdo (Pricing) affiche des entrées vides/incolores pour les actions `read` et `star`** · **P2 · S**
  `frontend/src/pages/Pricing.js:42-48,200-210`. `ACTION_COLORS`/`ACTION_LABELS` ne
  définissent que `archive/delete/label/keep`. Mais l'agrégateur backend produit
  aussi `read` (fold `markRead`→`read`, `activity.go:60-61`) et `star` — deux
  actions **créables par l'utilisateur** dans l'éditeur de Règles (`Rules.js:36-37`).
  Dès qu'un `read`/`star` est journalisé sur 7 jours, la légende rend une pastille
  **sans couleur** (`ACTION_COLORS['read']` = `undefined`) et un libellé **vide**
  (`ACTION_LABELS['read']` = `undefined`) → entrée corrompue « ⚪ · 5 ». Le
  `.filter(v>0)` ne protège pas. **Fix** : ajouter `read`/`star` aux maps + fallback
  neutre pour toute clé inconnue.

- [x] **B4 — `getUserToken` renvoie un token expiré avec erreur `nil` quand le refresh échoue → 500 en boucle au lieu de 401** · **P1 · M**
  `backend/internal/api/ai_handlers.go:784-796`. Si le token est expiré, le refresh
  n'est tenté que si `RefreshToken != ""`, et **en cas d'échec** le vieux token
  expiré est conservé avec `return token, nil`. Un utilisateur dont l'autorisation
  Google a été **révoquée** obtient alors des **500 perpétuels** (« Failed to fetch
  emails »…) au lieu d'un 401 propre — or le frontend ne nettoie la session et ne
  renvoie au login **que sur 401** (`api.js:27-33`). L'utilisateur est **coincé**,
  jamais réinvité à se reconnecter. **Fix** : renvoyer une erreur typée
  (re-auth requise) quand aucun token valide ne peut être produit, et la mapper en
  **401** dans les 8 appelants.

- [x] **B6 — Un snooze en échec permanent est réessayé toutes les minutes indéfiniment** · **P2 · S**
  `backend/internal/api/snooze.go:255-263` (`wakeDueSnoozes`). Un snooze dû ne passe
  `status:"done"` qu'**après** `restoreSnoozed` réussi ; en échec il est loggé et
  `continue`d sans changement d'état. Si le message a disparu (hard-delete, erreur
  Gmail permanente), la requête `{scheduled, wakeAt<=now}` le re-renvoie **à chaque
  passage de 60 s**, réessayé (avec tout le backoff) **pour toujours**. Aucun plafond
  de tentatives ni dead-letter. **Fix** : compteur de tentatives → statut d'échec
  après N essais.

- [x] **D1 — Checklist de déploiement (ROADMAP) : CORS « dans `routes.go` » est périmé** · **P2 · S**
  `docs/ROADMAP.md` (checklist) : « CORS backend : domaine de prod présent dans
  `routes.go` (`AllowedOrigins`) ». Or le CORS est désormais piloté par la variable
  d'env **`ALLOWED_ORIGINS`** (`config.go:67`, `handlers.go:50-57`, documentée dans
  `.env.example:57-61`) — **aucun rebuild requis**. La checklist ment. **Fix** :
  remplacer par « renseigner `ALLOWED_ORIGINS` ».

## 🟡 Essentiel manquant

_Rien._ Tous les parcours critiques (connexion → tri IA → application →
désabonnement → règles → snooze → historique/undo → digest → RGPD → billing) sont
branchés de bout en bout. Barre de scope respectée : aucune feature inventée.

## 🟢 Contenu à compléter

- [x] **C1 — Code mort à retirer** · **P2 · S**
  - `backend/internal/ai/mistral.go:321` : `MistralClient.FindMatchingLabel` **jamais
    appelé** (supplanté par `localMatchLabel`, `analysis.go:218`) — round-trip IA
    payant laissé derrière.
  - `frontend/src/contexts/EmailContext.js` : `refreshSuggestions` (138-145) et
    `clearCache` (162-172) exportés dans le contexte mais **jamais consommés**.

- [x] **D2 — `docs/API.md` incomplet alors que le README le dit « complète »** · **P2 · M**
  Le README lie `docs/API.md` comme « Documentation complète », mais il **manque des
  sections entières** vs. `routes.go` : toute la surface **AI Sorting**
  (`/api/ai/analyze`, `analyze-async`, `jobs/{id}`, `analyze-sender`, `apply`,
  `apply-batch`, `apply-bulk`, `suggestions`, `suggestions/{id}/reject`) — la feature
  **phare** —, les **Senders** (`/api/senders*`), `POST /api/rules/preview`,
  `POST /api/emails/action`. **Fix** : ajouter les sections manquantes.

---

### Blocages / décisions produit
_Aucun._ Tous les items ci-dessus sont mécaniques, sans risque de destruction de
données. Les fixes B1/B2 touchent le moteur Gmail : ils sont couverts par des tests
unitaires ajoutés au passage.

### Endpoints « morts » laissés volontairement (comme au précédent audit)
`GET /api/stats/digest`, `GET /api/labels`, `GET`+`POST /api/smart-labels` :
fonctionnels, testés, sans risque, potentiellement câblés par une UI future. Non
touchés.
