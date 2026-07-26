import React, { useEffect, useState } from 'react';
import { useNavigate, useLocation } from 'react-router-dom';
import { cn } from '../ui/cn';
import { Logo, Inbox, Settings, LogOut, Bolt, Tag, Clock, History, Menu, X } from '../ui/icons';

function Header() {
  const navigate = useNavigate();
  const location = useLocation();
  const userEmail = localStorage.getItem('userEmail');
  const [mobileOpen, setMobileOpen] = useState(false);

  // Close the mobile menu whenever the route changes.
  useEffect(() => {
    setMobileOpen(false);
  }, [location.pathname]);

  const handleLogout = () => {
    localStorage.removeItem('userEmail');
    localStorage.removeItem('accessToken');
    navigate('/');
  };

  // Hidden on auth/marketing surfaces.
  const hiddenPaths = ['/', '/setup', '/auth/callback'];
  if (!userEmail || hiddenPaths.includes(location.pathname)) {
    return null;
  }

  const navItems = [
    { to: '/inbox', label: 'Boîte', Icon: Inbox },
    { to: '/rules', label: 'Règles', Icon: Tag },
    { to: '/snoozed', label: 'Reporté', Icon: Clock },
    { to: '/history', label: 'Historique', Icon: History },
    { to: '/pricing', label: 'Tarifs', Icon: Bolt },
    { to: '/settings', label: 'Réglages', Icon: Settings },
  ];

  const initial = (userEmail[0] || '?').toUpperCase();

  return (
    <header className="sticky top-0 z-40 border-b border-ink-200/70 bg-white/80 backdrop-blur-xl">
      <div className="mx-auto flex h-16 max-w-7xl items-center justify-between gap-4 px-4 sm:px-6">
        <div className="flex items-center gap-6">
          <button
            onClick={() => navigate('/inbox')}
            className="flex items-center gap-2.5 transition-opacity hover:opacity-80"
          >
            <Logo size={30} />
            <span className="font-display text-lg font-extrabold tracking-tight text-ink-900">Mailsorter</span>
          </button>
          <nav className="hidden items-center gap-1 sm:flex">
            {navItems.map(({ to, label, Icon }) => {
              const active = location.pathname === to;
              return (
                <button
                  key={to}
                  onClick={() => navigate(to)}
                  className={cn(
                    'flex items-center gap-2 rounded-lg px-3 py-2 text-sm font-semibold transition-colors',
                    active ? 'bg-brand-50 text-brand-700' : 'text-ink-500 hover:bg-ink-100 hover:text-ink-900'
                  )}
                >
                  <Icon size={17} />
                  {label}
                </button>
              );
            })}
          </nav>
        </div>

        <div className="flex items-center gap-3">
          {/* Clicking your own address should open your account, which is what
              everyone tries first. It used to be an inert div. */}
          <button
            onClick={() => navigate('/account')}
            aria-current={location.pathname === '/account' ? 'page' : undefined}
            title="Mon compte"
            className={cn(
              'hidden items-center gap-2.5 rounded-full border bg-white py-1 pl-1 pr-3 shadow-soft transition-colors sm:flex',
              location.pathname === '/account'
                ? 'border-brand-300 bg-brand-50'
                : 'border-ink-200 hover:border-ink-300 hover:bg-ink-50'
            )}
          >
            <span className="flex h-7 w-7 items-center justify-center rounded-full bg-brand-600 text-xs font-bold text-white">
              {initial}
            </span>
            <span className="max-w-[180px] truncate text-sm font-medium text-ink-600">{userEmail}</span>
          </button>
          <button
            onClick={handleLogout}
            className="btn-ghost hidden px-2.5 sm:inline-flex"
            title="Se déconnecter"
            aria-label="Se déconnecter"
          >
            <LogOut size={18} />
          </button>

          {/* Mobile menu toggle */}
          <button
            onClick={() => setMobileOpen((o) => !o)}
            className="btn-ghost px-2.5 sm:hidden"
            aria-label={mobileOpen ? 'Fermer le menu' : 'Ouvrir le menu'}
            aria-expanded={mobileOpen}
            aria-controls="mobile-nav"
          >
            {mobileOpen ? <X size={20} /> : <Menu size={20} />}
          </button>
        </div>
      </div>

      {/* Mobile navigation drawer */}
      {mobileOpen && (
        <div className="sm:hidden">
          {/* Backdrop */}
          <button
            className="fixed inset-0 top-16 z-30 cursor-default bg-ink-900/20 backdrop-blur-sm"
            aria-label="Fermer le menu"
            onClick={() => setMobileOpen(false)}
          />
          <nav
            id="mobile-nav"
            className="relative z-40 space-y-1 border-t border-ink-200/70 bg-white px-4 pb-4 pt-3 shadow-card"
          >
            {navItems.map(({ to, label, Icon }) => {
              const active = location.pathname === to;
              return (
                <button
                  key={to}
                  onClick={() => navigate(to)}
                  className={cn(
                    'flex w-full items-center gap-3 rounded-lg px-3 py-3 text-sm font-semibold transition-colors',
                    active ? 'bg-brand-50 text-brand-700' : 'text-ink-600 hover:bg-ink-100 hover:text-ink-900'
                  )}
                >
                  <Icon size={19} />
                  {label}
                </button>
              );
            })}

            <div className="mt-3 flex items-center justify-between gap-3 border-t border-ink-200/70 pt-3">
              <div className="flex min-w-0 items-center gap-2.5">
                <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-brand-600 text-xs font-bold text-white">
                  {initial}
                </span>
                <span className="truncate text-sm font-medium text-ink-600">{userEmail}</span>
              </div>
              <button
                onClick={handleLogout}
                className="btn-ghost shrink-0 px-2.5"
                title="Se déconnecter"
                aria-label="Se déconnecter"
              >
                <LogOut size={18} />
              </button>
            </div>
          </nav>
        </div>
      )}
    </header>
  );
}

export default Header;
