import React from 'react';
import { Link } from 'react-router-dom';
import Vault from '../../ui/hotel/Vault';
import FloorHeading from './FloorHeading';
import { canPromise } from './features';
import './PrivacyVault.css';

// Three facts, each checked against the code: the model only sees the sender,
// the subject and at most 200 characters (internal/ai/mistral.go); credentials
// are sealed at rest (api/tokens.go) and the sync keeps a copy of the mailbox,
// bodies included on the Gmail path (models.Email.Body), which the privacy
// policy states too; a rule the user wrote can trash, hence "sans votre
// accord" rather than "sans vous demander".
export default function PrivacyVault({ floorNo, isConfigured }) {
  const undo = canPromise('undo', isConfigured);
  return (
    <section className="hl-section" id="confidentialite" data-floor="confidentialite" aria-labelledby="hl-privacy-title">
      <div className="hl-wrap">
        <FloorHeading n={floorNo} kicker="Confidentialité" id="hl-privacy-title">
          Ce qu'on lit. Ce qu'on garde.
          <br />
          <em>Ce qu'on ne fait jamais.</em>
        </FloorHeading>
        <div className="hl-vault">
          <Vault className="hl-vault__art" />
          <div>
            <div className="hl-vplq">
              <b aria-hidden="true">I</b>
              <h3>On lit</h3>
              <p>L'IA ne voit que l'expéditeur, l'objet et au plus 200 caractères du message. Jamais le message entier, jamais les pièces jointes.</p>
            </div>
            <div className="hl-vplq">
              <b aria-hidden="true">II</b>
              <h3>On garde</h3>
              <p>
                Vos accès, chiffrés. Une copie de vos e-mails, pour vous les afficher sans rappeler votre fournisseur. L'historique de vos
                actions{undo ? ', pour pouvoir les annuler' : ''}. Vous pouvez tout exporter, ou tout supprimer, quand vous voulez.
              </p>
            </div>
            <div className="hl-vplq">
              <b aria-hidden="true">III</b>
              <h3>On ne fait jamais</h3>
              <p>Revendre quoi que ce soit. Supprimer un e-mail sans votre accord. Et le code est public : vous pouvez vérifier.</p>
            </div>
            <Link className="ht-link" to="/confidentialite">
              Lire la politique de confidentialité
            </Link>
          </div>
        </div>
      </div>
    </section>
  );
}
