# Guide de Configuration Gmail API

Ce guide explique comment configurer l'API Gmail pour utiliser Mailsorter.

## Étape 1 : Créer un projet Google Cloud

1. Allez sur [Google Cloud Console](https://console.cloud.google.com/)
2. Cliquez sur **Sélectionner un projet** en haut de la page
3. Cliquez sur **Nouveau projet**
4. Donnez un nom à votre projet (ex: "Mailsorter")
5. Cliquez sur **Créer**

## Étape 2 : Activer l'API Gmail

1. Dans le menu de navigation, allez à **APIs et services** > **Bibliothèque**
2. Recherchez "Gmail API"
3. Cliquez sur **Gmail API**
4. Cliquez sur **Activer**

## Étape 3 : Configurer l'écran de consentement OAuth

1. Allez à **APIs et services** > **Écran de consentement OAuth**
2. Sélectionnez **Externe** (pour tester avec votre propre compte)
3. Cliquez sur **Créer**

4. Remplissez les informations requises :
   - **Nom de l'application** : Mailsorter
   - **Adresse e-mail de l'assistance** : votre email
   - **Adresse e-mail du développeur** : votre email
   
5. Cliquez sur **Enregistrer et continuer**

6. **Champs d'application** : Cliquez sur **Ajouter ou supprimer des champs**
   - Ajoutez ces scopes :
     - `.../auth/gmail.readonly`
     - `.../auth/gmail.modify`
     - `.../auth/gmail.labels`
   
7. Cliquez sur **Mettre à jour** puis **Enregistrer et continuer**

8. **Utilisateurs test** :
   - Ajoutez votre adresse Gmail comme utilisateur test
   - Cliquez sur **Enregistrer et continuer**

9. Cliquez sur **Retour au tableau de bord**

## Étape 4 : Créer les identifiants OAuth 2.0

1. Allez à **APIs et services** > **Identifiants**
2. Cliquez sur **Créer des identifiants** > **ID client OAuth**
3. Sélectionnez le type d'application : **Application Web**

4. Configurez l'application :
   - **Nom** : Mailsorter Web Client
   
   - **URI de redirection autorisés** : Ajoutez ces URIs :
     - `http://localhost:3000`
     - `http://localhost:3000/auth/callback`
   
   - **Origines JavaScript autorisées** :
     - `http://localhost:3000`

5. Cliquez sur **Créer**

6. Une fenêtre s'affiche avec :
   - **ID client** (Client ID)
   - **Code secret du client** (Client Secret)
   
   ⚠️ **IMPORTANT** : Copiez ces informations, vous en aurez besoin !

## Étape 5 : Configurer Mailsorter

1. Ouvrez le fichier `.env` à la racine du projet

2. Remplacez les valeurs par vos identifiants :

```env
GMAIL_CLIENT_ID=votre_client_id_ici.apps.googleusercontent.com
GMAIL_CLIENT_SECRET=votre_client_secret_ici
GMAIL_REDIRECT_URL=http://localhost:3000/auth/callback
```

## Étape 6 : Tester la connexion

1. Démarrez l'application :
```bash
docker compose up -d
# ou
./dev-start.sh
```

2. Ouvrez http://localhost:3000

3. Cliquez sur **Se connecter avec Gmail**

4. Vous devriez être redirigé vers Google pour autoriser l'application

5. Une fois autorisé, vous serez redirigé vers la page des emails

## Dépannage

### Erreur "redirect_uri_mismatch"

- Vérifiez que l'URL de redirection dans `.env` correspond exactement à celle configurée dans Google Cloud Console
- Les URIs doivent inclure le protocole (`http://`)
- Pas de slash à la fin

### Erreur "access_denied"

- Vérifiez que votre compte Gmail est ajouté comme utilisateur test
- Vérifiez que l'application n'est pas en mode production sans vérification

### Erreur "invalid_client"

- Vérifiez que le Client ID et le Client Secret sont corrects
- Pas d'espaces avant ou après les valeurs dans le fichier `.env`

### L'application ne démarre pas

- Vérifiez que MongoDB est bien démarré
- Vérifiez que les ports 3000, 8080 et 27017 ne sont pas déjà utilisés
- Consultez les logs : `docker compose logs` ou `make logs`

## Scopes utilisés

Mailsorter utilise les scopes suivants :

- **gmail.readonly** : Lire les emails et les métadonnées
- **gmail.modify** : Modifier les libellés et l'état des emails
- **gmail.labels** : Créer et gérer les libellés

Ces scopes sont nécessaires pour :
- Lire vos emails
- Appliquer des règles de tri
- Créer et assigner des libellés
- Marquer les emails comme lus
- Archiver les emails

## Limites de l'API Gmail

Google impose des limites d'utilisation :

- **250 quota units/user/second**
- **1 milliard quota units/day** (projet)

Actions et leur coût :
- Liste de messages : 5 units
- Obtenir un message : 5 units
- Modifier un message : 5 units

Pour une utilisation normale, ces limites sont largement suffisantes.

## Passer en production

Pour publier votre application :

1. Complétez toutes les informations dans l'écran de consentement
2. Soumettez l'application pour vérification Google
3. Attendez l'approbation (peut prendre plusieurs semaines)
4. Mettez à jour les URI de redirection avec votre domaine

Pour un usage personnel uniquement, l'application peut rester en mode "Test".
