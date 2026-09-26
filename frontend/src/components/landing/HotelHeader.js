import React from 'react';
import Emblem from '../../ui/hotel/Emblem';
import ElevatorPanel from '../../ui/hotel/ElevatorPanel';
import { scrollToId } from './scroll';
import './HotelHeader.css';

// The floors the header can take you to. Tarifs is absent when the instance
// bills nobody, like everywhere else in the SPA.
export function floorsFor(selfHosted) {
  return [
    { n: 1, id: 'fonctionnement', label: 'Fonctionnement' },
    { n: 2, id: 'confidentialite', label: 'Confidentialité' },
    ...(selfHosted ? [] : [{ n: 3, id: 'tarifs', label: 'Tarifs' }]),
    { n: selfHosted ? 3 : 4, id: 'questions', label: 'Questions' },
  ];
}

export default function HotelHeader({ floors, active, onSignIn }) {
  const go = (event, id) => {
    event.preventDefault();
    scrollToId(id);
  };
  return (
    <header className="hl-top">
      <div className="hl-wrap hl-top__row">
        <a className="hl-brand" href="#hall" onClick={(event) => go(event, 'hall')}>
          <Emblem size={34} />
          <b>Mailsorter</b>
        </a>
        <ElevatorPanel items={floors} active={active} onGo={go} />
        <button type="button" className="hl-top__login" onClick={onSignIn}>
          Se connecter
        </button>
      </div>
    </header>
  );
}
