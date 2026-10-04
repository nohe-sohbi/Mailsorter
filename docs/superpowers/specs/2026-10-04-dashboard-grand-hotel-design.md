# Dashboard "Grand Hôtel" : design

Date : 2026-10-04. Suite directe de `2026-09-24-landing-grand-hotel-design.md`, dont la
section 3 laissait "la migration du dashboard (projet suivant, qui réutilisera
l'interrupteur)" hors périmètre. Référence visuelle : la section 7 de
`docs/design/moodboard.html` ("Dans le dashboard").

## 1. Objectif

Habiller tout l'espace connecté (et ce que le serveur dessine lui-même) avec le thème
de la landing, branché sur le même interrupteur `UI_THEME` et la même prévisualisation
`?ui=`, sans toucher d'un pixel au thème classique.

## 2. Décisions

| Sujet | Décision |
|---|---|
| Interrupteur | Le même : `UI_THEME` côté serveur, `?ui=hotel\|classic\|default` par onglet. Lu une fois par `HotelThemeProvider` (`ui/hotel/HotelTheme.js`), exposé par `useHotel()` |
| Chargement | `hotel.css` + `hotel-app.css` en chunk à la demande, polices comprises. Une instance classique n'en télécharge rien (vérifié : zéro règle hôtel chargée, zéro requête Bodoni). Si le chunk échoue, retour au classique plutôt qu'un écran blanc |
| Architecture | Deux couches. 1) CSS seul : les jetons classiques (`brand`, `ink`, `surface`, statuts) re-pointés sur la palette hôtel, jour et nuit, et la forme hôtel donnée aux classes partagées (`.btn*`, `.card`, `.input`, `.chip`, `.skeleton`). 2) JSX derrière `useHotel()` pour ce que l'hôtel dit : en-tête ascenseur, plaques d'étage, tableau des compteurs, étiquettes, propositions, registre, dessins des états vides |
| Classique | Chaque variante hôtel est une branche ; le balisage classique est inchangé. Contrôlé par 68 captures avant/après (jour, nuit, 1440, 390, modales, toasts) : toutes identiques au pixel |
| Landing | Intouchée : `ht-app` n'est jamais posé sur `/`. Contrôlé : 4 captures identiques au pixel |
| Mouvement | Les portes de l'ascenseur s'ouvrent à chaque changement d'étage (0,5 s, décoratif, ignoré par les lecteurs d'écran, absent avec `prefers-reduced-motion`) |
| Serveur | Le récap quotidien suit le thème : `digest.RenderStyle`, style `hotel` en tableaux et styles en ligne (compatible clients mail), objet et texte brut identiques entre styles. L'aperçu de Réglages est rendu dans une iframe isolée pour être fidèle à l'envoi |

## 3. Ce que l'hôtel dessine, écran par écran

| Écran | Classique | Grand Hôtel |
|---|---|---|
| En-tête | Onglets | Panneau d'ascenseur numéroté (étage allumé), jauge du quota, avatar ; sur mobile, l'étage courant et un tiroir de boutons d'ascenseur |
| Boîte | Cartes de stats, suggestions en liste | Plaque d'étage, tableau des compteurs, filtres en étiquettes à bagages, "Mailsorter propose" avec "Archiver ?" en Bodoni, proposition posée sur la ligne du message, "Inbox zéro. Respect." devant la porte "Ne pas déranger" |
| Lecteur | Carte de recommandation | Sujet en Bodoni, `ht-prop` "Mailsorter propose / Archiver", Valider / Passer, l'e-mail posé comme une lettre |
| Règles, Plus tard | En-tête à icône | Plaques d'étage, portes au panneau de direction et au réveil sur les états vides |
| Historique | Cartes | Un registre : un titre par jour, une ligne par acte ("Le Monde *archivé*"), Annuler en bout de ligne |
| Réglages, Compte | Cartes | Titres de section en Bodoni italique, interrupteurs hôtel, coffre-fort sur la confidentialité, semaine en barres de laiton |
| Brancher une boîte | Encadré d'aide | Le tableau à clés de la landing, en petit |
| 404, démarrage | Logo bleu | Porte "404", emblème |
| Récap par e-mail | HTML sobre | Enseigne MAILSORTER, semaine en barres de laiton, détail en registre |

## 4. Ton

Celui de la moodboard : les verbes du produit restent simples (Valider, Passer,
Annuler), l'humour se range dans les titres et les états vides ("Inbox zéro.
Respect."), l'hôtel se voit mais ne parle pas (aucun jeu de rôle de concierge).
Ponctuation ASCII.

## 5. Vérification

- `go vet`, `go build`, `go test -race ./...` verts ; tests ajoutés pour le style du récap
  et pour `digestStyle()` qui suit `UI_THEME`.
- `CI=false npm run build` compile.
- Parcours Playwright contre une API simulée (scratchpad, rien dans le dépôt) : toutes les
  pages en jour et nuit, 1440 et 390 ; portes, tiroir mobile, Échap, focus clavier
  moutarde, validation d'une proposition sur une ligne, `?ui=` dans les deux sens,
  aucune erreur console.
