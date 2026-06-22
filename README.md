<div align="center">

# 📬 Mailsorter

### Inbox Zero, propulsé par l'IA.

**Mailsorter lit, comprend et range vos emails Gmail à votre place.**
Stop au scroll infini — atteignez l'Inbox Zero en quelques clics, et gardez-la propre pour toujours.

`React + Tailwind` · `Go` · `MongoDB` · `Mistral AI`

</div>

---

## ⚡ Pourquoi Mailsorter

Votre boîte mail déborde. Les newsletters s'empilent, les confirmations de colis noient les messages importants, le spam passe entre les mailles. Le tri manuel prend des heures — et recommence chaque semaine.

Mailsorter automatise tout ça :

- **🧠 Tri par IA en un clic** — L'IA analyse chaque email (expéditeur, sujet, contenu) et propose une action : *archiver*, *supprimer*, *libellé* ou *garder*, avec un score de confiance.
- **⚙️ Règles de tri déterministes** — Encodez vos cas évidents une fois (un expéditeur, un sujet récurrent) : les règles s'appliquent **instantanément, gratuitement et sans consommer votre quota IA**. Conditions (contient / **ne contient pas** / égal / **différent** / commence / finit / regex / **plus vieux que** / **plus récent que** *N* jours) → action (archiver, supprimer, étiqueter, lire, favori). **Autopilote** : appliquez-les automatiquement à chaque synchro, et **prévisualisez** leur effet (dry-run) avant tout changement.
- **📰 Digest quotidien par email** — Un récap de votre tri des 7 derniers jours, envoyé **chaque jour dans votre boîte** à l'heure (UTC) que vous choisissez. Activez-le en un clic depuis les *Réglages*.
- **👤 Apprendre une fois, en 1 clic** — Depuis la vue *Expéditeurs*, transformez n'importe quel expéditeur en **règle permanente** : ses futurs emails sont rangés tout seuls, pour toujours.
- **🔕 Désabonnement en 1 clic** — Mailsorter détecte les newsletters via les en-têtes `List-Unsubscribe` (RFC 8058) et vous désabonne **sans quitter l'app** — puis archive tout le backlog de l'expéditeur d'un geste.
- **🛡️ Expéditeurs protégés (VIP)** — Marquez une adresse ou un domaine entier comme **protégé** : ses emails ne seront **jamais** archivés ni supprimés automatiquement (ni par l'IA, ni par les règles, ni en masse). Le filet de sécurité de l'Inbox Zero.
- **⏰ Reporter (snooze)** — Sortez un email de la boîte d'un geste ; il **revient tout seul**, marqué non lu, au moment que vous choisissez (ce soir, demain, ce week-end…).
- **⚡ Auto-pilote « Tout appliquer »** — Validez des dizaines de suggestions d'un seul geste, en une requête serveur optimisée.
- **👥 Règles par expéditeur** — Apprenez une fois, appliquez pour toujours. Archivez ou supprimez en masse tous les emails d'un expéditeur.
- **🏷️ Libellés intelligents** — Des étiquettes précises et cohérentes, créées et appliquées automatiquement dans votre Gmail.
- **🔒 Zéro mot de passe stocké** — OAuth Google natif. Le secret API est chiffré au repos. Vos emails ne quittent jamais votre contrôle.

---

## 🏗️ Architecture

```
┌──────────────┐      ┌──────────────┐      ┌──────────────┐
│   Frontend   │─────▶│   Backend    │─────▶│   MongoDB    │
│ React+Tailwind│      │   Go API     │      │  Persistence │
│   (nginx)    │◀─────│  REST + IA   │◀─────│              │
└──────────────┘      └──────┬───────┘      └──────────────┘
                             │
                ┌────────────┴────────────┐
                ▼                         ▼
          ┌───────────┐            ┌───────────┐
          │ Gmail API │            │ Mistral AI│
          └───────────┘            └───────────┘
```

| Service  | Stack                          | Rôle                                   |
| -------- | ------------------------------ | -------------------------------------- |
| Frontend | React 18, Tailwind CSS, Axios  | Cockpit de tri, design system maison   |
| Backend  | Go 1.21+, Gorilla Mux, OAuth2  | API REST, orchestration IA, Gmail      |
| Database | MongoDB 7.0                    | Users, suggestions, préférences        |
| IA       | Mistral AI                     | Analyse et classification des emails   |

---

## 🚀 Démarrage rapide

### 1. Prérequis

- Docker & Docker Compose
- Identifiants **OAuth 2.0** Google (API Gmail activée)
- Une clé **Mistral AI** ([console.mistral.ai](https://console.mistral.ai/))

### 2. Configuration

```bash
git clone https://github.com/nohe-sohbi/Mailsorter.git
cd Mailsorter
cp .env.example .env
```

Éditez `.env` :

```env
ENCRYPTION_KEY=une-chaine-aleatoire-de-32-caracteres-minimum
MISTRAL_API_KEY=votre_cle_mistral
MISTRAL_MODEL=mistral-large-2411
```

> Les identifiants Gmail peuvent être renseignés **dans `.env`** ou directement via la **page Setup** de l'application (chiffrés en base).

### 3. Lancement

```bash
docker compose up -d        # ou : make up
```

| Surface       | URL                     |
| ------------- | ----------------------- |
| App           | http://localhost:3000   |
| API           | http://localhost:8080   |
| Health check  | http://localhost:8080/health |

---

## 🧭 Le flow utilisateur

1. **Connectez Gmail** — un clic, OAuth Google sécurisé.
2. **Lancez « Trier ma boîte »** — l'IA analyse vos emails et empile ses suggestions.
3. **Validez** — *Tout appliquer* pour l'auto-pilote, ou tranchez au cas par cas. Filtrez sur *haute confiance* pour aller encore plus vite.
4. **Industrialisez** — passez en vue *Expéditeurs* pour archiver/supprimer en masse et mémoriser vos préférences.

---

## 🔌 API (extrait)

| Méthode | Endpoint                  | Description                                   |
| ------- | ------------------------- | --------------------------------------------- |
| `GET`   | `/api/emails`             | Liste paginée de la boîte                     |
| `POST`  | `/api/emails/action`      | Action directe (archive/trash/read) sur 1 msg |
| `POST`  | `/api/emails/snooze`      | **Reporter** un email (preset ou date) → revient tout seul |
| `GET`   | `/api/snoozes`            | Emails reportés (programmés)                   |
| `POST`  | `/api/snoozes/{id}/wake`  | **Réactiver** un email reporté maintenant      |
| `GET`   | `/api/protected`          | **Expéditeurs protégés** (VIP)                 |
| `POST`  | `/api/protected`          | Protège une adresse ou un domaine entier       |
| `DELETE`| `/api/protected/{id}`     | Retire une protection                          |
| `POST`  | `/api/ai/analyze`         | Génère des suggestions (synchrone, cache+batch) |
| `POST`  | `/api/ai/analyze-async`   | **Lance un job d'analyse** (worker, non bloquant) |
| `GET`   | `/api/ai/jobs/{id}`       | Statut/progression d'un job d'analyse         |
| `POST`  | `/api/ai/apply`           | Applique une suggestion                       |
| `POST`  | `/api/ai/apply-batch`     | **Applique N suggestions en une requête**     |
| `POST`  | `/api/ai/analyze-sender`  | Apprend une préférence par expéditeur         |
| `GET`   | `/api/subscriptions`      | **Newsletters détectées** (agrégées par expéditeur) |
| `POST`  | `/api/unsubscribe`        | **Désabonnement 1-clic** (+ archivage optionnel) |
| `GET`   | `/api/stats`              | Statistiques de la boîte                      |
| `GET`   | `/api/stats/activity`     | Récap d'activité (7 j, par jour/action/**source**, depuis le journal d'actions) |
| `GET`   | `/api/usage`              | Quota mensuel + plan (free/pro)               |
| `GET`   | `/api/account/settings`   | Réglages du compte (ex. autopilote des règles) |
| `PUT`   | `/api/account/settings`   | Met à jour les réglages (`autoApplyRules`)    |
| `GET`   | `/api/rules`              | **Règles de tri** (liste, triées par priorité) |
| `POST`  | `/api/rules`              | Crée une règle déterministe (validée serveur) |
| `POST`  | `/api/rules/apply`        | **Applique les règles** sur la boîte (sans IA, sans quota) |
| `POST`  | `/api/rules/preview`      | **Aperçu (dry-run)** : ce que feraient les règles, sans rien modifier |
| `PUT`   | `/api/rules/{id}`         | Met à jour une règle                          |
| `DELETE`| `/api/rules/{id}`         | Supprime une règle                            |
| `POST`  | `/api/senders/rule`       | **Crée une règle en 1 clic** depuis un expéditeur |
| `POST`  | `/api/billing/checkout`   | **Stripe Checkout** (passage à Pro = illimité) |
| `POST`  | `/api/billing/portal`     | **Portail Stripe** (gérer/résilier en self-service) |
| `POST`  | `/api/billing/webhook`    | Webhook Stripe (signature vérifiée, sync plan) |
| `GET`   | `/health`                 | Liveness/readiness : **ping MongoDB**, build, uptime (`503` si DB KO) |
| `GET`   | `/metrics`                | **Métriques d'exploitation** (req. par méthode/classe de statut, latence, uptime) |

Documentation complète : [`docs/API.md`](docs/API.md) · Architecture : [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) · Roadmap : [`docs/ROADMAP.md`](docs/ROADMAP.md)

---

## 🛠️ Développement local

**Backend**
```bash
cd backend && go run cmd/server/main.go
```

**Frontend**
```bash
cd frontend && npm install && npm start
```

**Build de production (frontend)**
```bash
cd frontend && npm run build
```

---

## 📄 Licence

MIT
