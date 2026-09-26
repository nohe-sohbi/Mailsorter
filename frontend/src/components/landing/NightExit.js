import React from 'react';
import HotelFacade from '../../ui/hotel/HotelFacade';
import FloorHeading from './FloorHeading';
import { formatCount } from './useUnreadCounter';
import './NightExit.css';

// The last call, at night whatever the theme: the same figure as the hall, and
// both ways back up to the form.
export default function NightExit({ count, onStart, onSignIn }) {
  return (
    <section className="hl-exit" data-floor="" aria-labelledby="hl-exit-title">
      <div className="hl-wrap">
        <FloorHeading kicker="Il est tard" id="hl-exit-title">
          Toujours <span className="hl-count">{formatCount(count)}</span> non lus ?
        </FloorHeading>
        <div className="hl-exit__cta">
          <button type="button" className="ht-btn ht-btn-gold" onClick={onStart}>
            Faire le tri
          </button>
          <button type="button" className="hl-exit__link" onClick={onSignIn}>
            Se connecter
          </button>
        </div>
        <HotelFacade night className="hl-exit__facade" title="L'hôtel la nuit, fenêtres allumées" />
      </div>
    </section>
  );
}
