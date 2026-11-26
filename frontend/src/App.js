import React, { Suspense, lazy, useEffect, useMemo } from 'react';
import { BrowserRouter as Router, Routes, Route, Navigate, useLocation, useNavigate } from 'react-router-dom';
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
import Privacy from './pages/Privacy';
import Terms from './pages/Terms';
import Header from './components/Header';
import { EmailProvider, useMailbox } from './contexts/EmailContext';
import { InstanceProvider, useInstance } from './contexts/InstanceContext';
import { ToastProvider } from './ui/Toast';
import { ConfirmProvider } from './ui/Confirm';
import { ThemeProvider } from './ui/theme';
import { Logo, Alert, Inbox as InboxIcon, Mail } from './ui/icons';
import { CONTACT_EMAIL } from './components/PublicFooter';
import Spinner from './ui/Spinner';
import { isAuthed, sessionOwner } from './lib/session';
import { effectiveUiTheme, bootUiTheme } from './lib/uiTheme';
import BackToTop from './ui/BackToTop';
import { HotelThemeProvider, useHotel, loadHotelStyles } from './ui/hotel/HotelTheme';
import { useHotelFonts } from './ui/hotel/useHotelFonts';
import Emblem from './ui/hotel/Emblem';
import Door from './ui/hotel/Door';
import LiftDoors from './components/hotel/LiftDoors';

// The theme the boot screen is drawn in, decided before the instance has
// answered (see bootUiTheme). Read once: the URL and storage it reads from do
// not change while the app boots.
const BOOT_HOTEL = typeof window !== 'undefined' && bootUiTheme(window.location.search) === 'hotel';
// Start the skin and its fonts now rather than after GET /api/config/status:
// the two requests then run side by side. If the server ends up saying
// classic, the chunk is simply never applied.
if (BOOT_HOTEL) loadHotelStyles().catch(() => {});

// The hotel splash is styled inline: it is on screen before the hotel
// stylesheet has arrived, which is the whole point of it.
function HotelBoot({ label }) {
  const night = typeof document !== 'undefined' && document.documentElement.classList.contains('dark');
  return (
    <div
      className="flex min-h-screen flex-col items-center justify-center gap-4 px-6 text-center"
      style={{ background: night ? '#1C1220' : '#F6E3D9', color: night ? '#F3E6DA' : '#2B1B1E' }}
    >
      <div className="animate-fade-up">
        <Emblem size={64} />
      </div>
      <span
        style={{
          fontFamily: "'Bodoni Moda', Didot, 'Times New Roman', serif",
          fontStyle: 'italic',
          fontWeight: 500,
          fontSize: 26,
        }}
      >
        Mailsorter
      </span>
      <span className="flex items-center gap-2 text-sm" style={{ color: night ? '#BFA6A8' : '#6E4B52' }}>
        <Spinner size={16} /> {label}
      </span>
    </div>
  );
}

function BootScreen({ children }) {
  return (
    <div className="flex min-h-screen flex-col items-center justify-center gap-6 bg-ink-50 px-6 text-center">
      {children}
    </div>
  );
}

// What a visitor sees when a HOSTED instance is not wired up.
//
// Until now every public route redirected to /setup, which asks the reader to
// fill in GMAIL_CLIENT_ID, GMAIL_CLIENT_SECRET and GMAIL_REDIRECT_URL and then
// restart the service. On a self-hosted instance that is the right screen: the
// reader owns the deployment. On a hosted one the visitor owns none of it, and
// was handed an environment-variable briefing they could do nothing with.
function Unavailable() {
  return (
    <BootScreen>
      <Logo size={52} />
      <div className="max-w-sm space-y-2">
        <h1 className="font-display text-xl font-bold text-ink-900">Service momentanément indisponible</h1>
        <p className="text-sm text-muted">
          Cette instance de Mailsorter n'offre encore aucune façon de se connecter. Elle sera
          ouverte dès que l'exploitant aura terminé la configuration.
        </p>
      </div>
      <a href={`mailto:${CONTACT_EMAIL}`} className="btn-secondary">
        <Mail size={16} /> Prévenir l'exploitant
      </a>
    </BootScreen>
  );
}

