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

## 🔜 Phase 3 — Go-to-market (3–5 jours)

1. **Onboarding guidé** — première analyse pré-remplie + tooltip de bienvenue.
2. **Digest quotidien** — email récap « 142 emails triés cette semaine ».
3. **Multi-comptes Gmail** par utilisateur.
4. **Billing** — quota gratuit (ex. 200 emails/mois) + offre Pro illimitée.
5. **Analytics produit** — funnel connexion → 1ʳᵉ analyse → 1ʳᵉ application.

---

## 🚢 Checklist de déploiement (aujourd'hui)

- [ ] `cp .env.example .env` puis renseigner `ENCRYPTION_KEY` (32+ car.) et `MISTRAL_API_KEY`.
- [ ] OAuth Google : ajouter l'URI de prod aux *Authorized redirect URIs*.
- [ ] CORS backend : domaine de prod présent dans `routes.go` (`AllowedOrigins`).
- [ ] `docker compose build && docker compose up -d`.
- [ ] Vérifier `GET /health` → `{"status":"ok"}`.
- [ ] Smoke test : connexion → *Trier ma boîte* → *Tout appliquer*.
