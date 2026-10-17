import React, { useEffect, useRef, useState } from 'react';
import { useLocation } from 'react-router-dom';

// Screens the lift serves. The landing, the legal pages and the OAuth return
// are not floors: arriving there is not a ride.
const FLOORS = ['/inbox', '/rules', '/snoozed', '/history', '/pricing', '/settings', '/account', '/connect'];

// How long the doors stay drawn: the opening animation (hotel-app.css,
// .hd-doors) plus a margin, after which they are taken out of the DOM.
const OPEN_MS = 650;

// The lift doors part each time the user changes floor. Pure decoration: the
// new screen is already rendered behind them, clicks go through, nothing is
// announced, and the CSS does not draw them for reduced motion.
export default function LiftDoors() {
  const { pathname } = useLocation();
  const previous = useRef(pathname);
  const [ride, setRide] = useState(0);

  useEffect(() => {
    if (previous.current === pathname) return undefined;
    const moved = FLOORS.includes(previous.current) && FLOORS.includes(pathname);
    previous.current = pathname;
    if (!moved) {
      setRide(0);
      return undefined;
    }
    setRide((n) => n + 1);
    const id = window.setTimeout(() => setRide(0), OPEN_MS);
    return () => window.clearTimeout(id);
  }, [pathname]);

  if (!ride) return null;
  // Keyed on the ride so a second move before the first finishes restarts it.
  return (
    <div key={ride} className="hd-doors" aria-hidden="true">
      <span />
      <span />
    </div>
  );
}
