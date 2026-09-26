import React from 'react';
import { Link } from 'react-router-dom';
import { THEMES, useTheme } from '../../ui/theme';
import { CONTACT_EMAIL, SOURCE_URL } from '../PublicFooter';
import './HotelFooter.css';

const LABELS = { light: 'Jour', dark: 'Nuit', system: 'Auto' };

// The pavement under the hotel: the legal links every public page must carry
// (Google's OAuth review asks for the privacy policy from the home page), and
// the day / night / auto switch, which is the app's own theme setting.
export default function HotelFooter() {
  const { theme, setTheme } = useTheme();
  return (
    <footer className="hl-walk">
      <div className="hl-wrap hl-walk__row">
        <nav className="hl-walk__links" aria-label="Liens utiles">
          <Link to="/confidentialite">Confidentialité</Link>
          <Link to="/conditions">Conditions</Link>
          <a href={`mailto:${CONTACT_EMAIL}`}>Contact</a>
          <a href={SOURCE_URL} target="_blank" rel="noreferrer">
            Code source
          </a>
        </nav>
        <div className="hl-walk__theme" role="group" aria-label="Thème">
          {THEMES.map((t) => (
            <button key={t} type="button" aria-pressed={theme === t} className={theme === t ? 'is-on' : undefined} onClick={() => setTheme(t)}>
              {LABELS[t]}
            </button>
          ))}
        </div>
      </div>
    </footer>
  );
}
