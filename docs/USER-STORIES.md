# Audit des user stories du front public

Perimetre : ce qu'un visiteur **non connecte** atteint. La landing (`/`), la page
de tarifs (`/pricing`, publique), le retour d'authentification
(`/auth/callback`), l'ecran de configuration (`/setup`, quand l'instance n'est
pas cablee), la page 404 et les ecrans de demarrage de `App.js`.

Hors perimetre : tout ce qui est derriere la connexion (boite, regles, reporte,
historique, reglages, compte). Le `Header` ne s'affiche pas sur ces routes
(`hiddenPaths` dans `components/Header.js`), donc la navigation applicative
n'existe pas ici.

Fichiers lus : `pages/Login.js`, `pages/Pricing.js`, `pages/AuthCallback.js`,
`pages/Setup.js`, `App.js`, `public/index.html`, `public/robots.txt`.

Chiffres : 20 US existantes, 12 manquantes, 7 en trop.

---

## 1. User stories existantes

### Comprendre le produit (landing `/`)

| # | En tant que visiteur, je veux | Ou |
|---|---|---|
| US-01 | comprendre en une phrase ce que fait le produit | hero, "Votre boite mail, triee pendant que vous dormez" |
| US-02 | savoir sur quoi repose la promesse | puce "Propulse par l'IA Mistral" |
| US-03 | voir les fonctionnalites principales | section `FEATURES`, 5 cartes |
| US-04 | comprendre le parcours en trois etapes | section `STEPS` |
| US-05 | voir a quoi ressemble l'interface | maquette "Suggestions IA" du hero |
| US-06 | etre rassure sur la securite avant de donner acces a ma boite | puce "OAuth Google securise", "0 mot de passe stocke", pied de page |

### Entrer dans le produit

| # | En tant que visiteur, je veux | Ou |
|---|---|---|
| US-07 | me connecter avec Google depuis le hero | `handleLogin` |
| US-08 | me connecter avec Google depuis le CTA de fin de page | meme handler |
| US-09 | voir une erreur lisible si la connexion ne demarre pas | etat `error` de `Login` |
| US-10 | etre envoye directement dans la boite si je suis deja connecte | `useEffect` de `Login` |
| US-11 | voir que la connexion se finalise, sans ecran fige | `AuthCallback`, spinner |
| US-12 | comprendre un refus Google et revenir a l'accueil | `AuthCallback`, branche `error` |

### Tarifs (`/pricing`)

| # | En tant que visiteur, je veux | Ou |
|---|---|---|
| US-13 | aller voir les tarifs depuis la landing | bouton "Tarifs" de la nav |
| US-14 | comparer Free et Pro sur le prix et le contenu | `PLANS` |
| US-15 | savoir qu'il n'y a pas de carte bancaire pour demarrer | sous-titre |
| US-16 | commencer gratuitement depuis la page de tarifs | boutons "Commencer gratuitement" vers `/` |
| US-17 | laisser mon adresse pour la liste d'attente Pro, sans compte | formulaire `handleWaitlist` |
| US-18 | savoir que je suis deja inscrit sur la liste | `WAITLIST_KEY` en `localStorage` |

### Etats de service

| # | En tant que visiteur, je veux | Ou |
|---|---|---|
| US-19 | savoir que l'app demarre, ou que le moteur ne repond pas, avec un bouton pour reessayer | `BootScreen` dans `App.js` |
| US-20 | atterrir sur une 404 explicite plutot qu'une page blanche | `NotFound` |

Hors tableau, `/setup` : quand l'instance n'a pas d'identifiants OAuth, toute
route publique y renvoie. C'est un briefing de configuration destine a
l'exploitant, pas au visiteur (voir T-06).

Referencement et partage : `public/index.html` porte un `title`, une
`description`, `og:title` et `og:description`. `robots.txt` ouvre l'indexation.

---

## 2. User stories manquantes

### Bloquantes

