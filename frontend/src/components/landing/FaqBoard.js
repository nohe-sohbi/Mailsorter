import React, { useState } from 'react';
import FloorHeading from './FloorHeading';
import { canPromise, outlookAnswer } from './features';
import './FaqBoard.css';

// The felt letter board of a hotel lobby: the questions on the board, the
// answer beside it. Every answer holds on this instance (features.js): no undo
// promised without Google, no Pro on a self-hosted instance.
export default function FaqBoard({ floorNo, isConfigured, selfHosted }) {
  const undo = canPromise('undo', isConfigured);
  const rules = canPromise('rules', isConfigured);
  const qa = [
    [
      'Vous lisez mes mails ?',
      "Nous, non. Le modèle voit l'expéditeur, l'objet et au plus 200 caractères, le temps de proposer un tri. Il ne voit jamais le message entier ni les pièces jointes.",
    ],
    [
      "Et si l'IA se trompe ?",
      undo
        ? "Vous dites non. Et si vous aviez déjà dit oui, vous annulez depuis l'historique. Rien n'est supprimé sans votre accord."
        : "Vous dites non. Rien ne bouge sans votre accord, et rien n'est supprimé sans lui.",
    ],
    ['Ça marche avec Outlook ?', outlookAnswer()],
    [
      'Je peux tout supprimer ?',
      'Oui. Vous exportez tout, puis vous supprimez votre compte depuis la page Compte. Vos accès et votre historique partent avec.',
    ],
    [
      "Pourquoi c'est gratuit ?",
      selfHosted
        ? "Parce que c'est votre instance : vous l'hébergez, personne ne vous facture."
        : `Jusqu'à 200 tris par mois, ça nous coûte peu. ${rules ? 'Les règles et les tris' : 'Les tris'} déjà connus ne comptent même pas. Au-delà, il y aura Pro.`,
    ],
  ];
  const [open, setOpen] = useState(0);

  return (
    <section className="hl-section hl-section--alt" id="questions" data-floor="questions" aria-labelledby="hl-faq-title">
      <div className="hl-wrap">
        <FloorHeading n={floorNo} kicker="Questions" id="hl-faq-title">
          Les questions qu'on nous pose <em>vraiment.</em>
        </FloorHeading>
        <div className="hl-qa">
          <div className="hl-lb">
            <p className="hl-lb__hd" aria-hidden="true">
              QUESTIONS
            </p>
            {qa.map(([question], i) => (
              <button key={question} type="button" aria-pressed={open === i} aria-controls="hl-faq-answer" onClick={() => setOpen(i)}>
                {question}
              </button>
            ))}
          </div>
          <div className="hl-ans" id="hl-faq-answer" aria-live="polite">
            <h3>{qa[open][0]}</h3>
            <p>{qa[open][1]}</p>
          </div>
        </div>
      </div>
    </section>
  );
}
