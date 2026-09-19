# Audit des user stories du front

Etat des lieux de ce que le SPA permet reellement de faire, de ce qu'il ne permet
pas encore, et de ce qu'il porte sans qu'une US le justifie.

Methode : lecture exhaustive de `frontend/src` (10 routes, 2 contextes,
`services/api.js`, `ui/`), confrontee a la table de routes du backend
(`backend/internal/api/routes.go`, 70 enregistrements) et aux promesses de
`README.md` et de la landing. Une US n'est comptee comme existante que si un
utilisateur peut l'atteindre par l'interface, pas parce que l'endpoint repond.

Perimetre : le front uniquement. Les manques qui demandent aussi du backend sont
signales comme tels.

Chiffres : 84 US existantes, 17 manquantes (dont 5 bloquantes), 9 en trop.

---

## 1. User stories existantes

### Connexion, instance, identite

| # | En tant qu'utilisateur, je veux | Ou |
|---|---|---|
| US-01 | me connecter avec mon compte Google | `Login`, `AuthCallback` |
| US-02 | me deconnecter | `Header` |
| US-03 | savoir si l'instance est configuree et comment la configurer | `/setup` |
| US-04 | voir quels fournisseurs cette instance sait joindre | `/setup`, via `GET /api/providers` |
| US-05 | reconnecter Gmail apres une revocation, sans perdre mes regles | `Reglages` |
| US-06 | choisir un theme clair, sombre ou systeme | `Header`, `ui/theme.js` |
| US-07 | etre redirige vers la connexion si ma session est morte | `RequireAuth`, intercepteur axios 401 |

### Boite de reception : lire et trier

| # | En tant qu'utilisateur, je veux | Ou |
|---|---|---|
| US-08 | voir ma boite (expediteur, sujet, extrait, date, non lu, favori) | `Inbox` |
| US-09 | synchroniser a la demande, et etre sur que la synchro a eu lieu | `Inbox`, bouton Synchroniser et touche `r` |
| US-10 | charger la page suivante d'emails | `Inbox`, `loadMoreEmails` |
| US-11 | ouvrir un email et lire son contenu HTML assaini | `EmailReader` |
| US-12 | que l'ouverture marque l'email comme lu | `EmailReader`, `markRead=1` |
| US-13 | telecharger une piece jointe | `EmailReader` |
| US-14 | archiver ou supprimer un email depuis la ligne, le lecteur ou le clavier | `Inbox`, `EmailReader` |
| US-15 | annuler immediatement un archivage ou une suppression | toast `action` |
| US-16 | marquer lu, non lu, favori, non favori | `Inbox` (clavier), `EmailReader` |
| US-17 | reporter un email a un preset ou a une date que je choisis | `ui/SnoozeMenu` |
| US-18 | proteger l'expediteur d'un email en cours de lecture | `EmailReader` |
| US-19 | me desabonner depuis le lecteur quand l'entete le permet | `EmailReader` |
| US-20 | selectionner plusieurs emails, tout selectionner, vider ma selection | `Inbox` |
| US-21 | archiver, marquer lu, mettre en favori, etiqueter ou supprimer ma selection | `Inbox`, `BULK_ACTIONS` |
| US-22 | reporter toute ma selection en une fois | `Inbox`, `runBulkSnooze` |
| US-23 | annuler une action en masse | toast `action`, `batchUndo` |
| US-24 | choisir un libelle Gmail existant ou en creer un | `LabelPicker` |
| US-25 | etre prevenu quand des expediteurs proteges ont ete ignores | toasts de `runBulk` |

### Boite de reception : chercher