// A deploy (every push to main) renames every chunk. A tab opened before it
// still runs the old main bundle, asks for a chunk hash that no longer exists,
// and a rejected lazy import unmounts the whole tree. Reloading fetches the new
// index.html and its new hashes. Once only: the flag, set before the reload and
// cleared by the next chunk that loads, stops a chunk that is really missing
// from reloading forever, and without storage there is no flag, so no reload.
const CHUNK_RELOAD_FLAG = 'mailsorter_chunk_reload';

function lazyWithReload(load) {
  return lazy(() =>
    load().then(
      (module) => {
        try {
          sessionStorage.removeItem(CHUNK_RELOAD_FLAG);
        } catch {
          // Storage blocked: there is no flag to clear.
        }
        return module;
      },
      (err) => {
        let reload = false;
        try {
          if (sessionStorage.getItem(CHUNK_RELOAD_FLAG) !== '1') {
            sessionStorage.setItem(CHUNK_RELOAD_FLAG, '1');
            reload = true;
          }
        } catch {
          // Storage blocked: a second failure would look like a first, so no reload.
        }
        if (!reload) throw err;
        window.location.reload();
        // Never settles: the fallback stays up until the reload replaces the
        // page, instead of the error screen flashing in between.
        return new Promise(() => {});
      }
    )
  );
}

// Both landings are separate chunks: a visitor downloads only the one this
// instance serves, fonts included.
const ClassicLanding = lazyWithReload(() => import('./pages/Login'));
const HotelLanding = lazyWithReload(() => import('./pages/HotelLanding'));

// The only class component in the app: React catches a render error only in a
// class. It holds the landing chunk that still fails after the one reload, so
// the visitor gets a way out instead of a blank page. React already logs the
// error it caught; this only decides what to render.
class LandingErrorBoundary extends React.Component {
  constructor(props) {
    super(props);
    this.state = { failed: false };
  }

  static getDerivedStateFromError() {
    return { failed: true };
  }

  render() {
    if (!this.state.failed) return this.props.children;
    return (
      <BootScreen>
        <span className="flex h-14 w-14 items-center justify-center rounded-2xl bg-danger-50 text-danger-600">
          <Alert size={28} />
        </span>
        <div className="max-w-sm space-y-2">
          <h1 className="text-xl font-bold text-ink-900">La page n'a pas pu se charger.</h1>
        </div>
        <button onClick={() => window.location.reload()} className="btn-primary">
          Recharger
        </button>
      </BootScreen>
    );
  }
}

// The landing on /, chosen at runtime from UI_THEME (see lib/uiTheme.js), so
// the Grand Hotel theme can be unplugged without a rebuild.
function Landing() {
  const { uiTheme } = useInstance();
  const { search } = useLocation();
  const theme = useMemo(() => effectiveUiTheme(uiTheme, search), [uiTheme, search]);
  const Page = theme === 'hotel' ? HotelLanding : ClassicLanding;
  return (
    <LandingErrorBoundary>
      <Suspense
        fallback={
          <BootScreen>
            <Spinner size={18} className="text-brand-600" />
          </BootScreen>
        }
      >
        <Page />
      </Suspense>
    </LandingErrorBoundary>
  );
}

// RequireAuth is the single place the session is checked. It used to be a
// per-page copy-paste that four of the six screens simply skipped: they fired
// their API calls, took a 401, and the axios interceptor recovered with a full
// browser reload, losing all state and flashing the login page.
function RequireAuth({ children }) {
  const navigate = useNavigate();
  const authed = isAuthed();

  useEffect(() => {
    if (!authed) navigate('/', { replace: true });
  }, [authed, navigate]);

  return authed ? children : null;
}

