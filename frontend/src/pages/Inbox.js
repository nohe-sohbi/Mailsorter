import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useEmails, DEFAULT_QUERY } from '../contexts/EmailContext';
import { aiService, senderService, emailService, subscriptionService, protectService, labelService, searchService, apiError } from '../services/api';
import { useToast } from '../ui/Toast';
import { useConfirm } from '../ui/Confirm';
import { track } from '../lib/analytics';
import { recordTriage, getStreakState } from '../ui/streak';
import EmailReader from '../components/EmailReader';
import SnoozeButton from '../ui/SnoozeMenu';
import Spinner from '../ui/Spinner';
import Modal from '../ui/Modal';
import { EmptyState, ErrorState, Progress, LiveAnnouncer } from '../ui/primitives';
import { useScrollLock } from '../ui/scrollLock';
import { actionMeta, pastParticiple, plural, BULK_ACTIONS } from '../ui/actions';
import { cn } from '../ui/cn';
import {
  Sparkles, Archive, Trash, Tag, Search, Refresh, Inbox as InboxIcon,
  Users, Bolt, Check, X, Mail, Shield, Flame, Keyboard, BellOff, Star, Filter,
} from '../ui/icons';

const isReversible = (a) => a === 'archive' || a === 'delete';

// The four non-removing actions and how each one shows in the list before the
// server has answered. Their inverses double as the rollback when it refuses.
const FLAG_PATCH = {
  read: { isRead: true },
  unread: { isRead: false },
  star: { isStarred: true },
  unstar: { isStarred: false },
};
const FLAG_INVERSE = { read: 'unread', unread: 'read', star: 'unstar', unstar: 'star' };

// Mirrors maxBatchActionSize in backend/internal/api/batch.go.
const BATCH_LIMIT = 200;

const SHORTCUTS = [
  ['J / K', 'Naviguer entre les emails'],
  ['Entrée', "Ouvrir l'email ciblé"],
  ['X', "Sélectionner l'email ciblé"],
  ['E', 'Archiver'],
  ['Suppr / Retour arrière', 'Supprimer'],
  ['C', "Conserver l'email (en mode tri)"],
  ['U', 'Marquer comme lu'],
  ['S', 'Mettre en favori'],
  ['A', 'Tout appliquer (suggestions)'],
  ['I', "Demander un tri IA (dans l'email)"],
  ['R', 'Synchroniser'],
  ['/', 'Rechercher'],
  ['Échap', 'Fermer le lecteur ou vider la sélection'],
  ['?', 'Afficher cette aide'],
];

// Saved searches, expressed in the Gmail query language the backend already
// speaks. They exist because the search box asked people to know that language:
// the placeholder literally read "from:amazon, is:unread…".
const QUICK_FILTERS = [
  { id: 'inbox', label: 'Tout', query: DEFAULT_QUERY, Icon: InboxIcon },
  { id: 'unread', label: 'Non lus', query: 'in:inbox is:unread', Icon: Mail },
  { id: 'today', label: "Aujourd'hui", query: 'in:inbox newer_than:1d', Icon: Bolt },
  { id: 'starred', label: 'Favoris', query: 'in:inbox is:starred', Icon: Star },
  { id: 'attach', label: 'Pièces jointes', query: 'in:inbox has:attachment', Icon: Tag },
  { id: 'big', label: 'Volumineux', query: 'in:inbox larger:5M', Icon: Archive },
];

const AVATAR_TONES = ['bg-brand-fill', 'bg-info-fill', 'bg-positive-fill', 'bg-caution-fill', 'bg-danger-fill'];
const toneFor = (seed = '') => {
  let h = 0;
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) >>> 0;
  return AVATAR_TONES[h % AVATAR_TONES.length];
};

