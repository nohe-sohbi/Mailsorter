# Landing "Grand Hôtel" et thème branchable : design

Date : 2026-09-24. Statut : validé en brainstorming, à relire avant le plan d'implémentation.

Maquettes de référence (locales, non versionnées) :
`.superpowers/brainstorm/46865-1790278425/content/landing-v3.html` (la landing validée)
et `.superpowers/brainstorm/46865-1790278425/content/moodboard.html` (le système visuel validé).
Elles font foi pour le rendu et les interactions. Pour les textes, cette spec prime : trois
phrases y ont été corrigées après vérification dans le code (section 10).

## 1. Objectif

Refaire la landing de zéro avec un univers visuel propre, loin des codes classiques de
landing page, et poser un système visuel réutilisable ensuite par le dashboard. Le thème
est volontairement très marqué : il doit pouvoir être débranché et rebranché sans rebuild,
l'ancienne landing restant intacte.

## 2. Décisions prises

| Sujet | Décision |
|---|---|
| Audience | Grand public d'abord. Objectif : créer un compte. La technique sert d'argument de confiance |
| Direction artistique | "Grand Hôtel" : hôtel en coupe symétrique façon maison de poupée, dessiné en SVG. Rose poudré, bordeaux, moutarde, canard, crème, encre |
| Ton | Franc, avec de l'humour. L'humour vient de la galère de la boîte mail, jamais du décor. L'hôtel se voit, il ne parle pas |
| Architecture du style | Tokens partagés dans un seul fichier, lus par la landing, la moodboard et plus tard le dashboard (option A) |
| Interrupteur | Variable d'environnement `UI_THEME` lue par le backend, renvoyée par `/api/config/status`. Pas de rebuild pour basculer |
| Ancienne landing | Conservée telle quelle (`pages/Login.js`), servie quand `UI_THEME=classic` |
| Assets | Tout en SVG dessiné à la main. Pas d'image générée (animations, mode nuit, poids, texte net) |

## 3. Périmètre

Dans le périmètre :

1. L'interrupteur de thème (backend et frontend), avec prévisualisation par URL.
2. Le fichier de tokens `frontend/src/styles/hotel.css` (jour et nuit), confiné au thème.
3. La nouvelle landing "Grand Hôtel" sur `/`, en jour et en nuit, du mobile au grand écran.
4. La moodboard `docs/design/moodboard.html`, branchée sur le même fichier de tokens.
5. L'extraction de la logique d'inscription et de connexion dans un hook partagé par les deux landings.

