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

### Reste à brancher (dépend d'infra externe)

- **Digest quotidien par email** — la donnée existe (`/api/stats/activity`) ; il manque un scope `gmail.send` (ou SMTP) + un scheduler (cron/worker) pour l'envoi réel.
- **Portail de gestion d'abonnement** — brancher le Stripe Billing Portal (`/v1/billing_portal/sessions`) pour la résiliation self-service depuis l'app.
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
