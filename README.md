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
| `POST`  | `/api/ai/analyze`         | Génère des suggestions (synchrone, cache+batch) |
| `POST`  | `/api/ai/analyze-async`   | **Lance un job d'analyse** (worker, non bloquant) |
| `GET`   | `/api/ai/jobs/{id}`       | Statut/progression d'un job d'analyse         |
| `POST`  | `/api/ai/apply`           | Applique une suggestion                       |
| `POST`  | `/api/ai/apply-batch`     | **Applique N suggestions en une requête**     |
| `POST`  | `/api/ai/analyze-sender`  | Apprend une préférence par expéditeur         |
| `GET`   | `/api/stats`              | Statistiques de la boîte                      |

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