| # | En tant qu'utilisateur, je veux | Ou |
|---|---|---|
| US-26 | chercher dans ma boite en langage Gmail | `Inbox`, formulaire de recherche |
| US-27 | que ma recherche reste dans la boite sauf mention contraire | `handleSearch`, scope `in:inbox` |
| US-28 | filtrer en un clic (Tout, Non lus, Aujourd'hui, Favoris, Pieces jointes, Volumineux) | `QUICK_FILTERS` |
| US-29 | enregistrer une recherche qui a marche, et la rejouer d'un clic | `SaveSearchDialog`, `searchService` |
| US-30 | supprimer une recherche enregistree | `Inbox` |
| US-31 | filtrer ma boite en cliquant sur une carte de statistique | `STAT_CARDS` |

### Boite de reception : IA

| # | En tant qu'utilisateur, je veux | Ou |
|---|---|---|
| US-32 | faire trier ma boite (ou ma selection) par l'IA | `handleAnalyze` |
| US-33 | que l'app ne bloque pas au dela de 10 emails, avec une barre de progression | bascule async, `pollJob` |
| US-34 | voir chaque suggestion avec son action, son motif et sa confiance | `ConfidenceRing`, panneau Suggestions |
| US-35 | appliquer ou ignorer une suggestion | `Inbox` |
| US-36 | tout appliquer ou tout ignorer, avec confirmation si des suppressions sont dedans | `handleApplyAll` |
| US-37 | ne voir que les suggestions a haute confiance | filtre `highConfOnly` |
| US-38 | savoir que mon quota mensuel est atteint, et ou aller ensuite | `handleQuotaError`, 402 vers `/pricing` |
| US-39 | savoir que le cache et l'auto-pilote ne consomment pas mon quota | `Compte`, `Tarifs` |

### Boite de reception : expediteurs et abonnements

| # | En tant qu'utilisateur, je veux | Ou |
|---|---|---|
| US-40 | voir qui m'ecrit le plus, et filtrer cette liste | onglet Expediteurs |
| US-41 | faire analyser un expediteur par l'IA | `handleAnalyzeSender` |
| US-42 | activer l'auto-pilote pour un expediteur | `handleToggleAutoApply` |
| US-43 | tout archiver ou tout supprimer d'un expediteur, avec confirmation typee | `handleApplyBulk` |
| US-44 | transformer un expediteur en regle permanente | `handleCreateSenderRule` |
| US-45 | voir mes abonnements detectes et lesquels supportent le 1-clic | onglet Abonnements |
| US-46 | me desabonner, en un clic ou par la page de l'expediteur | `handleUnsubscribe` |
| US-47 | me desabonner ET archiver tout le passe de cet expediteur | option `alsoArchive` |

### Regles

| # | En tant qu'utilisateur, je veux | Ou |
|---|---|---|
| US-48 | lister mes regles et leur ordre d'evaluation | `Rules` |
| US-49 | creer une regle multi-conditions et multi-actions | `RuleEditor` |
| US-50 | choisir un libelle existant et etre prevenu si j'en cree un nouveau | `LabelField` |
| US-51 | activer ou mettre en pause une regle | `Rules` |
| US-52 | changer la priorite d'une regle | boutons de reordonnancement |
| US-53 | dupliquer une regle (la copie arrive en pause) | `duplicate` |
| US-54 | supprimer une regle | `Rules` |
| US-55 | voir ce que mes regles feraient sans rien modifier | Apercu, `preview` |
| US-56 | appliquer mes regles maintenant | `applyNow` |
| US-57 | que mes regles s'appliquent a chaque synchro | bascule Autopilote |
| US-58 | exporter et importer mon jeu de regles en JSON | `exportRules`, `importRules` |

### Reporte

| # | En tant qu'utilisateur, je veux | Ou |
|---|---|---|
| US-59 | voir mes reports prevus, termines et en echec | `Snoozed`, 3 onglets |
| US-60 | savoir dans combien de temps un email revient | compte a rebours et date exacte |
| US-61 | ramener un email dans ma boite maintenant | `handleWake` |
| US-62 | comprendre pourquoi un retour a echoue, et relancer | onglet En echec |

### Historique

| # | En tant qu'utilisateur, je veux | Ou |
|---|---|---|
| US-63 | voir tout ce que Mailsorter a fait a ma place | `History` |
| US-64 | savoir qui a decide (regle, IA, auto-pilote, masse, direct, report, desabo, annulation) | `SOURCE_FILTERS` |
| US-65 | chercher un sujet ou un expediteur dans l'historique | recherche avec anti-rebond |
| US-66 | annuler une action reversible, et voir celles qui ne le sont pas | `undoAction` |

### Reglages

| # | En tant qu'utilisateur, je veux | Ou |
|---|---|---|
| US-67 | faire synchroniser ma boite automatiquement | `AutoSyncSettings` |
| US-68 | recevoir un recap quotidien, a l'heure que je choisis, en sachant l'heure locale | `DigestSettings` |
| US-69 | voir a quoi ressemblera ce recap, et m'en envoyer un test tout de suite | Apercu, Envoi de test |
| US-70 | gerer mes expediteurs proteges (adresse ou domaine entier) | `ProtectedSenders` |

### Compte et facturation

| # | En tant qu'utilisateur, je veux | Ou |
|---|---|---|
| US-71 | voir mon identite, ma date d'inscription, mon plan et mon quota | `Account` |
| US-72 | voir mon activite de tri de la semaine | `Account`, `Pricing` |
| US-73 | exporter toutes mes donnees (RGPD) | `PrivacyData` |
| US-74 | supprimer definitivement mon compte, avec confirmation typee | `PrivacyData` |
| US-75 | comparer les plans | `Pricing` |
| US-76 | passer a Pro par Stripe | `handleUpgrade` |
| US-77 | gerer mon abonnement dans le portail Stripe | `handleManage` |
| US-78 | rejoindre la liste d'attente quand le paiement n'est pas ouvert | `handleWaitlist` |

### Transverse

| # | En tant qu'utilisateur, je veux | Ou |
|---|---|---|
| US-79 | decouvrir les 4 leviers au premier lancement | modale de bienvenue |
| US-80 | connaitre et consulter les raccourcis clavier | modale `?` |
| US-81 | etre confirme avant toute action destructrice | `ui/Confirm`, `typeToConfirm` |
| US-82 | comprendre une erreur et pouvoir reessayer plutot que voir un ecran vide | `ErrorState`, `EmptyState` |
| US-83 | utiliser l'app au clavier et au lecteur d'ecran | skip link, piege de focus, `LiveAnnouncer`, `aria-*` |
| US-84 | utiliser l'app sur telephone (lecteur en feuille plein ecran, menu tiroir) | `isNarrow`, `useScrollLock`, drawer |

---

## 2. User stories manquantes

### Bloquantes

**M-01. Connecter une boite non-Gmail (IMAP).**
`mailboxService.get/connect/disconnect` existe dans `services/api.js`, les routes
`GET/POST/DELETE /api/mailbox` existent et sont testees, et aucun composant ne les
appelle. Consequence directe : en `EDITION=hosted`, qui ne peut structurellement
pas atteindre l'API Gmail (plafond de 100 autorisations, cf. `internal/provider`),
le front n'offre aucun chemin pour atteindre du courrier. La landing propose
"Continuer avec Gmail" et rien d'autre. `README.md` affirme pourtant que
"l'application vous indique lequel et ou le trouver" pour les autres fournisseurs :
c'est vrai du backend, pas de l'interface.

**M-02. Voir et deconnecter la boite connectee.**
Meme cause. Une fois M-01 fait, il faut l'ecran qui dit quelle adresse est
branchee et permet de la detacher sans supprimer le compte.

**M-03. Choisir son fournisseur au moment de connecter.**
`GET /api/providers` n'est lu que par `/setup`, ou il ne produit que des puces
informatives. L'ecran de connexion, lui, code Google en dur, ce qui est
exactement ce que le catalogue devait empecher.

**M-04. Lire la politique de confidentialite, les CGU, contacter le support.**
Aucune de ces pages n'existe dans le front, et aucun lien nulle part (verifie par
recherche sur l'ensemble de `frontend/src`). Pour un produit qui lit du courrier,
stocke des jetons OAuth et encaisse par Stripe, c.est le manque le plus expose.
Le pied de page de la landing porte un copyright et une phrase de reassurance,
rien de plus.

**M-05. Annuler ou replanifier un report.**
`Snoozed` n'offre que "Reactiver", c'est-a-dire ramener l'email maintenant. Il n'y
a aucun moyen de dire "finalement, dans une semaine" ni "laisse tomber, ne le
ramene pas". Manque aussi cote backend : pas de `PUT`/`DELETE /api/snoozes/{id}`.

### Importantes

**M-06. Ouvrir l'email concerne depuis Historique et depuis Reporte.**
Les deux pages affichent sujet et expediteur, et aucune ligne n'est cliquable. On
lit "Archive - Regle - 14:32" sans pouvoir aller voir de quoi il s'agit.

**M-07. Proteger un expediteur depuis les onglets Expediteurs et Abonnements.**
La protection ne s'attrape que depuis le lecteur d'un email ou depuis les
Reglages, alors que les deux ecrans ou l'on raisonne par expediteur ne la
proposent pas. Ce sont pourtant les ecrans qui offrent "Tout supprimer".

**M-08. Choisir l'action par defaut d'un expediteur.**
`PUT /api/senders/{id}/preferences` accepte `defaultAction` et `defaultLabel`, et
l'UI ne sait que basculer `autoApply`. La preference affichee vient donc
uniquement de l'IA, et "Creer une regle" force `archive` en dur
(`handleCreateSenderRule`). Un utilisateur qui veut "etiqueter Factures" pour cet
expediteur doit aller ecrire la regle a la main.

**M-09. Marquer non lu ou retirer des favoris en masse.**
`BULK_ACTIONS` ne contient que `read` et `star`. Les inverses existent
(`unread`, `unstar`, dans `ui/actions.js` et cote API) et ne sont atteignables
qu'email par email.

**M-10. Renommer une recherche enregistree.**
Creation et suppression seulement. Une faute de frappe dans le nom oblige a
supprimer et recreer la recherche.

**M-11. Les libelles intelligents.**
`GET/POST /api/smart-labels` existent et sont testes, deliberement non cables
(`CLAUDE.md`). Mais la landing vend "Libelles intelligents : des etiquettes
precises et coherentes, creees et appliquees automatiquement". Soit on cable
l'ecran, soit on retire la promesse ; aujourd'hui la page d'accueil annonce une
fonctionnalite sans surface.

**M-12. Filtrer ou chercher dans Reporte et dans Abonnements.**
L'onglet Expediteurs a un champ de filtre, les deux autres listes n'en ont pas.
Elles grossissent pourtant aussi vite.

**M-13. Gerer son abonnement depuis `/account`.**
La page Compte affiche le plan et renvoie vers `/pricing` ; le portail Stripe
n'est accessible que depuis `/pricing`. Un abonne qui veut resilier passe par la
page de vente.

### Secondaires

**M-14. Repondre, transferer, ecrire.**
Absent par choix produit (Mailsorter trie, il ne remplace pas Gmail), mais ce
choix n'est jamais dit a l'utilisateur : le lecteur ressemble a un client mail
et ne porte aucun signe qu'il est en lecture seule. Une ligne "Repondre dans
Gmail" avec un lien profond couterait peu et fermerait la question.

**M-15. Plusieurs boites pour un meme compte.**
Deja au roadmap, bloque par `X-User-Email` qui sert a la fois d'identite et de
`userId` (`docs/ROADMAP.md`, "Reste a brancher").

**M-16. Tester une regle seule.**
L'apercu est global : on ne peut pas demander "que ferait celle-ci" pendant
qu'on l'ecrit, ce qui est justement le moment ou la question se pose.

**M-17. Voir et effacer ses donnees locales.**
L'onboarding vu (`mailsorter_onboarded`), la serie (`mailsorter_gamify`) et la
liste d'attente (`mailsorter_pro_waitlist`) vivent dans `localStorage` et ne
figurent ni dans l'export RGPD ni dans la suppression de compte. Le trou est
petit mais il contredit la promesse "tout ce que Mailsorter stocke a votre
sujet".

---

## 3. User stories en trop

**T-01. La gamification (serie, objectif de 20 par jour, "Objectif atteint").**
Le candidat le plus net. `ui/streak.js` est du `localStorage` pur : la serie
disparait en changeant de navigateur, en vidant le cache ou en passant en
navigation privee, elle ne suit pas l'utilisateur et ne repose sur aucune donnee
serveur, alors que le ledger `action_log` sait deja compter les actions par jour
(c'est exactement ce qu'affiche `GET /api/stats/activity`). Elle occupe une carte
permanente entre les statistiques et le premier email. Deux issues honnetes : la
brancher sur le ledger, ou la retirer.

**T-02. Le tableau de bord d'usage duplique sur `/pricing`.**
"Usage du mois" et "Cette semaine" sont rendus a l'identique dans `Account.js` et
dans `Pricing.js`, avec les memes appels (`getUsage`, `getActivity`). Deux ecrans
a maintenir pour une seule US, et deja deux rendus qui divergent (l'un affiche la
periode, l'autre non). `/pricing` doit vendre ; le suivi appartient a `/account`.

**T-03. Les cartes de statistiques "Total" et "Spam" comme filtres.**
Cliquer dessus lance `in:anywhere` et `in:spam`. L'utilisateur sort de sa boite
tandis que la barre d'actions en masse continue d'offrir Archiver et Supprimer,
sur des messages deja archives ou deja en spam. Aucune US de triage ne couvre ces
deux vues ; comme indicateurs ces cartes sont utiles, comme filtres elles ouvrent
un mode que le reste de l'ecran ne sait pas traiter.

**T-04. Les filtres rapides "Pieces jointes" et "Volumineux".**
`has:attachment` et `larger:5M` repondent a une US de liberation d'espace Gmail,
pas de triage. Rien dans l'application n'exploite la taille d'un message, aucune
regle ne porte dessus, aucune suggestion ne la mentionne. Ce sont deux filtres
Gmail recopies.

**T-05. Deux onglets pour une seule US : Expediteurs et Abonnements.**
Les deux listent des expediteurs avec un compteur et des actions en masse.
Abonnements, c'est Expediteurs plus le desabonnement et moins l'auto-pilote. Un
seul ecran d'expediteurs, avec une puce "newsletter" et le bouton de
desabonnement quand l'entete le permet, dirait la meme chose en une navigation de
moins.

**T-06. `mailboxService` dans `services/api.js`.**
Trois methodes qu'aucun composant n'appelle. C'est du code mort tant que M-01
n'est pas fait, et c'est le seul service de ce fichier dans ce cas (verifie
methode par methode).

**T-07. `/setup` en edition `hosted`.**
La page demande de renseigner `GMAIL_CLIENT_ID`, `GMAIL_CLIENT_SECRET` et
`GMAIL_REDIRECT_URL` puis de redemarrer le service. En auto-heberge c'est la
bonne US. En heberge, le visiteur qui la voit ne controle rien de tout cela, et
l'edition ne peut de toute facon pas utiliser l'API Gmail : l'ecran lui demande
d'agir sur une infrastructure qui n'est pas la sienne.

**T-08. Les chiffres de la landing.**
"10x plus rapide", "< 2 min pour vider 500 emails", "100% sous votre controle"
sont des affirmations sans mesure derriere. Elles ne coutent rien a enlever et
elles fragilisent le reste de la page, qui decrit des fonctionnalites reelles.
Meme remarque pour la carte "Libelles intelligents" (cf. M-11).

**T-09. Les redirections `/emails` et `/triage`.**
Deux routes conservees pour des adresses qui ne sont plus publiees nulle part.
Sans cout, mais sans US non plus : a dater et a retirer.

---

## 4. Ce qu'il faut en retenir

Le front couvre la promesse centrale de bout en bout : lire, trier, deleguer,
verifier, revenir en arriere. Le triage, les regles, le report, le journal et le
RGPD sont complets et coherents.

Les trous ne sont pas dans le triage, ils sont autour :

1. **La connexion** est mono-fournisseur alors que tout le backend est deja
   multi-fournisseur. C'est le seul manque qui rend une edition entiere
   inutilisable (M-01 a M-03).
2. **Le cadre legal** est absent d'un produit qui facture et lit du courrier (M-04).
3. **Les ecrans de suivi** (Historique, Reporte) montrent des lignes sur lesquelles
   on ne peut pas agir (M-05, M-06).

Et l'interface porte quelques fonctionnalites que personne n'a demandees, dont
une qui occupe le haut du cockpit sans reposer sur la moindre donnee durable
(T-01).
