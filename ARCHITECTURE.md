# Architecture Mailsorter

## Vue d'ensemble

```
┌─────────────────────────────────────────────────────────────────┐
│                         Utilisateur                              │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ HTTP
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                     Frontend (React)                             │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  - Interface utilisateur                                   │ │
│  │  - Authentification Gmail                                  │ │
│  │  - Gestion des emails                                      │ │
│  │  - Configuration des règles de tri                        │ │
│  └────────────────────────────────────────────────────────────┘ │
│                      Port: 3000 (dev) / 80 (prod)               │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ REST API
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                      Backend (Go)                                │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  API Endpoints:                                            │ │
│  │  - GET  /api/auth/url          (Get OAuth URL)            │ │
│  │  - GET  /api/auth/callback     (Handle OAuth)             │ │
│  │  - GET  /api/emails            (List emails)              │ │
│  │  - POST /api/emails/sync       (Sync from Gmail)          │ │
│  │  - GET  /api/rules             (List rules)               │ │
│  │  - POST /api/rules             (Create rule)              │ │
│  │  - PUT  /api/rules/:id         (Update rule)              │ │
│  │  - DELETE /api/rules/:id       (Delete rule)              │ │
│  │  - GET  /api/labels            (List labels)              │ │
│  └────────────────────────────────────────────────────────────┘ │
│                          Port: 8080                              │
└─────────────────────────────────────────────────────────────────┘
                    │                        │
                    │                        │
         ┌──────────▼──────────┐   ┌────────▼─────────┐
         │   MongoDB           │   │  Gmail API       │
         │                     │   │                  │
         │  Collections:       │   │  - OAuth 2.0     │
         │  - users            │   │  - Messages      │
         │  - emails           │   │  - Labels        │
         │  - sorting_rules    │   │  - Threads       │
         │  - labels           │   │                  │
         │  Port: 27017        │   │  Google Cloud    │
         └─────────────────────┘   └──────────────────┘
```

## Composants

### 1. Frontend (React)

**Technologies:**
- React 18
- React Router DOM (navigation)
- Axios (API client)
- CSS moderne

**Pages:**
- `/` - Page de connexion
- `/emails` - Liste des emails
- `/rules` - Gestion des règles de tri

**Composants:**
- `EmailList` - Affichage de la liste d'emails
- `RuleForm` - Formulaire de création/édition de règle
- `RuleList` - Affichage de la liste des règles

### 2. Backend (Go)

**Technologies:**
- Go 1.21
- Gorilla Mux (routing)
- MongoDB Driver
- Google OAuth2 & Gmail API
- CORS middleware

**Structure:**
```
backend/
├── cmd/server/          # Point d'entrée
├── internal/
│   ├── api/            # Handlers et routes HTTP
│   ├── config/         # Configuration
│   ├── database/       # Client MongoDB
│   ├── gmail/          # Service Gmail API
│   └── models/         # Structures de données
└── pkg/                # Packages publics (vide pour l'instant)
```

**Modèles de données:**
- `User` - Utilisateur avec tokens OAuth
- `Email` - Email synchronisé depuis Gmail
- `SortingRule` - Règle de tri automatique
- `Label` - Libellé Gmail

### 3. MongoDB

**Collections:**
- `users` - Stockage des utilisateurs et leurs tokens
- `emails` - Cache des emails Gmail
- `sorting_rules` - Règles de tri définies par l'utilisateur
- `labels` - Libellés Gmail synchronisés

**Index:**
- `emails.messageId` - Unique
- `emails.userId + receivedDate` - Performance
- `sorting_rules.userId + priority` - Tri des règles
- `users.email` - Unique

## Flux d'authentification

```
1. Utilisateur clique sur "Se connecter avec Gmail"
   ↓
2. Frontend demande l'URL d'autorisation au Backend
   GET /api/auth/url
   ↓
3. Backend génère l'URL OAuth Google et la retourne
   ↓
4. Frontend redirige l'utilisateur vers Google
   ↓
5. Utilisateur autorise l'application
   ↓
6. Google redirige vers Frontend avec le code
   callback?code=XXXX
   ↓
7. Frontend envoie le code au Backend
   GET /api/auth/callback?code=XXXX
   ↓
8. Backend échange le code contre des tokens
   ↓
9. Backend sauvegarde les tokens dans MongoDB
   ↓
10. Backend retourne les informations utilisateur
    ↓
11. Frontend stocke l'email et redirige vers /emails
```

## Flux de synchronisation des emails

```
1. Utilisateur clique sur "Synchroniser"
   ↓
2. Frontend envoie la requête
   POST /api/emails/sync
   Header: X-User-Email: user@gmail.com
   ↓
3. Backend récupère les tokens de l'utilisateur
   ↓
4. Backend vérifie et rafraîchit les tokens si nécessaire
   ↓
5. Backend appelle Gmail API pour lister les messages
   ↓
6. Pour chaque message, Backend :
   - Récupère les détails complets
   - Parse les headers
   - Extrait le corps
   - Sauvegarde dans MongoDB (upsert)
   ↓
7. Backend retourne le nombre d'emails synchronisés
   ↓
8. Frontend affiche le résultat et rafraîchit la liste
```

## Flux d'application des règles

```
1. Utilisateur crée une règle de tri
   - Conditions: from contains "example.com"
   - Actions: addLabel "Work"
   ↓
2. Lors de la synchronisation, Backend :
   - Récupère toutes les règles actives de l'utilisateur
   - Trie par priorité
   - Pour chaque email :
     a. Évalue toutes les conditions
     b. Si toutes les conditions sont vraies :
        - Applique toutes les actions
        - Met à jour Gmail via API
        - Met à jour MongoDB
```

## Sécurité

- **Authentification:** OAuth 2.0 avec Google
- **Tokens:** Stockés en base de données, jamais exposés au frontend
- **API:** Header `X-User-Email` pour identifier l'utilisateur
- **CORS:** Configuré pour autoriser uniquement localhost en dev
- **Secrets:** Stockés dans variables d'environnement

## Performance

- **Cache:** Emails stockés dans MongoDB pour éviter les appels répétés à Gmail
- **Indexes:** Index MongoDB pour des requêtes rapides
- **Pagination:** Support de la pagination pour grandes listes
- **Async:** Synchronisation asynchrone des emails

## Extensibilité

L'architecture permet facilement d'ajouter :
- Support d'autres services email (Outlook, etc.)
- Intelligence artificielle pour le tri automatique
- Notifications en temps réel (WebSocket)
- Analytics et statistiques
- Export de données
- Règles plus complexes avec conditions multiples
