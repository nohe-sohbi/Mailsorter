import React, { useEffect, useState } from 'react';
import { NavLink, useNavigate, useLocation, Link } from 'react-router-dom';
import { cn } from '../ui/cn';
import { useTheme } from '../ui/theme';
import { Logo, Inbox, Settings, LogOut, Bolt, Tag, Clock, History, Menu, X, Sun, Moon, Monitor } from '../ui/icons';

const NAV_ITEMS = [
  { to: '/inbox', label: 'Boîte', Icon: Inbox },
  { to: '/rules', label: 'Règles', Icon: Tag },
  { to: '/snoozed', label: 'Reporté', Icon: Clock },
  { to: '/history', label: 'Historique', Icon: History },
  { to: '/pricing', label: 'Tarifs', Icon: Bolt },
  { to: '/settings', label: 'Réglages', Icon: Settings },
];

const THEME_META = {
  light: { Icon: Sun, label: 'Thème clair', next: 'sombre' },
  dark: { Icon: Moon, label: 'Thème sombre', next: 'système' },
  system: { Icon: Monitor, label: 'Thème système', next: 'clair' },
};

function ThemeButton({ className }) {
  const { theme, cycleTheme } = useTheme();
  const { Icon, label, next } = THEME_META[theme] || THEME_META.system;
  return (
    <button
      onClick={cycleTheme}
      className={cn('btn-ghost btn-sm btn-icon', className)}
      title={`${label} — basculer en ${next}`}
      aria-label={`${label}. Basculer en thème ${next}.`}
    >
      <Icon size={18} />
    </button>
  );
}

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

  // Hidden on auth/marketing surfaces. /pricing is public and ships its own
  // header, so rendering this one on top gave a signed-in visitor two stacked
  // headers and two logos.
  const hiddenPaths = ['/', '/setup', '/auth/callback', '/pricing'];
  if (!userEmail || hiddenPaths.includes(location.pathname)) {
    return null;
  }

  const initial = (userEmail[0] || '?').toUpperCase();

  // Real anchors, not buttons: middle-click, ctrl-click, "open in new tab" and
  // the browser's own affordances all come for free, and assistive technology
  // reads them as navigation instead of as six unlabelled controls.
  const navClass = ({ isActive }) =>
    cn(
      'flex items-center gap-2 rounded-lg px-3 py-2 text-sm font-semibold transition-colors',
      isActive ? 'bg-brand-50 text-brand-700' : 'text-muted hover:bg-ink-100 hover:text-ink-900'
    );

  return (
    <header className="sticky top-0 z-40 border-b border-hairline bg-surface/85 backdrop-blur-xl">
      <div className="mx-auto flex h-16 max-w-7xl items-center justify-between gap-4 px-4 sm:px-6">
        <div className="flex items-center gap-6">
          <Link to="/inbox" className="flex items-center gap-2.5 transition-opacity hover:opacity-80">
            <Logo size={30} />
            <span className="font-display text-lg font-extrabold tracking-tight text-ink-900">Mailsorter</span>
          </Link>
          <nav className="hidden items-center gap-1 lg:flex" aria-label="Navigation principale">
            {NAV_ITEMS.map(({ to, label, Icon }) => (
              <NavLink key={to} to={to} className={navClass}>
                <Icon size={17} />
                {label}
              </NavLink>
            ))}
          </nav>
        </div>

        <div className="flex items-center gap-2">
          <ThemeButton />
          {/* Clicking your own address should open your account, which is what
              everyone tries first. */}
          <NavLink
            to="/account"
            className={({ isActive }) =>
              cn(
                'hidden items-center gap-2.5 rounded-full border bg-surface py-1 pl-1 pr-3 shadow-soft transition-colors sm:flex',
                isActive ? 'border-brand-300 bg-brand-50' : 'border-hairline hover:border-ink-300 hover:bg-ink-100'
              )
            }
            title="Mon compte"
          >
            <span className="flex h-7 w-7 items-center justify-center rounded-full bg-brand-fill text-xs font-bold text-white">
              {initial}
            </span>
            <span className="max-w-[180px] truncate text-sm font-medium text-ink-700">{userEmail}</span>
          </NavLink>
          <button
            onClick={handleLogout}
            className="btn-ghost btn-sm btn-icon hidden sm:inline-flex"
            title="Se déconnecter"
            aria-label="Se déconnecter"
          >
            <LogOut size={18} />
          </button>

          {/* Mobile menu toggle. The breakpoint is lg, not sm: six tabs plus the
              identity chip do not fit on a tablet either. */}
          <button
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

      {/* Mobile navigation drawer */}
      {mobileOpen && (
        <div className="lg:hidden">
          <button
            className="fixed inset-0 top-16 z-30 cursor-default bg-ink-950/30 backdrop-blur-sm"
            aria-label="Fermer le menu"
            onClick={() => setMobileOpen(false)}
          />
          <nav
            id="mobile-nav"
            aria-label="Navigation principale"
            className="relative z-40 space-y-1 border-t border-hairline bg-surface px-4 pb-4 pt-3 shadow-card"
          >
            {NAV_ITEMS.map(({ to, label, Icon }) => (
              <NavLink
                key={to}
                to={to}
                className={({ isActive }) =>
                  cn(
                    'flex w-full items-center gap-3 rounded-lg px-3 py-3 text-sm font-semibold transition-colors',
                    isActive ? 'bg-brand-50 text-brand-700' : 'text-ink-700 hover:bg-ink-100 hover:text-ink-900'
                  )
                }
              >
                <Icon size={19} />
                {label}
              </NavLink>
            ))}

            <div className="mt-3 flex items-center justify-between gap-3 border-t border-hairline pt-3">
              <NavLink
                to="/account"
                className="flex min-w-0 items-center gap-2.5 rounded-lg px-1 py-1 text-left transition-colors hover:bg-ink-100"
                aria-label="Mon compte"
              >
                <span className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-brand-fill text-xs font-bold text-white">
                  {initial}
                </span>
                <span className="truncate text-sm font-medium text-ink-700">{userEmail}</span>
              </NavLink>
              <div className="flex shrink-0 items-center gap-1">
                <ThemeButton />
                <button
                  onClick={handleLogout}
                  className="btn-ghost btn-sm btn-icon"
                  title="Se déconnecter"
                  aria-label="Se déconnecter"
                >
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

export default Header;
