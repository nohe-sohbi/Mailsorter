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

## 🔜 Phase 2 — Échelle (2–4 jours)

1. **Analyse asynchrone** — file de jobs (worker Go) pour analyser 100+ emails sans bloquer l'UI ; statut en temps réel.
2. **Cache d'analyses** — ne pas réanalyser un email/expéditeur déjà vu (clé : `from`+`subject` hash).
3. **Batching Mistral** — un appel pour N emails au lieu de N appels (coût ÷ 5, latence ÷ 3).
4. **Index MongoDB** — `{userId, status}` sur suggestions, `{userId, from}` sur emails.

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
