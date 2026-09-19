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

Chiffres a l'ouverture de l'audit : 20 US existantes, 12 manquantes, 7 en trop.

**Etat au 19 septembre 2026** : 16 des 19 points ouverts sont corriges. Les trois
qui restent sont nommes en section 4, avec ce qui les bloque. Chaque point corrige
porte la mention **[Corrige]** et dit ou.

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

Hors tableau, `/setup` : un briefing de configuration destine a l'exploitant. Il
etait la destination automatique de toute route publique sur une instance non
cablee, y compris pour un visiteur qui ne controle rien du deploiement ; ce n'est
plus le cas (voir T-06).

Referencement et partage : `public/index.html` portait un `title`, une
`description`, `og:title` et `og:description`, sans image ni lien canonique, et
`robots.txt` ouvrait l'indexation sans rien exclure (voir M-08 et M-09).

---

## 2. User stories manquantes

### Bloquantes

**M-01. Lire la politique de confidentialite et les conditions d'utilisation.** **[Corrige]**
Aucune page, aucun lien, nulle part sur la surface publique (verifie sur
l'ensemble de `frontend/src`). Ce n'est pas qu'un manque de conformite : Google
exige un lien vers une politique de confidentialite **depuis la page d'accueil**
pour valider un client OAuth qui demande les scopes Gmail. Or `/setup` et le
`README` insistent tous les deux sur "Publier l'application", qui declenche
precisement cette verification. La landing est donc bloquante pour l'etape que
le produit demande a l'exploitant de franchir.

> `pages/Privacy.js` et `pages/Terms.js`, sur `/confidentialite` et `/conditions`, avec
> `components/LegalLayout.js` pour la coquille et `components/PublicFooter.js` pour les
> liens, presents sur la landing, sur les tarifs et sur les pages legales elles-memes.
> Les deux routes sont publiques et inconditionnelles, y compris avant que l'instance
> soit configuree. Le contenu est ecrit depuis le code (scopes reels, ce qui est stocke,
> ce qui part chez Mistral, le cache partage, l'export et l'effacement), pas depuis un
> modele, et chaque affirmation cite son fichier source en commentaire. **A faire relire
> par un juriste avant exploitation commerciale** : le texte est fidele au logiciel, ce
> qui n'est pas la meme chose qu'un document valide.

**M-02. Savoir ce qu'il advient de mes emails avant de cliquer.** **[Corrige]**
La seule reponse publique est une puce ("OAuth Google securise") et une phrase de
pied de page ("Vos emails ne quittent jamais votre controle"). Pour un outil qui
demande un acces en lecture ET en modification a une boite Gmail, c'est
l'objection numero un et elle n'est traitee nulle part : quelles permissions
exactement, qu'est-ce qui est stocke, qu'est-ce qui part chez Mistral, comment on
efface. Les reponses existent dans le produit (chiffrement AES-256-GCM, cache
partage anonyme, export et suppression RGPD) et aucune n'est dite avant la
connexion.

> Section "Ce que Mailsorter fait de vos emails" sur la landing (quatre cartes : les
> permissions demandees, ce qui part a l'IA, l'absence d'irreversible, la reprise des
> donnees), plus une FAQ de six entrees en `<details>` natif, plus un lien vers la
> politique complete.

**M-03. Laisser mon adresse sans donner acces a ma boite.** **[Corrige]**
Le seul CTA de la landing est "Continuer avec Gmail". Un visiteur interesse mais
pas pret a autoriser un acces Gmail au premier contact n'a aucun moyen de se
manifester. La capture d'email existe pourtant deja (`waitlistService.join`,
`POST /api/waitlist`, publique par construction) mais uniquement sur `/pricing`,
donc apres un clic de plus.

> Section "Pas encore pret ?" en bas de landing, plus un lien depuis le hero. Elle poste
> sur `POST /api/waitlist` avec `source: 'landing'`. L'etat "deja inscrit" est partage
> avec `/pricing` par `lib/waitlist.js` : s'inscrire sur l'une vaut s'inscrire sur
> l'autre, et reafficher le formulaire apres un succes se lit comme un echec.

