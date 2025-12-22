# Mailsorter

Application dockerisée pour trier automatiquement les emails Gmail avec une interface React, un backend Go et une base de données MongoDB.

## Architecture

L'application est composée de 3 services Docker distincts :

1. **Frontend** (React) - Interface utilisateur sur le port 3000
2. **Backend** (Go) - API REST sur le port 8080
3. **MongoDB** - Base de données sur le port 27017

## Prérequis

- Docker et Docker Compose installés
- Un compte Google Cloud Platform avec l'API Gmail activée
- Client ID et Client Secret OAuth 2.0 de Google

## Configuration de l'API Gmail

1. Allez sur [Google Cloud Console](https://console.cloud.google.com/)
2. Créez un nouveau projet ou sélectionnez un projet existant
3. Activez l'API Gmail
4. Créez des identifiants OAuth 2.0 :
   - Type d'application : Application Web
   - URI de redirection autorisés : `http://localhost:3000/auth/callback`
5. Notez le Client ID et le Client Secret

## Installation

1. Clonez le repository :
```bash
git clone https://github.com/nohe-sohbi/Mailsorter.git
cd Mailsorter
```

2. Copiez le fichier d'exemple d'environnement et configurez-le :
```bash
cp .env.example .env
```

3. Éditez le fichier `.env` et ajoutez vos identifiants Gmail API :
```env
GMAIL_CLIENT_ID=votre_client_id
GMAIL_CLIENT_SECRET=votre_client_secret
```

## Démarrage de l'application

### Avec Docker Compose (Production)

Lancez tous les services avec Docker Compose :

```bash
docker compose up -d
```

L'application sera accessible à :
- Frontend : http://localhost:3000
- Backend API : http://localhost:8080
- MongoDB : localhost:27017

### Avec Make

Si vous avez Make installé, vous pouvez utiliser les commandes suivantes :

```bash
make build    # Construire les images Docker
make up       # Démarrer tous les services
make down     # Arrêter tous les services
make logs     # Voir les logs
make clean    # Nettoyer tous les conteneurs et volumes
```

### Développement Local (sans Docker)

Pour le développement local, vous pouvez exécuter chaque service séparément :

#### Backend
```bash
cd backend
go run cmd/server/main.go
```

#### Frontend
```bash
cd frontend
npm install
npm start
```

#### MongoDB
```bash
docker run -d -p 27017:27017 \
  -e MONGO_INITDB_ROOT_USERNAME=admin \
  -e MONGO_INITDB_ROOT_PASSWORD=password \
  mongo:7.0
```

Ou utilisez le script de développement automatique :
```bash
./dev-start.sh
```

## Utilisation

1. Ouvrez votre navigateur à http://localhost:3000
2. Cliquez sur "Se connecter avec Gmail"
3. Autorisez l'application à accéder à vos emails Gmail
4. Une fois connecté, vous pourrez :
   - Voir vos emails
   - Synchroniser vos emails
   - Créer des règles de tri automatique
   - Gérer vos libellés

## Fonctionnalités

### Gestion des emails
- Affichage des emails de la boîte de réception
- Synchronisation avec Gmail
- Recherche avec les requêtes Gmail (ex: `from:example@gmail.com`)
- Affichage des libellés

### Règles de tri
- Créer des règles basées sur des conditions (expéditeur, destinataire, objet, corps)
- Actions possibles : ajouter/retirer des libellés, marquer comme lu, archiver
- Priorités des règles
- Activer/désactiver les règles

## Structure du projet

```
Mailsorter/
├── backend/              # API Go
│   ├── cmd/
│   │   └── server/       # Point d'entrée de l'application
│   ├── internal/
│   │   ├── api/          # Handlers et routes
│   │   ├── config/       # Configuration
│   │   ├── database/     # Connexion MongoDB
│   │   ├── gmail/        # Service Gmail API
│   │   └── models/       # Modèles de données
│   └── Dockerfile
├── frontend/             # Application React
│   ├── public/
│   ├── src/
│   │   ├── components/   # Composants React
│   │   ├── pages/        # Pages
│   │   ├── services/     # Services API
│   │   └── styles/       # CSS
│   └── Dockerfile
├── mongo-init/           # Scripts d'initialisation MongoDB
├── docker-compose.yml    # Orchestration des services
└── .env.example          # Exemple de configuration

```

## Arrêt de l'application

### Avec Docker Compose
```bash
docker compose down
```

Pour supprimer également les volumes (données) :
```bash
docker compose down -v
```

### Avec Make
```bash
make down    # Arrêter les services
make clean   # Arrêter et nettoyer complètement
```

## Développement

### Backend (Go)

```bash
cd backend
go run cmd/server/main.go
```

### Frontend (React)

```bash
cd frontend
npm install
npm start
```

## Technologies utilisées

- **Frontend** : React 18, React Router, Axios
- **Backend** : Go 1.21, Gorilla Mux, OAuth2, Gmail API
- **Database** : MongoDB 7.0
- **Containerization** : Docker, Docker Compose

## Licence

MIT
