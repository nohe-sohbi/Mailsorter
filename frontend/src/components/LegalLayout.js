import React from 'react';
import { Link } from 'react-router-dom';
import PublicFooter from './PublicFooter';
import { Logo } from '../ui/icons';

// The shell both legal pages share: the logo back to the landing, a title, a
// last-updated date, then the prose. Kept in one place so the two pages cannot
// drift apart in layout or in typography.
//
// The prose styles are spelled out here rather than through a typography
// plugin: the project ships Tailwind alone, with no @tailwindcss/typography.
function LegalLayout({ title, updatedAt, intro, children }) {
  return (
    <div className="min-h-screen bg-ink-50">
      <div className="mx-auto max-w-3xl px-6 py-12">
        <Link to="/" className="mb-10 flex items-center gap-2.5 transition-opacity hover:opacity-80">
          <Logo size={30} />
          <span className="font-display text-lg font-extrabold tracking-tight text-ink-900">Mailsorter</span>
        </Link>

        <h1 className="font-display text-3xl font-extrabold tracking-tight text-ink-900">{title}</h1>
        <p className="mt-2 text-sm text-muted">Dernière mise à jour : {updatedAt}</p>
        {intro && <p className="mt-6 text-base leading-relaxed text-ink-700">{intro}</p>}

        <div className="mt-10 space-y-10">{children}</div>

        <PublicFooter />
      </div>
    </div>
  );
}

// One numbered section of a legal page.
export function LegalSection({ n, title, children }) {
  return (
    <section>
      <h2 className="font-display text-xl font-bold tracking-tight text-ink-900">
        <span className="text-brand-600">{n}.</span> {title}
      </h2>
      <div className="mt-3 space-y-3 text-sm leading-relaxed text-ink-700">{children}</div>
    </section>
  );
}

// A bulleted list with the app's own bullet treatment.
export function LegalList({ items }) {
  return (
    <ul className="space-y-2">
      {items.map((item, i) => (
        <li key={i} className="flex gap-2.5">
          <span aria-hidden className="mt-2 h-1.5 w-1.5 shrink-0 rounded-full bg-brand-500" />
          <span>{item}</span>
        </li>
      ))}
    </ul>
  );
}

export default LegalLayout;