### Importantes

**M-04. Savoir que mon fournisseur est supporte.** **[Corrige]**
`GET /api/providers`, donc par le meme catalogue que celui avec lequel le serveur se
connecte. Un echec de la requete coute la bande, pas la page. La bande dit aussi
honnetement que la connexion se fait aujourd'hui par Gmail.
La landing ne parle que de Gmail, en dur, alors que le catalogue backend expose
seize fournisseurs (`internal/provider`, revendique dans le `README`). Un
visiteur Outlook, Proton ou Fastmail repart en pensant que le produit ne le
concerne pas. `GET /api/providers` est une route publique : la landing pourrait
l'afficher, elle ne l'appelle pas.

> Bande "Boites joignables par cette instance" sur la landing, alimentee par

**M-05. Voir le vrai produit.** **[Non corrige]** Voir section 4.
La maquette du hero est un faux ecran dessine en HTML, avec des donnees
inventees (Medium Digest, Amazon, Promo Casino). Il n'y a ni capture, ni GIF, ni
video du produit reel, alors qu'il tourne en production.

**M-06. Trouver des reponses aux questions courantes (FAQ).** **[Corrige]**
Rien sur le tarif apres la liste d'attente, sur ce qui arrive aux regles si on
resilie, sur la reversibilite des actions, sur l'auto-hebergement. La
reversibilite est pourtant l'argument central du produit et n'apparait pas une
seule fois sur la landing.

> Six questions, celles qui bloquent reellement : est-ce que vous lisez mes emails, est-ce
> que ca part chez une IA, que se passe-t-il en cas d'erreur, dois-je tout valider,
> combien ca coute, puis-je tout recuperer ou effacer.

**M-07. Savoir que le produit est auto-hebergeable.** **[Corrige]**
L'edition `self-hosted` existe, le depot est public, et la surface publique n'en
dit pas un mot. C'est un argument de confiance gratuit qui n'est pas utilise.

> Encart "Ou hebergez-le vous-meme" entre la section confiance et la FAQ, avec le lien
> vers le depot.

**M-08. Partager un lien qui affiche un visuel.** **[Corrige]**
`og:locale`, le lien canonique et les balises Twitter `summary_large_image`. L'image est
generee depuis `docs/assets/og-image.source.html` en Chromium headless, donc
reproductible plutot que binaire opaque.
`og:image` est absent de `public/index.html`, ainsi que `og:url`, le lien
canonique et les balises Twitter. Tout partage social sort sans vignette.

> `og:image` (1200x630), `og:image:width/height/alt`, `og:url`, `og:site_name`,

**M-09. Etre trouve par un moteur.** **[Partiellement corrige]**
`robots.txt` etendu (les ecrans applicatifs en `Disallow`, le sitemap declare) et des
donnees structurees JSON-LD `SoftwareApplication`, limitees a ce qui est vrai : le
prix du palier gratuit et la nature du logiciel, pas de note moyenne inventee. La
version anglaise reste a faire (voir section 4).
Pas de `sitemap.xml`, pas de donnees structurees, et une seule langue
(`lang="fr"`, tout le contenu en francais) pour un produit dont l'interface
parle deja a un public technique international.

> `public/sitemap.xml` (les quatre adresses publiques, pas les ecrans derriere session),

### Secondaires

**M-10. Contacter quelqu'un.** **[Corrige]**
Aucune adresse, aucun formulaire, aucun lien vers le depot. Le pied de page ne
contient qu'un copyright et une phrase de reassurance.

> `components/PublicFooter.js` : confidentialite, conditions, contact (`nohe@sohbi.dev`)
> et lien vers le depot, sur toutes les pages publiques.

