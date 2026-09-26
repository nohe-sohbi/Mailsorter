import React, { useEffect, useState } from 'react';
import { configService } from '../../services/api';
import KeyTag from '../../ui/hotel/KeyTag';
import FloorHeading from './FloorHeading';
import { imapLegend } from './features';
import './ProviderBoard.css';

const TILTS = [-3, 2, -1, 3, -2, 2, -3, 1, -2, 3, -1, 2, -3, 1, -2];

// The key board: the mailbox catalog for this edition, as GET /api/providers
// returns it. Nothing is written in here: a provider on the board is one the
// server can reach. A key is gold when it reaches the Gmail API on an instance
// configured for Google, which is the only route with every feature today.
export default function ProviderBoard({ isConfigured }) {
  const [providers, setProviders] = useState(null);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    configService
      .getProviders()
      .then(({ data }) => {
        if (!cancelled) setProviders(data.providers || []);
      })
      .catch(() => {
        if (!cancelled) setFailed(true);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const keys = (providers || []).map((p, i) => {
    const routes = p.routes || [];
    const gold = Boolean(isConfigured) && routes.some((r) => r.transport === 'gmail-api');
    const imap = routes.some((r) => r.transport === 'imap');
    return { key: p.key, name: p.name, gold, note: gold ? 'via Google' : imap ? 'IMAP' : '', tilt: TILTS[i % TILTS.length] };
  });

  return (
    <section className="hl-section hl-section--alt" data-floor="confidentialite" aria-labelledby="hl-providers-title">
      <div className="hl-wrap">
        <FloorHeading id="hl-providers-title" lede="Même avec l'adresse Orange que vous avez depuis 2004.">
          Ça marche avec <em>votre boîte.</em>
        </FloorHeading>
        <div className="hl-board" aria-busy={providers === null && !failed}>
          {failed && <p className="hl-board__note">La liste des boîtes n'a pas pu être chargée. Rechargez la page pour la voir.</p>}
          {keys.map((k) => (
            <KeyTag key={k.key} name={k.name} note={k.note} gold={k.gold} tilt={k.tilt} />
          ))}
        </div>
        <p className="hl-legend">
          {keys.some((k) => k.gold) && (
            <span>
              <i className="is-gold" aria-hidden="true" />
              Toutes les fonctions
            </span>
          )}
          <span>
            <i aria-hidden="true" />
            {imapLegend()}
          </span>
        </p>
      </div>
    </section>
  );
}
