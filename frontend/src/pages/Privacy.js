import React from 'react';
import LegalLayout, { LegalSection, LegalList } from '../components/LegalLayout';
import { CONTACT_EMAIL, SOURCE_URL } from '../components/PublicFooter';

// La politique de confidentialite, ecrite depuis ce que le code fait vraiment
// plutot que depuis un modele. Chaque affirmation est verifiable dans ce depot :
//
//   scopes ............. backend/internal/gmail/gmail.go (NewService)
//   ce qui est stocke .. backend/internal/models/models.go (Email, User)
//   envoi a Mistral .... backend/internal/ai/mistral.go (From, Subject, Snippet tronque)
//   cache partage ...... backend/internal/api/analysis.go, cle sha256(from|subject)
//   chiffrement ........ backend/internal/crypto, backend/internal/api/tokens.go
//   export/effacement .. backend/internal/account/account.go (Datasets)
//
// Si l'un de ces points change, cette page fait partie du changement : une
// politique qui decrit une version anterieure du code est pire que pas de page
// du tout.
//
// Note pour l'exploitant : ce texte decrit fidelement le comportement du
// logiciel, il ne remplace pas la relecture d'un juriste avant une exploitation
// commerciale.
function Privacy() {
  return (
    <LegalLayout
      title="Politique de confidentialité"
      updatedAt="19 septembre 2026"
      intro="Mailsorter lit votre boîte mail pour la trier. C'est un accès sensible, et cette page dit exactement ce qui est demandé à Google, ce qui est conservé, ce qui est envoyé à un tiers, et comment tout effacer."
    >
      <LegalSection n="1" title="Qui traite vos données">
        <p>
          Mailsorter est un logiciel auto-hébergeable. Le responsable du traitement est l'exploitant de
          l'instance sur laquelle vous vous connectez. Pour l'instance publique hébergée sur{' '}
          <span className="font-mono text-xs text-ink-900">mailsorter.sohbi.dev</span>, il est joignable
          à{' '}
          <a className="font-semibold text-brand-600 hover:underline" href={`mailto:${CONTACT_EMAIL}`}>
            {CONTACT_EMAIL}
          </a>
          .
        </p>
        <p>
          Si vous hébergez Mailsorter vous-même, aucune donnée ne transite par nous : vos données
          restent sur votre serveur, et c'est vous le responsable du traitement.
        </p>
      </LegalSection>

      <LegalSection n="2" title="Ce que Mailsorter demande à Google">
        <p>
          À la connexion, Google vous présente les autorisations demandées. Elles sont au nombre de
          quatre, et chacune sert à une fonction précise :
        </p>
        <LegalList
          items={[
            <>
              <strong className="text-ink-900">Lecture de vos emails</strong> (gmail.readonly) : afficher
              votre boîte, et donner à l'analyse de quoi décider.
            </>,
            <>
              <strong className="text-ink-900">Modification de vos emails</strong> (gmail.modify) :
              archiver, étiqueter, marquer lu, mettre à la corbeille. C'est ce qui rend le tri possible,
              et c'est aussi ce qui rend chaque action réversible.
            </>,
            <>
              <strong className="text-ink-900">Gestion de vos libellés</strong> (gmail.labels) : créer le
              libellé que vous demandez quand il n'existe pas encore.
            </>,
            <>
              <strong className="text-ink-900">Envoi d'un email</strong> (gmail.send) : uniquement pour
              vous envoyer à vous-même le récap quotidien, si vous l'activez. Mailsorter n'écrit jamais à
              personne d'autre, et n'envoie rien si le récap est désactivé.
            </>,
          ]}
        />
        <p>
          Mailsorter ne supprime jamais définitivement un message : une suppression est un envoi à la
          corbeille Gmail, d'où le message reste récupérable pendant trente jours.
        </p>
      </LegalSection>

      <LegalSection n="3" title="Ce qui est conservé, et où">
        <p>
          Pour afficher votre boîte sans rappeler Google à chaque clic, Mailsorter conserve une copie
          locale des messages synchronisés dans la base de données de l'instance :{' '}
          <strong className="text-ink-900">expéditeur, destinataires, sujet, extrait, contenu, libellés
          et date</strong>. Les pièces jointes ne sont pas stockées : elles sont récupérées chez Google
          au moment où vous les téléchargez.
        </p>
        <p>Sont également conservés les éléments que vous créez dans l'application :</p>
        <LegalList
          items={[
            'vos règles de tri, vos expéditeurs protégés et vos recherches enregistrées,',
            'vos reports (emails mis de côté et leur heure de retour),',
            'le journal de toutes les actions effectuées, qui est ce qui vous permet de les annuler,',
            "les suggestions de l'analyse, vos préférences par expéditeur, et votre compteur d'usage mensuel.",
          ]}
        />
        <p>
          Votre jeton d'accès Google est <strong className="text-ink-900">chiffré en AES-256-GCM</strong>{' '}
          avant d'atteindre la base. Il n'est jamais inclus dans un export, ni affiché nulle part dans
          l'application.
        </p>
      </LegalSection>

      <LegalSection n="4" title="Ce qui est envoyé à l'intelligence artificielle">
        <p>
          Quand vous lancez un tri par IA, Mailsorter envoie à Mistral, pour chaque email analysé,{' '}
          <strong className="text-ink-900">l'expéditeur, le sujet et un extrait tronqué à 200
          caractères</strong>. Le contenu complet du message n'est jamais transmis, et les pièces
          jointes non plus.
        </p>
        <p>
          Rien n'est envoyé sans votre geste : l'analyse se déclenche quand vous cliquez. Les règles de
          tri, elles, sont purement déterministes et ne font appel à aucune IA.
        </p>
      </LegalSection>

      <LegalSection n="5" title="Le cache d'analyses, partagé entre comptes">
        <p>
          Pour ne pas payer deux fois la même décision, Mailsorter garde en cache le résultat d'une
          analyse. Ce cache est <strong className="text-ink-900">commun à tous les comptes de
          l'instance</strong>, et c'est un choix assumé : une newsletter déjà vue par quelqu'un d'autre
          n'a pas besoin de repasser par le modèle.
        </p>
        <p>
          Ce cache est indexé sur une empreinte cryptographique (SHA-256) de l'expéditeur et du sujet, et
          il ne contient que la décision (archiver, étiqueter, garder) : ni votre adresse, ni le contenu
          du message, ni rien qui permette de savoir qui a reçu quoi.
        </p>
      </LegalSection>

      <LegalSection n="6" title="Paiement et mesure d'audience">
        <p>
          <strong className="text-ink-900">Paiement.</strong> Quand l'abonnement Pro est ouvert sur
          l'instance, il passe par Stripe. Mailsorter ne voit jamais votre numéro de carte : il ne
          conserve que l'identifiant de client Stripe et l'état de votre abonnement. Cet identifiant est
          exclu de l'export de données.
        </p>
        <p>
          <strong className="text-ink-900">Audience.</strong> L'instance publique utilise Umami,
          auto-hébergé, pour compter les visites et quelques évènements d'usage (une analyse lancée, un
          désabonnement effectué). Ces évènements ne portent jamais d'adresse email, d'expéditeur ni de
          sujet : des compteurs et des libellés, rien d'autre. Aucun cookie publicitaire n'est déposé.
        </p>
      </LegalSection>

      <LegalSection n="7" title="Durée de conservation">
        <p>
          Vos données sont conservées tant que votre compte existe. Il n'y a pas de purge automatique :
          c'est vous qui décidez quand effacer, et l'effacement est immédiat et total (section 8).
        </p>
        <p>
          Révoquer l'accès de Mailsorter depuis votre compte Google coupe l'accès à votre boîte, mais
          n'efface pas ce qui est déjà stocké : pour cela, supprimez votre compte depuis l'application.
        </p>
      </LegalSection>

      <LegalSection n="8" title="Vos droits">
        <p>
          Depuis la page <strong className="text-ink-900">Compte</strong> de l'application, sans avoir à
          écrire à qui que ce soit :
        </p>
        <LegalList
          items={[
            <>
              <strong className="text-ink-900">Exporter</strong> : vous téléchargez un fichier JSON qui
              contient tout ce que Mailsorter détient à votre sujet, expurgé des seuls secrets (jeton
              Google, identifiant Stripe).
            </>,
            <>
              <strong className="text-ink-900">Supprimer</strong> : votre compte et l'intégralité des
              données dérivées sont effacés. Votre boîte Gmail n'est pas touchée : Mailsorter n'efface
              que ce qu'il a lui-même créé.
            </>,
          ]}
        />
        <p>
          L'export et l'effacement sont pilotés par la même liste de catégories dans le code, ce qui
          garantit qu'aucune donnée ne peut être conservée sans être exportable, ni exportée sans être
          effaçable. Vous disposez par ailleurs des droits d'accès, de rectification, d'opposition et de
          limitation : écrivez à{' '}
          <a className="font-semibold text-brand-600 hover:underline" href={`mailto:${CONTACT_EMAIL}`}>
            {CONTACT_EMAIL}
          </a>
          .
        </p>
      </LegalSection>

      <LegalSection n="9" title="Destinataires">
        <p>
          Vos données ne sont ni vendues, ni louées, ni transmises à des fins publicitaires. Les seuls
          tiers qui en voient une partie sont ceux que le service requiert :
        </p>
        <LegalList
          items={[
            'Google, dont provient votre courrier et à qui sont adressées les actions de tri,',
            'Mistral AI, pour la seule analyse décrite en section 4,',
            'Stripe, uniquement si vous souscrivez un abonnement,',
            "l'hébergeur de l'instance, qui héberge la base de données.",
          ]}
        />
      </LegalSection>

      <LegalSection n="10" title="Modifications de cette politique">
        <p>
          Le code de Mailsorter est public. Toute évolution de cette page est donc visible dans
          l'historique du dépôt, à côté du changement de code qui l'a motivée :{' '}
          <a
            className="font-semibold text-brand-600 hover:underline"
            href={SOURCE_URL}
            target="_blank"
            rel="noopener noreferrer"
          >
            github.com/nohe-sohbi/Mailsorter
          </a>
          .
        </p>
      </LegalSection>
    </LegalLayout>
  );
}

export default Privacy;
