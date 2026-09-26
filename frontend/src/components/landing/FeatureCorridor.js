import React from 'react';
import Door from '../../ui/hotel/Door';
import FloorHeading from './FloorHeading';
import { canPromise, isGmailOnly } from './features';
import './FeatureCorridor.css';

// What runs while the visitor does something else, one door per feature.
// A door is shown only when this instance can keep its promise, and carries a
// "Gmail" badge when an IMAP mailbox would not get it (features.js).
const ROOMS = [
  {
    feature: 'rules',
    color: '#2F6F73',
    number: '301',
    prop: 'signpost',
    title: "Les évidences n'ont pas besoin d'IA.",
    text: 'Vos règles passent avant le modèle. Plus rapide, et ça ne touche pas à votre quota.',
  },
  {
    feature: 'snooze',
    color: '#7A2E3B',
    number: '302',
    prop: 'alarm',
    title: 'Pas maintenant.',
    text: "Un e-mail disparaît et revient ce soir, demain ou ce week-end. Comme si vous l'aviez reçu à ce moment-là.",
  },
  {
    feature: 'protected',
    color: '#E48FA5',
    number: 'VIP',
    prop: 'rope',
    title: 'Votre mère ne sera jamais archivée.',
    text: 'Les expéditeurs protégés échappent à tout tri automatique. Même quand ils écrivent en majuscules.',
  },
  {
    feature: 'unsubscribe',
    color: '#7A2E3B',
    number: '304',
    prop: 'dnd',
    title: 'Désabonnez-vous en un clic.',
    text: 'Sans chercher le lien minuscule en gris clair tout en bas du mail.',
  },
  {
    feature: 'digest',
    color: '#2F6F73',
    number: '305',
    prop: 'newspaper',
    title: 'Un récap par jour. Pas un de plus.',
    text: 'Chaque matin, si vous le voulez, un e-mail résume ce qui a été trié ces sept derniers jours.',
  },
  {
    feature: 'autosync',
    color: '#7A2E3B',
    number: '306',
    prop: 'wallclock',
    title: 'Il repasse toutes les 30 minutes.',
    text: "Activez-le une fois : les nouveaux e-mails sont relevés sans que vous ayez à ouvrir l'app. Ni à y penser.",
  },
];

export default function FeatureCorridor({ isConfigured }) {
  const rooms = ROOMS.filter((r) => canPromise(r.feature, isConfigured));
  return (
    <section className="hl-section hl-section--alt hl-corridor" data-floor="fonctionnement" aria-labelledby="hl-corridor-title">
      <div className="hl-wrap">
        <FloorHeading id="hl-corridor-title">
          Et pendant que vous faites <em>autre chose.</em>
        </FloorHeading>
        <div className="hl-corr">
          {rooms.map((r) => (
            <article key={r.feature} className="hl-room">
              <div className="hl-room__wall">
                <Door color={r.color} number={r.number} prop={r.prop} />
              </div>
              <div className="hl-room__floor" aria-hidden="true" />
              <div className="hl-room__txt">
                <h3>{r.title}</h3>
                <p>{r.text}</p>
                {isGmailOnly(r.feature) && <span className="ht-tag is-gold">Gmail</span>}
              </div>
            </article>
          ))}
        </div>
      </div>
    </section>
  );
}
