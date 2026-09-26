import React from 'react';
import HotelFacade from '../../ui/hotel/HotelFacade';
import Plaque from '../../ui/hotel/Plaque';
import SignupCard from './SignupCard';
import { canPromise } from './features';
import { formatCount } from './useUnreadCounter';
import './HallHero.css';

// The hall: the unread figure, the hotel in cross-section, three plain lines on
// what happens, and the form. The lines only promise what this instance can do
// (features.js): without Google there is no label, no snooze and no undo to
// offer, so the second line names only the verbs this instance can perform.
export default function HallHero({ count, auth, isConfigured, night }) {
  const undo = canPromise('undo', isConfigured);
  const verbs = ['archiver', canPromise('label', isConfigured) && 'classer', canPromise('snooze', isConfigured) && 'reporter'].filter(Boolean);
  return (
    <section className="hl-hall" id="hall" data-floor="">
      <div className="hl-wrap">
        <h1 className="hl-h1">
          <span className="hl-h1__big">
            <span className="hl-count">{formatCount(count)}</span> non lus ?
          </span>
          <span className="hl-h1__sub">
            <em>On s'en occupe.</em> Vous validez.
          </span>
        </h1>
        <div className="hl-tri">
          <div className="hl-tri__lines">
            <Plaque n="I">
              <strong>Il lit</strong> l'expéditeur, l'objet et le début du message. Pas plus.
            </Plaque>
            <Plaque n="II">
              {verbs.length === 1 ? (
                <>
                  <strong>Il propose</strong> d'archiver ce qui traîne. Vous dites oui ou non.
                </>
              ) : (
                <>
                  <strong>Il propose</strong> : {verbs.join(', ')}. Vous dites oui ou non.
                </>
              )}
            </Plaque>
            {undo ? (
              <Plaque n="III">
                <strong>Tout s'annule</strong> en un clic. Même à 2 h du matin.
              </Plaque>
            ) : (
              <Plaque n="III">
                <strong>Rien ne bouge</strong> sans votre accord. Même à 2 h du matin.
              </Plaque>
            )}
          </div>
          <HotelFacade night={night} className="hl-tri__hotel" />
          <SignupCard auth={auth} isConfigured={isConfigured} />
        </div>
      </div>
      <div className="ht-slab" aria-hidden="true" />
    </section>
  );
}
