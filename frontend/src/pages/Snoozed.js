import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { snoozeService, apiError } from '../services/api';
import { useToast } from '../ui/Toast';
import { useConfirm } from '../ui/Confirm';
import { EmptyState, ErrorState, LiveAnnouncer } from '../ui/primitives';
import { Clock, Undo, Mail, Alert, Check } from '../ui/icons';
import Spinner from '../ui/Spinner';
import { cn } from '../ui/cn';

const AVATAR_GRADIENTS = [
  'bg-brand-fill', 'bg-info-fill',
  'bg-positive-fill', 'bg-caution-fill', 'bg-danger-fill',
];
const gradientFor = (seed = '') => {
  let h = 0;
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) >>> 0;
  return AVATAR_GRADIENTS[h % AVATAR_GRADIENTS.length];
};
const senderName = (from = '') => from.split('<')[0].replace(/"/g, '').trim() || from;

// Le backend connaît trois états (`scheduled`, `done`, `failed`) et la page n'en
// montrait qu'un. Un report en échec (message introuvable côté Gmail, cinq
// tentatives épuisées) quittait donc la liste sans un mot : l'email restait
// hors de la boîte, définitivement, sans que personne ne l'apprenne.
const TABS = [
  {
    key: 'scheduled',
    label: 'Prévus',
    dateLabel: 'Retour prévu',
    chip: 'bg-brand-50 text-brand-700',
    errorTitle: 'Reports indisponibles',
    empty: {
      Icon: Clock,
      tone: 'brand',
      title: 'Rien en attente',
      description:
        "Depuis le lecteur d'email, utilisez « Reporter » pour mettre un email de côté jusqu'au moment qui vous arrange.",
    },
  },
  {
    key: 'done',
    label: 'Terminés',
    dateLabel: 'Revenu',
    chip: 'bg-positive-50 text-positive-700',
    errorTitle: 'Historique des reports indisponible',
    empty: {
      Icon: Check,
      tone: 'positive',
      title: 'Aucun report terminé',
      description:
        'Les emails revenus dans votre boîte, à l’heure prévue ou réactivés à la main, s’afficheront ici.',
    },
  },
  {
    key: 'failed',
    label: 'En échec',
    dateLabel: 'Aurait dû revenir',
    chip: 'bg-danger-50 text-danger-700',
    errorTitle: 'Échecs indisponibles',
    empty: {
      Icon: Check,
      tone: 'positive',
      title: 'Aucun échec',
      description: 'Tous vos reports sont revenus dans votre boîte comme prévu.',
    },
  },
];

const tabByKey = (key) => TABS.find((t) => t.key === key) || TABS[0];

// Un report terminé garde son heure PRÉVUE dans `wakeAt` : réactiver un email en
// avance laisserait une date future sur une ligne pourtant déjà revenue.
// `updatedAt` est l'instant où le statut a basculé, donc le vrai retour. Un
// échec, lui, se raconte par l'heure à laquelle l'email aurait dû revenir.
const eventDate = (snooze, tabKey) =>
  tabKey === 'done' ? snooze.updatedAt || snooze.wakeAt : snooze.wakeAt;

const timeOf = (value) => {
  const t = new Date(value).getTime();
  return Number.isNaN(t) ? 0 : t;
};

function formatWake(dateStr) {
  if (!dateStr) return '';
  const d = new Date(dateStr);
  if (Number.isNaN(d.getTime())) return '';
  const sameDay = d.toDateString() === new Date().toDateString();
  // toLocaleDateString ignore `hour`/`minute` : le jour même, « 14:44 » sortait
  // donc en « 01/08/2026 », et les autres jours perdaient l'heure au passage.
  return sameDay
    ? d.toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit' })
    : d.toLocaleString('fr-FR', {
        weekday: 'short',
        day: 'numeric',
        month: 'short',
        hour: '2-digit',
        minute: '2-digit',
      });
}

function fullWhen(dateStr) {
  if (!dateStr) return '';
  const d = new Date(dateStr);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleString('fr-FR', { dateStyle: 'full', timeStyle: 'short' });
}

const MINUTE = 60 * 1000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

const startOfDay = (t) => {
  const d = new Date(t);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
};

// « dans 3 h », « demain » : la vraie question de l'utilisateur devant cette page
// n'est pas la date exacte, c'est le délai. `numeric: 'auto'` est ce qui rend
// « demain » plutôt que « dans 1 jour ».
function countdown(dateStr, now) {
  if (!dateStr) return '';
  const target = new Date(dateStr).getTime();
  if (Number.isNaN(target)) return '';

  const diff = target - now;
  const abs = Math.abs(diff);
  if (abs < MINUTE) return "à l'instant";

  const rtf = new Intl.RelativeTimeFormat('fr-FR', { numeric: 'auto' });
  if (abs < HOUR) return rtf.format(Math.round(diff / MINUTE), 'minute');
  if (abs < DAY) return rtf.format(Math.round(diff / HOUR), 'hour');

  // Au-delà de la journée on raisonne en jours civils : à 23 h, « demain » doit
  // désigner le lendemain matin, pas une tranche de 24 heures.
  const days = Math.round((startOfDay(target) - startOfDay(now)) / DAY);
  if (Math.abs(days) < 7) return rtf.format(days, 'day');
  if (Math.abs(days) < 30) return rtf.format(Math.round(days / 7), 'week');
  return rtf.format(Math.round(days / 30), 'month');
}

function Snoozed() {
  const toast = useToast();
  const confirm = useConfirm();
  const [tab, setTab] = useState('scheduled');
  const [snoozes, setSnoozes] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [waking, setWaking] = useState(null);
  const [failedCount, setFailedCount] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  const tabRefs = useRef({});
  // Read inside async callbacks to tell whether the tab has moved on since the
  // request was issued.
  const tabRef = useRef(tab);
  tabRef.current = tab;

  const meta = tabByKey(tab);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    // The tab this request belongs to. Every date, colour and button on screen
    // is driven by `tab`, so a slow response landing after the user has moved on
    // would paint one tab's rows under another tab's semantics.
    const tabAtCall = tab;
    const stale = () => tabAtCall !== tabRef.current;
    try {
      const { data } = await snoozeService.list(tab);
      if (stale()) return;
      const rows = data.snoozes || [];
      setSnoozes(rows);
      setNow(Date.now());
      if (tab === 'failed') setFailedCount(rows.length);
    } catch (err) {
      if (stale()) return;
      // Une panne s'affichait mot pour mot comme un état vide (« Rien en
      // attente ») : l'utilisateur en concluait qu'aucun email n'était reporté
      // alors qu'ils y étaient tous. On garde l'erreur pour un état distinct.
      setError(apiError(err, "Les reports n'ont pas pu être chargés."));
      setSnoozes([]);
    } finally {
      if (!stale()) setLoading(false);
    }
  }, [tab]);

  useEffect(() => {
    load();
  }, [load]);

  // Un échec est invisible par nature : l'email est resté hors de la boîte et
  // rien ne le signale. On compte donc les échecs quel que soit l'onglet ouvert,
  // pour les annoncer sur l'onglet lui-même.
  useEffect(() => {
    let cancelled = false;
    snoozeService
      .list('failed')
      .then(({ data }) => {
        if (!cancelled) setFailedCount((data.snoozes || []).length);
      })
      .catch(() => {
        // Silencieux : ce n'est qu'un compteur, et l'onglet actif porte déjà son
        // propre état d'erreur.
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // Le compte à rebours doit rester vrai sans rechargement de page : une demie
  // minute borne l'écart entre ce qui est affiché et ce qui est.
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 30 * 1000);
    return () => clearInterval(id);
  }, []);

  const rows = useMemo(() => {
    // Le serveur trie déjà par `wakeAt` croissant : pour les reports à venir
    // c'est exactement « le réveil le plus proche d'abord », rien à inverser.
    // Les onglets d'historique lisent au contraire du plus récent au plus
    // ancien, et sur leur propre date d'événement : d'où un tri explicite.
    const dir = tab === 'scheduled' ? 1 : -1;
    return [...snoozes].sort(
      (a, b) => dir * (timeOf(eventDate(a, tab)) - timeOf(eventDate(b, tab)))
    );
  }, [snoozes, tab]);

  const handleWake = async (snooze) => {
    // Réactiver un report prévu annule l'échéance que l'utilisateur avait posée :
    // on le lui dit avant. Depuis l'onglet des échecs c'est au contraire une
    // réparation : il n'y a rien à perdre, donc rien à confirmer.
    if (tab === 'scheduled') {
      const confirmed = await confirm({
        title: 'Ramener cet email maintenant ?',
        message: `« ${snooze.subject || '(Sans sujet)'} » reviendra tout de suite dans votre boîte, en non lu.`,
        detail: `Le retour était prévu pour ${formatWake(snooze.wakeAt)} : cette échéance sera annulée.`,
        confirmLabel: 'Ramener maintenant',
        cancelLabel: 'Laisser en attente',
        danger: false,
      });
      if (!confirmed) return;
    }

    setWaking(snooze.id);
    try {
      await snoozeService.wake(snooze.id);
      setSnoozes((prev) => prev.filter((s) => s.id !== snooze.id));
      if (tab === 'failed') setFailedCount((c) => Math.max(0, c - 1));
      toast.success('Email réactivé, de retour dans votre boîte');
    } catch (err) {
      toast.error(apiError(err, 'Réactivation impossible. Réessayez.'));
    } finally {
      setWaking(null);
    }
  };

  // Un vrai onglet se parcourt aux flèches : sans ça, `role="tab"` promet à un
  // lecteur d'écran une navigation qui n'existe pas.
  const onTabKeyDown = (e) => {
    const i = TABS.findIndex((t) => t.key === tab);
    let next = null;
    if (e.key === 'ArrowRight') next = TABS[(i + 1) % TABS.length];
    else if (e.key === 'ArrowLeft') next = TABS[(i - 1 + TABS.length) % TABS.length];
    else if (e.key === 'Home') next = TABS[0];
    else if (e.key === 'End') next = TABS[TABS.length - 1];
    if (!next) return;
    e.preventDefault();
    setTab(next.key);
    tabRefs.current[next.key]?.focus();
  };

  const plural = rows.length > 1 ? 's' : '';
  const announcement = loading
    ? ''
    : error
    ? `${meta.errorTitle}.`
    : `${meta.label} : ${rows.length} report${plural}.`;

  return (
    <div className="mx-auto max-w-3xl px-4 py-10 sm:px-6">
      <LiveAnnouncer message={announcement} />

      <div className="mb-6 flex items-center gap-3">
        <span className="flex h-11 w-11 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
          <Clock size={22} />
        </span>
        <div>
          <h1 className="font-display text-2xl font-extrabold tracking-tight text-ink-900">Reporté</h1>
          <p className="text-sm text-muted">Les emails sortis de votre boîte, qui reviendront au bon moment.</p>
        </div>
      </div>

      <div
        role="tablist"
        aria-label="État des reports"
        onKeyDown={onTabKeyDown}
        className="mb-5 flex flex-wrap gap-1.5"
      >
        {TABS.map((t) => {
          const active = t.key === tab;
          const alert = t.key === 'failed' && failedCount > 0;
          return (
            <button
              key={t.key}
              type="button"
              role="tab"
              id={`snooze-tab-${t.key}`}
              aria-selected={active}
              aria-controls="snooze-panel"
              tabIndex={active ? 0 : -1}
              ref={(el) => {
                tabRefs.current[t.key] = el;
              }}
              onClick={() => setTab(t.key)}
              // Même base dans les deux états : seules les couleurs changent, la
              // barre d'onglets ne se réaligne donc pas au clic.
              className={cn(
                'btn-ghost btn-sm',
                active && 'bg-brand-fill text-white shadow-soft hover:bg-brand-fill-strong hover:text-white'
              )}
            >
              {t.label}
              {alert && (
                <span
                  // Sur l'onglet actif le fond est déjà plein : une pastille de
                  // plus n'ajouterait que du bruit, le nombre suffit.
                  className={cn(
                    'text-[11px] font-extrabold',
                    !active && 'chip bg-danger-100 px-1.5 py-0 text-danger-700'
                  )}
                >
                  {failedCount}
                </span>
              )}
            </button>
          );
        })}
      </div>

      <div role="tabpanel" id="snooze-panel" aria-labelledby={`snooze-tab-${tab}`} tabIndex={-1}>
        {loading ? (
          <div className="flex min-h-[40vh] items-center justify-center">
            <Spinner size={28} className="text-brand-500" />
          </div>
        ) : error ? (
          <div className="card">
            <ErrorState title={meta.errorTitle} message={error} onRetry={load} />
          </div>
        ) : rows.length === 0 ? (
          <div className="card">
            <EmptyState
              Icon={meta.empty.Icon}
              tone={meta.empty.tone}
              title={meta.empty.title}
              description={meta.empty.description}
            />
          </div>
        ) : (
          <ul className="space-y-3">
            {rows.map((s) => {
              const date = eventDate(s, tab);
              const overdue = tab === 'scheduled' && timeOf(date) <= now;
              return (
                <li key={s.id} className="card flex flex-wrap items-center gap-x-4 gap-y-3 p-4 animate-fade-up">
                  <span
                    aria-hidden="true"
                    className={cn(
                      'flex h-11 w-11 shrink-0 items-center justify-center rounded-full text-sm font-bold text-white',
                      gradientFor(s.from)
                    )}
                  >
                    {senderName(s.from)[0]?.toUpperCase() || '?'}
                  </span>

                  <div className="min-w-0 flex-1">
                    <div className="truncate font-semibold text-ink-900">{s.subject || '(Sans sujet)'}</div>
                    <div className="flex items-center gap-1.5 truncate text-xs text-muted">
                      <Mail size={12} /> {senderName(s.from) || 'Expéditeur inconnu'}
                    </div>
                  </div>

                  <div className="flex flex-col items-end gap-1">
                    <span className={cn('chip', meta.chip)}>
                      <Clock size={13} />
                      {/* Le délai d'abord : c'est l'information cherchée. La date
                          exacte reste juste en dessous, pour lever le doute. */}
                      {overdue ? 'retour imminent' : countdown(date, now)}
                    </span>
                    <time
                      dateTime={date || undefined}
                      title={fullWhen(date)}
                      className="text-xs text-muted"
                    >
                      {meta.dateLabel} {formatWake(date)}
                    </time>
                  </div>

                  {tab !== 'done' && (
                    <button
                      onClick={() => handleWake(s)}
                      disabled={waking === s.id}
                      className="btn-secondary btn-sm"
                      title={
                        tab === 'failed'
                          ? 'Réessayer de ramener cet email dans la boîte'
                          : 'Ramener cet email dans la boîte maintenant'
                      }
                    >
                      {waking === s.id ? <Spinner size={14} /> : <Undo size={14} />} Réactiver
                    </button>
                  )}

                  {tab === 'failed' && (
                    <p className="flex w-full items-start gap-2 rounded-xl bg-danger-50 p-3 text-xs leading-relaxed text-danger-700">
                      <Alert size={14} className="mt-0.5 shrink-0" />
                      <span>
                        Le retour dans votre boîte a échoué {s.attempts || 5} fois, puis Mailsorter a cessé
                        de réessayer. Le message a sans doute été supprimé ou déplacé définitivement dans
                        Gmail. Cet email est donc toujours hors de votre boîte : « Réactiver » relance la
                        tentative.
                      </span>
                    </p>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}

export default Snoozed;