// Mirrors internal/search.SuggestName: the save dialog opens on the query's
// most specific term rather than an empty field, because naming a filter is the
// step people abandon.
function suggestSearchName(query = '') {
  const tokens = query.match(/(?:[^\s"]+|"[^"]*")+/g) || [];
  let fallback = '';
  for (const token of tokens) {
    const colon = token.indexOf(':');
    const value = colon > 0 && colon < token.length - 1 ? token.slice(colon + 1).replace(/^["']|["']$/g, '') : token;
    if (token.toLowerCase().startsWith('in:')) {
      if (!fallback) fallback = value;
      continue;
    }
    return value;
  }
  return fallback || query.trim();
}

function SaveSearchDialog({ query, onCancel, onSave }) {
  const [name, setName] = useState(() => suggestSearchName(query));
  const [saving, setSaving] = useState(false);
  const inputRef = useRef(null);

  const submit = async (e) => {
    e.preventDefault();
    if (!name.trim() || saving) return;
    setSaving(true);
    const ok = await onSave(name.trim(), query);
    // Keep the dialog open on failure: the message says what to fix, and the
    // user should not have to retype the name to try again.
    if (!ok) setSaving(false);
  };

  return (
    <Modal
      open
      onClose={onCancel}
      title="Enregistrer cette recherche"
      description="Elle rejoindra vos filtres, à un clic."
      initialFocusRef={inputRef}
    >
      <form onSubmit={submit}>
        <label htmlFor="saved-search-name" className="block text-sm font-bold text-ink-900">
          Nom
        </label>
        <input
          ref={inputRef}
          id="saved-search-name"
          className="input mt-1.5"
          value={name}
          maxLength={60}
          onChange={(e) => setName(e.target.value)}
          placeholder="Ex. Recrutement"
        />
        <p className="mt-2 text-xs text-muted">
          Requête : <span className="font-mono text-ink-700">{query}</span>
        </p>
        <div className="mt-5 flex justify-end gap-2">
          <button type="button" onClick={onCancel} className="btn-secondary">
            Annuler
          </button>
          <button type="submit" disabled={saving || !name.trim()} className="btn-primary">
            {saving ? <Spinner size={16} /> : <Star size={16} />} Enregistrer
          </button>
        </div>
      </form>
    </Modal>
  );
}

function ConfidenceRing({ value = 0, color = 'rgb(var(--brand-600))' }) {
  const pct = Math.round((value || 0) * 100);
  const r = 13;
  const c = 2 * Math.PI * r;
  return (
    <div className="relative h-9 w-9 shrink-0" title={`Confiance ${pct}%`}>
      <svg viewBox="0 0 32 32" className="h-9 w-9 -rotate-90" aria-hidden>
        <circle cx="16" cy="16" r={r} fill="none" stroke="rgb(var(--ink-200))" strokeWidth="3" />
        <circle
          cx="16" cy="16" r={r} fill="none" stroke={color} strokeWidth="3" strokeLinecap="round"
          strokeDasharray={c} strokeDashoffset={c - (pct / 100) * c}
        />
      </svg>
      <span className="absolute inset-0 flex items-center justify-center text-[10px] font-bold text-ink-700">
        {pct}
      </span>
      <span className="sr-only">Confiance {pct} %</span>
    </div>
  );
}

// The display name out of a From header.
//
// The quotes are the part that is easy to forget: RFC 5322 wraps a display name
// in them as soon as it contains anything special, and the IMAP listing writes
// that form for every named sender, so "Acme News" rendered with its quotes and
// the avatar letter was a quotation mark. EmailReader, Snoozed and History each
// strip them already; this file was the one that did not.
function senderLabel(from) {
  if (!from) return '';
  return from.split('<')[0].replace(/"/g, '').trim() || from;
}

const STAT_CARDS = [
  { key: 'inboxCount', label: 'Boîte de réception', tone: 'text-brand-600', Icon: InboxIcon, query: DEFAULT_QUERY },
  { key: 'unreadCount', label: 'Non lus', tone: 'text-caution-700', Icon: Mail, query: 'in:inbox is:unread' },
  { key: 'totalMessages', label: 'Total', tone: 'text-ink-700', Icon: Archive, query: 'in:anywhere' },
  { key: 'spamCount', label: 'Spam', tone: 'text-danger-600', Icon: Shield, query: 'in:spam' },
];

function Inbox() {
  const navigate = useNavigate();
  const toast = useToast();
  const confirm = useConfirm();
  const {
    emails, senders, subscriptions, suggestions, stats, pagination, error, activeQuery,
    loading, loadingMore, errorRetryable, fetchData, loadMoreEmails, removeEmails, patchEmail,
    addSuggestion, removeSuggestion, removeSuggestions, restoreSuggestions, markUnsubscribed,
  } = useEmails();

  const [aiAnalyzingId, setAiAnalyzingId] = useState(null);

  const [view, setView] = useState('emails');
  const [selectedEmails, setSelectedEmails] = useState([]);
  const [selectedEmail, setSelectedEmail] = useState(null);
  const [triageMode, setTriageMode] = useState(false);
  const [triageIndex, setTriageIndex] = useState(0);
  const [triageIds, setTriageIds] = useState([]);
  const [triageLabeling, setTriageLabeling] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [analyzing, setAnalyzing] = useState(false);
  const [applyingAll, setApplyingAll] = useState(false);
  const [bulkBusy, setBulkBusy] = useState(false);
  const [searchQuery, setSearchQuery] = useState('');
  const [localSenders, setLocalSenders] = useState([]);
  const [senderFilter, setSenderFilter] = useState('');
  const [analyzingSender, setAnalyzingSender] = useState(null);
  const [highConfOnly, setHighConfOnly] = useState(false);
  const [unsubscribing, setUnsubscribing] = useState(null);
  const [focusedIndex, setFocusedIndex] = useState(-1);
  const [showShortcuts, setShowShortcuts] = useState(false);
  const [showWelcome, setShowWelcome] = useState(false);
  const [labelPickerOpen, setLabelPickerOpen] = useState(false);
  const [gamify, setGamify] = useState(getStreakState);
  const [savedSearches, setSavedSearches] = useState([]);
  const [savingSearch, setSavingSearch] = useState(null);
  const [job, setJob] = useState(null);
  const [announcement, setAnnouncement] = useState('');

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

  useEffect(() => () => clearTimeout(pollRef.current), []);

  // Below lg the reader is a full-screen sheet; at lg and above it is a side
  // panel. Read synchronously at first render, not from an effect: initialising
  // to `false` would mount the desktop panel for one frame on a phone, then swap
  // containers, remounting the reader and fetching the message twice.
  const [isNarrow, setIsNarrow] = useState(
    () => typeof window !== 'undefined' && window.matchMedia('(max-width: 1023px)').matches
  );
  useEffect(() => {
    const mq = window.matchMedia('(max-width: 1023px)');
    const sync = () => setIsNarrow(mq.matches);
    sync();
    // Rotating a phone, or dragging a desktop window across the breakpoint, must
    // hand scrolling back rather than leave the page frozen.
    if (mq.addEventListener) mq.addEventListener('change', sync);
    else mq.addListener(sync);
    return () => {
      if (mq.removeEventListener) mq.removeEventListener('change', sync);
      else mq.removeListener(sync);
    };
  }, []);

  // The sheet covers the page, so the list behind it must stop scrolling:
  // otherwise flicking inside the message quietly scrolls the inbox underneath
  // and closing the reader lands the user somewhere else entirely.
  const readerIsOverlay = Boolean(selectedEmail) && isNarrow;
  useScrollLock(readerIsOverlay);

  useEffect(() => {
    if (!localStorage.getItem('mailsorter_onboarded')) setShowWelcome(true);
  }, []);

  // A selection is scoped to the list that produced it. Keeping it across a
  // query change meant "Archiver la sélection" could act on messages that were
  // no longer on screen: invisible, unreviewable collateral.
  useEffect(() => {
    setSelectedEmails([]);
    setFocusedIndex(-1);
  }, [activeQuery, view]);

  // Drop ids that have left the list (triaged elsewhere, filtered out), and
  // close the reader when the email it shows is gone: archiving from the list
  // used to leave the message open in the panel, still offering Archiver and
  // Supprimer on something that was no longer there.
  useEffect(() => {
    const live = new Set(emails.map((e) => e.messageId));
    setSelectedEmails((prev) => {
      if (prev.length === 0) return prev;
      const next = prev.filter((id) => live.has(id));
      return next.length === prev.length ? prev : next;
    });
    setSelectedEmail((prev) => (prev && !live.has(prev.messageId) ? null : prev));
  }, [emails]);

  const dismissWelcome = () => {
    localStorage.setItem('mailsorter_onboarded', '1');
    setShowWelcome(false);
  };

  const visibleSuggestions = useMemo(
    () => (highConfOnly ? suggestions.filter((s) => (s.confidence || 0) >= 0.8) : suggestions),
    [suggestions, highConfOnly]
  );

  const filteredSenders = useMemo(() => {
    const q = senderFilter.trim().toLowerCase();
    if (!q) return localSenders;
    return localSenders.filter(
      (s) =>
        (s.senderEmail || '').toLowerCase().includes(q) ||
        (s.senderName || '').toLowerCase().includes(q)
    );
  }, [localSenders, senderFilter]);

  const activeSubs = useMemo(() => subscriptions.filter((s) => !s.unsubscribed), [subscriptions]);

  const bumpGamify = (n) => setGamify(recordTriage(n));

  const formatNumber = (num) => {
    if (!num) return '0';
    if (num >= 1000) return `${(num / 1000).toFixed(1)}k`;
    return num.toString();
  };

  const formatDate = (dateStr) => {
    if (!dateStr) return '';
    const date = new Date(dateStr);
    if (Number.isNaN(date.getTime())) return '';
    const now = new Date();
    if (date.toDateString() === now.toDateString())
      return date.toLocaleTimeString('fr-FR', { hour: '2-digit', minute: '2-digit' });
    if (date.getFullYear() === now.getFullYear())
      return date.toLocaleDateString('fr-FR', { day: 'numeric', month: 'short' });
    return date.toLocaleDateString('fr-FR', { day: 'numeric', month: 'short', year: '2-digit' });
  };

  // --- Undo helper ---------------------------------------------------------
  const undoToast = (messageId, action, message) => {
    toast.action(message, 'Annuler', async () => {
      try {
        await emailService.action(messageId, action === 'archive' ? 'unarchive' : 'untrash');
        toast.success('Action annulée');
        fetchData({ forceRefresh: true, sync: false });
      } catch (err) {
        toast.error("Impossible d'annuler");
      }
    });
  };

  // --- Handlers ------------------------------------------------------------
  const runQuery = (query) => {
    setSelectedEmails([]);
    setSelectedEmail(null);
    fetchData({ forceRefresh: true, sync: false, query });
  };

  // --- Saved searches -------------------------------------------------------
  // A query worked out once ("in:inbox from:linkedin.com older_than:7d") was
  // usable exactly once: the box kept the language, not the result.
  const loadSavedSearches = useCallback(() => {
    searchService
      .list()
      .then(({ data }) => setSavedSearches(data.searches || []))
      // Silencieux : les filtres intégrés restent là, la page fonctionne sans.
      .catch(() => {});
  }, []);

  useEffect(() => {
    loadSavedSearches();
  }, [loadSavedSearches]);

  const isSaved = (query) => savedSearches.some((s) => s.query === query);

  const runSavedSearch = (saved) => {
    setSearchQuery('');
    runQuery(saved.query);
    track('saved_search_used');
    // Fire-and-forget: the counter only orders the bar, and a failed increment
    // must never cost the user their search.
    searchService.markUsed(saved.id).catch(() => {});
  };

  const saveSearch = async (name, query) => {
    try {
      const { data } = await searchService.save(name, query);
      setSavedSearches((prev) => [data, ...prev.filter((s) => s.id !== data.id)]);
      setSavingSearch(null);
      track('saved_search_created');
      toast.success(`« ${data.name} » ajoutée à vos filtres.`);
      return true;
    } catch (err) {
      toast.error(apiError(err, "La recherche n'a pas pu être enregistrée."));
      return false;
    }
  };

  const removeSavedSearch = async (saved) => {
    // Optimiste : c'est un raccourci, pas une donnée. Le rétablir coûte un clic.
    setSavedSearches((prev) => prev.filter((s) => s.id !== saved.id));
    try {
      await searchService.remove(saved.id);
    } catch (err) {
      toast.error(apiError(err, 'Suppression impossible.'));
      loadSavedSearches();
    }
  };

  const handleSync = async () => {
    setSyncing(true);
    // sync: true forces the Gmail round-trip. Previously this button reused the
    // five-minute sync throttle, so pressing it twice in a row reported
    // "Boîte synchronisée" without having synchronised anything.
    await fetchData({ forceRefresh: true, sync: true });
    setSyncing(false);
    track('inbox_sync');
    toast.success('Boîte synchronisée');
  };

  const handleSearch = (e) => {
    e.preventDefault();
    const raw = searchQuery.trim();
    if (!raw) return runQuery(DEFAULT_QUERY);
    // Stay in the inbox unless the user explicitly says otherwise. Typing
    // "facture" used to search the entire account (archive, spam and trash
    // included) while the header still claimed to show the inbox.
    const scoped = /\b(?:in|label)\s*:|\bis\s*:\s*(?:sent|draft|trash|spam)\b/i.test(raw)
      ? raw
      : `${DEFAULT_QUERY} ${raw}`;
    runQuery(scoped);
  };

  const clearSearch = () => {
    setSearchQuery('');
    runQuery(DEFAULT_QUERY);
  };

  const handleSelectEmail = (email) =>
    setSelectedEmails((prev) =>
      prev.includes(email.messageId) ? prev.filter((id) => id !== email.messageId) : [...prev, email.messageId]
    );

  const handleSelectAll = () => {
    // Computed outside the updater on purpose: React double-invokes state
    // updaters in StrictMode, so queueing another setState from inside one fires
    // it twice and makes the updater impure.
    const next = selectedEmails.length === emails.length ? [] : emails.map((e) => e.messageId);
    setSelectedEmails(next);
    setAnnouncement(
      next.length === 0
        ? 'Sélection vidée'
        : `${next.length} email${next.length > 1 ? 's' : ''} sélectionné${next.length > 1 ? 's' : ''}`
    );
  };

  const handleAnalyze = async () => {
    const raw = selectedEmails.length > 0 ? selectedEmails : emails.map((e) => e.messageId);
    // Skip emails that already have a pending suggestion locally.
    const alreadySuggested = new Set(suggestions.map((s) => s.emailId));
    const ids = raw.filter((id) => !alreadySuggested.has(id));
    if (ids.length === 0) {
      toast.info('Tous ces emails ont déjà été analysés');
      return;
    }
    const async = ids.length > ASYNC_THRESHOLD;
    track('ai_analyze', { mode: async ? 'async' : 'sync', count: ids.length });
    if (async) runAsyncAnalyze(ids);
    else runSyncAnalyze(ids);
  };

  const announceResult = ({ suggestionsCreated = 0, autoApplied = 0, cachedHits = 0, skipped = 0 }) => {
    if (autoApplied > 0) {
      bumpGamify(autoApplied);
      toast.success(`${autoApplied} email${autoApplied > 1 ? 's' : ''} auto-trié${autoApplied > 1 ? 's' : ''} (auto-pilote)`);
    }
    const extra = cachedHits > 0 ? ` · ${cachedHits} depuis le cache` : '';
    const skippedMsg = skipped > 0 ? ` · ${skipped} déjà traité${skipped > 1 ? 's' : ''}` : '';
    toast.success(
      suggestionsCreated
        ? `${suggestionsCreated} suggestion${suggestionsCreated > 1 ? 's' : ''} générée${suggestionsCreated > 1 ? 's' : ''}${extra}${skippedMsg}`
        : `Analyse terminée${skippedMsg}`
    );
  };

  const runSyncAnalyze = async (ids) => {
    setAnalyzing(true);
    try {
      const { data } = await aiService.analyzeEmails(ids);
      await fetchData({ forceRefresh: true, sync: false });
      announceResult({
        suggestionsCreated: data?.suggestions?.length || 0,
        autoApplied: data?.autoApplied || 0,
        cachedHits: data?.cachedHits || 0,
        skipped: data?.skipped || 0,
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
        await fetchData({ forceRefresh: true, sync: false });
        if (data.status === 'error') toast.error('Analyse interrompue. Réessayez.');
        else announceResult(data);
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
      track('suggestion_applied', { action: act });
      bumpGamify(1);
      if (act !== 'keep') {
        removeEmails(suggestion.emailId);
        setSelectedEmail((prev) => (prev?.messageId === suggestion.emailId ? null : prev));
      }
      const msg = `${actionMeta(act).past}`;
      if (isReversible(act)) undoToast(suggestion.emailId, act, msg);
      else toast.success(msg);
    } catch (err) {
      // Undo the optimism. Without this the suggestion vanished for good while
      // the email stayed exactly where it was.
      restoreSuggestions([suggestion]);
      toast.error("Action impossible. L'email reste en place.");
    }
  };

  const handleRejectSuggestion = async (suggestion) => {
    const id = suggestion.id || suggestion._id;
    removeSuggestion(id);
    aiService.rejectSuggestion(id).catch(() => restoreSuggestions([suggestion]));
  };

  const handleAiAnalyzeSingle = async (emailToAnalyze) => {
    const id = emailToAnalyze?.messageId;
    if (!id) return;
    setAiAnalyzingId(id);
    try {
      const { data } = await aiService.analyzeEmails([id]);
      if (data?.autoApplied > 0) {
        toast.success("Règle expéditeur auto-appliquée !");
        removeEmails(id);
        if (triageMode) handleTriageNext();
        else setSelectedEmail(null);
        fetchData({ forceRefresh: true, sync: false });
        return;
      }
      if (data?.suggestions && data.suggestions.length > 0) {
        const newSug = data.suggestions[0];
        addSuggestion(newSug);
        toast.success("Recommandation IA prête", { duration: 1800 });
      } else {
        await fetchData({ forceRefresh: true, sync: false });
        toast.error("Impossible d'obtenir une recommandation IA pour cet email.");
      }
    } catch (err) {
      if (!handleQuotaError(err)) {
        toast.error(apiError(err, "L'analyse IA a échoué. Réessayez."));
      }
    } finally {
      setAiAnalyzingId(null);
    }
  };

  const handleTriageApplySuggestion = async (suggestion) => {
    const act = suggestion.action;
    await handleApplySuggestion(suggestion);
    if (act !== 'keep') {
      const currentId = suggestion.emailId;
      setSelectedEmails((prev) => prev.filter((id) => id !== currentId));
      const nextIds = triageIds.filter((id) => id !== currentId);
      setTriageIds(nextIds);

      if (nextIds.length === 0) {
        setTriageMode(false);
        setSelectedEmail(null);
        toast.success('Tri terminé ! Tous les emails sélectionnés ont été traités.');
        return;
      }

      const nextIdx = Math.min(triageIndex, nextIds.length - 1);
      setTriageIndex(nextIdx);
      const nextId = nextIds[nextIdx];
      const nextEmail = emails.find((e) => e.messageId === nextId) || { messageId: nextId };
      setSelectedEmail(nextEmail);
    }
  };

  const handleApplyAll = async () => {
    const batch = visibleSuggestions;
    if (batch.length === 0) return;
    const destructive = batch.filter((s) => actionMeta(s.action).destructive);
    // "Tout appliquer" is also bound to a single keystroke, so it must never be
    // able to trash mail without asking.
    if (destructive.length > 0) {
      const ok = await confirm({
        title: 'Appliquer toutes les suggestions ?',
        message: `${batch.length} action${batch.length > 1 ? 's' : ''} seront appliquées, dont ${destructive.length} suppression${destructive.length > 1 ? 's' : ''}.`,
        detail: 'Les suppressions partent à la corbeille Gmail et restent récupérables 30 jours.',
        confirmLabel: 'Tout appliquer',
        danger: true,
      });
      if (!ok) return;
    }

    const ids = batch.map((s) => s.id || s._id);
    setApplyingAll(true);
    try {
      const res = await aiService.applyBatch(ids);
      const appliedIds = res.data?.appliedIds || ids;
      removeSuggestions(appliedIds);
      const appliedSet = new Set(appliedIds);
      removeEmails(batch.filter((s) => appliedSet.has(s.id || s._id) && s.action !== 'keep').map((s) => s.emailId));
      track('apply_all', { applied: res.data?.applied ?? appliedIds.length });
      bumpGamify(res.data?.applied ?? appliedIds.length);
      const n = res.data?.applied ?? appliedIds.length;
      toast.success(`${n} action${n > 1 ? 's' : ''} appliquée${n > 1 ? 's' : ''}`);
      if (res.data?.failed) toast.error(`${res.data.failed} action(s) ont échoué`);
    } catch (err) {
      toast.error("Impossible d'appliquer les suggestions");
    } finally {
      setApplyingAll(false);
    }
  };

  const handleRejectAll = () => {
    const batch = visibleSuggestions;
    const ids = batch.map((s) => s.id || s._id);
    removeSuggestions(ids);
    ids.forEach((id) => aiService.rejectSuggestion(id).catch(() => {}));
    // No "Rétablir" here: rejection is persisted server-side and there is no
    // un-reject endpoint, so the button would only put rows back on screen that
    // the next refresh would remove again. Rejecting costs nothing anyway:
    // nothing was done to the emails themselves.
    toast.info(`${ids.length} suggestion${plural(ids.length)} ignorée${plural(ids.length)}`);
  };

  // --- Bulk actions over the selection --------------------------------------
  // The whole point of the checkboxes. Until now selecting emails only scoped
  // the AI analysis: there was no way to archive, trash, label or mark a
  // selection as read without going through the model (and its quota).
  const runBulk = useCallback(
    async (action, labelName = '') => {
      const ids = selectedEmails;
      if (ids.length === 0) return;

      const meta = actionMeta(action);
      if (meta.destructive) {
        const ok = await confirm({
          title: `Supprimer ${ids.length} email${ids.length > 1 ? 's' : ''} ?`,
          message: 'Ils partent à la corbeille Gmail.',
          detail: 'Récupérables pendant 30 jours, et annulables immédiatement depuis la notification.',
          confirmLabel: 'Supprimer',
          danger: true,
        });
        if (!ok) return;
      }

      setBulkBusy(true);
      try {
        // The server caps a batch at 200 so one request can't hold a Gmail
        // connection open indefinitely. "Charger plus" makes selections larger
        // than that trivially reachable, so chunk here rather than let the user
        // meet a raw 400.
        const chunks = [];
        for (let i = 0; i < ids.length; i += BATCH_LIMIT) chunks.push(ids.slice(i, i + BATCH_LIMIT));

        const applied = [];
        let failed = 0;
        let protectedSkipped = 0;
        let reversible = false;
        let hardError = null;
        for (const chunk of chunks) {
          // Each chunk is caught on its own. A single try around the loop meant
          // a second chunk failing threw away everything the first one had
          // already done in Gmail: those emails stayed on screen, unselected
          // from nothing, with no way to undo work that had actually happened.
          try {
            const { data } = await emailService.batchAction(chunk, action, labelName);
            applied.push(...(data.applied || []));
            failed += data.failed || 0;
            protectedSkipped += data.protectedSkipped || 0;
            reversible = reversible || Boolean(data.reversible);
          } catch (err) {
            failed += chunk.length;
            hardError = err;
          }
        }
        const data = { applied, failed, protectedSkipped, reversible };
        track('bulk_selection', { action, applied: applied.length });
        bumpGamify(applied.length);

        // Archiving and trashing take the message out of the inbox view; a
        // label or a read flag does not.
        if (action === 'archive' || action === 'delete') removeEmails(applied);
        else if (action === 'read') applied.forEach((id) => patchEmail(id, { isRead: true }));
        else if (action === 'unread') applied.forEach((id) => patchEmail(id, { isRead: false }));
        else if (action === 'star') applied.forEach((id) => patchEmail(id, { isStarred: true }));
        else if (action === 'unstar') applied.forEach((id) => patchEmail(id, { isStarred: false }));

        setSelectedEmails([]);

        const parts = [`${applied.length} email${plural(applied.length)} ${pastParticiple(action, applied.length)}`];
        if (data.protectedSkipped)
          parts.push(`${data.protectedSkipped} protégé${plural(data.protectedSkipped)} ignoré${plural(data.protectedSkipped)}`);
        const message = parts.join(' · ');

        if (applied.length === 0) {
          toast.error(
            hardError
              ? hardError.response?.data?.trim?.() || "L'action groupée a échoué."
              : data.protectedSkipped
              ? 'Aucun email traité : tous sont protégés.'
              : "Aucun email n'a pu être traité."
          );
        } else if (data.reversible) {
          toast.action(message, 'Annuler', async () => {
            try {
              // Same cap as the forward path: undoing 400 archives is two calls.
              // The label name travels back too: taking a label off needs to
              // know which one, and only this closure still remembers.
              for (let i = 0; i < applied.length; i += BATCH_LIMIT) {
                await emailService.batchUndo(applied.slice(i, i + BATCH_LIMIT), action, labelName);
              }
              toast.success('Action annulée');
              fetchData({ forceRefresh: true, sync: false });
            } catch {
              toast.error("Impossible d'annuler");
            }
          });
        } else {
          toast.success(message);
        }
        if (data.failed && applied.length > 0) {
          toast.error(`${data.failed} email${plural(data.failed)} n'${data.failed > 1 ? 'ont' : 'a'} pas pu être traité${plural(data.failed)}`);
        }
      } catch (err) {
        toast.error(err.response?.data?.trim?.() || "L'action groupée a échoué.");
      } finally {
        setBulkBusy(false);
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [selectedEmails, confirm]
  );

  // Snooze the whole selection to one wake time. Same chunking as runBulk (the
  // server caps a batch at 200) and the same per-chunk isolation, so a second
  // chunk failing never discards what the first one already did in Gmail.
  const runBulkSnooze = useCallback(
    async (choice) => {
      const ids = selectedEmails;
      if (ids.length === 0) return;

      setBulkBusy(true);
      try {
        const snoozed = [];
        let failed = 0;
        let protectedSkipped = 0;
        for (let i = 0; i < ids.length; i += BATCH_LIMIT) {
          const chunk = ids.slice(i, i + BATCH_LIMIT);
          try {
            const { data } = await emailService.batchSnooze(chunk, choice);
            snoozed.push(...(data.snoozed || []));
            failed += data.failed || 0;
            protectedSkipped += data.protectedSkipped || 0;
          } catch (err) {
            failed += chunk.length;
          }
        }

        track('bulk_snooze', { preset: choice.preset || 'custom', snoozed: snoozed.length });
        bumpGamify(snoozed.length);
        removeEmails(snoozed);
        setSelectedEmails([]);

        if (snoozed.length === 0) {
          toast.error(
            protectedSkipped
              ? 'Aucun email reporté : tous sont protégés.'
              : "Aucun email n'a pu être reporté."
          );
          return;
        }
        const parts = [`${snoozed.length} email${plural(snoozed.length)} reporté${plural(snoozed.length)}`];
        if (protectedSkipped) parts.push(`${protectedSkipped} protégé${plural(protectedSkipped)} ignoré${plural(protectedSkipped)}`);
        toast.success(parts.join(' · '));
        if (failed) {
          toast.error(`${failed} email${plural(failed)} n'${failed > 1 ? 'ont' : 'a'} pas pu être reporté${plural(failed)}`);
        }
      } finally {
        setBulkBusy(false);
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [selectedEmails]
  );

  // Direct action on a single email (used by reader buttons + keyboard).
  const directAction = async (email, action) => {
    // Optimistic: the row leaves immediately so a burst of j/e/j/e actually
    // walks down the list instead of hammering the same message.
    removeEmails(email.messageId);
    setSelectedEmails((prev) => prev.filter((id) => id !== email.messageId));
    try {
      await emailService.action(email.messageId, action);
      bumpGamify(1);
      undoToast(email.messageId, action, action === 'archive' ? 'Email archivé' : 'Email déplacé vers la corbeille');
    } catch (err) {
      toast.error("L'action a échoué");
      fetchData({ forceRefresh: true, sync: false });
    }
  };

  // Read/star from the keyboard and from the reader. Unlike archive/delete these
  // leave the message in the list, so without an explicit acknowledgement the
  // keystroke would look like it did nothing at all.
  const flagAction = async (email, action) => {
    const optimistic = FLAG_PATCH[action];
    const rollback = FLAG_PATCH[FLAG_INVERSE[action]];
    if (optimistic) patchEmail(email.messageId, optimistic);
    try {
      const { data } = await emailService.batchAction([email.messageId], action);
      // The endpoint answers 200 while counting per-message failures, so a
      // rejected promise is not the only failure mode: without this the UI
      // announced "Lu" for a message Gmail had refused to touch, and the
      // optimistic patch was never rolled back.
      if (!(data.applied || []).length) {
        if (rollback) patchEmail(email.messageId, rollback);
        toast.error(data.protectedSkipped ? 'Expéditeur protégé : action ignorée.' : "L'action a échoué");
        return;
      }
      toast.success(actionMeta(action).past, { duration: 1800 });
    } catch {
      if (rollback) patchEmail(email.messageId, rollback);
      toast.error("L'action a échoué");
    }
  };

  const handleReaderAction = (email, action) => {
    setSelectedEmail(null);
    directAction(email, action);
  };

  const handleSnooze = async (email, choice) => {
    setSelectedEmail(null);
    removeEmails(email.messageId);
    setSelectedEmails((prev) => prev.filter((id) => id !== email.messageId));
    try {
      await emailService.snooze(email.messageId, choice);
      // `custom` rather than the instant itself: an exact wake time is personal
      // data and product analytics only needs to know the affordance was used.
      track('snooze', { preset: choice.preset || 'custom' });
      bumpGamify(1);
      toast.success('Email reporté, il reviendra au bon moment');
    } catch (err) {
      toast.error(apiError(err, 'Report impossible. Réessayez.'));
      fetchData({ forceRefresh: true, sync: false });
    }
  };

  // --- Step-by-step triage for selected emails ------------------------------
  const handleStartStepTriage = () => {
    if (selectedEmails.length === 0) return;
    setTriageMode(true);
    setTriageIndex(0);
    setTriageIds([...selectedEmails]);
    const first = emails.find((e) => e.messageId === selectedEmails[0]) || { messageId: selectedEmails[0] };
    setSelectedEmail(first);
    track('step_triage_start', { count: selectedEmails.length });
    setAnnouncement(`Tri pas-à-pas démarré pour ${selectedEmails.length} emails`);
  };

  const handleExitTriage = useCallback(() => {
    setTriageMode(false);
    setAnnouncement('Tri pas-à-pas terminé');
  }, []);

  const handleTriageNext = useCallback(() => {
    if (triageIndex < triageIds.length - 1) {
      const nextIdx = triageIndex + 1;
      setTriageIndex(nextIdx);
      const nextId = triageIds[nextIdx];
      const nextEmail = emails.find((e) => e.messageId === nextId) || { messageId: nextId };
      setSelectedEmail(nextEmail);
    }
  }, [triageIndex, triageIds, emails]);

  const handleTriagePrev = useCallback(() => {
    if (triageIndex > 0) {
      const prevIdx = triageIndex - 1;
      setTriageIndex(prevIdx);
      const prevId = triageIds[prevIdx];
      const prevEmail = emails.find((e) => e.messageId === prevId) || { messageId: prevId };
      setSelectedEmail(prevEmail);
    }
  }, [triageIndex, triageIds, emails]);

  const handleTriageAction = useCallback(
    async (action) => {
      const currentId = triageIds[triageIndex];
      if (!currentId) return;
      const cur = emails.find((e) => e.messageId === currentId) || { messageId: currentId };

      directAction(cur, action);

      setSelectedEmails((prev) => prev.filter((id) => id !== currentId));
      const nextIds = triageIds.filter((id) => id !== currentId);
      setTriageIds(nextIds);

      if (nextIds.length === 0) {
        setTriageMode(false);
        setSelectedEmail(null);
        toast.success('Tri terminé ! Tous les emails sélectionnés ont été traités.');
        return;
      }

      const nextIdx = Math.min(triageIndex, nextIds.length - 1);
      setTriageIndex(nextIdx);
      const nextId = nextIds[nextIdx];
      const nextEmail = emails.find((e) => e.messageId === nextId) || { messageId: nextId };
      setSelectedEmail(nextEmail);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [triageIds, triageIndex, emails, directAction]
  );

  const handleTriageKeep = useCallback(() => {
    const currentId = triageIds[triageIndex];
    if (!currentId) return;
    const cur = emails.find((e) => e.messageId === currentId) || { messageId: currentId };

    flagAction(cur, 'read');

    setSelectedEmails((prev) => prev.filter((id) => id !== currentId));
    const nextIds = triageIds.filter((id) => id !== currentId);
    setTriageIds(nextIds);
    toast.info('Email conservé dans la boîte');

    if (nextIds.length === 0) {
      setTriageMode(false);
      setSelectedEmail(null);
      toast.success('Tri terminé ! Tous les emails sélectionnés ont été traités.');
      return;
    }

    const nextIdx = Math.min(triageIndex, nextIds.length - 1);
    setTriageIndex(nextIdx);
    const nextId = nextIds[nextIdx];
    const nextEmail = emails.find((e) => e.messageId === nextId) || { messageId: nextId };
    setSelectedEmail(nextEmail);
  },
  // eslint-disable-next-line react-hooks/exhaustive-deps
  [triageIds, triageIndex, emails, flagAction]
  );

  const handleTriageSnooze = useCallback(
    async (choice) => {
      const currentId = triageIds[triageIndex];
      if (!currentId) return;
      const cur = emails.find((e) => e.messageId === currentId) || { messageId: currentId };

      await handleSnooze(cur, choice);

      setSelectedEmails((prev) => prev.filter((id) => id !== currentId));
      const nextIds = triageIds.filter((id) => id !== currentId);
      setTriageIds(nextIds);

      if (nextIds.length === 0) {
        setTriageMode(false);
        setSelectedEmail(null);
        toast.success('Tri terminé ! Tous les emails sélectionnés ont été traités.');
        return;
      }

      const nextIdx = Math.min(triageIndex, nextIds.length - 1);
      setTriageIndex(nextIdx);
      const nextId = nextIds[nextIdx];
      const nextEmail = emails.find((e) => e.messageId === nextId) || { messageId: nextId };
      setSelectedEmail(nextEmail);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [triageIds, triageIndex, emails, handleSnooze]
  );

  const handleProtect = async (email) => {
    try {
      const { data } = await protectService.add(email.from);
      toast.action(`${data.value} est désormais protégé.`, 'Gérer', () => navigate('/settings'));
    } catch (err) {
      toast.error(apiError(err, 'Protection impossible.'));
    }
  };

  const handleUnsubscribe = async ({ messageId, alsoArchive = false, key }) => {
    if (!messageId) return;
    setUnsubscribing(key || messageId);
    try {
      const { data } = await subscriptionService.unsubscribe(messageId, alsoArchive);
      const archivedNote = data.archived ? ` · ${data.archived} email${data.archived > 1 ? 's' : ''} archivé${data.archived > 1 ? 's' : ''}` : '';
      if (data.done) {
        track('unsubscribe', { mode: 'oneclick' });
        toast.success(`Désabonné en un clic${archivedNote}`);
      } else if (data.url) {
        track('unsubscribe', { mode: 'url' });
        window.open(data.url, '_blank', 'noopener,noreferrer');
        toast.info(`Page de désabonnement ouverte dans un nouvel onglet${archivedNote}`);
      } else if (data.mailto) {
        track('unsubscribe', { mode: 'mailto' });
        window.location.href = data.mailto;
        toast.info('Email de désabonnement préparé');
      }
      if (data.sender) markUnsubscribed(data.sender);
      if (alsoArchive || data.archived) fetchData({ forceRefresh: true, sync: false });
    } catch (err) {
      if (err.response?.status === 422) toast.error("Cet expéditeur ne propose pas de désabonnement automatique");
      else toast.error('Désabonnement impossible. Réessayez.');
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
        prev.map((s) => (s.senderEmail === sender.senderEmail ? { ...s, preference: { ...pref, autoApply: next } } : s))
      );
      toast.success(next ? 'Auto-pilote activé pour cet expéditeur' : 'Auto-pilote désactivé');
    } catch (err) {
      toast.error('Mise à jour impossible');
    }
  };

  const handleApplyBulk = async (sender, action) => {
    const who = sender.senderName || sender.senderEmail;
    const meta = actionMeta(action);
    const ok = await confirm({
      title: `${meta.label} tous les emails de ${who} ?`,
      message: `${sender.emailCount} email${sender.emailCount > 1 ? 's' : ''} déjà reçu${sender.emailCount > 1 ? 's' : ''} ${meta.destructive ? 'partiront à la corbeille' : 'seront archivés'}.`,
      detail: meta.destructive
        ? 'Cette action porte sur tout l’historique de cet expéditeur, pas seulement les emails affichés.'
        : 'Les futurs emails ne sont pas concernés : pour cela, créez une règle.',
      confirmLabel: meta.label,
      danger: meta.destructive,
      typeToConfirm: meta.destructive ? 'supprimer' : null,
    });
    if (!ok) return;

    try {
      const response = await aiService.applyBulk(sender.senderEmail, action, sender.preference?.defaultLabel || '');
      track('bulk_by_sender', { action, applied: response.data.applied || 0 });
      bumpGamify(response.data.applied || 0);
      toast.success(`${response.data.applied} email${response.data.applied > 1 ? 's' : ''} traité${response.data.applied > 1 ? 's' : ''}`);
      fetchData({ forceRefresh: true, sync: false });
    } catch (err) {
      toast.error('Traitement en masse impossible');
    }
  };

  const handleCreateSenderRule = async (sender) => {
    try {
      await senderService.createRule(sender.senderEmail, 'archive');
      track('rule_created', { source: 'sender' });
      toast.action(
        `Règle créée : les emails de ${sender.senderName || sender.senderEmail} seront archivés.`,
        'Voir les règles',
        () => navigate('/rules')
      );
    } catch (err) {
      toast.error(apiError(err, 'Création de la règle impossible'));
    }
  };

  // The open message, as the LIST currently knows it rather than as it was when
  // the row was clicked. selectedEmail is a snapshot: without this, every
  // optimistic patch (read, starred) landed in the list and left the reader
  // showing the old state, so its own favourite toggle never flipped.
  const openEmail = selectedEmail
    ? emails.find((e) => e.messageId === selectedEmail.messageId) || selectedEmail
    : null;

  // --- Keyboard shortcuts --------------------------------------------------
  useEffect(() => {
    const onKey = (e) => {
      const el = document.activeElement;
      const typing = el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable);

      if (e.key === 'Escape') {
        // Escape inside a field means "leave this field", not "throw away my
        // selection": pressing it in the search box used to silently discard
        // everything the user had ticked.
        if (typing) {
          el.blur();
          return;
        }
        if (triageMode) {
          handleExitTriage();
          return;
        }
        if (selectedEmail) setSelectedEmail(null);
        else if (selectedEmails.length) setSelectedEmails([]);
        return;
      }
      if (typing || e.metaKey || e.ctrlKey || e.altKey) return;
      // A dialog owns the keyboard while it is open (except reader overlay on mobile).
      if (document.querySelector('[role="dialog"]') && !readerIsOverlay) return;

      // In triage mode, single keystrokes act on the currently inspected email
      if (triageMode && openEmail) {
        if (e.key === 'e' || e.key === 'E') {
          e.preventDefault();
          handleTriageAction('archive');
          return;
        }
        if (e.key === '#' || e.key === 'Delete' || e.key === 'Backspace') {
          e.preventDefault();
          handleTriageAction('delete');
          return;
        }
        if (e.key === 'c' || e.key === 'C') {
          e.preventDefault();
          handleTriageKeep();
          return;
        }
        if (e.key === 'i' || e.key === 'I') {
          e.preventDefault();
          handleAiAnalyzeSingle(openEmail);
          return;
        }
        if (e.key === 'j' || e.key === 'ArrowRight') {
          e.preventDefault();
          handleTriageNext();
          return;
        }
        if (e.key === 'k' || e.key === 'ArrowLeft') {
          e.preventDefault();
          handleTriagePrev();
          return;
        }
      }

      if (openEmail && (e.key === 'i' || e.key === 'I')) {
        e.preventDefault();
        handleAiAnalyzeSingle(openEmail);
        return;
      }
      if (e.key === '?') { setShowShortcuts((s) => !s); return; }
      if (e.key === '/') { e.preventDefault(); searchRef.current?.focus(); return; }
      if (e.key === 'r') { handleSync(); return; }
      if (e.key === 'a' && visibleSuggestions.length) { handleApplyAll(); return; }
      if (view !== 'emails' || emails.length === 0) return;

      const cur = emails[focusedIndex];
      // Archiving removes the row, so the same index now points at the NEXT
      // email: staying put is what makes burst triage work.
      const stay = () => setFocusedIndex((i) => Math.min(i, emails.length - 2));

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
          if (cur) { directAction(cur, 'archive'); stay(); }
          break;
        case 'u':
          if (cur) flagAction(cur, 'read');
          break;
        case 's':
          if (cur) flagAction(cur, 'star');
          break;
        // '#' is Gmail's delete key but needs Alt+3 on a French AZERTY layout,
        // and Alt-modified events are filtered out above, so it was simply
        // unreachable for the app's own audience. Delete/Backspace work anywhere.
        case '#':
        case 'Delete':
        case 'Backspace':
          if (cur) { e.preventDefault(); directAction(cur, 'delete'); stay(); }
          break;
        default:
          break;
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [emails, focusedIndex, view, visibleSuggestions, selectedEmail, selectedEmails, triageMode, triageIndex, triageIds, openEmail]);

  const allSelected = emails.length > 0 && selectedEmails.length === emails.length;
  const goalHit = gamify.today >= gamify.goal;
  const hasSelection = selectedEmails.length > 0;
  rowRefs.current = [];

  const activeFilter = QUICK_FILTERS.find((f) => f.query === activeQuery);



  const openEmailSuggestion = openEmail
    ? suggestions.find((s) => s.emailId === openEmail.messageId)
    : null;

  // One element, rendered into whichever of the two containers the breakpoint
  // shows. Only one is ever visible, so React mounts a single EmailReader.
  const readerPanel = openEmail ? (
    <EmailReader
      email={openEmail}
      onClose={() => {
        setSelectedEmail(null);
        if (triageMode) setTriageMode(false);
      }}
      onRead={(id) => patchEmail(id, { isRead: true })}
      onArchive={() => (triageMode ? handleTriageAction('archive') : handleReaderAction(openEmail, 'archive'))}
      onDelete={() => (triageMode ? handleTriageAction('delete') : handleReaderAction(openEmail, 'delete'))}
      onSnooze={(choice) => (triageMode ? handleTriageSnooze(choice) : handleSnooze(openEmail, choice))}
      onFlag={(action) => flagAction(openEmail, action)}
      onProtect={() => handleProtect(openEmail)}
      onUnsubscribe={() => handleUnsubscribe({ messageId: openEmail.messageId })}
      unsubscribing={unsubscribing === openEmail.messageId}
      aiSuggestion={openEmailSuggestion}
      onAiAnalyze={handleAiAnalyzeSingle}
      aiAnalyzing={aiAnalyzingId === openEmail.messageId}
      onApplySuggestion={triageMode ? handleTriageApplySuggestion : handleApplySuggestion}
      onRejectSuggestion={handleRejectSuggestion}
      triage={
        triageMode && triageIds.length > 0
          ? {
              current: triageIndex + 1,
              total: triageIds.length,
              hasPrev: triageIndex > 0,
              hasNext: triageIndex < triageIds.length - 1,
              onPrev: handleTriagePrev,
              onNext: handleTriageNext,
              onKeep: handleTriageKeep,
              onLabel: () => setTriageLabeling(true),
            }
          : null
      }
    />
  ) : null;

  return (
    <div className="mx-auto max-w-7xl px-4 py-5 sm:px-6">
      <LiveAnnouncer message={announcement} />

      {/* Command bar */}
      <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0">
          <h1 className="font-display text-xl font-extrabold tracking-tight text-ink-900 sm:text-2xl">
            Votre boîte, sous contrôle.
          </h1>
          <p className="mt-0.5 text-sm text-muted">
            {stats?.inboxCount
              ? `${formatNumber(stats.inboxCount)} email${stats.inboxCount > 1 ? 's' : ''} en attente de tri.`
              : 'Synchronisez pour commencer le tri intelligent.'}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <form onSubmit={handleSearch} className="relative flex-1 sm:flex-none" role="search">
            <Search size={18} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-subtle" />
            <input
              ref={searchRef}
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder="Rechercher…  ( / )"
              aria-label="Rechercher dans la boîte de réception"
              className="input w-full pl-10 pr-9 sm:w-64"
            />
            {searchQuery && (
              <button
                type="button"
                onClick={clearSearch}
                className="absolute right-2 top-1/2 -translate-y-1/2 rounded-md p-1 text-subtle hover:bg-ink-100 hover:text-ink-900"
                aria-label="Effacer la recherche"
              >
                <X size={15} />
              </button>
            )}
          </form>
          <button onClick={() => setShowShortcuts(true)} className="btn-secondary btn-icon" aria-label="Raccourcis clavier" title="Raccourcis clavier (?)">
            <Keyboard size={18} />
          </button>
          <button onClick={handleSync} disabled={syncing} className="btn-secondary btn-icon" aria-label="Synchroniser" title="Synchroniser (r)">
            <Refresh size={18} className={syncing ? 'animate-spin' : ''} />
          </button>
        </div>
      </div>

      {/* Stats: each one is also the filter it describes */}
      {stats && (
        <div className="mb-4 grid grid-cols-2 gap-2.5 sm:grid-cols-4">
          {STAT_CARDS.map(({ key, label, tone, Icon, query }) => (
            <button
              key={key}
              onClick={() => runQuery(query)}
              aria-pressed={activeQuery === query}
              className={cn(
                'card flex items-center gap-3 p-3 text-left transition-colors hover:border-ink-300',
                activeQuery === query && 'border-brand-500 ring-1 ring-brand-500'
              )}
            >
              <span className={cn('flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-surface-sunken', tone)}>
                <Icon size={18} />
              </span>
              <span className="min-w-0">
                <span className={cn('block font-display text-lg font-extrabold leading-none', tone)}>
                  {formatNumber(stats[key])}
                </span>
                <span className="mt-1 block truncate text-xs font-medium text-muted">{label}</span>
              </span>
            </button>
          ))}
        </div>
      )}

      {/* Streak, folded into one compact strip. It used to occupy a full card
          of its own above the fold, pushing the first actual email off screen. */}
      <div className="card mb-4 flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-2.5">
        <span className="flex items-center gap-2">
          <Flame size={16} className={gamify.streak > 0 ? 'text-caution-600' : 'text-subtle'} />
          <span className="text-sm font-bold text-ink-900">
            {gamify.streak > 0 ? `Série de ${gamify.streak} j` : 'Lancez votre série'}
          </span>
        </span>
        <div className="flex min-w-[140px] flex-1 items-center gap-2">
          <Progress value={gamify.today} max={gamify.goal} tone={goalHit ? 'positive' : 'brand'} label="Objectif du jour" />
          <span className={cn('shrink-0 text-xs font-semibold', goalHit ? 'text-positive-700' : 'text-muted')}>
            {gamify.today}/{gamify.goal}
          </span>
        </div>
        {goalHit && <span className="text-xs font-semibold text-positive-700">Objectif atteint 🎉</span>}
      </div>

      {/* View toggle + primary action */}
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <div className="inline-flex rounded-xl border border-hairline bg-surface p-1 shadow-soft" role="tablist">
          {[
            { id: 'emails', label: 'Emails', Icon: InboxIcon },
            { id: 'senders', label: `Expéditeurs · ${localSenders.length}`, Icon: Users },
            { id: 'subs', label: `Abonnements · ${activeSubs.length}`, Icon: BellOff },
          ].map(({ id, label, Icon }) => (
            <button
              key={id}
              role="tab"
              aria-selected={view === id}
              onClick={() => setView(id)}
              className={cn(
                'flex items-center gap-2 rounded-lg px-3 py-1.5 text-sm font-semibold transition-colors',
                view === id ? 'bg-brand-fill text-white shadow-soft' : 'text-muted hover:text-ink-900'
              )}
            >
              <Icon size={16} /> {label}
            </button>
          ))}
        </div>

        {view === 'emails' && (
          <button onClick={handleAnalyze} disabled={analyzing} className="btn-primary ml-auto">
            {analyzing ? <Spinner size={18} /> : <Sparkles size={18} />}
            {analyzing ? 'Analyse…' : hasSelection ? `Trier avec l'IA · ${selectedEmails.length}` : 'Trier ma boîte'}
          </button>
        )}
      </div>

      {/* Quick filters */}
      {view === 'emails' && (
        <div className="mb-4 flex flex-wrap items-center gap-1.5">
          <Filter size={14} className="text-subtle" aria-hidden />
          {QUICK_FILTERS.map(({ id, label, query }) => (
            <button
              key={id}
              onClick={() => { setSearchQuery(''); runQuery(query); }}
              aria-pressed={activeQuery === query}
              className={cn(
                'chip transition-colors',
                activeQuery === query
                  ? 'bg-brand-fill text-white'
                  : 'bg-ink-100 text-ink-700 hover:bg-ink-200'
              )}
            >
              {label}
            </button>
          ))}
          {/* Les recherches de l'utilisateur, à la suite des filtres intégrés :
              ce sont les mêmes objets pour qui les utilise, une requête à un
              clic. Seul le nom vient de lui. */}
          {savedSearches.map((s) => (
            <span
              key={s.id}
              className={cn(
                'chip group transition-colors',
                activeQuery === s.query ? 'bg-brand-fill text-white' : 'bg-brand-50 text-brand-700 hover:bg-brand-100'
              )}
            >
              <button
                onClick={() => runSavedSearch(s)}
                aria-pressed={activeQuery === s.query}
                title={s.query}
                className="flex items-center gap-1.5"
              >
                <Search size={12} /> {s.name}
              </button>
              <button
                onClick={() => removeSavedSearch(s)}
                aria-label={`Supprimer la recherche « ${s.name} »`}
                className="ml-0.5 rounded-full p-0.5 opacity-0 transition-opacity hover:bg-brand-200 focus:opacity-100 group-hover:opacity-100"
              >
                <X size={12} />
              </button>
            </span>
          ))}

          {!activeFilter && activeQuery !== DEFAULT_QUERY && (
            <span className="chip bg-brand-50 text-brand-700">
              <Search size={12} /> {activeQuery.replace(`${DEFAULT_QUERY} `, '')}
              {/* Une recherche qu'on vient d'écrire ne vaut souvent d'être
                  gardée qu'une fois qu'elle a donné le bon résultat : le bouton
                  est donc ici, sur le filtre actif, et pas dans le champ. */}
              {!isSaved(activeQuery) && (
                <button
                  onClick={() => setSavingSearch(activeQuery)}
                  aria-label="Enregistrer cette recherche"
                  title="Enregistrer cette recherche"
                  className="ml-0.5 rounded-full p-0.5 hover:bg-brand-100"
                >
                  <Star size={12} />
                </button>
              )}
              <button onClick={clearSearch} aria-label="Effacer le filtre" className="ml-0.5 rounded-full p-0.5 hover:bg-brand-100">
                <X size={12} />
              </button>
            </span>
          )}
        </div>
      )}

      {savingSearch && (
        <SaveSearchDialog
          query={savingSearch}
          onCancel={() => setSavingSearch(null)}
          onSave={saveSearch}
        />
      )}

      {/* Async analysis progress */}
      {job && (
        <div className="card mb-4 flex flex-col gap-3 p-4 animate-fade-up sm:flex-row sm:items-center sm:gap-5">
          <div className="flex items-center gap-3">
            <span className="flex h-10 w-10 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
              <Spinner size={20} className="text-brand-600" />
            </span>
            <div>
              <div className="text-sm font-bold text-ink-900">
                {job.status === 'queued' ? 'Analyse en file…' : "L'IA trie votre boîte…"}
              </div>
              <div className="text-xs text-muted">
                {job.processed || 0}/{job.total || 0} traités
                {job.cachedHits ? ` · ${job.cachedHits} en cache` : ''}
                {job.autoApplied ? ` · ${job.autoApplied} auto` : ''}
              </div>
            </div>
          </div>
          <div className="flex-1">
            <Progress value={job.processed || 0} max={job.total || 1} label="Progression de l'analyse" />
          </div>
        </div>
      )}

      {/* Suggestions panel */}
      {visibleSuggestions.length > 0 && view === 'emails' && (
        <div className="card mb-4 overflow-hidden animate-fade-up">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-hairline bg-brand-50/60 px-5 py-3">
            <div className="flex items-center gap-2">
              <Sparkles size={18} className="text-brand-600" />
              <span className="font-bold text-ink-900">Suggestions IA</span>
              <span className="chip bg-brand-100 text-brand-700">{visibleSuggestions.length}</span>
            </div>
            <div className="flex items-center gap-2">
              <button
                onClick={() => setHighConfOnly((v) => !v)}
                aria-pressed={highConfOnly}
                className={cn('chip transition-colors', highConfOnly ? 'bg-positive-100 text-positive-700' : 'bg-ink-100 text-ink-700 hover:bg-ink-200')}
                title="N'afficher que les suggestions à haute confiance"
              >
                <Shield size={13} /> Haute confiance
              </button>
              <button onClick={handleRejectAll} className="btn-ghost btn-sm">Tout ignorer</button>
              <button onClick={handleApplyAll} disabled={applyingAll} className="btn-primary btn-sm" title="Tout appliquer (a)">
                {applyingAll ? <Spinner size={14} /> : <Bolt size={14} />} Tout appliquer
              </button>
            </div>
          </div>
          <ul className="divide-y divide-[rgb(var(--hairline))]">
            {visibleSuggestions.map((suggestion) => {
              const meta = actionMeta(suggestion.action);
              // Identity now comes with the suggestion itself; the local list is
              // only a fallback for an older backend.
              const local = emails.find((e) => e.messageId === suggestion.emailId);
              const subject = suggestion.subject || local?.subject;
              const from = suggestion.from || local?.from;
              return (
                <li key={suggestion.id || suggestion._id} className="flex items-center gap-3 px-4 py-2.5 transition-colors hover:bg-surface-sunken sm:px-5">
                  <ConfidenceRing value={suggestion.confidence} color={meta.ring} />
                  <span className={cn('chip shrink-0', meta.chip)}>
                    <meta.Icon size={13} />
                    <span className="hidden sm:inline">
                      {suggestion.action === 'label' ? suggestion.labelName || 'Libellé' : meta.label}
                    </span>
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-semibold text-ink-900">{subject || '(Sans sujet)'}</div>
                    <div className="truncate text-xs text-muted">
                      <span>{senderLabel(from) || 'Expéditeur inconnu'}</span>
                      {suggestion.reasoning ? ` · ${suggestion.reasoning}` : ''}
                    </div>
                  </div>
                  <div className="flex shrink-0 items-center gap-1">
                    <button
                      onClick={() => handleApplySuggestion(suggestion)}
                      className="rounded-lg p-2 text-positive-600 transition-colors hover:bg-positive-50"
                      aria-label={`Appliquer : ${meta.label}, ${subject || 'sans sujet'}`}
                    >
                      <Check size={18} />
                    </button>
                    <button
                      onClick={() => handleRejectSuggestion(suggestion)}
                      className="rounded-lg p-2 text-muted transition-colors hover:bg-ink-100"
                      aria-label={`Ignorer la suggestion pour ${subject || 'sans sujet'}`}
                    >
                      <X size={18} />
                    </button>
                  </div>
                </li>
              );
            })}
          </ul>
        </div>
      )}

      {/* Main content */}
      <div
        className={cn(
          'grid gap-4',
          triageMode
            ? 'lg:grid-cols-[minmax(320px,360px)_1fr]'
            : selectedEmail
            ? 'lg:grid-cols-[1fr_minmax(380px,460px)]'
            : 'grid-cols-1'
        )}
      >
        {view === 'emails' ? (
          <div className="card overflow-hidden">
            {/* List toolbar: selection lives here, and so do the actions on it */}
            {triageMode ? (
              <div className="flex items-center justify-between gap-2 border-b border-hairline bg-brand-50/70 px-3 py-2.5 sm:px-4">
                <div className="flex items-center gap-2 min-w-0">
                  <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-md bg-brand-600 text-white">
                    <Sparkles size={13} />
                  </span>
                  <div className="min-w-0">
                    <div className="text-xs font-bold text-brand-900 truncate">Tri pas-à-pas</div>
                    <div className="text-[11px] text-brand-700">
                      {triageIds.length} email{plural(triageIds.length)} restant{plural(triageIds.length)}
                    </div>
                  </div>
                </div>
                <button
                  type="button"
                  onClick={handleExitTriage}
                  className="btn-ghost btn-xs text-brand-700 hover:bg-brand-100 hover:text-brand-900 shrink-0"
                  title="Quitter le mode tri pas-à-pas (Échap)"
                >
                  Quitter
                </button>
              </div>
            ) : (
              <div className="flex flex-wrap items-center gap-2 border-b border-hairline px-3 py-2 sm:px-4">
                <button
                  onClick={handleSelectAll}
                  role="checkbox"
                  aria-checked={allSelected ? 'true' : hasSelection ? 'mixed' : 'false'}
                  disabled={emails.length === 0}
                  className="flex items-center gap-2 rounded-lg px-1.5 py-1 text-xs font-semibold text-muted hover:bg-ink-100 hover:text-ink-900 disabled:opacity-40"
                >
                  <span
                    className={cn(
                      'flex h-4 w-4 items-center justify-center rounded border',
                      allSelected || hasSelection ? 'border-brand-600 bg-brand-fill text-white' : 'border-ink-300'
                    )}
                    aria-hidden
                  >
                    {allSelected ? <Check size={11} /> : hasSelection ? <span className="h-0.5 w-2 rounded bg-white" /> : null}
                  </span>
                  {allSelected ? 'Tout désélectionner' : 'Tout sélectionner'}
                </button>

                {hasSelection ? (
                  <div className="flex flex-wrap items-center gap-1.5">
                    <span className="chip bg-brand-100 text-brand-700 font-semibold">
                      {selectedEmails.length} sélectionné{selectedEmails.length > 1 ? 's' : ''}
                    </span>
                    <button
                      type="button"
                      onClick={handleStartStepTriage}
                      disabled={bulkBusy}
                      className="btn-primary btn-sm flex items-center gap-1.5 shadow-sm"
                      title="Trier ces emails un par un"
                    >
                      <Sparkles size={14} />
                      <span>Trier un par un</span>
                    </button>
                    <div className="h-4 w-px bg-hairline mx-1 hidden sm:block" />
                  {BULK_ACTIONS.map((key) => {
                    const meta = actionMeta(key);
                    return (
                      <button
                        key={key}
                        onClick={() => (key === 'label' ? setLabelPickerOpen(true) : runBulk(key))}
                        disabled={bulkBusy}
                        className={cn(
                          'btn-ghost btn-sm',
                          meta.destructive && 'text-danger-600 hover:bg-danger-50'
                        )}
                        title={meta.label}
                      >
                        {bulkBusy ? <Spinner size={14} /> : <meta.Icon size={15} />}
                        <span className="hidden sm:inline">{meta.label}</span>
                      </button>
                    );
                  })}
                  {/* Snooze is the one bulk action that needs a "until when",
                      so it carries its own menu instead of a flat button. */}
                  <SnoozeButton
                    onSnooze={runBulkSnooze}
                    disabled={bulkBusy}
                    label={<span className="hidden sm:inline">Reporter</span>}
                    ariaLabel="Reporter la sélection"
                    title="Reporter la sélection (sortir de la boîte, revenir plus tard)"
                    className="btn-ghost btn-sm"
                    iconSize={15}
                  />
                  <button onClick={() => setSelectedEmails([])} className="btn-ghost btn-sm btn-icon" aria-label="Vider la sélection">
                    <X size={15} />
                  </button>
                </div>
              ) : (
                <span className="ml-auto text-xs font-medium text-muted">
                  {emails.length} affiché{emails.length > 1 ? 's' : ''}
                  {pagination.resultSizeEstimate > 0 && ` · ~${formatNumber(pagination.resultSizeEstimate)} au total`}
                </span>
              )}
            </div>
            )}

            {/* An error with results still on screen: the list stays (it is
                still valid, just stale) but the failure has to be visible and
                retryable rather than silently swallowed. */}
            {error && emails.length > 0 && (
              <div
                role="alert"
                className="flex flex-wrap items-center gap-3 border-b border-hairline bg-danger-50 px-4 py-2.5"
              >
                <span className="flex-1 text-sm text-danger-700">
                  {errorRetryable
                    ? `Actualisation impossible : ${error} Les emails affichés datent de la dernière synchronisation réussie.`
                    : error}
                </span>
                {errorRetryable ? (
                  <button onClick={() => fetchData({ forceRefresh: true, sync: true })} className="btn-secondary btn-sm">
                    Réessayer
                  </button>
                ) : (
                  /* Retrying is the one thing that cannot help here: the filter does
                     not exist on this mailbox. Getting back to one that works is what
                     the user actually needs. */
                  <button
                    onClick={() => { setSearchQuery(''); runQuery(DEFAULT_QUERY); }}
                    className="btn-secondary btn-sm"
                  >
                    Revenir à la boîte
                  </button>
                )}
              </div>
            )}

            {loading && emails.length === 0 ? (
              <div className="divide-y divide-[rgb(var(--hairline))]">
                {Array.from({ length: 8 }).map((_, i) => (
                  <div key={i} className="flex items-center gap-3 px-4 py-3.5">
                    <div className="skeleton h-10 w-10 rounded-full" />
                    <div className="flex-1 space-y-2">
                      <div className="skeleton h-3 w-1/3" />
                      <div className="skeleton h-3 w-2/3" />
                    </div>
                  </div>
                ))}
              </div>
            ) : error && emails.length === 0 ? (
              // A failed load must never wear the celebratory empty state.
              <ErrorState
                title="Impossible de charger votre boîte"
                message={error}
                onRetry={() => fetchData({ forceRefresh: true, sync: true })}
              />
            ) : emails.length === 0 ? (
              <EmptyState
                Icon={Check}
                tone="positive"
                title={activeQuery === DEFAULT_QUERY ? 'Inbox Zero atteint 🎉' : 'Aucun email pour ce filtre'}
                description={
                  activeQuery === DEFAULT_QUERY
                    ? 'Plus rien à trier ici. Synchronisez pour récupérer de nouveaux emails.'
                    : 'Essayez un autre filtre, ou revenez à la boîte complète.'
                }
                action={
                  activeQuery === DEFAULT_QUERY ? (
                    <button onClick={handleSync} className="btn-secondary">
                      <Refresh size={16} /> Synchroniser
                    </button>
                  ) : (
                    <button onClick={clearSearch} className="btn-secondary">
                      Revenir à la boîte
                    </button>
                  )
                }
              />
            ) : (
              <ul className="divide-y divide-[rgb(var(--hairline))]">
                {(triageMode
                  ? triageIds.map((id) => emails.find((e) => e.messageId === id) || { messageId: id, subject: 'Chargement...' })
                  : emails
                ).map((email, idx) => {
                  const name = senderLabel(email.from) || '?';
                  const isActive = selectedEmail?.messageId === email.messageId;
                  const isChecked = selectedEmails.includes(email.messageId);
                  const isFocused = idx === focusedIndex;
                  return (
                    <li
                      key={email.messageId}
                      ref={(el) => (rowRefs.current[idx] = el)}
                      className={cn(
                        'group flex items-center gap-3 px-3 py-2.5 transition-colors sm:px-4',
                        isActive ? 'bg-brand-50' : 'hover:bg-surface-sunken',
                        isChecked && 'bg-brand-50/60',
                        isFocused && 'ring-2 ring-inset ring-brand-500'
                      )}
                    >
                      <button
                        onClick={() => handleSelectEmail(email)}
                        role="checkbox"
                        aria-checked={isChecked}
                        aria-label={`Sélectionner : ${email.subject || 'sans sujet'}, de ${name}`}
                        className={cn(
                          'flex h-5 w-5 shrink-0 items-center justify-center rounded-md border transition-all',
                          isChecked ? 'border-brand-600 bg-brand-fill text-white' : 'border-ink-300 group-hover:border-ink-400'
                        )}
                      >
                        {isChecked && <Check size={13} />}
                      </button>
                      <span
                        className={cn(
                          'relative flex h-10 w-10 shrink-0 items-center justify-center rounded-full text-sm font-bold text-white',
                          toneFor(email.from)
                        )}
                        aria-hidden
                      >
                        {name[0]?.toUpperCase() || '?'}
                        {!email.isRead && (
                          <span className="absolute -right-0.5 -top-0.5 h-3 w-3 rounded-full border-2 border-[rgb(var(--surface))] bg-brand-500" />
                        )}
                      </span>
                      <button
                        onClick={() => {
                          if (triageMode) {
                            const foundIdx = triageIds.indexOf(email.messageId);
                            if (foundIdx !== -1) setTriageIndex(foundIdx);
                            setSelectedEmail(email);
                          } else {
                            setFocusedIndex(idx);
                            setSelectedEmail(email);
                          }
                        }}
                        className="min-w-0 flex-1 text-left"
                      >
                        <span className="flex items-baseline justify-between gap-2">
                          <span className={cn('truncate text-sm', email.isRead ? 'font-medium text-ink-700' : 'font-bold text-ink-900')}>
                            {name}
                            {!email.isRead && <span className="sr-only"> (non lu)</span>}
                          </span>
                          <span className="flex shrink-0 items-center gap-1.5">
                            {triageMode && isActive && (
                              <span className="chip bg-brand-100 text-brand-800 text-[10px] font-bold">
                                En cours
                              </span>
                            )}
                            {/* Starring had no visible effect at all: the action
                                fired, and the row looked exactly the same. */}
                            {(email.isStarred || (email.labelIds || []).includes('STARRED')) && (
                              <Star size={13} className="text-caution-600" aria-label="Favori" />
                            )}
                            <span className="text-xs text-muted">{formatDate(email.receivedDate)}</span>
                          </span>
                        </span>
                        <span className={cn('block truncate text-sm', email.isRead ? 'text-ink-600' : 'font-semibold text-ink-800')}>
                          {email.subject || '(Sans sujet)'}
                        </span>
                        <span className="block truncate text-xs text-muted">{email.snippet}</span>
                      </button>
                      {/* Per-row quick actions, revealed on hover/focus */}
                      <span className="hidden shrink-0 items-center gap-0.5 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100 sm:flex">
                        <button
                          onClick={() => directAction(email, 'archive')}
                          className="btn-ghost btn-sm btn-icon"
                          aria-label={`Archiver : ${email.subject || 'sans sujet'}`}
                          title="Archiver"
                        >
                          <Archive size={16} />
                        </button>
                        <button
                          onClick={() => directAction(email, 'delete')}
                          className="btn-ghost btn-sm btn-icon text-danger-600 hover:bg-danger-50"
                          aria-label={`Supprimer : ${email.subject || 'sans sujet'}`}
                          title="Supprimer"
                        >
                          <Trash size={16} />
                        </button>
                      </span>
                    </li>
                  );
                })}

                {pagination.nextPageToken && !triageMode && (
                  <li className="p-3">
                    <button onClick={loadMoreEmails} disabled={loadingMore} className="btn-secondary w-full">
                      {loadingMore ? <Spinner size={18} /> : null}
                      {loadingMore ? 'Chargement…' : "Charger plus d'emails"}
                    </button>
                  </li>
                )}
              </ul>
            )}
          </div>
        ) : view === 'senders' ? (
          <div className="space-y-3">
            {localSenders.length > 4 && (
              <div className="relative">
                <Search size={16} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-subtle" />
                <input
                  value={senderFilter}
                  onChange={(e) => setSenderFilter(e.target.value)}
                  placeholder="Filtrer les expéditeurs…"
                  aria-label="Filtrer les expéditeurs"
                  className="input pl-10"
                />
              </div>
            )}
            {filteredSenders.length === 0 ? (
              <div className="card">
                <EmptyState
                  Icon={Users}
                  title={senderFilter ? 'Aucun expéditeur ne correspond' : "Aucun expéditeur pour l'instant"}
                  description={
                    senderFilter
                      ? 'Essayez un autre terme.'
                      : 'Synchronisez votre boîte pour voir qui vous écrit le plus.'
                  }
                />
              </div>
            ) : (
              filteredSenders.map((sender) => {
                const pref = sender.preference;
                const prefMeta = pref ? actionMeta(pref.defaultAction) : null;
                return (
                  <div key={sender.senderEmail} className="card flex flex-wrap items-center gap-3 p-4">
                    <span className={cn('flex h-11 w-11 shrink-0 items-center justify-center rounded-full text-sm font-bold text-white', toneFor(sender.senderEmail))} aria-hidden>
                      {(sender.senderName || sender.senderEmail)[0]?.toUpperCase()}
                    </span>
                    <div className="min-w-0 flex-1">
                      <div className="truncate font-semibold text-ink-900">{sender.senderName || sender.senderEmail.split('@')[0]}</div>
                      <div className="truncate text-xs text-muted">{sender.senderEmail}</div>
                    </div>
                    <span className="chip bg-ink-100 text-ink-700">{sender.emailCount} emails</span>
                    {pref ? (
                      <>
                        <span className={cn('chip', prefMeta.chip)}>
                          <prefMeta.Icon size={13} />
                          {prefMeta.label}
                        </span>
                        <button
                          onClick={() => handleToggleAutoApply(sender)}
                          aria-pressed={Boolean(pref.autoApply)}
                          className={cn('chip transition-colors', pref.autoApply ? 'bg-positive-100 text-positive-700' : 'bg-ink-100 text-ink-700 hover:bg-ink-200')}
                          title="Appliquer automatiquement à chaque tri"
                        >
                          <Bolt size={13} /> Auto-pilote {pref.autoApply ? 'ON' : 'OFF'}
                        </button>
                      </>
                    ) : (
                      <button
                        onClick={() => handleAnalyzeSender(sender)}
                        disabled={analyzingSender === sender.senderEmail}
                        className="btn-secondary btn-sm"
                      >
                        {analyzingSender === sender.senderEmail ? <Spinner size={16} /> : <Sparkles size={16} />}
                        Analyser
                      </button>
                    )}
                    <div className="flex items-center gap-0.5">
                      <button
                        onClick={() => handleCreateSenderRule(sender)}
                        className="btn-ghost btn-sm btn-icon text-brand-600 hover:bg-brand-50"
                        aria-label={`Créer une règle pour ${sender.senderEmail}`}
                        title="Créer une règle : toujours archiver les futurs emails de cet expéditeur"
                      >
                        <Bolt size={17} />
                      </button>
                      <button
                        onClick={() => handleApplyBulk(sender, 'archive')}
                        className="btn-ghost btn-sm btn-icon"
                        aria-label={`Tout archiver de ${sender.senderEmail}`}
                        title="Tout archiver (emails existants)"
                      >
                        <Archive size={17} />
                      </button>
                      <button
                        onClick={() => handleApplyBulk(sender, 'delete')}
                        className="btn-ghost btn-sm btn-icon text-danger-600 hover:bg-danger-50"
                        aria-label={`Tout supprimer de ${sender.senderEmail}`}
                        title="Tout supprimer (emails existants)"
                      >
                        <Trash size={17} />
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
              <div className="card">
                <EmptyState
                  Icon={BellOff}
                  tone="caution"
                  title="Aucun abonnement détecté"
                  description="Synchronisez votre boîte : Mailsorter repère vos newsletters et listes de diffusion pour un désabonnement en un clic."
                />
              </div>
            ) : (
              <>
                <div className="card flex items-center gap-3 bg-caution-50 p-4">
                  <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-caution-100 text-caution-700">
                    <BellOff size={20} />
                  </span>
                  <p className="text-sm text-ink-700">
                    <span className="font-bold text-ink-900">
                      {activeSubs.length} newsletter{activeSubs.length > 1 ? 's' : ''}
                    </span>{' '}
                    encombrent votre boîte. Coupez le robinet, et archivez le passé d'un seul geste.
                  </p>
                </div>
                {subscriptions.map((sub) => {
                  const busy = unsubscribing === sub.senderEmail;
                  return (
                    <div key={sub.senderEmail} className={cn('card flex flex-wrap items-center gap-3 p-4', sub.unsubscribed && 'opacity-60')}>
                      <span className={cn('flex h-11 w-11 shrink-0 items-center justify-center rounded-full text-sm font-bold text-white', toneFor(sub.senderEmail))} aria-hidden>
                        {(sub.senderName || sub.senderEmail)[0]?.toUpperCase()}
                      </span>
                      <div className="min-w-0 flex-1">
                        <div className="truncate font-semibold text-ink-900">{sub.senderName || sub.senderEmail.split('@')[0]}</div>
                        <div className="truncate text-xs text-muted">{sub.senderEmail}</div>
                      </div>
                      <span className="chip bg-ink-100 text-ink-700">{sub.emailCount} email{sub.emailCount > 1 ? 's' : ''}</span>
                      {sub.oneClick && !sub.unsubscribed && (
                        <span className="chip bg-positive-100 text-positive-700" title="Désabonnement instantané supporté">
                          <Bolt size={13} /> 1-clic
                        </span>
                      )}
                      {sub.unsubscribed ? (
                        <span className="chip bg-positive-100 text-positive-700">
                          <Check size={13} /> Désabonné
                        </span>
                      ) : (
                        <div className="flex items-center gap-1.5">
                          <button
                            onClick={() => handleUnsubscribe({ messageId: sub.sampleMessageId, key: sub.senderEmail })}
                            disabled={busy}
                            className="btn-secondary btn-sm"
                          >
                            {busy ? <Spinner size={16} /> : <BellOff size={16} />} Se désabonner
                          </button>
                          <button
                            onClick={() => handleUnsubscribe({ messageId: sub.sampleMessageId, alsoArchive: true, key: sub.senderEmail })}
                            disabled={busy}
                            className="btn-ghost btn-sm btn-icon"
                            aria-label={`Se désabonner et archiver tous les emails de ${sub.senderEmail}`}
                            title="Se désabonner et archiver tous les emails de cet expéditeur"
                          >
                            <Archive size={17} />
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

        {/* Reader: a side panel on a wide screen, a full-screen sheet below it.
            It used to be a grid cell that simply did not exist under lg, so
            tapping an email on a phone appeared to do nothing at all. */}
        {/* A side panel on a wide screen, a full-screen sheet below it. The
            reader used to live in a grid column that simply did not exist under
            lg, so tapping an email on a phone appeared to do nothing.

            Exactly ONE container is mounted, chosen from the media query rather
            than by rendering both and hiding one with CSS: two mounted readers
            would each fetch the message and each mark it read.

            Rendered as an element, never as <ReaderPanel />: a component defined
            inside this one gets a fresh identity on every render, so React would
            tear it down and rebuild it each time, cancelling its own in-flight
            body request and leaving it stuck on the loading skeleton forever. */}
        {selectedEmail &&
          (readerIsOverlay ? (
            // Full-screen sheet: it covers the page, so it has to declare itself
            // as a dialog. Otherwise a screen-reader user keeps browsing the
            // inbox list that is still in the accessibility tree behind it.
            <div
              role="dialog"
              aria-modal="true"
              aria-label={`Email : ${selectedEmail.subject || 'sans sujet'}`}
              className="fixed inset-0 z-[90] bg-surface lg:hidden"
            >
              {readerPanel}
            </div>
          ) : (
            <div className="card sticky top-20 h-[calc(100vh-7rem)] overflow-hidden">{readerPanel}</div>
          ))}
      </div>

      {/* Label picker for the bulk "Étiqueter" action and triage mode */}
      <LabelPicker
        open={labelPickerOpen || triageLabeling}
        count={triageLabeling ? 1 : selectedEmails.length}
        onClose={() => {
          setLabelPickerOpen(false);
          setTriageLabeling(false);
        }}
        onPick={async (name) => {
          if (triageLabeling) {
            setTriageLabeling(false);
            const currentId = triageIds[triageIndex];
            if (currentId) {
              try {
                await emailService.batchAction([currentId], 'label', name);
                toast.success(`Étiquette "${name}" appliquée`);
                handleTriageNext();
              } catch {
                toast.error("Impossible d'appliquer l'étiquette");
              }
            }
          } else {
            setLabelPickerOpen(false);
            runBulk('label', name);
          }
        }}
      />

      {/* First-run onboarding */}
      <Modal
        open={showWelcome}
        onClose={dismissWelcome}
        size="lg"
        title="Bienvenue dans Mailsorter 👋"
        description="Votre boîte va enfin se ranger toute seule. Voici les quatre leviers."
        footer={
          <>
            <button onClick={dismissWelcome} className="btn-secondary w-full sm:w-auto">Explorer d'abord</button>
            <button
              onClick={() => { dismissWelcome(); emails.length ? handleAnalyze() : handleSync(); }}
              className="btn-primary w-full sm:w-auto"
            >
              <Sparkles size={18} /> {emails.length ? 'Trier ma boîte' : 'Synchroniser ma boîte'}
            </button>
          </>
        }
      >
        <div className="space-y-4">
          {[
            { Icon: Sparkles, t: 'Trier avec l’IA', d: "L'IA lit vos emails et propose une action pour chacun. Vous validez, en un clic ou tout d'un coup." },
            { Icon: Bolt, t: 'Encoder vos évidences en règles', d: 'Les règles trient sans IA, gratuitement et sans quota. Un aperçu montre ce qu’elles feraient avant de rien toucher.' },
            { Icon: BellOff, t: 'Couper le robinet', d: "L'onglet Abonnements repère vos newsletters et vous désabonne, souvent en un clic." },
            { Icon: Shield, t: 'Ne rien perdre', d: 'Chaque action est journalisée et annulable, et vos expéditeurs protégés ne sont jamais touchés.' },
          ].map(({ Icon, t, d }, i) => (
            <div key={i} className="flex gap-3">
              <span className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-brand-50 text-brand-600">
                <Icon size={18} />
              </span>
              <div>
                <div className="text-sm font-bold text-ink-900">{t}</div>
                <div className="text-sm text-muted">{d}</div>
              </div>
            </div>
          ))}
        </div>
      </Modal>

      {/* Keyboard shortcuts */}
      <Modal open={showShortcuts} onClose={() => setShowShortcuts(false)} title="Raccourcis clavier" size="md">
        <div className="space-y-1">
          {SHORTCUTS.map(([keys, desc]) => (
            <div key={keys} className="flex items-center justify-between gap-4 rounded-lg px-2 py-1.5 text-sm hover:bg-surface-sunken">
              <span className="text-ink-700">{desc}</span>
              <kbd className="shrink-0 rounded-md border border-hairline bg-surface-sunken px-2 py-0.5 font-mono text-xs font-semibold text-ink-800">
                {keys}
              </kbd>
            </div>
          ))}
        </div>
      </Modal>
    </div>
  );

}

// LabelPicker offers the user's real Gmail labels while still allowing a new
// one: /api/labels existed and was never called, so labelling meant typing a
// name blind and hoping it matched.
function LabelPicker({ open, count, onClose, onPick }) {
  const [labels, setLabels] = useState(null);
  const [value, setValue] = useState('');
  const inputRef = useRef(null);

  useEffect(() => {
    if (!open) return;
    setValue('');
    labelService
      .list()
      .then(({ data }) =>
        setLabels(
          (data || [])
            .filter((l) => l?.type === 'user' && l?.name)
            .map((l) => l.name)
            .sort((a, b) => a.localeCompare(b, 'fr'))
        )
      )
      .catch(() => setLabels([]));
  }, [open]);

  const trimmed = value.trim();
  const isNew = trimmed && labels && !labels.some((l) => l.toLowerCase() === trimmed.toLowerCase());

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Étiqueter la sélection"
      description={`${count} email${count > 1 ? 's' : ''} recevront ce libellé.`}
      size="sm"
      initialFocusRef={inputRef}
      footer={
        <>
          <button onClick={onClose} className="btn-secondary w-full sm:w-auto">Annuler</button>
          <button onClick={() => onPick(trimmed)} disabled={!trimmed} className="btn-primary w-full sm:w-auto">
            <Tag size={16} /> Étiqueter
          </button>
        </>
      }
    >
      <label className="block">
        <span className="mb-1.5 block text-sm font-semibold text-ink-700">Libellé</span>
        <input
          ref={inputRef}
          list="mailsorter-labels"
          className="input"
          value={value}
          onChange={(e) => setValue(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && trimmed) onPick(trimmed);
          }}
          placeholder={labels === null ? 'Chargement…' : 'Choisir ou créer…'}
          autoComplete="off"
        />
        <datalist id="mailsorter-labels">
          {(labels || []).map((l) => (
            <option key={l} value={l} />
          ))}
        </datalist>
      </label>
      {isNew && (
        <p className="mt-2 text-xs text-muted">
          <span className="font-semibold text-caution-700">Nouveau libellé</span> : « {trimmed} » sera créé dans Gmail.
        </p>
      )}
      {labels && labels.length > 0 && (
        <div className="mt-4 flex flex-wrap gap-1.5">
          {labels.slice(0, 8).map((l) => (
            <button key={l} onClick={() => setValue(l)} className="chip bg-ink-100 text-ink-700 hover:bg-ink-200">
              <Tag size={12} /> {l}
            </button>
          ))}
        </div>
      )}
    </Modal>
  );
}

export default Inbox;