**M-11. Comprendre ce qui se passe sans JavaScript.** **[Corrige]**
Le `<noscript>` se reduit a une phrase brute non stylee, sur une page qui, sans
JavaScript, est integralement vide.

> `<noscript>` mis en forme, qui explique pourquoi l'app a besoin de JavaScript et donne
> l'adresse de contact. Il ne renvoie deliberement PAS vers les pages legales : le SPA
> sert `index.html` pour toute adresse, donc ces liens ramenaient au meme message.

**M-12. Voir la page de tarifs quand l'instance est auto-hebergee.** **[Corrige]** Voir T-05.
Elle redirige vers `/`, ce qui est le bon comportement, mais le bouton qui y mene
reste affiche (voir T-05).

---

## 3. User stories en trop

**T-01. Vendre "Plusieurs comptes Gmail" dans le plan Pro.** **[Corrige, decision produit]**
La ligne est dans `PLANS` sur `/pricing`. Or `docs/ROADMAP.md` classe le
multi-comptes dans "Reste a brancher" : il demande un modele `account` distinct
du `userId`, qui n'existe pas. On fait donc payer, ou attendre, pour une
fonctionnalite qui n'est pas livree. C'est le plus serieux des points de cette
section : les quatre autres lignes du plan Pro sont reelles, celle-ci ne l'est
pas.

> La ligne reste, marquee d'une puce "Bientot", en texte attenue et avec une icone
> d'horloge au lieu de la coche verte. Le plan continue donc d'annoncer l'intention sans
> la faire passer pour livree. Les conditions d'utilisation (section 4) disent en toutes
> lettres qu'une fonctionnalite marquee a venir n'est pas due.

**T-02. Les quatre chiffres de la landing.** **[Corrige]**
"10x plus rapide", "< 2 min pour vider 500 emails", "0 mot de passe stocke",
"100% sous votre controle". Seul le troisieme est verifiable. Les trois autres
n'ont aucune mesure derriere, occupent une section entiere, et fragilisent les
affirmations voisines qui, elles, sont vraies.

> Remplaces par quatre faits verifiables dans le depot : `0` mot de passe stocke (OAuth),
> `AES-256` pour les jetons au repos (`internal/crypto`), `200` analyses offertes par mois
> (`FreeMonthlyLimit`), `1 clic` pour annuler (le ledger et son inverse). Chacun porte en
> infobulle la raison pour laquelle il est vrai.

**T-03. La carte "Libelles intelligents".** **[Corrige, decision produit]**
Elle promet "des etiquettes precises et coherentes, creees et appliquees
automatiquement". Les routes `GET/POST /api/smart-labels` existent mais sont
**deliberement non cablees** (`CLAUDE.md` le dit explicitement). La landing vend
donc une fonctionnalite sans surface dans l'application.

> Meme traitement que T-01 : la carte reste, avec une puce "Bientot".

**T-04. La puce "Le plus populaire" sur le plan Pro.** **[Corrige]**
Le plan n'est pas achetable sur l'instance par defaut : le bouton en dessous dit
"Rejoindre la liste d'attente". Un plan que personne ne peut souscrire ne peut
pas etre le plus populaire.

> Elle devient "Bientot disponible" quand `billingOn` est faux, c'est-a-dire exactement
> quand le bouton en dessous propose une liste d'attente.

**T-05. Le bouton "Tarifs" de la nav du Login en edition auto-hebergee.** **[Corrige]**
`Login.js` n'importe pas `useInstance` (verifie), donc le bouton s'affiche
toujours, alors que `/pricing` redirige vers `/` quand `selfHosted` est vrai. Le
`Header` applique pourtant deja la regle inverse (`billingOnly`). Resultat en
auto-heberge : un bouton qui ramene ou on etait.

> `Login.js` lit `useInstance()` et applique la meme regle que le `Header` : en
> auto-heberge le bouton n'est pas rendu.

