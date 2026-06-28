import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useEmails } from '../contexts/EmailContext';
import { aiService, senderService, emailService, subscriptionService, protectService } from '../services/api';
import { useToast } from '../ui/Toast';
import { recordTriage, getStreakState } from '../ui/streak';
import EmailReader from '../components/EmailReader';
import Spinner from '../ui/Spinner';
import { cn } from '../ui/cn';
import {
  Sparkles, Archive, Trash, Tag, Pin, Search, Refresh, Inbox as InboxIcon,
  Users, Bolt, Check, X, Mail, Shield, Flame, Keyboard, BellOff,
} from '../ui/icons';

// --- Action design tokens (static classes so Tailwind keeps them) ---
const ACTIONS = {
  archive: { label: 'Archiver', Icon: Archive, badge: 'bg-sky-50 text-sky-600', ring: '#0ea5e9' },
  delete: { label: 'Supprimer', Icon: Trash, badge: 'bg-rose-50 text-rose-600', ring: '#f43f5e' },
  label: { label: 'Libellé', Icon: Tag, badge: 'bg-amber-50 text-amber-700', ring: '#d97706' },
  keep: { label: 'Garder', Icon: Pin, badge: 'bg-emerald-50 text-emerald-600', ring: '#10b981' },
};
const actionMeta = (a) => ACTIONS[a] || ACTIONS.keep;
const isReversible = (a) => a === 'archive' || a === 'delete';

const SHORTCUTS = [
  ['J / K', 'Naviguer entre les emails'],
  ['Entrée', "Ouvrir l'email ciblé"],
  ['X', "Sélectionner l'email ciblé"],
  ['E', 'Archiver'],
  ['# / Suppr', 'Supprimer'],
  ['A', 'Tout appliquer (suggestions)'],
  ['R', 'Synchroniser'],
  ['/', 'Rechercher'],
  ['?', 'Afficher cette aide'],
  ['Échap', 'Fermer le lecteur'],
];

const AVATAR_GRADIENTS = [
  'bg-brand-500', 'bg-sky-500',
  'bg-emerald-500', 'bg-amber-500', 'bg-rose-500',
];
const gradientFor = (seed = '') => {
  let h = 0;
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) >>> 0;
  return AVATAR_GRADIENTS[h % AVATAR_GRADIENTS.length];
};

function ConfidenceRing({ value = 0, color = '#2563eb' }) {
  const pct = Math.round((value || 0) * 100);
  const r = 13;
  const c = 2 * Math.PI * r;
  return (
    <div className="relative h-9 w-9 shrink-0" title={`Confiance ${pct}%`}>
      <svg viewBox="0 0 32 32" className="h-9 w-9 -rotate-90">
        <circle cx="16" cy="16" r={r} fill="none" stroke="#e2e8f0" strokeWidth="3" />
        <circle
          cx="16" cy="16" r={r} fill="none" stroke={color} strokeWidth="3" strokeLinecap="round"
          strokeDasharray={c} strokeDashoffset={c - (pct / 100) * c}
        />
      </svg>
      <span className="absolute inset-0 flex items-center justify-center text-[10px] font-bold text-ink-700">
        {pct}
      </span>
    </div>
  );
}

const STAT_CARDS = [
  { key: 'inboxCount', label: 'Boîte de réception', tone: 'text-brand-600', Icon: InboxIcon },
  { key: 'unreadCount', label: 'Non lus', tone: 'text-amber-600', Icon: Mail },
  { key: 'totalMessages', label: 'Total', tone: 'text-ink-700', Icon: Archive },
  { key: 'spamCount', label: 'Spam', tone: 'text-rose-600', Icon: Shield },
];