Hors périmètre : la migration du dashboard (projet suivant, qui réutilisera l'interrupteur),
la nouvelle image de partage social, la suppression de l'ancienne landing.

## 4. L'interrupteur de thème

### Backend

- `internal/config` : type `UITheme` avec les constantes `UIThemeHotel = "hotel"` et
  `UIThemeClassic = "classic"`. Variable `UI_THEME`, normalisée (minuscules, espaces retirés),
  valeur par défaut `hotel`. `Validate()` refuse toute autre valeur au démarrage, sur le
  modèle d'`EDITION` (échec rapide sur la configuration).
- `internal/api` : une variable de package `UITheme`, fixée au démarrage par `cmd/server/main.go`,
  comme `Edition`.
- `models.InstanceStatus` gagne `UITheme string` sérialisé en `uiTheme`.
  `GET /api/config/status` le renvoie. La route reste publique.
- `docker-compose.yml` transmet `UI_THEME: ${UI_THEME:-hotel}` au backend. `.env.example`
  documente la variable et son défaut.
- Tests : validation de config en table (défaut, `hotel`, `classic`, casse et espaces
  normalisés, valeur inconnue refusée), et un test `httptest` sur le vrai routeur qui vérifie
  la présence de `uiTheme` dans `/api/config/status`.

Basculer en production : changer `UI_THEME` dans Dokploy et redémarrer le backend. Le bundle
frontend contient les deux landings et choisit à l'exécution.

### Frontend

- `InstanceContext` expose `uiTheme` (`hotel` ou `classic`). Si le champ est absent (backend
  plus ancien) ou inconnu, le frontend retombe sur `classic`.
- `lib/uiTheme.js` résout le thème effectif : le paramètre `?ui=hotel` ou `?ui=classic`
  prime et est mémorisé pour la session (`sessionStorage`, clé `mailsorter_ui_preview`,
  lectures et écritures dans un try/catch). `?ui=default` efface la prévisualisation. Sinon,
  la valeur du serveur s'applique. La prévisualisation ne concerne que le navigateur qui l'utilise.
- La route `/` rend un composant de choix qui charge à la demande (`React.lazy`) soit
  `pages/HotelLanding.js`, soit `pages/Login.js`. Un visiteur ne télécharge que la landing
  active, polices comprises.

## 5. Système visuel

### Tokens (`frontend/src/styles/hotel.css`)

Confinés à la classe `.theme-hotel` posée sur la racine de la landing (et plus tard du
dashboard), jamais sur `:root`. Débranché, le thème ne laisse aucune trace. Variante de nuit
sous `.dark .theme-hotel`, pilotée par le réglage jour / nuit / auto existant (`ui/theme.js`).

| Token | Jour | Nuit | Rôle |
|---|---|---|---|
| `--h-bg` | `#F6E3D9` | `#1C1220` | fond de page (rayures) |
| `--h-bg-alt` | `#FBF1E4` | `#221620` | une section sur deux, listes |
| `--h-surface` | `#FFFAF2` | `#2A1A24` | cartes, champs, panneaux |
| `--h-sunk` | `#F7D6DC` | `#33202C` | survol, ligne sélectionnée |
| `--h-pink` | `#EFB7C3` | `#5A2A3A` | avatars, étiquettes, façade |
| `--h-text` | `#2B1B1E` | `#F3E6DA` | texte |
| `--h-muted` | `#6E4B52` | `#BFA6A8` | texte secondaire |
| `--h-line` | `#2B1B1E` | `#0B0609` | tous les traits |
| `--h-hair` | encre 16 % | crème 12 % | filets fins |
| `--h-plum` | `#7A2E3B` | `#A8455A` | action principale |
| `--h-on-plum` | `#FBF1E4` | `#FFF4E8` | texte sur bordeaux |
| `--h-accent` | `#7A2E3B` | `#EE9CB0` | texte d'accent (italiques, liens) |
| `--h-mustard` | `#E3A93B` | `#F4CF6B` | badges, compteurs, étage actif, focus |
| `--h-mustard-soft` | `#FBE8C4` | `#3A2A18` | fond des propositions |
| `--h-teal` | `#2F6F73` | `#4F9A96` | protégé, sûr |
| `--h-teal-soft` / `--h-teal-text` | `#D5E7E4` / `#2F6F73` | `#1E3533` / `#7CC4BF` | encadré "protégé" |
| `--h-offset` | `#7A2E3B` | `#000000` | ombre décalée pleine |

Principes : une couleur, un sens ; bordures de 1,5 px et ombres décalées pleines, aucune
ombre floue sur l'interface, aucun dégradé ; angles droits sauf boutons d'ascenseur, badges
et bout des étiquettes.

Typographie : Bodoni Moda pour les titres et les propositions, toujours avec
`font-variation-settings: 'opsz' 28` (au-delà, le "4" et les traits d'union deviennent
illisibles), chiffres en graisse 500. Jost pour l'interface. Polices chargées uniquement
par le thème hôtel.

Exception assumée à la règle "Tailwind only" : le thème est une feuille de style confinée,
parce qu'un thème débranchable ne peut pas vivre dans `index.css`. Tailwind reste utilisé
pour la mise en page, avec un groupe de couleurs `hotel` qui pointe vers les variables.
Les classes de composants du thème sont préfixées `h-` (`h-btn`, `h-card`, `h-tag`...).

### Moodboard (`docs/design/moodboard.html`)

Page statique qui charge `../../frontend/src/styles/hotel.css`, avec un bouton jour / nuit.
Contenu, conforme à la maquette validée : quatre principes, couleurs (jour et nuit, rôle),
typographie, formes et reliefs, composants, illustrations (avec le nom du composant React),
mouvement, une maquette du dashboard (barre ascenseur, boîte de réception avec propositions,
tri pas à pas, notification d'annulation, état vide, historique, réglages), et le ton
(exemples à faire et à ne pas faire, règles).

## 6. La landing "Grand Hôtel"

Les textes ci-dessous sont les textes validés. Ponctuation ASCII uniquement.

### En-tête

Emblème (enveloppe coiffée d'un fronton) et "Mailsorter". Au centre, le menu ascenseur :
1 Fonctionnement, 2 Confidentialité, 3 Tarifs, 4 Questions ; l'étage visible s'allume.
À droite, "Se connecter" (bascule le formulaire du hall en mode connexion et y remonte).
Sur grand écran (plus de 1340 px), une cabine d'ascenseur longe le bord gauche et suit le défilement.
Tarifs absent du menu en `self-hosted`.

### Hall

- Titre : "4 212 non lus ?" puis "On s'en occupe. Vous validez." Le nombre descend de 1 à 3
  toutes les 3,2 s, en phase avec l'ascenseur. Décoratif (pas d'annonce aux lecteurs d'écran).
- Au centre, l'hôtel en coupe : enseigne "MAILSORTER", chambres Newsletters, Promos, Travail,
  Voyages, Factures, Colis, Perso, Suite VIP, ascenseur animé, réception.
- À gauche, trois plaques : "Il lit l'expéditeur, l'objet et le début du message. Pas plus." /
  "Il propose : archiver, classer, reporter. Vous dites oui ou non." / "Tout s'annule en un
  clic. Même à 2 h du matin."
- À droite, la fiche : "Créer un compte" (mention "gratuit"), e-mail, mot de passe,
  bouton "Faire le tri", "Continuer avec Google" (seulement si `isConfigured`),
  "Déjà un compte ? Se connecter", "Gratuit jusqu'à 200 tris par mois. Sans carte bancaire."
  En mode connexion : titre "Se connecter", bouton "Entrer", lien "Pas encore de compte ?
  Créer un compte". Les erreurs de l'API s'affichent dans la fiche.

### 1. Fonctionnement

- "Un mail, une question, un clic." / "Pour chaque e-mail, Mailsorter vous dit ce qu'il en
  ferait. Vous validez ou vous passez. Et si vous changez d'avis, Annuler est juste là."
- Étiquette "Démo interactive". Une vraie pile : le mail de tête en grand, les deux suivants
  déjà visibles en retrait. "Valider" envoie la carte vers sa porte (Archives, Factures,
  Désabonnements, Plus tard) dont le compteur s'incrémente ; "Garder tel quel" la fait glisser
  de côté ; la notification "Archivé. Annuler" permet de la faire ressortir de sa porte.
  Le dernier mail (Maman) est marqué "Expéditrice protégée / Mailsorter n'y touche pas." avec
  "Suivant". Pile vide : "Pile vide. Plus que N chez vous." avec "Faire le tri" (remonte à la
  fiche) et "Recommencer la démo". Données fictives, aucun appel réseau.

### 1 bis. Ce qu'il fait pendant que vous faites autre chose

Couloir de six portes illustrées, chacune avec son texte :

| Porte | Titre | Texte | Badge |
|---|---|---|---|
| Panneau de direction | Les évidences n'ont pas besoin d'IA. | Vos règles passent avant le modèle. Plus rapide, et ça ne touche pas à votre quota. | Gmail |
| Réveil | Pas maintenant. | Un e-mail disparaît et revient ce soir, demain ou ce week-end. Comme si vous l'aviez reçu à ce moment-là. | Gmail |
| Cordon VIP | Votre mère ne sera jamais archivée. | Les expéditeurs protégés échappent à tout tri automatique. Même quand ils écrivent en majuscules. | aucun (respecté en IMAP) |
| "Ne pas déranger" | Désabonnez-vous en un clic. | Sans chercher le lien minuscule en gris clair tout en bas du mail. | Gmail |
| Journal et café | Un récap par jour. Pas un de plus. | Chaque matin, si vous le voulez, un e-mail résume ce qui a été trié ces sept derniers jours. | Gmail |
| Horloge | Il repasse toutes les 30 minutes. | Activez-le une fois : les nouveaux e-mails sont relevés sans que vous ayez à ouvrir l'app. Ni à y penser. | aucun (marche aussi en IMAP) |

### Règle d'honnêteté

Une seule constante de la landing (`GMAIL_ONLY` dans `components/landing/features.js`) liste les
fonctions encore réservées à Gmail. Elle pilote les badges, la légende du tableau à clés et la
réponse "Ça marche avec Outlook ?". Porter une fonction en IMAP, c'est retirer une ligne ici.

État sur `fbf24e1` :

| Marche aussi en IMAP | Réservé à Gmail |
|---|---|
| lecture, tri par IA, expéditeurs protégés, actions une par une, tri toutes les 30 minutes | annulation, règles, report, désabonnement, récap, actions groupées, pièces jointes |

Sur une instance sans Google (`isConfigured` faux), rien de ce qui dépend de Gmail n'est promis :

- les cartes du couloir marquées Gmail sont retirées ;
- la troisième plaque du hall devient "Rien ne bouge sans votre accord. Même à 2 h du matin." ;
- la phrase "Et si vous changez d'avis, Annuler est juste là." disparaît du sous-titre de la démo,
  et la notification de la démo confirme sans proposer "Annuler".

Sur une instance avec Google, tout s'affiche, et le tableau à clés dit ce qui manque en IMAP.

### 2. Confidentialité

"Ce qu'on lit. Ce qu'on garde. Ce qu'on ne fait jamais." Un coffre-fort illustré (plaque
"AES-256") et trois plaques :

- On lit : "L'expéditeur, l'objet et au plus 200 caractères du message. Jamais le message
  entier, jamais les pièces jointes."
- On garde : "Vos accès, chiffrés. L'historique de vos actions, pour pouvoir les annuler. Vous
  pouvez tout exporter, ou tout supprimer, quand vous voulez."
- On ne fait jamais : "Revendre quoi que ce soit. Supprimer un e-mail sans votre accord. Et le
  code est public : vous pouvez vérifier."

Puis le lien "Lire la politique de confidentialité" vers `/confidentialite`.

### 2 bis. Boîtes compatibles

"Ça marche avec votre boîte." / "Même avec l'adresse Orange que vous avez depuis 2004."
Tableau à clés alimenté par `GET /api/providers` : une étiquette par fournisseur. Dorée
("via Google", toutes les fonctions) quand le fournisseur a une route `transport: "gmail-api"`
et que l'instance est configurée pour Google ; rose ("IMAP") sinon. Légende : "Toutes les
fonctions" / "Lecture et tri par IA. L'annulation, les règles et le report arrivent."
(texte dérivé de `GMAIL_ONLY`).
Aucun nom de fournisseur écrit en dur.

### 3. Tarifs (absent en `self-hosted`)

"Gratuit jusqu'à 200 tris par mois." Carte encadrée : Gratuit / 200 tris par mois ; Toutes les
fonctions / incluses ; Carte bancaire / jamais demandée ; Pro / illimité, bientôt.
Si le paiement n'est pas branché : "Laissez votre e-mail, on vous écrit une fois : le jour où
Pro ouvre." avec le champ et "Prévenez-moi" (liste d'attente, source `landing`, mémorisée comme
aujourd'hui). Si le paiement est branché : un lien vers `/pricing` à la place.

### 4. Questions

"Les questions qu'on nous pose vraiment." Tableau à lettres cliquable, réponse affichée à côté :
"Vous lisez mes mails ?", "Et si l'IA se trompe ?", "Ça marche avec Outlook ?",
"Je peux tout supprimer ?", "Pourquoi c'est gratuit ?". Réponses de la maquette, sauf Outlook :
"Oui pour lire vos e-mails, les faire trier par l'IA et agir dessus. L'annulation, les règles,
le report et le récap sont pour l'instant réservés à Gmail. Ça arrive." (dérivée de `GMAIL_ONLY`).

### Sortie

Section de nuit : "Il est tard" / "Toujours N non lus ?" (même compteur), "Faire le tri",
"Se connecter", façade de nuit aux fenêtres allumées. Pied de page : Confidentialité,
Conditions, Contact, Code source, et le sélecteur Jour / Nuit / Auto relié à `ui/theme.js`.

### Nuit, mobile, mouvement

- Nuit : toute la page bascule par les tokens ; le hall utilise la façade de nuit.
- Mobile (375 px) : titre, hôtel, fiche, plaques. Le menu ascenseur disparaît (marque et
  "Se connecter" seulement). Portes et étiquettes sur deux colonnes. Aucun défilement horizontal.
- Mouvement : ascenseur (boucle de 6,4 s), compteur, envol de carte (0,5 s), pile, badges.
  Avec `prefers-reduced-motion`, plus de boucle ni de compteur animé, transitions instantanées.

## 7. Comportement conservé

Tout ce que fait la landing actuelle, dans les deux thèmes :

- inscription et connexion e-mail et mot de passe, Google seulement si `isConfigured` ;
- après connexion, redirection vers `/inbox` si une boîte est connue, sinon `/connect` ;
- redirection immédiate d'un visiteur déjà connecté (`lib/session.js`) ;
- fournisseurs lus depuis `/api/providers`, rien en dur ;
- liste d'attente Pro (`lib/waitlist.js`, source `landing`) ;
- Tarifs masqués en `self-hosted` ;
- liens légaux, contact, code source ;
- événements analytics existants, plus `landing_demo` avec `{ action: validate | skip | undo }`,
  sans aucune donnée personnelle.

La logique d'authentification de `Login.js` part dans un hook partagé (`lib/useAuthForm.js`)
utilisé par les deux landings. `Login.js` garde exactement son rendu.

## 8. Fichiers

| Fichier | Rôle |
|---|---|
| `backend/internal/config/config.go` (+ test) | `UITheme`, `UI_THEME`, validation |
| `backend/internal/models/models.go` | champ `uiTheme` de `InstanceStatus` |
| `backend/internal/api/handlers.go` (+ test) | variable `UITheme`, statut |
| `backend/cmd/server/main.go` | fixe `api.UITheme` |
| `docker-compose.yml`, `.env.example` | `UI_THEME` |
| `frontend/src/contexts/InstanceContext.js` | expose `uiTheme` |
| `frontend/src/lib/uiTheme.js` | thème effectif et prévisualisation |
| `frontend/src/lib/useAuthForm.js` | logique d'authentification partagée |
| `frontend/src/App.js` | choix de la landing sur `/`, chargement à la demande |
| `frontend/src/pages/Login.js` | landing classique, branchée sur le hook, rendu inchangé |
| `frontend/src/pages/HotelLanding.js` | landing "Grand Hôtel" |
| `frontend/src/components/landing/features.js` | `GMAIL_ONLY` et le texte des fonctions |
| `frontend/src/components/landing/*` | sections : en-tête, hall, fiche, démo, couloir, coffre, tableau à clés, tarifs, questions, sortie, rail d'ascenseur |
| `frontend/src/ui/hotel/*` | illustrations et briques réutilisables : `Emblem`, `HotelFacade` (jour, nuit), `Door` (et ses accessoires), `Vault`, `KeyTag`, `ElevatorPanel` |
| `frontend/src/styles/hotel.css` | tokens, classes `h-*`, animations |
| `frontend/tailwind.config.js` | couleurs `hotel` et familles de polices |
| `docs/design/moodboard.html` | la moodboard |

## 9. Vérification

- Backend : `go vet ./... && go build ./... && go test -race ./...` verts.
- Frontend : `CI=false npm run build` compile.
- Parcours scripté dans un vrai navigateur (Playwright lancé depuis le scratchpad, rien
  ajouté au dépôt) à 375, 768 et 1440 px, en jour et en nuit : aucune erreur console, démo
  (valider, garder, annuler, pile vide, recommencer), bascule inscription et connexion,
  navigation clavier et focus visible, `prefers-reduced-motion`.
- Contre une stack locale (`make up`) : inscription, connexion, redirection, liste d'attente.
- `UI_THEME=classic` : l'ancienne landing s'affiche à l'identique (capture avant et après
  l'extraction du hook). `?ui=hotel` et `?ui=classic` basculent ce navigateur seulement.
- `UI_THEME=nimporte` : le backend refuse de démarrer.

## 10. Points ouverts à trancher pendant l'implémentation

1. **Textes vérifiés contre le code** pendant le brainstorming (plus rien d'ouvert) :
   - l'IA ne reçoit que l'expéditeur, l'objet et un extrait tronqué à 200 caractères au plus
     (`internal/ai/mistral.go`) ;
   - les règles ne consomment jamais de quota (`api/rules.go`), et seuls les e-mails envoyés au
     modèle comptent (`api/analysis.go`) ;
   - le récap part au plus une fois par jour, couvre 7 jours, et seulement pour qui l'a activé
     (`api/digest_scheduler.go`) ;
   - le tri toutes les 30 minutes ne concerne que les utilisateurs qui l'ont activé
     (`api/auto_sync.go`) et passe par `syncInbox`, déjà porté en IMAP ;
   - une règle peut mettre à la corbeille (`rules.ActionTrash`), d'où "sans votre accord"
     plutôt que "sans vous demander" ;
   - le dépôt de `SOURCE_URL` est public.
2. **Base de travail** : `main` à `fbf24e1`. Les modifications qui étaient non commitées au début
   du brainstorming (`session.js`, `waitlist.js`, `App.js`, `Pricing.js`) sont dans `09d8517`.
   Branche de travail : `claude/landing-grand-hotel`.
3. **Polices** : chargement par `@import` dans `hotel.css` ou par une balise injectée au montage,
   à confirmer selon le comportement du bundler de CRA.

## 11. Documentation à mettre à jour

`CLAUDE.md` (variable `UI_THEME`, choix de landing sur `/`, exception `hotel.css`, `ui/hotel`,
`components/landing`, `docs/design`, et la consigne "porter une fonction en IMAP : mettre à jour
`GMAIL_ONLY`" dans le paragraphe sur `gmailClientFor`), `docs/API.md` (champ `uiTheme`), `.env.example`.
