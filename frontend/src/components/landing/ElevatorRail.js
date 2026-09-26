import React, { useEffect, useRef } from 'react';
import './ElevatorRail.css';

// A thin shaft down the left edge on wide screens; the cabin follows the scroll.
export default function ElevatorRail() {
  const cab = useRef(null);

  useEffect(() => {
    const el = cab.current;
    if (!el) return undefined;
    const move = () => {
      const rail = el.parentElement;
      const max = document.documentElement.scrollHeight - window.innerHeight;
      const progress = max > 0 ? window.scrollY / max : 0;
      el.style.transform = `translateY(${Math.round(progress * (rail.clientHeight - el.offsetHeight))}px)`;
    };
    move();
    window.addEventListener('scroll', move, { passive: true });
    window.addEventListener('resize', move);
    return () => {
      window.removeEventListener('scroll', move);
      window.removeEventListener('resize', move);
    };
  }, []);

  return (
    <div className="hl-rail" aria-hidden="true">
      <div className="hl-rail__cab" ref={cab} />
    </div>
  );
}
