import React, { useEffect, useState } from 'react';
import { cn } from './cn';
import { useHotel } from './hotel/HotelTheme';

// Retour en haut de page : bouton flottant qui n'apparaît que quand
// l'utilisateur a fait défiler la page. Ajout pur (aucun composant, aucune
// dépendance, aucune modification du code existant) - respecte le design
// system (tokens CSS vars du Grand Hotel + rampe ink/surface de l'UI classique).

const SCROLL_THRESHOLD = 400;

export default function BackToTop({ className = '' }) {
  const hotel = useHotel();
  const [shown, setShown] = useState(false);
  const [dark, setDark] = useState(false);

  useEffect(() => {
    const html = document.documentElement;
    setDark(html.classList.contains('dark'));

    const reconcile = () => {
      setShown(window.scrollY > SCROLL_THRESHOLD);
    };

    reconcile();
    window.addEventListener('scroll', reconcile, { passive: true });
    window.addEventListener('resize', reconcile, { passive: true });
    return () => {
      window.removeEventListener('scroll', reconcile);
      window.removeEventListener('resize', reconcile);
    };
  }, []);

  if (!shown) return null;

  const bg =
    hotel || dark
      ? 'var(--h-surface)'
      : 'rgb(var(--surface))';
  const textColor =
    hotel || dark
      ? 'var(--h-text)'
      : 'rgb(var(--ink-900))';
  const borderColor = hotel
    ? 'var(--h-hair)'
    : 'rgb(var(--hairline))';

  return (
    <button
      type="button"
      className={cn(
        'fixed bottom-6 end-6 z-50 flex items-center justify-center rounded-full px-4 py-3 text-sm font-semibold shadow-card transition duration-300 ease-out focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-600 focus-visible:ring-offset-2 hover:scale-105',
        'bg-surface text-ink-900',
        className
      )}
      style={{
        ...(hotel
          ? {
              backgroundColor: 'var(--h-surface)',
              color: 'var(--h-text)',
              borderColor: 'var(--h-hair)',
            }
          : {
              backgroundColor: 'rgb(var(--surface))',
              color: 'rgb(var(--ink-900))',
              borderColor: 'rgb(var(--hairline))',
            }),
      }}
      aria-label="Retour en haut de page"
      title="Retour en haut"
      onClick={(e) => {
        e.preventDefault();
        window.scrollTo({ top: 0, behavior: 'smooth' });
      }}
    >
      <svg
        className="h-5 w-5"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2.5"
        strokeLinecap="round"
        strokeLinejoin="round"
        aria-hidden
      >
        <path d="M12 19V5" />
        <path d="m5 12 7-7 7 7" />
      </svg>
    </button>
  );
}
