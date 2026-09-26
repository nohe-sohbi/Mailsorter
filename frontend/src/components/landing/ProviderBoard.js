import React from 'react';
import KeyTag from '../../ui/hotel/KeyTag';
import FloorHeading from './FloorHeading';
import { canConnect, imapLegend } from './features';
import './ProviderBoard.css';

const TILTS = [-3, 2, -1, 3, -2, 2, -3, 1, -2, 3, -1, 2, -3, 1, -2];

// The key board: the mailbox catalog for this edition, as GET /api/providers
// returns it (fetched by HotelLanding, which shares it with the FAQ). Nothing
// is written in here. A key is gold when it reaches the Gmail API on an
// instance configured for Google, which is the only route with every feature
// today. A provider no implemented transport reaches (features.js) keeps its
// key, muted and marked "bientôt": dropping it would hide that Outlook is
// coming, and a pink key would promise a connection that fails.
export default function ProviderBoard({ isConfigured, providers, failed }) {
  const keys = (providers || []).map((p, i) => {
    const routes = p.routes || [];
    const soon = !canConnect(p, isConfigured);
    const gold = !soon && Boolean(isConfigured) && routes.some((r) => r.transport === 'gmail-api');
    return { key: p.key, name: p.name, gold, soon, note: soon ? 'bientôt' : gold ? 'via Google' : 'IMAP', tilt: TILTS[i % TILTS.length] };
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
            <KeyTag key={k.key} name={k.name} note={k.note} gold={k.gold} soon={k.soon} tilt={k.tilt} />
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
          {keys.some((k) => k.soon) && (
            <span>
              <i className="is-soon" aria-hidden="true" />
              Pas encore branché
            </span>
          )}
        </p>
      </div>
    </section>
  );
}
