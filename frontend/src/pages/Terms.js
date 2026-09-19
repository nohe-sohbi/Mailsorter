import React from 'react';
import { Link } from 'react-router-dom';
import LegalLayout, { LegalSection, LegalList } from '../components/LegalLayout';
import { CONTACT_EMAIL, SOURCE_URL } from '../components/PublicFooter';

// Conditions d'utilisation. Meme discipline que Privacy.js : chaque affirmation
// correspond a un comportement reel du logiciel (quota mensuel de 200 analyses,
// reversibilite journalisee, corbeille plutot que suppression definitive,
// effacement total du compte), pas a un modele generique.
//
// Note pour l'exploitant : a faire relire par un juriste avant toute
// exploitation commerciale, en particulier les sections 6 et 9.
function Terms() {
  return (
    <LegalLayout
      title="Conditions d'utilisation"
      updatedAt="19 septembre 2026"
      intro="Ce que Mailsorter s'engage à faire, ce que vous acceptez en l'utilisant, et ce sur quoi vous gardez la main."
    >
      <LegalSection n="1" title="Objet">
        <p>
          Mailsorter est un service de tri de boîte mail assisté par intelligence artificielle. Il lit
          votre courrier, propose une action pour chaque message, et applique celles que vous validez.
          Ce n'est pas un client de messagerie : on n'y rédige pas, on n'y répond pas, on n'y transfère
          pas.
        </p>
        <p>
          Utiliser le service vaut acceptation des présentes conditions et de la{' '}
          <Link className="font-semibold text-brand-600 hover:underline" to="/confidentialite">
            politique de confidentialité
          </Link>
          .
        </p>
      </LegalSection>

      <LegalSection n="2" title="Compte et accès">
        <p>
          L'accès se fait par votre compte Google. Il n'y a pas de mot de passe Mailsorter : aucun mot de
          passe n'est demandé, ni stocké. Vous êtes responsable de la sécurité du compte Google avec
          lequel vous vous connectez.
        </p>
        <p>
          Vous pouvez retirer l'accès à tout moment, depuis les autorisations de votre compte Google ou
          en supprimant votre compte Mailsorter.
        </p>
      </LegalSection>

      <LegalSection n="3" title="Ce que vous autorisez, et ce que vous gardez">
        <p>
          En vous connectant, vous autorisez Mailsorter à lire et à modifier votre boîte dans les limites
          décrites en section 2 de la politique de confidentialité. En pratique :
        </p>
        <LegalList
          items={[
            "aucune action n'est appliquée sans votre validation, sauf celles que vous avez explicitement automatisées (règles de tri, auto-pilote par expéditeur, synchronisation automatique),",
            "chaque action est inscrite dans un journal consultable, avec son origine : vous, une règle, l'IA ou l'auto-pilote,",
            'les actions réversibles peuvent être annulées depuis ce journal,',
            'une suppression est un envoi à la corbeille Gmail, jamais un effacement définitif,',
            'les expéditeurs que vous protégez ne sont jamais archivés ni supprimés automatiquement.',
          ]}
        />
      </LegalSection>

      <LegalSection n="4" title="Plans, quota et facturation">
        <p>
          Le plan gratuit donne droit à{' '}
          <strong className="text-ink-900">200 emails analysés par mois</strong>. Les analyses servies
          depuis le cache et celles déclenchées par l'auto-pilote ne sont pas décomptées. Les règles de
          tri ne consomment aucun quota : elles ne font appel à aucune IA.
        </p>
        <p>
          Le plan Pro, quand il est ouvert sur l'instance, est un abonnement mensuel sans engagement,
          résiliable à tout moment depuis le portail de facturation. La résiliation prend effet à la fin
          de la période en cours ; les sommes déjà versées ne sont pas remboursées au prorata. Aucune de
          vos données n'est supprimée par une résiliation : vous repassez simplement sur le quota
          gratuit.
        </p>
        <p>
          Certaines fonctionnalités annoncées comme <strong className="text-ink-900">à venir</strong> ne
          sont pas encore disponibles. Elles sont signalées comme telles, et leur absence ne constitue
          pas un manquement au présent contrat.
        </p>
      </LegalSection>

      <LegalSection n="5" title="Usage acceptable">
        <p>Vous vous engagez à ne pas utiliser le service pour :</p>
        <LegalList
          items={[
            "accéder à une boîte mail qui n'est pas la vôtre ou pour laquelle vous n'avez pas d'autorisation,",
            "contourner les quotas, sonder l'infrastructure ou en perturber le fonctionnement,",
            'traiter des données dont la réglementation vous interdit la transmission à un tiers.',
          ]}
        />
      </LegalSection>

      <LegalSection n="6" title="Disponibilité">
        <p>
          Le service est fourni en l'état, sans garantie de disponibilité. Il dépend de tiers (Google,
          Mistral, l'hébergeur) dont les interruptions échappent à l'exploitant. Des interruptions pour
          maintenance ou mise à jour peuvent survenir sans préavis.
        </p>
        <p>
          Mailsorter ne se substitue pas à votre boîte mail : vos messages restent chez votre
          fournisseur, et restent accessibles depuis son interface si le service est indisponible.
        </p>
      </LegalSection>

      <LegalSection n="7" title="Résiliation">
        <p>
          Vous pouvez supprimer votre compte à tout moment depuis la page Compte. La suppression efface
          l'intégralité des données Mailsorter vous concernant, et est irréversible. Votre boîte Gmail
          n'est pas affectée.
        </p>
        <p>
          L'exploitant peut suspendre un compte qui contrevient à la section 5, après information lorsque
          les circonstances le permettent.
        </p>
      </LegalSection>

      <LegalSection n="8" title="Propriété">
        <p>
          Vos emails, vos règles et vos données vous appartiennent. Vos règles sont d'ailleurs
          exportables en JSON depuis l'application, sans identifiant ni compteur, pour être rejouées
          ailleurs.
        </p>
        <p>
          Le code de Mailsorter est publié sur{' '}
          <a
            className="font-semibold text-brand-600 hover:underline"
            href={SOURCE_URL}
            target="_blank"
            rel="noopener noreferrer"
          >
            github.com/nohe-sohbi/Mailsorter
          </a>{' '}
          et reste la propriété de son auteur, sous les termes de la licence qui y figure.
        </p>
      </LegalSection>

      <LegalSection n="9" title="Responsabilité">
        <p>
          Mailsorter agit sur votre boîte selon vos instructions et celles des automatismes que vous
          activez. Les garde-fous sont réels (validation, journal, annulation, expéditeurs protégés,
          corbeille plutôt qu'effacement), mais la responsabilité des automatismes que vous configurez
          vous revient.
        </p>
        <p>
          Dans la limite permise par la loi, la responsabilité de l'exploitant ne saurait excéder les
          sommes que vous lui avez versées au cours des douze derniers mois.
        </p>
      </LegalSection>

      <LegalSection n="10" title="Droit applicable et contact">
        <p>
          Les présentes conditions sont soumises au droit français. Pour toute question, réclamation ou
          demande relative à vos données :{' '}
          <a className="font-semibold text-brand-600 hover:underline" href={`mailto:${CONTACT_EMAIL}`}>
            {CONTACT_EMAIL}
          </a>
          .
        </p>
      </LegalSection>
    </LegalLayout>
  );
}

export default Terms;
