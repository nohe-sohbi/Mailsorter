import React from 'react';
import HotelFacade from '../../ui/hotel/HotelFacade';
import { formatCount } from './useUnreadCounter';
import './NightExit.css';

// The last call, at night whatever the theme: the same figure as the hall, and
// both ways back up to the form.
export default function NightExit({ count, onStart, onSignIn }) {
  return (
    <section className="hl-exit" data-floor="" aria-labelledby="hl-exit-title">
      <div className="hl-wrap">
        <div className="hl-fh">
          <span className="hl-fh__k">Il est tard</span>
          <h2 id="hl-exit-title" className="hl-fh__title">
            Toujours <span className="hl-count">{formatCount(count)}</span> non lus ?
          </h2>
        </div>
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