// RequireMailbox holds a screen until the shared answer to "which mailbox is
// this" (useMailbox, GET /api/mailbox) is in, or has failed.
//
// Every screen it wraps draws controls from that answer: the inbox's filters,
// counters, Abonnements tab and welcome levers, the history's subtitle, the
// recap card in Réglages. Drawn before it, a Gmail account saw three filters
// become six and the hotel's numbered plaques renumber, and an IMAP account
// saw Gmail-only controls for a moment. The wait is one Mongo lookup, once per
// session (the answer is shared), and the loader only fades in if it lasts.
//
// `redirect` sends an account with no mailbox to /connect, as before: only
// when this browser has not already seen one (`hasMailbox`), and never on a
// failed request, which says nothing about the mailbox.
function RequireMailbox({ children, redirect = true }) {
  const navigate = useNavigate();
  const { mailbox, known, settled, generation } = useMailbox();
  const remembered = Boolean(localStorage.getItem('hasMailbox'));
  const missing = redirect && known && !mailbox && !remembered;

  useEffect(() => {
    if (!redirect || !known) return;
    if (mailbox) localStorage.setItem('hasMailbox', 'true');
    else if (!remembered) navigate('/connect', { replace: true });
  }, [redirect, known, mailbox, remembered, navigate]);

  if (!settled) {
    return (
      <div className="flex min-h-[60vh] items-center justify-center px-6">
        <div className="flex animate-fade-in items-center gap-3 text-muted" style={{ animationDelay: '400ms' }}>
          <Spinner size={18} className="text-brand-600" />
          <span className="text-sm font-medium">Chargement de votre boîte...</span>
        </div>
      </div>
    );
  }

  // Keyed on the mailbox's generation: once another mailbox is connected (a
  // 409 mailboxChanged, a connection from /connect), the screen starts again
  // and loads what it shows of the new one, rather than keep the other's.
  return missing ? null : <React.Fragment key={generation ?? 'none'}>{children}</React.Fragment>;
}

// The page under the header. The hotel leaves it transparent so the striped
// wallpaper on <body> shows through, and opens the lift doors on every change
// of floor.
// The inbox store, scoped to whoever is signed in. EmailProvider caches a
// mailbox for five minutes and sits above every route, so on its own it outlived
// a sign-out: the next person to sign in on the same browser was served the
// previous one's emails from that cache, without a single request. Keying it on
// the session owner remounts it, empty, the moment the owner changes. Reading
// the location is what makes this re-render on the navigation that follows a
// sign-in or a sign-out, since localStorage itself notifies nobody.
function SessionEmailProvider({ children }) {
  useLocation();
  return <EmailProvider key={sessionOwner() || 'signed-out'}>{children}</EmailProvider>;
}

function AppFrame({ children }) {
  const hotel = useHotel();
  if (!hotel) return <div className="min-h-screen bg-ink-50">{children}</div>;
  return (
    <div className="min-h-screen">
      <LiftDoors />
      {children}
    </div>
  );
}