function Inbox() {
  const navigate = useNavigate();
  const toast = useToast();
  const {
    emails, senders, subscriptions, suggestions, stats, pagination,
    loading, loadingMore, fetchData, loadMoreEmails, removeSuggestion, removeSuggestions, markUnsubscribed,
  } = useEmails();

  const [view, setView] = useState('emails');
  const [selectedEmails, setSelectedEmails] = useState([]);
  const [selectedEmail, setSelectedEmail] = useState(null);
  const [syncing, setSyncing] = useState(false);
  const [analyzing, setAnalyzing] = useState(false);
  const [applyingAll, setApplyingAll] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [localSenders, setLocalSenders] = useState([]);
  const [analyzingSender, setAnalyzingSender] = useState(null);
  const [highConfOnly, setHighConfOnly] = useState(false);
  const [unsubscribing, setUnsubscribing] = useState(null);
  const [focusedIndex, setFocusedIndex] = useState(-1);
  const [showShortcuts, setShowShortcuts] = useState(false);
  const [gamify, setGamify] = useState(getStreakState);
  const [job, setJob] = useState(null);
  const [showWelcome, setShowWelcome] = useState(false);

  const searchRef = useRef(null);
  const rowRefs = useRef([]);
  const pollRef = useRef(null);

  // Switch to the async worker beyond this many emails so the UI never blocks.
  const ASYNC_THRESHOLD = 10;

  useEffect(() => {
    if (!localStorage.getItem('userEmail')) {
      navigate('/');
      return;
    }
    fetchData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [navigate]);

  useEffect(() => setLocalSenders(senders), [senders]);

  useEffect(() => {
    if (focusedIndex >= 0) rowRefs.current[focusedIndex]?.scrollIntoView({ block: 'nearest' });
  }, [focusedIndex]);

  // Stop polling if the page unmounts mid-job.
  useEffect(() => () => clearTimeout(pollRef.current), []);

  // First-run onboarding.
  useEffect(() => {
    if (!localStorage.getItem('mailsorter_onboarded')) setShowWelcome(true);
  }, []);

  const dismissWelcome = () => {
    localStorage.setItem('mailsorter_onboarded', '1');
    setShowWelcome(false);
  };

  const visibleSuggestions = useMemo(
    () => (highConfOnly ? suggestions.filter((s) => (s.confidence || 0) >= 0.8) : suggestions),
    [suggestions, highConfOnly]
  );

  const bumpGamify = (n) => setGamify(recordTriage(n));

  const formatNumber = (num) => {
    if (!num) return '0';
    if (num >= 1000) return `${(num / 1000).toFixed(1)}k`;
    return num.toString();
  };

  const formatDate = (dateStr) => {
    if (!dateStr) return '';
    const date = new Date(dateStr);
    const now = new Date();
    if (date.toDateString() === now.toDateString())
      return date.toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit' });
    return date.toLocaleDateString('fr-FR', { day: 'numeric', month: 'short' });
  };

  // --- Undo helper ---------------------------------------------------------
  const undoToast = (messageId, action, message) => {
    toast.action(message, 'Annuler', async () => {
      try {
        await emailService.action(messageId, action === 'archive' ? 'unarchive' : 'untrash');
        toast.success('Action annulée');
        fetchData({ forceRefresh: true });
      } catch (err) {
        toast.error("Impossible d'annuler");
      }
    });
  };

  // --- Handlers ------------------------------------------------------------
  const handleSync = async () => {
    setSyncing(true);
    await fetchData({ forceRefresh: true });
    setSyncing(false);
    toast.success('Boîte synchronisée');
  };

  const handleSearch = (e) => {
    e.preventDefault();
    fetchData({ forceRefresh: true, query: searchQuery || 'in:inbox' });
  };

  const handleSelectEmail = (email) =>
    setSelectedEmails((prev) =>
      prev.includes(email.messageId) ? prev.filter((id) => id !== email.messageId) : [...prev, email.messageId]
    );

  const handleSelectAll = () =>
    setSelectedEmails((prev) => (prev.length === emails.length ? [] : emails.map((e) => e.messageId)));

  const handleAnalyze = async () => {
    const ids = selectedEmails.length > 0 ? selectedEmails : emails.map((e) => e.messageId);
    if (ids.length === 0) {
      toast.error('Aucun email à analyser');
      return;
    }
    if (ids.length > ASYNC_THRESHOLD) {
      runAsyncAnalyze(ids);
    } else {
      runSyncAnalyze(ids);
    }
  };

  const announceResult = ({ suggestionsCreated = 0, autoApplied = 0, cachedHits = 0 }) => {
    if (autoApplied > 0) {
      bumpGamify(autoApplied);
      toast.success(`${autoApplied} email${autoApplied > 1 ? 's' : ''} auto-trié${autoApplied > 1 ? 's' : ''} (auto-pilote)`);
    }
    const extra = cachedHits > 0 ? ` · ${cachedHits} depuis le cache` : '';
    toast.success(suggestionsCreated ? `${suggestionsCreated} suggestion${suggestionsCreated > 1 ? 's' : ''} générée${suggestionsCreated > 1 ? 's' : ''}${extra}` : 'Analyse terminée');
  };

  const runSyncAnalyze = async (ids) => {
    setAnalyzing(true);
    try {
      const { data } = await aiService.analyzeEmails(ids);
      await fetchData({ forceRefresh: true });
      announceResult({
        suggestionsCreated: data?.suggestions?.length || 0,
        autoApplied: data?.autoApplied || 0,
        cachedHits: data?.cachedHits || 0,
      });
      setSelectedEmails([]);
    } catch (err) {
      if (!handleQuotaError(err)) toast.error("L'analyse a échoué. Réessayez.");
    } finally {
      setAnalyzing(false);
    }
  };

  const runAsyncAnalyze = async (ids) => {
    setAnalyzing(true);
    setJob({ status: 'queued', processed: 0, total: ids.length });
    try {
      const { data } = await aiService.analyzeAsync(ids);
      setSelectedEmails([]);
      pollJob(data.jobId);
    } catch (err) {
      if (!handleQuotaError(err)) toast.error("Impossible de lancer l'analyse");
      setJob(null);
      setAnalyzing(false);
    }
  };

  // Returns true if it handled a 402 quota error.
  const handleQuotaError = (err) => {
    if (err.response?.status === 402) {
      toast.action('Quota mensuel atteint.', 'Voir Pro', () => navigate('/pricing'), { variant: 'error' });
      return true;
    }
    return false;
  };

  const pollJob = async (jobId) => {
    try {
      const { data } = await aiService.getJob(jobId);
      setJob(data);
      if (data.status === 'done' || data.status === 'error') {
        setJob(null);
        setAnalyzing(false);
        await fetchData({ forceRefresh: true });
        if (data.status === 'error') {
          toast.error('Analyse interrompue. Réessayez.');
        } else {
          announceResult(data);
        }
        return;
      }
      pollRef.current = setTimeout(() => pollJob(jobId), 1500);
    } catch (err) {
      pollRef.current = setTimeout(() => pollJob(jobId), 2500);
    }
  };

  const handleApplySuggestion = async (suggestion) => {
    const id = suggestion.id || suggestion._id;
    const act = suggestion.action;
    removeSuggestion(id);
    try {
      await aiService.applySuggestion(id);
      bumpGamify(1);
      const msg = `${actionMeta(act).label} appliqué`;
      if (isReversible(act)) undoToast(suggestion.emailId, act, msg);
      else toast.success(msg);
      fetchData({ forceRefresh: true });
    } catch (err) {
      toast.error("Action impossible. L'email reste en place.");
    }
  };

  const handleRejectSuggestion = async (suggestion) => {
    const id = suggestion.id || suggestion._id;
    removeSuggestion(id);
    aiService.rejectSuggestion(id).catch(() => {});
  };

  const handleApplyAll = async () => {
    const ids = visibleSuggestions.map((s) => s.id || s._id);
    if (ids.length === 0) return;
    setApplyingAll(true);
    try {
      const res = await aiService.applyBatch(ids);
      const appliedIds = res.data?.appliedIds || ids;
      removeSuggestions(appliedIds);
      bumpGamify(res.data?.applied ?? appliedIds.length);
      toast.success(`${res.data?.applied ?? appliedIds.length} action${appliedIds.length > 1 ? 's' : ''} appliquée${appliedIds.length > 1 ? 's' : ''}`);
      if (res.data?.failed) toast.error(`${res.data.failed} action(s) ont échoué`);
      fetchData({ forceRefresh: true });
    } catch (err) {
      toast.error("Impossible d'appliquer les suggestions");
    } finally {
      setApplyingAll(false);
    }
  };

  const handleRejectAll = () => {
    const ids = visibleSuggestions.map((s) => s.id || s._id);
    removeSuggestions(ids);
    ids.forEach((id) => aiService.rejectSuggestion(id).catch(() => {}));
    toast.info('Suggestions ignorées');
  };

  // Direct action on a single email (used by reader buttons + keyboard).
  const directAction = async (email, action) => {
    try {
      await emailService.action(email.messageId, action);
      bumpGamify(1);
      undoToast(email.messageId, action, action === 'archive' ? 'Email archivé' : 'Email déplacé vers la corbeille');
      fetchData({ forceRefresh: true });
    } catch (err) {
      toast.error("L'action a échoué");
    }
  };

  const handleReaderAction = (email, action) => {
    setSelectedEmail(null);
    directAction(email, action);
  };

  // Snooze: pull the email out of the inbox until the chosen preset, then it
  // returns on its own (marked unread).
  const handleSnooze = async (email, preset) => {
    setSelectedEmail(null);
    try {
      await emailService.snooze(email.messageId, preset);
      bumpGamify(1);
      toast.success('Email reporté — il reviendra au bon moment');
      fetchData({ forceRefresh: true });
    } catch (err) {
      toast.error('Report impossible. Réessayez.');
    }
  };

  // Protect: shield a sender so no automated pass ever archives/deletes them.
  const handleProtect = async (email) => {
    try {
      const { data } = await protectService.add(email.from);
      toast.action(
        `${data.value} est désormais protégé.`,
        'Gérer',
        () => navigate('/settings')
      );
    } catch (err) {
      toast.error(err.response?.data?.trim() || 'Protection impossible.');
    }
  };

  // One-click (or assisted) unsubscribe. Used by the reader and the subscriptions view.
  const handleUnsubscribe = async ({ messageId, alsoArchive = false, key }) => {
    if (!messageId) return;
    setUnsubscribing(key || messageId);
    try {
      const { data } = await subscriptionService.unsubscribe(messageId, alsoArchive);
      const archivedNote = data.archived ? ` · ${data.archived} email${data.archived > 1 ? 's' : ''} archivé${data.archived > 1 ? 's' : ''}` : '';
      if (data.done) {
        toast.success(`Désabonné en un clic${archivedNote}`);
      } else if (data.url) {
        window.open(data.url, '_blank', 'noopener,noreferrer');
        toast.info(`Page de désabonnement ouverte dans un nouvel onglet${archivedNote}`);
      } else if (data.mailto) {
        window.location.href = data.mailto;
        toast.info('Email de désabonnement préparé');
      }
      if (data.sender) markUnsubscribed(data.sender);
      if (alsoArchive || data.archived) fetchData({ forceRefresh: true });
    } catch (err) {
      if (err.response?.status === 422) {
        toast.error("Cet expéditeur ne propose pas de désabonnement automatique");
      } else {
        toast.error('Désabonnement impossible. Réessayez.');
      }
    } finally {
      setUnsubscribing(null);
    }
  };

  const handleAnalyzeSender = async (sender) => {
    setAnalyzingSender(sender.senderEmail);
    try {
      await aiService.analyzeSender(sender.senderEmail);
      const sendersRes = await senderService.getSenders();
      setLocalSenders(sendersRes.data || []);
      toast.success(`${sender.senderName || sender.senderEmail} analysé`);
    } catch (err) {
      toast.error("Analyse de l'expéditeur impossible");
    } finally {
      setAnalyzingSender(null);
    }
  };

  const handleToggleAutoApply = async (sender) => {
    const pref = sender.preference;
    if (!pref?.id) return;
    const next = !pref.autoApply;
    try {
      await senderService.updatePreference(pref.id, {
        autoApply: next,
        defaultAction: pref.defaultAction,
        defaultLabel: pref.defaultLabel || '',
      });
      setLocalSenders((prev) =>
        prev.map((s) =>
          s.senderEmail === sender.senderEmail ? { ...s, preference: { ...pref, autoApply: next } } : s
        )
      );
      toast.success(next ? 'Auto-pilote activé pour cet expéditeur' : 'Auto-pilote désactivé');
    } catch (err) {
      toast.error('Mise à jour impossible');
    }
  };

  const handleApplyBulk = async (sender, action) => {
    try {
      const response = await aiService.applyBulk(sender.senderEmail, action, sender.preference?.defaultLabel || '');
      bumpGamify(response.data.applied || 0);
      toast.success(`${response.data.applied} email${response.data.applied > 1 ? 's' : ''} traité${response.data.applied > 1 ? 's' : ''}`);
      fetchData({ forceRefresh: true });
    } catch (err) {
      toast.error('Traitement en masse impossible');
    }
  };

  // "Learn once, apply forever": create a permanent deterministic rule that
  // archives every FUTURE email from this sender — for free, no AI, no quota.
  const handleCreateSenderRule = async (sender) => {
    try {
      await senderService.createRule(sender.senderEmail, 'archive');
      toast.action(
        `Règle créée : les emails de ${sender.senderName || sender.senderEmail} seront archivés.`,
        'Voir les règles',
        () => navigate('/rules')
      );
    } catch (err) {
      toast.error(err.response?.data?.trim() || 'Création de la règle impossible');
    }
  };

  // --- Keyboard shortcuts --------------------------------------------------
  useEffect(() => {
    const onKey = (e) => {
      if (e.key === 'Escape') {
        setSelectedEmail(null);
        setShowShortcuts(false);
        return;
      }
      const el = document.activeElement;
      const typing = el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable);
      if (typing || e.metaKey || e.ctrlKey || e.altKey) return;

      if (e.key === '?') { setShowShortcuts((s) => !s); return; }
      if (e.key === '/') { e.preventDefault(); searchRef.current?.focus(); return; }
      if (e.key === 'r') { handleSync(); return; }
      if (e.key === 'a' && visibleSuggestions.length) { handleApplyAll(); return; }
      if (view !== 'emails' || emails.length === 0) return;

      const cur = emails[focusedIndex];
      switch (e.key) {
        case 'j':
          e.preventDefault();
          setFocusedIndex((i) => Math.min((i < 0 ? -1 : i) + 1, emails.length - 1));
          break;
        case 'k':
          e.preventDefault();
          setFocusedIndex((i) => Math.max((i < 0 ? 1 : i) - 1, 0));
          break;
        case 'Enter':
          if (cur) setSelectedEmail(cur);
          break;
        case 'x':
          if (cur) handleSelectEmail(cur);
          break;
        case 'e':
          if (cur) directAction(cur, 'archive');
          break;
        case '#':
        case 'Delete':
          if (cur) directAction(cur, 'delete');
          break;
        default:
          break;
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [emails, focusedIndex, view, visibleSuggestions]);

  const allSelected = emails.length > 0 && selectedEmails.length === emails.length;
  const progressPct = Math.min(100, Math.round((gamify.today / gamify.goal) * 100));
  const goalHit = gamify.today >= gamify.goal;
  rowRefs.current = [];

  return (
    <div className="mx-auto max-w-7xl px-4 py-6 sm:px-6">
      {/* Hero command bar */}
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
        <div>
          <h1 className="font-display text-2xl font-extrabold tracking-tight text-ink-900 sm:text-3xl">
            Votre boîte, sous contrôle.
          </h1>
          <p className="mt-1 text-sm text-ink-500">
            {stats?.inboxCount
              ? `${formatNumber(stats.inboxCount)} email${stats.inboxCount > 1 ? 's' : ''} en attente de tri.`
              : 'Synchronisez pour commencer le tri intelligent.'}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <form onSubmit={handleSearch} className="relative">
            <Search size={18} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-ink-400" />
            <input
              ref={searchRef}
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder="from:amazon, is:unread…  ( / )"
              className="input w-44 pl-10 sm:w-64"
            />
          </form>
          <button onClick={() => setShowShortcuts(true)} className="btn-secondary px-3" title="Raccourcis clavier (?)">
            <Keyboard size={18} />
          </button>
          <button onClick={handleSync} disabled={syncing} className="btn-secondary px-3" title="Synchroniser (r)">
            <Refresh size={18} className={syncing ? 'animate-spin' : ''} />
          </button>
        </div>
      </div>

      {/* Daily progress + streak */}
      <div className="card mb-6 flex flex-col gap-4 p-4 sm:flex-row sm:items-center sm:gap-6">
        <div className="flex items-center gap-3">
          <span className={cn('flex h-12 w-12 items-center justify-center rounded-2xl text-white', gamify.streak > 0 ? 'bg-amber-500' : 'bg-ink-200')}>
            <Flame size={24} />
          </span>
          <div>
            <div className="font-display text-lg font-extrabold leading-none text-ink-900">
              {gamify.streak > 0 ? `Série de ${gamify.streak} jour${gamify.streak > 1 ? 's' : ''}` : 'Lancez votre série'}
            </div>
            <div className="text-xs text-ink-500">
              {goalHit ? 'Objectif du jour atteint 🎉' : 'Triez chaque jour pour la garder vivante'}
            </div>
          </div>
        </div>
        <div className="flex-1">
          <div className="mb-1.5 flex items-center justify-between text-xs font-medium text-ink-500">
            <span>Objectif du jour</span>
            <span className={goalHit ? 'font-bold text-emerald-600' : 'text-ink-600'}>
              {gamify.today}/{gamify.goal} triés
            </span>
          </div>
          <div className="h-2.5 w-full overflow-hidden rounded-full bg-ink-100">
            <div
              className={cn('h-full rounded-full transition-all duration-500', goalHit ? 'bg-emerald-500' : 'bg-brand-600')}
              style={{ width: `${progressPct}%` }}
            />
          </div>
        </div>
      </div>

      {/* Stats */}
      {stats && (
        <div className="mb-6 grid grid-cols-2 gap-3 sm:grid-cols-4">
          {STAT_CARDS.map(({ key, label, tone, Icon }) => (
            <div key={key} className="card flex items-center gap-3 p-4">
              <span className={cn('flex h-10 w-10 items-center justify-center rounded-xl bg-ink-50', tone)}>
                <Icon size={20} />
              </span>
              <div>
                <div className={cn('font-display text-xl font-extrabold leading-none', tone)}>
                  {formatNumber(stats[key])}
                </div>
                <div className="mt-1 text-xs font-medium text-ink-500">{label}</div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* View toggle + analyze */}
      <div className="mb-5 flex flex-wrap items-center gap-3">
        <div className="inline-flex rounded-xl border border-ink-200 bg-white p-1 shadow-soft">
          {[
            { id: 'emails', label: 'Emails', Icon: InboxIcon },
            { id: 'senders', label: `Expéditeurs · ${localSenders.length}`, Icon: Users },
            { id: 'subs', label: `Abonnements · ${subscriptions.filter((s) => !s.unsubscribed).length}`, Icon: BellOff },
          ].map(({ id, label, Icon }) => (
            <button
              key={id}
              onClick={() => setView(id)}
              className={cn(
                'flex items-center gap-2 rounded-lg px-3.5 py-2 text-sm font-semibold transition-all',
                view === id ? 'bg-brand-600 text-white shadow-soft' : 'text-ink-500 hover:text-ink-900'
              )}
            >
              <Icon size={16} /> {label}
            </button>
          ))}
        </div>

        {view === 'emails' && (
          <>
            <button onClick={handleSelectAll} className="btn-secondary">
              <span className={cn('flex h-4 w-4 items-center justify-center rounded border', allSelected ? 'border-brand-500 bg-brand-500 text-white' : 'border-ink-300')}>
                {allSelected && <Check size={12} />}
              </span>
              {allSelected ? 'Tout désélectionner' : 'Tout sélectionner'}
            </button>
            <button onClick={handleAnalyze} disabled={analyzing} className="btn-primary">
              {analyzing ? <Spinner size={18} /> : <Sparkles size={18} />}
              {analyzing
                ? 'Analyse…'
                : selectedEmails.length > 0
                ? `Trier avec l'IA · ${selectedEmails.length}`
                : "Trier ma boîte"}
            </button>
          </>
        )}
      </div>

      {/* Async analysis progress */}
      {job && (
        <div className="card mb-6 flex flex-col gap-3 p-4 animate-fade-up sm:flex-row sm:items-center sm:gap-5">
          <div className="flex items-center gap-3">
            <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
              <Spinner size={20} className="text-brand-500" />
            </span>
            <div>
              <div className="text-sm font-bold text-ink-900">
                {job.status === 'queued' ? 'Analyse en file…' : "L'IA trie votre boîte…"}
              </div>
              <div className="text-xs text-ink-500">
                {job.processed || 0}/{job.total || 0} traités
                {job.cachedHits ? ` · ${job.cachedHits} en cache` : ''}
                {job.autoApplied ? ` · ${job.autoApplied} auto` : ''}
              </div>
            </div>
          </div>
          <div className="flex-1">
            <div className="h-2.5 w-full overflow-hidden rounded-full bg-ink-100">
              <div
                className="h-full rounded-full bg-brand-600 transition-all duration-500"
                style={{ width: `${job.total ? Math.round(((job.processed || 0) / job.total) * 100) : 5}%` }}
              />
            </div>
          </div>
        </div>
      )}

      {/* Suggestions panel */}
      {visibleSuggestions.length > 0 && view === 'emails' && (
        <div className="card mb-6 overflow-hidden animate-fade-up">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-ink-100 bg-brand-50/50 px-5 py-3.5">
            <div className="flex items-center gap-2">
              <Sparkles size={18} className="text-brand-600" />
              <span className="font-bold text-ink-900">Suggestions IA</span>
              <span className="chip bg-brand-100 text-brand-700">{visibleSuggestions.length}</span>
            </div>
            <div className="flex items-center gap-2">
              <button
                onClick={() => setHighConfOnly((v) => !v)}
                className={cn('chip transition-colors', highConfOnly ? 'bg-emerald-100 text-emerald-700' : 'bg-ink-100 text-ink-500 hover:bg-ink-200')}
                title="N'afficher que les suggestions à haute confiance"
              >
                <Shield size={13} /> Haute confiance
              </button>
              <button onClick={handleRejectAll} className="btn-ghost px-3 py-1.5 text-xs">
                Tout ignorer
              </button>
              <button onClick={handleApplyAll} disabled={applyingAll} className="btn-primary px-3.5 py-1.5 text-xs" title="Tout appliquer (a)">
                {applyingAll ? <Spinner size={14} /> : <Bolt size={14} />} Tout appliquer
              </button>
            </div>
          </div>
          <div className="divide-y divide-ink-100">
            {visibleSuggestions.map((suggestion) => {
              const email = emails.find((e) => e.messageId === suggestion.emailId);
              const meta = actionMeta(suggestion.action);
              return (
                <div key={suggestion.id || suggestion._id} className="flex items-center gap-3 px-5 py-3 transition-colors hover:bg-ink-50/60">
                  <ConfidenceRing value={suggestion.confidence} color={meta.ring} />
                  <span className={cn('chip shrink-0', meta.badge)}>
                    <meta.Icon size={13} />
                    {suggestion.action === 'label' ? suggestion.labelName || 'Libellé' : meta.label}
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-semibold text-ink-900">
                      {email?.subject || 'Sans sujet'}
                    </div>
                    <div className="truncate text-xs text-ink-400">
                      <span className="text-ink-500">{email?.from?.split('<')[0]?.trim() || 'Expéditeur inconnu'}</span>
                      {suggestion.reasoning ? ` — ${suggestion.reasoning}` : ''}
                    </div>
                  </div>
                  <div className="flex shrink-0 items-center gap-1">
                    <button onClick={() => handleApplySuggestion(suggestion)} className="rounded-lg p-2 text-emerald-600 transition-colors hover:bg-emerald-50" title="Appliquer">
                      <Check size={18} />
                    </button>
                    <button onClick={() => handleRejectSuggestion(suggestion)} className="rounded-lg p-2 text-ink-400 transition-colors hover:bg-ink-100" title="Ignorer">
                      <X size={18} />
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      )}

      {/* Main content */}
      <div className={cn('grid gap-5', selectedEmail ? 'lg:grid-cols-[1fr_minmax(360px,440px)]' : 'grid-cols-1')}>
        {view === 'emails' ? (
          <div className="card overflow-hidden">
            <div className="flex items-center justify-between border-b border-ink-100 px-5 py-3 text-xs font-medium text-ink-400">
              <span>{emails.length} email{emails.length > 1 ? 's' : ''} affiché{emails.length > 1 ? 's' : ''}</span>
              {pagination.resultSizeEstimate > 0 && (
                <span>~{formatNumber(pagination.resultSizeEstimate)} au total</span>
              )}
            </div>

            {loading && emails.length === 0 ? (
              <div className="divide-y divide-ink-100">
                {Array.from({ length: 8 }).map((_, i) => (
                  <div key={i} className="flex items-center gap-3 px-5 py-4">
                    <div className="skeleton h-10 w-10 rounded-full" />
                    <div className="flex-1 space-y-2">
                      <div className="skeleton h-3 w-1/3" />
                      <div className="skeleton h-3 w-2/3" />
                    </div>
                  </div>
                ))}
              </div>
            ) : emails.length === 0 ? (
              <div className="flex flex-col items-center justify-center px-6 py-20 text-center">
                <span className="mb-4 flex h-16 w-16 items-center justify-center rounded-2xl bg-emerald-50 text-emerald-500">
                  <Check size={32} />
                </span>
                <h3 className="text-lg font-bold text-ink-900">Inbox Zero atteint 🎉</h3>
                <p className="mt-1 max-w-xs text-sm text-ink-500">
                  Plus rien à trier ici. Changez de recherche ou synchronisez pour récupérer de nouveaux emails.
                </p>
              </div>
            ) : (
              <div className="divide-y divide-ink-100">
                {emails.map((email, idx) => {
                  const name = email.from?.split('<')[0]?.trim() || email.from || '?';
                  const isActive = selectedEmail?.messageId === email.messageId;
                  const isChecked = selectedEmails.includes(email.messageId);
                  const isFocused = idx === focusedIndex;
                  return (
                    <div
                      key={email.messageId}
                      ref={(el) => (rowRefs.current[idx] = el)}
                      className={cn(
                        'group flex items-center gap-3 px-4 py-3 transition-colors',
                        isActive ? 'bg-brand-50/70' : 'hover:bg-ink-50/70',
                        isChecked && 'bg-brand-50/40',
                        isFocused && 'ring-2 ring-inset ring-brand-400'
                      )}
                    >
                      <button
                        onClick={() => handleSelectEmail(email)}
                        className={cn(
                          'flex h-5 w-5 shrink-0 items-center justify-center rounded-md border transition-all',
                          isChecked ? 'border-brand-500 bg-brand-500 text-white' : 'border-ink-300 group-hover:border-ink-400'
                        )}
                        aria-label="Sélectionner"
                      >
                        {isChecked && <Check size={13} />}
                      </button>
                      <span className={cn('relative flex h-10 w-10 shrink-0 items-center justify-center rounded-full text-sm font-bold text-white', gradientFor(email.from))}>
                        {name[0]?.toUpperCase() || '?'}
                        {!email.isRead && (
                          <span className="absolute -right-0.5 -top-0.5 h-3 w-3 rounded-full border-2 border-white bg-brand-500" />
                        )}
                      </span>
                      <button onClick={() => { setFocusedIndex(idx); setSelectedEmail(email); }} className="min-w-0 flex-1 text-left">
                        <div className="flex items-baseline justify-between gap-2">
                          <span className={cn('truncate text-sm', email.isRead ? 'font-medium text-ink-700' : 'font-bold text-ink-900')}>
                            {name}
                          </span>
                          <span className="shrink-0 text-xs text-ink-400">{formatDate(email.receivedDate)}</span>
                        </div>
                        <div className={cn('truncate text-sm', email.isRead ? 'text-ink-600' : 'font-semibold text-ink-800')}>
                          {email.subject || '(Sans sujet)'}
                        </div>
                        <div className="truncate text-xs text-ink-400">{email.snippet}</div>
                      </button>
                    </div>
                  );
                })}

                {pagination.nextPageToken && (
                  <div className="p-4">
                    <button
                      onClick={() => loadMoreEmails(searchQuery || 'in:inbox')}
                      disabled={loadingMore}
                      className="btn-secondary w-full"
                    >
                      {loadingMore ? <Spinner size={18} /> : null}
                      {loadingMore ? 'Chargement…' : "Charger plus d'emails"}
                    </button>
                  </div>
                )}
              </div>
            )}
          </div>
        ) : view === 'senders' ? (
          <div className="space-y-3">
            {localSenders.length === 0 ? (
              <div className="card flex flex-col items-center justify-center px-6 py-20 text-center">
                <span className="mb-4 flex h-16 w-16 items-center justify-center rounded-2xl bg-brand-50 text-brand-500">
                  <Users size={32} />
                </span>
                <h3 className="text-lg font-bold text-ink-900">Aucun expéditeur pour l'instant</h3>
                <p className="mt-1 max-w-xs text-sm text-ink-500">Synchronisez votre boîte pour voir qui vous écrit le plus.</p>
              </div>
            ) : (
              localSenders.map((sender) => {
                const pref = sender.preference;
                return (
                  <div key={sender.senderEmail} className="card flex flex-wrap items-center gap-4 p-4">
                    <span className={cn('flex h-11 w-11 shrink-0 items-center justify-center rounded-full text-sm font-bold text-white', gradientFor(sender.senderEmail))}>
                      {(sender.senderName || sender.senderEmail)[0]?.toUpperCase()}
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="truncate font-semibold text-ink-900">{sender.senderName || sender.senderEmail.split('@')[0]}</div>
                      <div className="truncate text-xs text-ink-400">{sender.senderEmail}</div>
                    </div>
                    <span className="chip bg-ink-100 text-ink-600">{sender.emailCount} emails</span>
                    {pref ? (
                      <>
                        <span className={cn('chip', actionMeta(pref.defaultAction).badge)}>
                          {(() => { const M = actionMeta(pref.defaultAction); return <M.Icon size={13} />; })()}
                          {actionMeta(pref.defaultAction).label}
                        </span>
                        <button
                          onClick={() => handleToggleAutoApply(sender)}
                          className={cn('chip transition-colors', pref.autoApply ? 'bg-emerald-100 text-emerald-700' : 'bg-ink-100 text-ink-500 hover:bg-ink-200')}
                          title="Appliquer automatiquement à chaque tri"
                        >
                          <Bolt size={13} /> Auto-pilote {pref.autoApply ? 'ON' : 'OFF'}
                        </button>
                      </>
                    ) : (
                      <button
                        onClick={() => handleAnalyzeSender(sender)}
                        disabled={analyzingSender === sender.senderEmail}
                        className="btn-secondary"
                      >
                        {analyzingSender === sender.senderEmail ? <Spinner size={16} /> : <Sparkles size={16} />}
                        Analyser
                      </button>
                    )}
                    <div className="flex items-center gap-1">
                      <button onClick={() => handleCreateSenderRule(sender)} className="btn-ghost px-2.5 text-brand-500 hover:bg-brand-50" title="Créer une règle : toujours archiver les futurs emails de cet expéditeur">
                        <Bolt size={18} />
                      </button>
                      <button onClick={() => handleApplyBulk(sender, 'archive')} className="btn-ghost px-2.5" title="Tout archiver (emails existants)">
                        <Archive size={18} />
                      </button>
                      <button onClick={() => handleApplyBulk(sender, 'delete')} className="btn-ghost px-2.5 text-rose-500 hover:bg-rose-50" title="Tout supprimer (emails existants)">
                        <Trash size={18} />
                      </button>
                    </div>
                  </div>
                );
              })
            )}
          </div>
        ) : (
          <div className="space-y-3">
            {subscriptions.length === 0 ? (
              <div className="card flex flex-col items-center justify-center px-6 py-20 text-center">
                <span className="mb-4 flex h-16 w-16 items-center justify-center rounded-2xl bg-amber-50 text-amber-500">
                  <BellOff size={32} />
                </span>
                <h3 className="text-lg font-bold text-ink-900">Aucun abonnement détecté</h3>
                <p className="mt-1 max-w-xs text-sm text-ink-500">
                  Synchronisez votre boîte : Mailsorter repère vos newsletters et listes de diffusion pour un désabonnement en un clic.
                </p>
              </div>
            ) : (
              <>
                <div className="card flex items-center gap-3 bg-amber-50/50 p-4">
                  <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-amber-100 text-amber-600">
                    <BellOff size={20} />
                  </span>
                  <p className="text-sm text-ink-600">
                    <span className="font-bold text-ink-900">
                      {subscriptions.filter((s) => !s.unsubscribed).length} newsletter{subscriptions.filter((s) => !s.unsubscribed).length > 1 ? 's' : ''}
                    </span>{' '}
                    encombrent votre boîte. Coupez le robinet — et archivez le passé d'un seul geste.
                  </p>
                </div>
                {subscriptions.map((sub) => {
                  const busy = unsubscribing === sub.senderEmail;
                  return (
                    <div
                      key={sub.senderEmail}
                      className={cn('card flex flex-wrap items-center gap-4 p-4', sub.unsubscribed && 'opacity-60')}
                    >
                      <span className={cn('flex h-11 w-11 shrink-0 items-center justify-center rounded-full text-sm font-bold text-white', gradientFor(sub.senderEmail))}>
                        {(sub.senderName || sub.senderEmail)[0]?.toUpperCase()}
                      </span>
                      <div className="min-w-0 flex-1">
                        <div className="truncate font-semibold text-ink-900">{sub.senderName || sub.senderEmail.split('@')[0]}</div>
                        <div className="truncate text-xs text-ink-400">{sub.senderEmail}</div>
                      </div>
                      <span className="chip bg-ink-100 text-ink-600">{sub.emailCount} email{sub.emailCount > 1 ? 's' : ''}</span>
                      {sub.oneClick && !sub.unsubscribed && (
                        <span className="chip bg-emerald-100 text-emerald-700" title="Désabonnement instantané supporté">
                          <Bolt size={13} /> 1-clic
                        </span>
                      )}
                      {sub.unsubscribed ? (
                        <span className="chip bg-emerald-100 text-emerald-700">
                          <Check size={13} /> Désabonné
                        </span>
                      ) : (
                        <div className="flex items-center gap-1.5">
                          <button
                            onClick={() => handleUnsubscribe({ messageId: sub.sampleMessageId, key: sub.senderEmail })}
                            disabled={busy}
                            className="btn-secondary"
                          >
                            {busy ? <Spinner size={16} /> : <BellOff size={16} />} Se désabonner
                          </button>
                          <button
                            onClick={() => handleUnsubscribe({ messageId: sub.sampleMessageId, alsoArchive: true, key: sub.senderEmail })}
                            disabled={busy}
                            className="btn-ghost px-2.5"
                            title="Se désabonner et archiver tous les emails de cet expéditeur"
                          >
                            <Archive size={18} />
                          </button>
                        </div>
                      )}
                    </div>
                  );
                })}
              </>
            )}
          </div>
        )}

        {selectedEmail && (
          <div className="card sticky top-20 h-[calc(100vh-7rem)] overflow-hidden">
            <EmailReader
              email={selectedEmail}
              onClose={() => setSelectedEmail(null)}
              onArchive={() => handleReaderAction(selectedEmail, 'archive')}
              onDelete={() => handleReaderAction(selectedEmail, 'delete')}
              onSnooze={(preset) => handleSnooze(selectedEmail, preset)}
              onProtect={() => handleProtect(selectedEmail)}
              onUnsubscribe={() => handleUnsubscribe({ messageId: selectedEmail.messageId })}
              unsubscribing={unsubscribing === selectedEmail.messageId}
            />
          </div>
        )}
      </div>

      {/* First-run onboarding */}
      {showWelcome && (
        <div className="fixed inset-0 z-[95] flex items-center justify-center bg-ink-950/50 p-4 backdrop-blur-sm">
          <div className="card w-full max-w-lg animate-scale-in overflow-hidden">
            <div className="bg-brand-600 px-7 py-8 text-center text-white">
              <span className="mx-auto mb-3 flex h-14 w-14 items-center justify-center rounded-2xl bg-white/15">
                <Sparkles size={28} />
              </span>
              <h2 className="font-display text-2xl font-extrabold tracking-tight">Bienvenue dans Mailsorter 👋</h2>
              <p className="mt-1 text-sm text-white/80">Votre boîte va enfin se ranger toute seule. Voici comment.</p>
            </div>
            <div className="space-y-4 p-7">
              {[
                { Icon: Sparkles, t: 'Lancez « Trier ma boîte »', d: "L'IA lit vos emails et propose une action pour chacun." },
                { Icon: Bolt, t: 'Validez d’un clic', d: '« Tout appliquer » exécute toutes les suggestions d’un coup.' },
                { Icon: Users, t: 'Activez l’auto-pilote', d: 'Mémorisez vos préférences par expéditeur pour la suite.' },
              ].map(({ Icon, t, d }, i) => (
                <div key={i} className="flex gap-3">
                  <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
                    <Icon size={18} />
                  </span>
                  <div>
                    <div className="text-sm font-bold text-ink-900">{t}</div>
                    <div className="text-sm text-ink-500">{d}</div>
                  </div>
                </div>
              ))}
              <div className="flex flex-col gap-2 pt-2 sm:flex-row">
                <button
                  onClick={() => { dismissWelcome(); emails.length ? handleAnalyze() : handleSync(); }}
                  className="btn-primary flex-1"
                >
                  <Sparkles size={18} /> {emails.length ? 'Trier ma boîte' : 'Synchroniser ma boîte'}
                </button>
                <button onClick={dismissWelcome} className="btn-secondary">
                  Explorer d'abord
                </button>
              </div>
            </div>
          </div>
        </div>
      )}

      {/* Keyboard shortcuts modal */}
      {showShortcuts && (
        <div
          className="fixed inset-0 z-[90] flex items-center justify-center bg-ink-950/40 p-4 backdrop-blur-sm"
          onClick={() => setShowShortcuts(false)}
        >
          <div className="card w-full max-w-md animate-scale-in p-6" onClick={(e) => e.stopPropagation()}>
            <div className="mb-4 flex items-center justify-between">
              <div className="flex items-center gap-2">
                <Keyboard size={20} className="text-brand-600" />
                <h3 className="text-lg font-bold text-ink-900">Raccourcis clavier</h3>
              </div>
              <button onClick={() => setShowShortcuts(false)} className="btn-ghost px-2">
                <X size={18} />
              </button>
            </div>
            <div className="space-y-1.5">
              {SHORTCUTS.map(([keys, desc]) => (
                <div key={keys} className="flex items-center justify-between rounded-lg px-2 py-1.5 text-sm hover:bg-ink-50">
                  <span className="text-ink-600">{desc}</span>
                  <kbd className="rounded-md border border-ink-200 bg-ink-50 px-2 py-0.5 font-mono text-xs font-semibold text-ink-700">
                    {keys}
                  </kbd>
                </div>
              ))}
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

export default Inbox;
