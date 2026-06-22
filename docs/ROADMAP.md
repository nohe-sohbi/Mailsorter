# 🗺️ Roadmap to Ship

> Approche **Ship First, Enhance After**. Chaque phase est livrable indépendamment.

## ✅ Phase 0 — Refonte produit (livrée)

Le socle « machine de guerre » est en place :

- **Design system Tailwind** — tokens de marque (`brand`/`ink`), composants (`btn-*`, `card`, `input`, `chip`, `skeleton`), animations (`fade-up`, `slide-in-right`, `shimmer`).
- **UI/UX radicale** — landing marketing, cockpit de tri, slide-over de lecture, états *loading / empty / error*, toasts, micro-interactions.
- **Features killer** :
  - Auto-pilote **« Tout appliquer »** via endpoint batch serveur (`/api/ai/apply-batch`).
  - **Actions directes** sur un email (`/api/emails/action` : archive / trash / read).
  - **Filtre haute-confiance** + anneaux de confiance par suggestion.
  - **Avatars déterministes** & indicateurs non-lus.
- **Copywriting** — FR cohérent, orienté bénéfice, hooks percutants.
- **Sécurité** — sanitisation HTML via **DOMPurify** côté lecture.

---

## ✅ Phase 1 — Rétention (livrée)

Rendre le produit **addictif** et **fiable**.