**M-01. Lire la politique de confidentialite et les conditions d'utilisation.**
Aucune page, aucun lien, nulle part sur la surface publique (verifie sur
l'ensemble de `frontend/src`). Ce n'est pas qu'un manque de conformite : Google
exige un lien vers une politique de confidentialite **depuis la page d'accueil**
pour valider un client OAuth qui demande les scopes Gmail. Or `/setup` et le
`README` insistent tous les deux sur "Publier l'application", qui declenche
precisement cette verification. La landing est donc bloquante pour l'etape que
le produit demande a l'exploitant de franchir.

**M-02. Savoir ce qu'il advient de mes emails avant de cliquer.**
La seule reponse publique est une puce ("OAuth Google securise") et une phrase de
pied de page ("Vos emails ne quittent jamais votre controle"). Pour un outil qui
demande un acces en lecture ET en modification a une boite Gmail, c'est
l'objection numero un et elle n'est traitee nulle part : quelles permissions
exactement, qu'est-ce qui est stocke, qu'est-ce qui part chez Mistral, comment on
efface. Les reponses existent dans le produit (chiffrement AES-256-GCM, cache
partage anonyme, export et suppression RGPD) et aucune n'est dite avant la
connexion.

**M-03. Laisser mon adresse sans donner acces a ma boite.**
Le seul CTA de la landing est "Continuer avec Gmail". Un visiteur interesse mais
pas pret a autoriser un acces Gmail au premier contact n'a aucun moyen de se
manifester. La capture d'email existe pourtant deja (`waitlistService.join`,
`POST /api/waitlist`, publique par construction) mais uniquement sur `/pricing`,
donc apres un clic de plus.

### Importantes

**M-04. Savoir que mon fournisseur est supporte.**
La landing ne parle que de Gmail, en dur, alors que le catalogue backend expose
seize fournisseurs (`internal/provider`, revendique dans le `README`). Un
visiteur Outlook, Proton ou Fastmail repart en pensant que le produit ne le
concerne pas. `GET /api/providers` est une route publique : la landing pourrait
l'afficher, elle ne l'appelle pas.

**M-05. Voir le vrai produit.**
La maquette du hero est un faux ecran dessine en HTML, avec des donnees
inventees (Medium Digest, Amazon, Promo Casino). Il n'y a ni capture, ni GIF, ni
video du produit reel, alors qu'il tourne en production.

**M-06. Trouver des reponses aux questions courantes (FAQ).**
Rien sur le tarif apres la liste d'attente, sur ce qui arrive aux regles si on
resilie, sur la reversibilite des actions, sur l'auto-hebergement. La
reversibilite est pourtant l'argument central du produit et n'apparait pas une
seule fois sur la landing.

**M-07. Savoir que le produit est auto-hebergeable.**
L'edition `self-hosted` existe, le depot est public, et la surface publique n'en
dit pas un mot. C'est un argument de confiance gratuit qui n'est pas utilise.

**M-08. Partager un lien qui affiche un visuel.**
`og:image` est absent de `public/index.html`, ainsi que `og:url`, le lien
canonique et les balises Twitter. Tout partage social sort sans vignette.

**M-09. Etre trouve par un moteur.**
Pas de `sitemap.xml`, pas de donnees structurees, et une seule langue
(`lang="fr"`, tout le contenu en francais) pour un produit dont l'interface
parle deja a un public technique international.

### Secondaires

**M-10. Contacter quelqu'un.**
Aucune adresse, aucun formulaire, aucun lien vers le depot. Le pied de page ne
contient qu'un copyright et une phrase de reassurance.

**M-11. Comprendre ce qui se passe sans JavaScript.**
Le `<noscript>` se reduit a une phrase brute non stylee, sur une page qui, sans
JavaScript, est integralement vide.

**M-12. Voir la page de tarifs quand l'instance est auto-hebergee.**
Elle redirige vers `/`, ce qui est le bon comportement, mais le bouton qui y mene
reste affiche (voir T-05).

---

## 3. User stories en trop

