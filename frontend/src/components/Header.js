import React from 'react';
import { useNavigate, useLocation } from 'react-router-dom';
import { cn } from '../ui/cn';
import { Logo, Inbox, Settings, LogOut, Bolt } from '../ui/icons';

function Header() {
  const navigate = useNavigate();
  const location = useLocation();
  const userEmail = localStorage.getItem('userEmail');

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
          <div className="hidden items-center gap-2.5 rounded-full border border-ink-200 bg-white py-1 pl-1 pr-3 shadow-soft sm:flex">
            <span className="flex h-7 w-7 items-center justify-center rounded-full bg-brand-gradient text-xs font-bold text-white">
              {initial}
            </span>
            <span className="max-w-[180px] truncate text-sm font-medium text-ink-600">{userEmail}</span>
          </div>
          <button
            onClick={handleLogout}
            className="btn-ghost px-2.5"
            title="Se déconnecter"
            aria-label="Se déconnecter"
          >
            <LogOut size={18} />
          </button>
        </div>
      </div>
    </header>
  );
}

export default Header;