1. **Undo (annulation)** — toast « Annuler » de 5,5 s après archive/suppression (lecteur, suggestions, raccourcis) ; restaure via les actions inverses `unarchive` / `untrash` sur `/api/emails/action`.
2. **Auto-pilote par expéditeur** — la préférence `autoApply` est appliquée directement lors du tri (court-circuite l'appel IA → plus rapide et moins coûteux) ; toggle ON/OFF dans la vue *Expéditeurs*.
3. **Raccourcis clavier** — `j/k` naviguer, `Entrée` ouvrir, `x` sélectionner, `e` archiver, `# / Suppr` supprimer, `a` tout appliquer, `r` synchroniser, `/` rechercher, `?` aide, `Échap` fermer.
4. **Scoring Inbox Zero** — barre de progression quotidienne (objectif 20) + **série de jours** (streak) avec flamme, persistés en `localStorage`.

## ✅ Phase 2 — Échelle (livrée)

1. **Analyse asynchrone** — pool de 3 workers Go drainant une file de jobs (`POST /api/ai/analyze-async` → `GET /api/ai/jobs/{id}`). Au-delà de 10 emails, l'UI bascule en mode async avec **barre de progression en temps réel** (polling 1,5 s) ; l'app ne bloque jamais.
2. **Cache d'analyses** — collection `analysis_cache` indexée sur un hash `sha256(from|subject)`. Les emails déjà vus (tous utilisateurs confondus) ne repassent jamais par l'IA.
3. **Batching Mistral** — `AnalyzeBatch` envoie 8 emails par appel (au lieu de 1) ; le matching de labels est désormais **local** (plus d'appel IA par label). Repli per-email automatique si la réponse ne s'aligne pas.
4. **Index MongoDB** — créés au démarrage (best-effort) : `{userId, messageId}` & `{userId, from}` sur emails, `{userId, status}` sur suggestions, `{userId, senderEmail}` sur préférences, `key` unique sur le cache, `{userId, createdAt}` sur les jobs.

## ✅ Phase 3 — Go-to-market (livrée)

1. **Onboarding guidé** — modale de bienvenue au premier lancement (3 étapes) avec CTA « Trier ma boîte » qui déclenche la 1ʳᵉ analyse ; flag `localStorage` pour ne plus la rejouer.
2. **Quota & socle billing** — compteur mensuel par utilisateur (collection `usage`), exposé via `GET /api/usage`. Plan Free = 200 emails analysés/mois (cache & auto-pilote **non décomptés**) ; dépassement → `402` géré côté UI (toast → page Tarifs).
3. **Récap d'activité** — `GET /api/stats/activity` (7 derniers jours, par jour + par action) affiché en mini-graphe sur la page **Tarifs**.
4. **Page Tarifs** — `/pricing` (Free vs Pro), jauge d'usage + récap, CTA liste d'attente Pro ; liée depuis le header et la landing.

## ✅ Phase 4 — Feature-killer désabonnement (livrée)

La fonctionnalité qui définit la catégorie « nettoyeur de boîte » (Unroll.me, Clean Email, Leave Me Alone) :

1. **Détection des newsletters** — parsing des en-têtes `List-Unsubscribe` (RFC 2369) et `List-Unsubscribe-Post` (RFC 8058) à la volée dans `GET /api/emails` et au sync. Les champs `unsubUrl` / `unsubMailto` / `unsubOneClick` sont stockés sur l'email.
2. **Désabonnement 1-clic serveur** — `POST /api/unsubscribe` exécute le POST RFC 8058 (`List-Unsubscribe=One-Click`) **côté serveur** quand l'expéditeur le supporte : l'utilisateur ne quitte jamais l'app. Repli automatique : ouverture du lien https ou du `mailto:`.
3. **Vue « Abonnements »** — `GET /api/subscriptions` agrège les expéditeurs de type liste de diffusion (volume, dernier reçu, support 1-clic, déjà désabonné). Nouvel onglet dans le cockpit avec badge « 1-clic » et compteur d'emails.
4. **Désabonner + archiver** — un seul geste coupe la source **et** vide le backlog de l'expéditeur (`alsoArchive`). Désabonnements idempotents (index unique `{userId, senderEmail}`), action proposée aussi depuis le lecteur d'email.

## ✅ Phase 5 — Monétisation Stripe (livrée)

Pro = analyses illimitées, branché sur l'enforcement `402` déjà en place.

1. **Client Stripe sans dépendance** — `internal/billing` parle directement à l'API REST Stripe (création de Checkout Session + vérification de signature webhook HMAC-SHA256 avec tolérance anti-rejeu), dans le style « zéro dépendance lourde » du repo.
2. **Checkout** — `POST /api/billing/checkout` crée une session d'abonnement (mode `subscription`, `client_reference_id` = email) et renvoie l'URL hébergée ; le front redirige.
3. **Webhook** — `POST /api/billing/webhook` vérifie la signature puis synchronise le champ `plan` de l'utilisateur sur le cycle de vie de l'abonnement (`checkout.session.completed` → pro ; `customer.subscription.updated/deleted` → pro/free). Index sparse sur `stripeSubscriptionId`.
4. **Enforcement plan** — `quotaExceeded` et `GET /api/usage` renvoient `limit: -1` (illimité) pour Pro ; la page **Tarifs** détecte `plan`/`billingOn`, déclenche le Checkout, et gère le retour `?checkout=success|cancel`.
5. **Dégradé propre** — sans `STRIPE_SECRET_KEY`, l'endpoint répond `503` et l'UI bascule automatiquement sur la liste d'attente.

## ✅ Phase 6 — Durcissement production (livrée)

Trois axes pour passer d'un produit riche en features à un produit **prêt pour la production**.

1. **Authentification réelle (sécurité critique)** — fin de la confiance aveugle en `X-User-Email`. Le login émet désormais un **token de session signé (HMAC-SHA256, expirant)** ; un middleware le valide sur chaque route protégée et **réinjecte** lui-même l'identité (toute valeur `X-User-Email` envoyée par le client est supprimée). Le `state` OAuth est **signé et vérifié** (anti-CSRF, stateless), et le **token d'accès Gmail n'est plus exposé** au navigateur — seul le token de session l'est. Côté front : `Authorization: Bearer …`, redirection automatique sur `401`.
2. **Robustesse HTTP** — chaîne de middlewares : *recover* (un panic ne tue plus le process), *request-id* + journalisation structurée, *rate limiting* par client (token bucket en mémoire, sans dépendance). Le serveur applique des **timeouts** (read/write/idle) et un **arrêt gracieux** sur SIGTERM (drainage des requêtes en cours au redéploiement).
3. **Tests + CI** — première suite de **tests unitaires Go** (tokens de session/CSRF, chiffrement, clés de cache, matching de labels, parsing expéditeur, en-têtes de désabonnement, rate limiter) et un **workflow GitHub Actions** (`vet` + `build` + `test -race` backend, build frontend) déclenché sur push/PR.

## ✅ Phase 7 — Profondeur produit & fiabilité (livrée)

Trois axes d'amélioration majeurs : une feature qui élargit l'usage, une qui complète la monétisation, et un durcissement de la fiabilité.

1. **Moteur de règles déterministes (feature)** — une couche de tri **sans IA, gratuite et prévisible** qui s'exécute en amont du modèle. L'utilisateur définit des règles (conditions `contient`/`égal`/`commence par`/`finit par`/`regex` sur `from`/`subject`/`snippet`/`to`/`body`) → une action (`archive`/`trash`/`label`/`markRead`/`star`), priorisées (la première qui matche gagne). Endpoints CRUD `/api/rules` + `POST /api/rules/apply` (jusqu'à 200 emails, **ne consomme pas le quota**). Le matcher vit dans `internal/rules` (pur, sans I/O) et est couvert par une suite de tests exhaustive. Nouvelle page **Règles** dans le cockpit.
2. **Portail de facturation Stripe (feature)** — `POST /api/billing/portal` ouvre une session **Stripe Billing Portal** (`/v1/billing_portal/sessions`) : les abonnés Pro mettent à jour leur moyen de paiement, changent de plan ou **résilient en self-service**, sans quitter l'app. Bouton « Gérer mon abonnement » sur la page Tarifs.
3. **Fiabilité d'accès Gmail (amélioration)** — centralisation de l'obtention du client Gmail dans un unique helper `gmailClientFor` qui **rafraîchit et persiste** le token OAuth expiré. Corrige un bug latent : `SyncEmails` et `GetLabels` ne rafraîchissaient pas le token et échouaient une fois l'`access_token` périmé. ~100 lignes de duplication supprimées.

## ✅ Phase 8 — Automatisation, confiance & résilience (livrée)

Trois axes majeurs, dans la cadence du repo : une feature qui automatise, une qui rapproche la promesse « apprendre une fois », et un durcissement de la fiabilité.

1. **Règles en autopilote + Aperçu (feature)** — les règles déterministes s'appliquent désormais **automatiquement à chaque synchronisation** (réglage `autoApplyRules` par utilisateur, exposé via `GET/PUT /api/account/settings`, **OFF par défaut** pour ne jamais modifier Gmail à l'improviste). En complément, un **dry-run** `POST /api/rules/preview` projette ce que les règles *feraient* (par règle + échantillon d'emails) **sans rien modifier ni consommer de quota** — la confiance avant l'action irréversible. Le forecast réutilise exactement la logique de `ApplyRules` (`rules.Preview`, pure et testée). Câble le point « Application automatique des règles à la synchro » laissé en attente.
2. **« Apprendre une fois » en 1 clic (feature)** — depuis la vue *Expéditeurs*, un bouton transforme un expéditeur en **règle déterministe permanente** (`POST /api/senders/rule` → `from contains <adresse>` → action). La règle tourne ensuite gratuitement à chaque application/synchro. Concrétise la promesse phare du README (« Apprenez une fois, appliquez pour toujours ») en s'appuyant sur le moteur de règles existant.
3. **Résilience des appels IA (amélioration)** — le client Mistral ne faisait qu'un seul essai : un simple `429` faisait s'effondrer un batch d'analyse vers « keep ». Il **réessaie** désormais les erreurs transitoires (429, 5xx, erreurs réseau) avec **backoff exponentiel + jitter**, en honorant l'en-tête `Retry-After` et plafonné pour rester dans les timeouts serveur. Les 4xx (hors 429) échouent vite. Nombre d'essais configurable (`MISTRAL_MAX_RETRIES`, défaut 2). Couvert par des tests `httptest`.

## ✅ Phase 9 — Confiance, productivité & vérité des données (livrée)

Trois axes majeurs, dans la cadence du repo : une feature qui renforce la
confiance, une qui élargit l'usage, et un durcissement de l'observabilité.

1. **Liste de protection — expéditeurs VIP (feature, sécurité)** — un garde-fou
   par utilisateur : tant qu'un expéditeur (adresse complète **ou domaine
   entier**, sous-domaines compris) est protégé, **aucun passage automatisé** ne
   peut l'archiver, le mettre à la corbeille ou le supprimer — ni l'IA, ni les
   règles déterministes, ni l'auto-pilote par expéditeur, ni une action en
   masse. Les actions non destructives (libellé, favori, marquer lu) restent
   permises, et l'utilisateur garde la main en manuel. Concrètement : une
   suggestion IA destructrice est **rétrogradée en « garder »** dès sa
   génération ; les balayages en masse et l'autopilote **sautent** les protégés
   (avec un compteur `protectedSkipped`). La logique vit dans un package pur
   `internal/protect` (`Match`, `Allowed`, normalisation adresse/domaine) couvert
   par des tests exhaustifs. CRUD `/api/protected` + bouton « Protéger » dans le
   lecteur et gestion dans les Réglages. Concrétise la promesse « vos emails ne
   quittent jamais votre contrôle ».
2. **Reporter / Snooze (feature, productivité)** — la fonctionnalité reine après
   le désabonnement : sortez un email de la boîte d'un geste et faites-le
   **revenir tout seul**, marqué non lu, au moment choisi. Presets résolus
   serveur (« plus tard », « ce soir », « demain matin », « ce week-end »,
   « semaine prochaine ») via un package pur `internal/snooze` testé au cas par
   cas. L'email est archivé + étiqueté `Mailsorter/Reporté` ; un **balayeur de
   fond** (ticker 1 min) le ramène à échéance, résilient aux pannes par message.
   Endpoints `POST /api/emails/snooze`, `GET /api/snoozes`,
   `POST /api/snoozes/{id}/wake`. Nouvelle page **Reporté** + menu de report dans
   le lecteur.
3. **Journal d'actions & récap véridique (amélioration, observabilité)** — un
   **ledger append-only** (`action_log`) capture désormais *chaque* mutation
   Gmail avec sa **source** (`direct`, `rule`, `ai`, `ai-auto`, `bulk`, `snooze`,
   `unsubscribe`). `GET /api/stats/activity` se calcule sur ce ledger via un
   agrégateur pur `internal/activity` (testé), au lieu des seules suggestions IA
   appliquées : le récap 7 jours devient **complet et vrai** (toutes les sources,
   plus une ventilation `bySource`), peu importe ce que le client a observé.

## ✅ Phase 10 — Livraison, contrôle & observabilité (livrée)

Trois axes majeurs, dans la cadence du repo : une feature qui **tient une promesse
restée en attente**, une feature qui **donne plus de contrôle**, et un
**durcissement de l'observabilité**.

1. **Digest quotidien — réellement envoyé (feature).** Le contenu était déjà
   rendu (`internal/digest`) ; ce qui manquait, c'était l'**envoi**. Désormais :
   le scope **`gmail.send`** est demandé à la connexion, un package pur
   `internal/mailer` construit le message **MIME multipart (texte + HTML),
   base64url** prêt pour l'API Gmail (`BuildRaw`) et décide **purement** si un
   digest est dû (`DueAt` — au plus une fois par jour, à l'heure UTC choisie), et
   un **scheduler de fond** (`startDigestLoop`, ticker 15 min) envoie à chaque
   utilisateur opté-in son récap des 7 derniers jours. Résilient par
   utilisateur, idempotent (horodatage `digestLastSentAt`), et propre quand il
   n'y a rien à dire (semaine vide ⇒ pas d'email). Réglages par utilisateur
   (`digestEnabled`, `digestHourUTC`) exposés via `GET/PUT /api/account/settings`
   et pilotés depuis la page **Réglages**. `BuildRaw`/`DueAt` couverts par des
   tests. *Note : les comptes connectés avant cette phase doivent **reconnecter
   Gmail** pour accorder le scope d'envoi.*
2. **Règles — négation & conditions temporelles (feature).** Le moteur
   déterministe gagne quatre opérateurs : `notContains` / `notEquals` (cibler ce
   qui **n'**est **pas** là) et surtout `olderThan` / `newerThan` (un **âge en
   jours** comparé à la date de réception). « Archiver les newsletters de plus de
   30 jours » devient une règle gratuite et prévisible. Le matcher reste **pur** :
   `now` est injecté (`MatchesAt` / `FirstMatchAt` / `PreviewAt`), les entrées
   publiques historiques délèguent à `time.Now()` (zéro changement d'appelant), un
   email sans date ne matche jamais une règle temporelle, et la validation exige
   un nombre de jours ≥ 0. Nouveaux opérateurs exposés dans la page **Règles**
   (saisie numérique pour l'âge). Tests exhaustifs.
3. **Observabilité — `/health` profond + `/metrics` (amélioration).** Le health
   check **vérifie réellement MongoDB** (ping) et renvoie le **build** et
   l'**uptime** — `503` si le datastore est injoignable, pour qu'un orchestrateur
   sorte l'instance de la rotation. Un nouveau package pur `internal/metrics`
   (concurrence-safe, **cardinalité bornée** : compteurs par méthode et **classe**
   de statut, latence moyenne/max, uptime) est alimenté par un middleware sur le
   hot-path et exposé en `GET /metrics` (agrégats seuls, sans donnée
   utilisateur ⇒ scrappable sans auth). Le build est configurable via
   `BUILD_VERSION`. Registry et middleware testés.

## ✅ Phase 11 — Profondeur, confiance & robustesse (livrée)

Trois axes majeurs, dans la cadence du repo : une feature qui **approfondit** le
moteur de règles, une feature qui **renforce la confiance** (RGPD), et un
**durcissement de la fiabilité** réseau.

1. **Règles multi-actions (feature).** Une règle déterministe pouvait jusqu'ici
   appliquer **une seule** action ; le cas canonique « *étiqueter* **puis**
   *archiver* une newsletter » était impossible. Une règle porte désormais une
   **liste ordonnée d'actions** (`Actions []RuleAction`), exécutées dans l'ordre.
   Le cœur reste **pur et rétrocompatible** : `rules.EffectiveActions` présente
   les anciennes règles (champ `action`/`labelName`) et les règles 1-clic par
   expéditeur comme une liste à un élément, et le champ legacy est rempli avec
   l'action primaire (premier de la liste) pour les lecteurs historiques. La
   **protection VIP** se fait par action : un expéditeur protégé voit ses actions
   destructrices (archive/corbeille) sautées, mais les actions non destructrices
   (libellé/favori/lu) de la même règle s'appliquent quand même. Le dry-run
   `preview` remonte la liste complète. Page **Règles** : éditeur d'actions
   multiples (anti-doublon). Tests exhaustifs (`EffectiveActions`, validation
   multi-actions, preview).
2. **Export & suppression de compte — RGPD (feature, confiance).** Concrétise la
   promesse « vos emails ne quittent jamais votre contrôle » côté **droits sur
   les données**. `GET /api/account/export` renvoie **un seul JSON** avec un profil
   de compte **expurgé** (jamais les tokens OAuth ni les identifiants Stripe) et
   **chaque dataset appartenant à l'utilisateur** (règles, protections, reports,
   suggestions, préférences, libellés, désabonnements, usage, journal d'actions,
   jobs). `DELETE /api/account` **efface définitivement** le compte et tous ces
   datasets (la boîte Gmail n'est jamais touchée), et renvoie les compteurs de
   suppression. Un package pur `internal/account` tient le **catalogue unique**
   des données (`Datasets`) qui pilote **à la fois** l'export et la suppression —
   impossible d'exporter une donnée qu'on ne sait pas effacer, ou d'effacer une
   donnée qu'on n'a pas divulguée — plus la redaction `RedactUser`. Section
   **Données & confidentialité** dans les *Réglages* : export téléchargeable +
   *zone de danger* avec confirmation tapée. Tests sur le catalogue et la
   redaction.
3. **Résilience des appels Gmail (amélioration, fiabilité).** Symétrique du
   durcissement Mistral (Phase 8) : Gmail **rate-limite agressivement** (429 +
   `Retry-After`) et a des 5xx ponctuels, et jusqu'ici un seul 429 en plein sync
   pouvait avorter la lecture de la boîte ou perdre une application de règle.
   Chaque appel Gmail (list/get messages, modify, send, labels, profil, stats)
   passe désormais par un wrapper qui **réessaie** les erreurs transitoires
   (429, 5xx, erreurs réseau) avec **backoff exponentiel + jitter**, en honorant
   `Retry-After` et plafonné pour rester dans les timeouts serveur. Les 4xx (hors
   429) et les annulations de contexte échouent vite. Le classifieur et le calcul
   de backoff sont **purs** (`internal/gmail/retry.go`) et couverts par des tests.

## ✅ Phase 12 — Contrôle, automatisation & robustesse (livrée)

Trois axes majeurs, dans la cadence du repo : une feature qui **rend le contrôle**
à l'utilisateur, une feature qui **automatise** la promesse Inbox Zero, et un
**durcissement de la robustesse** des entrées HTTP.

1. **Journal d'actions & annulation (feature, confiance/contrôle).** Le ledger
   append-only (`action_log`) était déjà la source de vérité du récap ; il devient
   désormais une **histoire visible et actionnable**. `GET /api/activity/log`
   expose les dernières mutations (par page bornée, filtrables par **source** :
   règle, IA, en masse, report, désabonnement…), chacune marquée *réversible* ou
   non. `POST /api/activity/undo` **annule** une action automatisée destructrice en
   rejouant son inverse Gmail (archive→désarchive, corbeille→restaure,
   lu→non lu), marque l'entrée *annulée* et **journalise la réversion** (source
   `undo`) pour que l'audit reste vrai. La table d'inversion vit dans le package
   pur `internal/activity` (`Inverse`, testée) — impossible de proposer une
   annulation qu'on ne sait pas exécuter. Nouvelle page **Historique** dans le
   cockpit. Concrétise « vos emails ne quittent jamais votre contrôle » côté
   *réversibilité*.
2. **Auto-sync en arrière-plan (feature, automatisation).** Jusqu'ici l'autopilote
   des règles ne tournait qu'à la **synchro manuelle** ; un nouveau **scheduler de
   fond** (ticker, dans la lignée des boucles snooze/digest) synchronise
   périodiquement la boîte des utilisateurs opté-in **sans aucun clic** et, si
   l'application automatique des règles est active, trie leurs nouveaux emails tout
   seul — le chemin **mains-libres** vers l'Inbox Zero. La logique de synchro est
   factorisée (`syncInbox`) et partagée par le handler et la boucle (comportement
   identique). La cadence par utilisateur est une décision **pure** (package
   `internal/schedule`, `Due`, testée) : un intervalle minimum est honoré, et
   chaque utilisateur est *stampé avant* la tentative pour éviter les tempêtes de
   reprise. Réglage `autoSyncEnabled` (**OFF par défaut**) exposé via
   `GET/PUT /api/account/settings` et piloté depuis les **Réglages**.
3. **Robustesse des entrées HTTP (amélioration, sécurité).** Tous les corps de
   requête JSON sont désormais **bornés** (`http.MaxBytesReader`, plafond 1 Mio) via
   un helper unique `decodeJSON` — un payload géant est rejeté en `413` sans être
   chargé en mémoire (vecteur d'épuisement de ressources fermé) — et les erreurs
   passent par une **enveloppe JSON cohérente** (`{"error":…,"status":n}`,
   `writeError`) au lieu de texte brut, parsable uniformément côté client. Au
   passage, `PUT /api/account/settings` devient un **merge partiel** (champs en
   pointeurs) : la bascule *autopilote des règles* (page Règles) et la carte
   *digest* (Réglages) ne **s'écrasent plus mutuellement** — un bug latent où
   enregistrer un réglage réinitialisait silencieusement les autres. Helpers
   couverts par des tests (`httptest`).

### Reste à brancher (dépend d'infra externe)

- **Multi-comptes Gmail** — nécessite un modèle `account` lié au `user` (refactor du `X-User-Email`).
- **Analytics produit** — instrumenter le funnel (connexion → 1ʳᵉ analyse → 1ʳᵉ application) vers PostHog/Segment.

---

## 🚢 Checklist de déploiement (aujourd'hui)

- [ ] `cp .env.example .env` puis renseigner `ENCRYPTION_KEY` (32+ car.) et `MISTRAL_API_KEY`.
- [ ] OAuth Google : ajouter l'URI de prod aux *Authorized redirect URIs*.
- [ ] CORS backend : domaine de prod présent dans `routes.go` (`AllowedOrigins`).
- [ ] (Optionnel) Stripe : `STRIPE_SECRET_KEY`, `STRIPE_PRICE_ID` (Price récurrent), `STRIPE_WEBHOOK_SECRET`, `APP_BASE_URL` ; webhook → `{backend}/api/billing/webhook` (events `checkout.session.completed`, `customer.subscription.updated`, `customer.subscription.deleted`).
- [ ] `docker compose build && docker compose up -d`.
- [ ] Vérifier `GET /health` → `{"status":"ok"}`.
- [ ] Smoke test : connexion → *Trier ma boîte* → *Tout appliquer* → *Désabonnements* → (si Stripe) *Passer à Pro*.
