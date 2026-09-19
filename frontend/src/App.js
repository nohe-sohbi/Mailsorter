import React, { useEffect } from 'react';
import { BrowserRouter as Router, Routes, Route, Navigate, useNavigate } from 'react-router-dom';
import Login from './pages/Login';
import Inbox from './pages/Inbox';
import Setup from './pages/Setup';
import Settings from './pages/Settings';
import Account from './pages/Account';
import Rules from './pages/Rules';
import Snoozed from './pages/Snoozed';
import Connect from './pages/Connect';
import History from './pages/History';
import Pricing from './pages/Pricing';
import AuthCallback from './pages/AuthCallback';
import Header from './components/Header';
import { EmailProvider } from './contexts/EmailContext';
import { InstanceProvider, useInstance } from './contexts/InstanceContext';
import { ToastProvider } from './ui/Toast';
import { ConfirmProvider } from './ui/Confirm';
import { ThemeProvider } from './ui/theme';
import { Logo, Alert, Inbox as InboxIcon } from './ui/icons';
import Spinner from './ui/Spinner';

function BootScreen({ children }) {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center gap-6 bg-ink-50 px-6 text-center">
      {children}
    </div>
  );
}

// RequireAuth is the single place the session is checked. It used to be a
// per-page copy-paste that four of the six screens simply skipped: they fired
// their API calls, took a 401, and the axios interceptor recovered with a full
// browser reload, losing all state and flashing the login page.
function RequireAuth({ children }) {
  const navigate = useNavigate();
  const authed = Boolean(localStorage.getItem('userEmail') && localStorage.getItem('accessToken'));

  useEffect(() => {
    if (!authed) navigate('/', { replace: true });
  }, [authed, navigate]);

  return authed ? children : null;
}

function NotFound() {
  const navigate = useNavigate();
  return (
    <div className="mx-auto flex min-h-[60vh] max-w-lg flex-col items-center justify-center px-6 text-center">
      <span className="mb-5 flex h-14 w-14 items-center justify-center rounded-2xl bg-ink-100 text-ink-600">
        <InboxIcon size={26} />
      </span>
      <h1 className="font-display text-2xl font-extrabold tracking-tight text-ink-900">Page introuvable</h1>
      <p className="mt-2 text-sm text-muted">
        Cette adresse ne correspond à aucun écran de Mailsorter.
      </p>
      <button onClick={() => navigate('/inbox')} className="btn-primary mt-6">
        Retour à ma boîte
      </button>
    </div>
  );
}

function App() {
  // The deployment is probed once, in InstanceProvider, and read here. Pricing
  // and the header read the same value instead of asking again.
  const { loading, error, isConfigured, selfHosted, reload } = useInstance();

  if (loading) {
    return (
      <BootScreen>
        <div className="animate-fade-up">
          <Logo size={56} />
        </div>
        <div className="flex items-center gap-3 text-muted">
          <Spinner size={18} className="text-brand-600" />
          <span className="text-sm font-medium">Démarrage de Mailsorter…</span>
        </div>
      </BootScreen>
    );
  }

  if (error && !isConfigured) {
    return (
      <BootScreen>
        <span className="flex h-14 w-14 items-center justify-center rounded-2xl bg-danger-50 text-danger-600">
          <Alert size={28} />
        </span>
        <div className="max-w-sm space-y-2">
          <h1 className="text-xl font-bold text-ink-900">Le moteur ne répond pas</h1>
          <p className="text-sm text-muted">{error} Vérifiez que le backend tourne, puis réessayez.</p>
        </div>
        <button onClick={reload} className="btn-primary">
          Réessayer
        </button>
      </BootScreen>
    );
  }

  const guard = (element) => (isConfigured ? <RequireAuth>{element}</RequireAuth> : <Navigate to="/setup" replace />);

  return (
    <Router>
      <ToastProvider>
        <ConfirmProvider>
          <EmailProvider>
            <div className="min-h-screen bg-ink-50">
              <a
                href="#main"
                className="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-4 focus:z-[200] focus:rounded-lg focus:bg-brand-fill focus:px-4 focus:py-2 focus:text-sm focus:font-semibold focus:text-white"
              >
                Aller au contenu
              </a>
              <Header />
              <main id="main">
                <Routes>
                  <Route
                    path="/setup"
                    element={isConfigured ? <Navigate to="/" replace /> : <Setup onComplete={reload} />}
                  />
                  <Route path="/" element={isConfigured ? <Login /> : <Navigate to="/setup" replace />} />
                  <Route path="/inbox" element={guard(<Inbox />)} />
                  <Route path="/rules" element={guard(<Rules />)} />
                  <Route path="/snoozed" element={guard(<Snoozed />)} />
                  <Route path="/history" element={guard(<History />)} />
                  <Route path="/settings" element={guard(<Settings />)} />
                  {/* Brancher une boite par IMAP. Garde comme les autres: on
                      branche une boite SUR un compte, donc il faut deja etre
                      connecte a Mailsorter. */}
                  <Route path="/connect" element={guard(<Connect />)} />
                  <Route path="/account" element={guard(<Account />)} />
                  {/* A self-hosted instance bills nobody, so there is no pricing
                      page to land on: the route redirects rather than rendering
                      an empty one. */}
                  <Route path="/pricing" element={selfHosted ? <Navigate to="/" replace /> : <Pricing />} />
                  <Route path="/auth/callback" element={<AuthCallback />} />
                  {/* Redirects for legacy routes */}
                  <Route path="/emails" element={<Navigate to="/inbox" replace />} />
                  <Route path="/triage" element={<Navigate to="/inbox" replace />} />
                  {/* Anything else used to render a blank page under the header. */}
                  <Route path="*" element={<NotFound />} />
                </Routes>
              </main>
            </div>
          </EmailProvider>
        </ConfirmProvider>
      </ToastProvider>
    </Router>
  );
}

// The theme must wrap the boot screens too, otherwise the first thing a
// dark-theme user sees is a full-white splash.
export default function AppWithTheme() {
  return (
    <ThemeProvider>
      <InstanceProvider>
        <App />
      </InstanceProvider>
    </ThemeProvider>
  );
}