**T-06. `/setup` presente a un visiteur.** **[Corrige]**
Quand l'instance n'est pas configuree, **toute** route publique y renvoie, y
compris `/`. Le visiteur se voit alors demander de renseigner
`GMAIL_CLIENT_ID`, `GMAIL_CLIENT_SECRET` et `GMAIL_REDIRECT_URL` puis de
redemarrer un service dont il ne controle rien. L'ecran s'adresse a
l'exploitant ; en edition `hosted`, un visiteur ne devrait jamais le voir.

> Une instance non configuree ne renvoie plus tout le monde vers `/setup`. En
> `self-hosted` c'est toujours le cas (le lecteur est le proprietaire du deploiement) ;
> en `hosted` le visiteur voit `Unavailable` dans `App.js`, qui dit que le service n'est
> pas encore connecte et donne l'adresse de l'exploitant. `/setup` reste joignable par
> son adresse pour l'exploitant.

**T-07. Le second chemin d'authentification sur `/`.** **[Corrige]**
`Login.js` lit lui aussi `?code=` et appelle `authService.handleCallback`, en
doublon de `AuthCallback`. Or `GMAIL_REDIRECT_URL` ne designe qu'une seule
adresse (`/auth/callback`, cf. `.env.example` et `README.md`). Ce chemin est donc
mort, et il a deja divergé : `AuthCallback` gere le parametre `error` renvoye par
Google, `Login` ne le lit pas.

> Le traitement de `?code=` est retire de `Login.js`. `AuthCallback` est desormais le seul
> chemin, ce qui supprime au passage la divergence sur le parametre `error`.

---

## 4. Ce qui reste ouvert

Trois points sur dix-neuf, et aucun n'est un oubli.

**M-05. Voir le vrai produit.** La maquette du hero reste une illustration dessinee en
HTML. Elle est desormais annoncee comme telle ("Illustration de l'ecran de
suggestions") plutot que de passer pour une capture, ce qui repare l'honnetete mais pas
le manque. Produire une vraie capture demande une instance connectee a une boite Gmail
reelle : impossible depuis l'environnement de developpement, et une capture d'une boite
de test avec trois emails inventes ne vaudrait pas mieux que l'illustration actuelle.
A faire depuis la production, avec une boite dont les expediteurs peuvent etre montres.

**M-09 (reste). La version anglaise.** Tout le contenu public est en francais, et
`lang="fr"`. Traduire n'est pas une correction de bug : c'est un second jeu de contenu
a maintenir, plus un choix de routage (`/en/`, un sous-domaine, ou la negociation de
langue). La convention du depot veut que l'UI soit en francais ; changer cela est une
decision produit, pas une correction d'audit.

**M-14 a M-17** etaient deja classes secondaires et le restent : repondre ou transferer
depuis le lecteur (choix produit assume, hors du perimetre de triage), le multi-comptes
(bloque par `X-User-Email` qui sert a la fois d'identite et de `userId`), tester une
regle seule, et l'absence des donnees `localStorage` dans l'export RGPD. Ces quatre-la
concernent l'application connectee, pas la surface publique auditee ici.

## 5. Ce qu'il faut en retenir

La landing savait presenter, elle ne savait pas rassurer. Les corrections portent
presque toutes sur ce deuxieme registre : dire ce que Google va demander, ce qui part
chez le modele, ce qui est conserve et comment tout reprendre, avant le clic plutot
qu'apres ; offrir une porte de sortie a qui n'est pas pret a confier sa boite ; et
donner enfin les pages legales, sans lesquelles la verification OAuth de Google, que
`/setup` demande pourtant de lancer, ne peut pas aboutir.

Cote promesses, rien n'a ete retire : sur decision produit, les deux fonctionnalites
non livrees restent annoncees, marquees "Bientot", et les conditions d'utilisation
precisent qu'une fonctionnalite ainsi marquee n'est pas due. Les chiffres inventes,
eux, ont ete remplaces par des faits verifiables plutot que supprimes : la page y gagne
plus qu'elle n'y perd.
