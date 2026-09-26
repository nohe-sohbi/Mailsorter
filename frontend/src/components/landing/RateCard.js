import React, { useState } from 'react';
import { Link } from 'react-router-dom';
import { waitlistService, apiError } from '../../services/api';
import { forgetWaitlistJoin, hasJoinedWaitlist, rememberWaitlistJoin, waitlistEmail } from '../../lib/waitlist';
import { track } from '../../lib/analytics';
import FloorHeading from './FloorHeading';
import './RateCard.css';

// The rate card on the wall. With paid checkout closed it keeps the Pro
// waitlist (source "landing", remembered by lib/waitlist.js like on the
// pricing page); with it open it points to /pricing. Never rendered on a
// self-hosted instance, which bills nobody.
export default function RateCard({ floorNo, billingOn }) {
  const [email, setEmail] = useState(waitlistEmail);
  const [joined, setJoined] = useState(hasJoinedWaitlist);
  const [joining, setJoining] = useState(false);
  const [error, setError] = useState('');

  const join = async (event) => {
    event.preventDefault();
    const address = email.trim();
    if (!address || joining) return;
    setJoining(true);
    setError('');
    try {
      await waitlistService.join(address, 'landing');
      rememberWaitlistJoin(address);
      setJoined(true);
      // The source only: the address itself must never reach analytics.
      track('waitlist_join', { source: 'landing' });
    } catch (err) {
      setError(apiError(err, 'Inscription impossible pour le moment. Réessayez.'));
    } finally {
      setJoining(false);
    }
  };

  const reset = () => {
    forgetWaitlistJoin();
    setJoined(false);
    setError('');
  };

  const lines = [
    ['Gratuit', '200 tris par mois'],
    ['Toutes les fonctions', 'incluses'],
    ['Carte bancaire', 'jamais demandée'],
  ];

  return (
    <section className="hl-section" id="tarifs" data-floor="tarifs" aria-labelledby="hl-rates-title">
      <div className="hl-wrap">
        <FloorHeading n={floorNo} kicker="Tarifs" id="hl-rates-title">
          Gratuit jusqu'à <em>200 tris par mois.</em>
        </FloorHeading>
        <div className="hl-rate">
          <h3>Tarifs</h3>
          <div className="hl-rate__orn" aria-hidden="true">
            <i />
            <i />
            <i />
          </div>
          <dl>
            {lines.map(([label, value]) => (
              <div key={label} className="hl-rate__line">
                <dt>{label}</dt>
                <span className="hl-rate__dots" aria-hidden="true" />
                <dd>{value}</dd>
              </div>
            ))}
            <div className="hl-rate__line">
              <dt>Pro</dt>
              <span className="hl-rate__dots" aria-hidden="true" />
              <dd>
                <em>{billingOn ? 'illimité' : 'illimité, bientôt'}</em>
              </dd>
            </div>
          </dl>
          {billingOn ? (
            <div className="hl-rate__wl">
              <Link className="ht-btn ht-btn-primary" to="/pricing">
                Voir l'offre Pro
              </Link>
            </div>
          ) : joined ? (
            <div className="hl-rate__wl">
              <p role="status">C'est noté : on vous écrit une seule fois, le jour où Pro ouvre.</p>
              <button type="button" className="ht-link" onClick={reset}>
                Changer d'adresse
              </button>
            </div>
          ) : (
            <form className="hl-rate__wl" onSubmit={join}>
              <p>Laissez votre e-mail, on vous écrit une fois : le jour où Pro ouvre.</p>
              <div className="hl-rate__row">
                <input
                  className="ht-input"
                  type="email"
                  required
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="vous@exemple.fr"
                  aria-label="Votre adresse e-mail"
                />
                <button type="submit" className="ht-btn ht-btn-primary" disabled={joining}>
                  Prévenez-moi
                </button>
              </div>
              {error && (
                <p className="hl-rate__error" role="alert">
                  {error}
                </p>
              )}
            </form>
          )}
        </div>
      </div>
    </section>
  );
}
