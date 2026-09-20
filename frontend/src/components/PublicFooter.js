import React from 'react';
import { Link } from 'react-router-dom';
import { Shield, Code, Mail } from '../ui/icons';

// The footer of every page a logged-out visitor can reach.
//
// It exists because the public surface had no way out of itself: no legal
// pages, no contact, no link to the source. That is not only a courtesy
// problem. Google's OAuth verification asks for a privacy policy reachable
// FROM THE HOME PAGE before it will approve the Gmail scopes, which is exactly
// the step /setup tells the operator to take ("Publier l'application").
//
// Rendered by Login, Pricing and the two legal pages, so the links can never be
// present on one and missing on another.
export const CONTACT_EMAIL = 'nohe@sohbi.dev';
export const SOURCE_URL = 'https://github.com/nohe-sohbi/Mailsorter';

function PublicFooter({ className = '' }) {
  return (
    <footer className={`mt-16 border-t border-hairline pt-8 text-sm text-muted ${className}`}>
      <div className="flex flex-col gap-6 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <span className="block font-semibold text-ink-700">Mailsorter</span>
          <span className="mt-1 flex items-center gap-2 text-xs">
            <Shield size={13} /> Vos emails ne quittent jamais votre contrôle.
          </span>
        </div>

        <nav aria-label="Liens de bas de page" className="flex flex-wrap gap-x-6 gap-y-2">
          <Link to="/confidentialite" className="transition-colors hover:text-ink-900">
            Confidentialité
          </Link>
          <Link to="/conditions" className="transition-colors hover:text-ink-900">
            Conditions d'utilisation
          </Link>
          <a
            href={`mailto:${CONTACT_EMAIL}`}
            className="flex items-center gap-1.5 transition-colors hover:text-ink-900"
          >
            <Mail size={13} /> Contact
          </a>
          <a
            href={SOURCE_URL}
            target="_blank"
            rel="noopener noreferrer"
            className="flex items-center gap-1.5 transition-colors hover:text-ink-900"
          >
            <Code size={13} /> Code source
          </a>
        </nav>
      </div>

      <p className="mt-6 text-xs">&copy; {new Date().getFullYear()} Mailsorter</p>
    </footer>
  );
}

export default PublicFooter;