**T-01. Vendre "Plusieurs comptes Gmail" dans le plan Pro.**
La ligne est dans `PLANS` sur `/pricing`. Or `docs/ROADMAP.md` classe le
multi-comptes dans "Reste a brancher" : il demande un modele `account` distinct
du `userId`, qui n'existe pas. On fait donc payer, ou attendre, pour une
fonctionnalite qui n'est pas livree. C'est le plus serieux des points de cette
section : les quatre autres lignes du plan Pro sont reelles, celle-ci ne l'est
pas.

**T-02. Les quatre chiffres de la landing.**
"10x plus rapide", "< 2 min pour vider 500 emails", "0 mot de passe stocke",
"100% sous votre controle". Seul le troisieme est verifiable. Les trois autres
n'ont aucune mesure derriere, occupent une section entiere, et fragilisent les
affirmations voisines qui, elles, sont vraies.

**T-03. La carte "Libelles intelligents".**
Elle promet "des etiquettes precises et coherentes, creees et appliquees
automatiquement". Les routes `GET/POST /api/smart-labels` existent mais sont
**deliberement non cablees** (`CLAUDE.md` le dit explicitement). La landing vend
donc une fonctionnalite sans surface dans l'application.

**T-04. La puce "Le plus populaire" sur le plan Pro.**
Le plan n'est pas achetable sur l'instance par defaut : le bouton en dessous dit
"Rejoindre la liste d'attente". Un plan que personne ne peut souscrire ne peut
pas etre le plus populaire.

**T-05. Le bouton "Tarifs" de la nav du Login en edition auto-hebergee.**
`Login.js` n'importe pas `useInstance` (verifie), donc le bouton s'affiche
toujours, alors que `/pricing` redirige vers `/` quand `selfHosted` est vrai. Le
`Header` applique pourtant deja la regle inverse (`billingOnly`). Resultat en
auto-heberge : un bouton qui ramene ou on etait.

**T-06. `/setup` presente a un visiteur.**
Quand l'instance n'est pas configuree, **toute** route publique y renvoie, y
compris `/`. Le visiteur se voit alors demander de renseigner
`GMAIL_CLIENT_ID`, `GMAIL_CLIENT_SECRET` et `GMAIL_REDIRECT_URL` puis de
redemarrer un service dont il ne controle rien. L'ecran s'adresse a
l'exploitant ; en edition `hosted`, un visiteur ne devrait jamais le voir.

**T-07. Le second chemin d'authentification sur `/`.**
`Login.js` lit lui aussi `?code=` et appelle `authService.handleCallback`, en
doublon de `AuthCallback`. Or `GMAIL_REDIRECT_URL` ne designe qu'une seule
adresse (`/auth/callback`, cf. `.env.example` et `README.md`). Ce chemin est donc
mort, et il a deja divergé : `AuthCallback` gere le parametre `error` renvoye par
Google, `Login` ne le lit pas.

---

## 4. Ce qu'il faut en retenir

La landing fait correctement son travail de presentation : la promesse est
claire, les fonctionnalites sont enoncees, le parcours en trois etapes est juste,
et le chemin vers la connexion est court. Vingt user stories pour une page
d'acquisition, c'est deja beaucoup.

Ce qui manque n'est pas de la presentation, c'est de la **confiance** :

1. Aucune page legale, ce qui bloque en plus la verification OAuth de Google que
   le produit demande lui-meme de lancer (M-01).
2. L'objection centrale (que faites-vous de mes emails) n'est traitee nulle part
   avant le clic, alors que le produit a les bonnes reponses (M-02).
3. Un seul CTA, qui exige tout de suite un acces Gmail complet (M-03).

Et ce qui est en trop est presque entierement de la **promesse non tenue** :
un plan Pro qui liste une fonctionnalite non livree (T-01), des chiffres non
mesures (T-02), une carte qui vend un ecran qui n'existe pas (T-03). Ce sont
trois retraits de texte, pas trois chantiers.
