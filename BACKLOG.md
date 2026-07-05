# 🗂️ Backlog d'audit — Mailsorter

> Audit réalisé sur la branche `claude/repo-audit-sprint-q2egt6` (identique à `main`).
> Verdict global : **repo mature et cohérent**. Le build passe (backend `go build`/`go vet`,
> frontend `npm run build` sans warning), **toute la suite de tests Go est verte**, et le
> contrat frontend↔backend est sain (aucun appel vers un endpoint non implémenté, aucun lien
> mort dans la navigation). Les 12 phases de la roadmap sont réellement livrées et branchées.
>
> Ce qui reste relève de la **correction de bugs ponctuels** et de la **cohérence
> doc/config/code mort** — pas de chantier structurel.

Légende : priorité **P0** bloquant · **P1** essentiel · **P2** confort — effort **S/M/L**.

---

## 🔴 À réparer

- [x] **R1 — `ApplySuggestion` (action `label`) avale l'erreur Gmail** · **P1 · S**
  `backend/internal/api/ai_handlers.go:245-256`. `labelID, err := h.ensureLabel(...)` **redéclare**
  `err` dans le bloc `case` ; l'échec de `ModifyMessage` (ligne 250) est écrit dans ce `err`
  masqué et **jamais vérifié** par le `if err != nil` de la ligne 256 (qui teste l'`err` externe,
  nil). Conséquence : si l'application d'un libellé échoue côté Gmail, l'API répond quand même
  `{"status":"applied"}`, marque la suggestion `applied` en base **et** écrit une entrée au ledger
  → succès factice + historique/undo faux. Les frères `ApplyBatch` / `autoApplySender` gèrent ça
  correctement : c'est un oubli isolé. **Vrai bug de correction.**

- [x] **R2 — Défaut `MISTRAL_MODEL` incohérent entre les sources** · **P2 · S**
  `backend/internal/config/config.go:59` défaut `"mistral-small-latest"`, alors que
  `.env.example:24`, `README.md:85` et `docker-compose.yml:32` disent `"mistral-large-2411"`.
  Mord uniquement le dev qui lance le backend en `go run` sans env (chemin documenté au README).
  → aligner le défaut du code sur `mistral-large-2411`.

- [x] **R3 — Commentaire d'opérateurs de règle périmé** · **P2 · S**
  `backend/internal/models/models.go:134-135`. Le doc-comment de `RuleCondition` liste 5
  opérateurs ; le moteur en implémente 9 (dont `notContains`/`notEquals`/`olderThan`/`newerThan`,
  cf. `rules/rules.go:31-66`). Code et README sont corrects, seul le commentaire ment.

- [x] **R4 — Commentaire « pas encore branché » périmé sur le digest** · **P2 · S**
  `backend/internal/api/account.go:228-231`. `GetDigest` se décrit comme la charge « qu'un
  scheduler passerait *once that scope is wired* ». Le scope **est** branché : `digest_scheduler.go`
  envoie réellement via `SendMessage`, la boucle est démarrée dans `NewHandler`, et `gmail.send`
  est dans les scopes OAuth. Commentaire trompeur à corriger.

- [x] **R5 — Logs de debug laissés en prod (frontend)** · **P2 · S**
  `frontend/src/contexts/EmailContext.js:34,38,48,60` — 4 `console.log('[Cache] …')`. À retirer.

## 🟡 Essentiel manquant

_Rien._ Aucune feature dont l'absence bloque un usage évident : les parcours critiques
(connexion → tri IA → application → désabonnement → règles → snooze → historique/undo →
digest → RGPD → billing) sont tous branchés de bout en bout. Barre de scope respectée :
je n'invente pas de feature.

> Endpoints « morts » repérés mais **volontairement laissés** (fonctionnels, testés, sans
> risque, potentiellement câblés par une UI future) : `GET /api/stats/digest`, `GET /api/labels`,
> `GET`+`POST /api/smart-labels`. À noter : la feature « Libellés intelligents » **fonctionne**
> déjà via le flux IA (`analysis.go:204`, `ai_handlers.go:818`) — seule l'UI de *gestion*
> manque, ce n'est pas un blocage.

## 🟢 Contenu à compléter

- [x] **C1 — `.env.example` : documenter `MONGODB_URI` et `PORT`** · **P2 · S**
  `config.go:52-53` les lit ; `.env.example` ne documente que `MONGO_ROOT_*` et `BACKEND_PORT`
  (dérivés par Compose). Trou de doc pour un run local hors Docker → ajouter en commentaire.

- [x] **C2 — Services frontend morts** · **P2 · S**
  `frontend/src/services/api.js:74-76,99-102` : `labelService` et `smartLabelService` définis mais
  **jamais importés** par aucune page. Code mort → retirer les wrappers inutilisés.

---

### Blocages / décisions produit
_Aucun à ce stade._ Tous les items ci-dessus sont mécaniques et sans risque de destruction de
données.
