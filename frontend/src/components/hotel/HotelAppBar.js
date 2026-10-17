import React, { useEffect, useState } from 'react';
import { Link, NavLink } from 'react-router-dom';
import { accountService } from '../../services/api';
import Emblem from '../../ui/hotel/Emblem';
import { LogOut, Menu, X } from '../../ui/icons';
import { floorOf, floorsFor } from './floors';

// The monthly allowance, read for the gauge. Cosmetic: a failure hides the
// gauge, it never reaches the user as an error.
function useUsage(pathname) {
  const [usage, setUsage] = useState(null);
  useEffect(() => {
    let live = true;
    accountService
      .getUsage()
      .then(({ data }) => live && setUsage(data || null))
      .catch(() => live && setUsage(null));
    return () => {
      live = false;
    };
    // Re-read on each change of floor: an analysis run elsewhere moves it.
  }, [pathname]);
  return usage;
}

function Quota({ usage }) {
  if (!usage) return null;
  if (usage.plan === 'pro') {
    return (
      <Link to="/account" className="hd-quota" title="Votre plan">
        <span>Pro, illimité</span>
        <span className="hd-meter is-teal" aria-hidden="true">
          <i style={{ width: '100%' }} />
        </span>
      </Link>
    );
  }
  if (!(usage.limit > 0)) return null;
  const pct = Math.max(0, Math.min(100, Math.round(((usage.used || 0) / usage.limit) * 100)));
  return (
    <Link to="/account" className="hd-quota" title="Tris de l'IA ce mois-ci">
      <span>
        {usage.used || 0} / {usage.limit} tris
      </span>
      <span
        className={pct >= 90 ? 'hd-meter is-plum' : 'hd-meter'}
        role="progressbar"
        aria-valuenow={pct}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label="Quota mensuel utilisé"
      >
        <i style={{ width: `${pct}%` }} />
      </span>
    </Link>
  );
}

// The Grand Hotel header. Header.js owns the state and the behaviour (the
// drawer's focus trap, Escape, logout) and hands it here, so both themes
// share one implementation of everything but the drawing.
export default function HotelAppBar({
  pathname,
  selfHosted,
  userEmail,
  initials,
  themeButton,
  onLogout,
  mobileOpen,
  setMobileOpen,
  drawerRef,
  toggleRef,
}) {
  const floors = floorsFor(selfHosted);
  const here = floorOf(pathname, selfHosted);
  const usage = useUsage(pathname);
  const floorClass = ({ isActive }) => (isActive ? 'is-on' : undefined);

  return (
    <header className="hd-bar">
      <div className="hd-bar__in">
        <Link to="/inbox" className="hd-brand" aria-label="Mailsorter, retour à la boîte">
          <Emblem size={32} />
          <b>Mailsorter</b>
        </Link>

        <nav className="ht-panel hd-panel" aria-label="Navigation principale">
          {floors.map((f) => (
            <NavLink key={f.to} to={f.to} className={floorClass}>
              <i aria-hidden="true">{f.n}</i>
              <span>{f.label}</span>
            </NavLink>
          ))}
        </nav>

        {/* Below lg the panel folds into the drawer; the floor you are on
            stays in sight, lit like the button that brought you there. */}
        {here && (
          <span className="hd-here" aria-hidden="true">
            <i>{here.n}</i>
            {here.label}
          </span>
        )}

        <div className="hd-me">
          <Quota usage={usage} />
          {themeButton}
          <NavLink
            to="/account"
            className={({ isActive }) => `hd-av${isActive ? ' is-on' : ''}`}
            title={`Mon compte (${userEmail})`}
            aria-label="Mon compte"
          >
            {initials}
          </NavLink>
          <button onClick={onLogout} className="btn-ghost btn-sm btn-icon hidden sm:inline-flex" title="Se déconnecter" aria-label="Se déconnecter">
            <LogOut size={18} />
          </button>
          <button
            ref={toggleRef}
            onClick={() => setMobileOpen((o) => !o)}
            className="btn-ghost btn-sm btn-icon lg:hidden"
            aria-label={mobileOpen ? 'Fermer le menu' : 'Ouvrir le menu'}
            aria-expanded={mobileOpen}
            aria-controls="mobile-nav"
          >
            {mobileOpen ? <X size={20} /> : <Menu size={20} />}
          </button>
        </div>
      </div>

      {mobileOpen && (
        <div className="lg:hidden">
          <button className="hd-scrim" aria-label="Fermer le menu" onClick={() => setMobileOpen(false)} />
          <nav ref={drawerRef} id="mobile-nav" aria-label="Navigation principale" className="hd-drawer">
            <div className="hd-drawer__grid">
              {floors.map((f) => (
                <NavLink key={f.to} to={f.to} className={floorClass}>
                  <i aria-hidden="true">{f.n}</i>
                  {f.label}
                </NavLink>
              ))}
            </div>
            <div className="hd-drawer__foot">
              <NavLink to="/account" aria-label="Mon compte">
                <span className="hd-av" aria-hidden="true">
                  {initials}
                </span>
                <span>{userEmail}</span>
              </NavLink>
              <div className="flex shrink-0 items-center gap-1">
                {themeButton}
                <button onClick={onLogout} className="btn-ghost btn-sm btn-icon" title="Se déconnecter" aria-label="Se déconnecter">
                  <LogOut size={18} />
                </button>
              </div>
            </div>
          </nav>
        </div>
      )}
    </header>
  );
}