function NotFound() {
  const navigate = useNavigate();
  const hotel = useHotel();
  if (hotel) {
    return (
      <div className="hd-empty mx-auto flex min-h-[60vh] max-w-lg justify-center">
        <Door number="404" color="#7A2E3B" crop className="hd-empty__art" />
        <h1 className="hd-title mt-5">Page introuvable</h1>
        <p>Cette adresse ne correspond à aucun écran de Mailsorter.</p>
        <button onClick={() => navigate('/inbox')} className="btn-primary hd-empty__act">
          Retour à ma boîte
        </button>
      </div>
    );
  }
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
  const { loading, error, isConfigured, isUsable, selfHosted, reload } = useInstance();
  useHotelFonts(BOOT_HOTEL);

  if (loading && BOOT_HOTEL) return <HotelBoot label="Démarrage de Mailsorter..." />;

  if (loading) {
    return (
      <BootScreen>
        <div className="animate-fade-up">
          <Logo size={56} />
        </div>
        <div className="flex items-center gap-3 text-muted">
          <Spinner size={18} className="text-brand-600" />
          <span className="text-sm font-medium">Démarrage de Mailsorter...</span>
        </div>
      </BootScreen>
    );
  }

  // Seen by anyone, visitors of the online service included, and the edition
  // is not known here (the probe that would say it is the one that failed): so
  // the visitor first, the administrator in a second sentence.
  if (error && !isUsable) {
    return (
      <BootScreen>
        <span className="flex h-14 w-14 items-center justify-center rounded-2xl bg-danger-50 text-danger-600">
          <Alert size={28} />
        </span>
        <div className="max-w-sm space-y-2">
          <h1 className="text-xl font-bold text-ink-900">Mailsorter ne répond pas</h1>
          <p className="text-sm text-muted">
            {error} Réessayez dans un instant. Si vous administrez cette instance, vérifiez que son serveur
            tourne.
          </p>
        </div>
        <button onClick={reload} className="btn-primary">
          Réessayer
        </button>
      </BootScreen>
    );
  }

  // Unconfigured: the owner of a self-hosted instance gets the setup briefing,
  // a visitor on a hosted one gets an honest "unavailable" instead of a list of
  // environment variables they do not control.
  const unconfigured = () => (selfHosted ? <Navigate to="/setup" replace /> : <Unavailable />);
  // Gated on "is there a way in at all", not on "does Google work here": an
  // instance whose only door is a mailbox is a working instance, and calling it
  // unconfigured made the hosted edition impossible to enter.
  // `mailbox`: 'required' holds the screen until the mailbox is known and sends
  // an account without one to /connect; 'known' only holds it (Réglages and
  // Compte draw from the answer, but are where an account without a mailbox
  // goes); none renders at once (/connect asks for itself).
  const guard = (element, mailbox) =>
    isUsable ? (
      <RequireAuth>
        {mailbox ? <RequireMailbox redirect={mailbox === 'required'}>{element}</RequireMailbox> : element}
      </RequireAuth>
    ) : (
      unconfigured()
    );

  return (
    <Router>
      <HotelThemeProvider fallback={<HotelBoot label="Démarrage de Mailsorter..." />}>
        <ToastProvider>
          <ConfirmProvider>
            <SessionEmailProvider>
              <AppFrame>
                <a
                  href="#main"
                  className="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-4 focus:z-[200] focus:rounded-lg focus:bg-brand-fill focus:px-4 focus:py-2 focus:text-sm focus:font-semibold focus:text-white"
                >
                  Aller au contenu
                </a>
                <Header />
                <BackToTop />
                <main id="main">
                  <Routes>
                    {/* /setup stays reachable by its address whatever the
                        edition: it is the operator's screen, and an operator on a
                        hosted instance still needs it. It is simply no longer
                        where a visitor is sent, and on an instance that signs
                        people in by mailbox it no longer swallows the landing
                        page either. */}
                    <Route
                      path="/setup"
                      element={isConfigured ? <Navigate to="/" replace /> : <Setup onComplete={reload} />}
                    />
                    <Route path="/" element={isUsable ? <Landing /> : unconfigured()} />
                    <Route path="/inbox" element={guard(<Inbox />, 'required')} />
                    <Route path="/rules" element={guard(<Rules />, 'required')} />
                    <Route path="/snoozed" element={guard(<Snoozed />, 'required')} />
                    <Route path="/history" element={guard(<History />, 'required')} />
                    <Route path="/settings" element={guard(<Settings />, 'known')} />
                    {/* Brancher une boite par IMAP. Garde comme les autres: on
                        branche une boite SUR un compte, donc il faut deja etre
                        connecte a Mailsorter. */}
                    <Route path="/connect" element={guard(<Connect />)} />
                    <Route path="/account" element={guard(<Account />, 'known')} />
                    {/* A self-hosted instance bills nobody, so there is no pricing
                        page to land on: the route redirects rather than rendering
                        an empty one. */}
                    <Route path="/pricing" element={selfHosted ? <Navigate to="/" replace /> : <Pricing />} />
                    <Route path="/auth/callback" element={<AuthCallback />} />
                    {/* Public and unconditional. Google's OAuth verification
                        expects the privacy policy to be reachable from the home
                        page, so it cannot depend on the instance being wired. */}
                    <Route path="/confidentialite" element={<Privacy />} />
                    <Route path="/conditions" element={<Terms />} />
                    {/* Redirects for legacy routes */}
                    <Route path="/emails" element={<Navigate to="/inbox" replace />} />
                    <Route path="/triage" element={<Navigate to="/inbox" replace />} />
                    {/* Anything else used to render a blank page under the header. */}
                    <Route path="*" element={<NotFound />} />
                  </Routes>
                </main>
              </AppFrame>
            </SessionEmailProvider>
          </ConfirmProvider>
        </ToastProvider>
      </HotelThemeProvider>
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
