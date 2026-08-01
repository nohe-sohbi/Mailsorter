import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useEmails, DEFAULT_QUERY } from '../contexts/EmailContext';
import { aiService, senderService, emailService, subscriptionService, protectService, labelService } from '../services/api';
import { useToast } from '../ui/Toast';
import { useConfirm } from '../ui/Confirm';
import { track } from '../lib/analytics';
import { recordTriage, getStreakState } from '../ui/streak';
import EmailReader from '../components/EmailReader';
import Spinner from '../ui/Spinner';
import Modal from '../ui/Modal';
import { EmptyState, ErrorState, Progress, LiveAnnouncer } from '../ui/primitives';
import { actionMeta, BULK_ACTIONS } from '../ui/actions';
import { cn } from '../ui/cn';
import {
  Sparkles, Archive, Trash, Tag, Search, Refresh, Inbox as InboxIcon,
  Users, Bolt, Check, X, Mail, Shield, Flame, Keyboard, BellOff, Star, Filter,
} from '../ui/icons';

const isReversible = (a) => a === 'archive' || a === 'delete';

const SHORTCUTS = [
  ['J / K', 'Naviguer entre les emails'],
  ['Entrée', "Ouvrir l'email ciblé"],
  ['X', "Sélectionner l'email ciblé"],
  ['E', 'Archiver'],
  ['Suppr / Retour arrière', 'Supprimer'],
  ['U', 'Marquer comme lu'],
  ['S', 'Mettre en favori'],
  ['A', 'Tout appliquer (suggestions)'],
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

const AVATAR_TONES = ['bg-brand-500', 'bg-info-500', 'bg-positive-500', 'bg-caution-500', 'bg-danger-500'];
const toneFor = (seed = '') => {
  let h = 0;
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) >>> 0;
  return AVATAR_TONES[h % AVATAR_TONES.length];
};

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
    loading, loadingMore, fetchData, loadMoreEmails, removeEmails, patchEmail,
    removeSuggestion, removeSuggestions, restoreSuggestions, markUnsubscribed,
  } = useEmails();

  const [view, setView] = useState('emails');
  const [selectedEmails, setSelectedEmails] = useState([]);
  const [selectedEmail, setSelectedEmail] = useState(null);
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

  useEffect(() => {
    if (!localStorage.getItem('mailsorter_onboarded')) setShowWelcome(true);
  }, []);

  // A selection is scoped to the list that produced it. Keeping it across a
  // query change meant "Archiver la sélection" could act on messages that were
  // no longer on screen — invisible, unreviewable collateral.
  useEffect(() => {
    setSelectedEmails([]);
    setFocusedIndex(-1);
  }, [activeQuery, view]);

  // Drop ids that have left the list (triaged elsewhere, filtered out).
  useEffect(() => {
    setSelectedEmails((prev) => {
      if (prev.length === 0) return prev;
      const live = new Set(emails.map((e) => e.messageId));
      const next = prev.filter((id) => live.has(id));
      return next.length === prev.length ? prev : next;
    });
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
    // "facture" used to search the entire account — archive, spam and trash
    // included — while the header still claimed to show the inbox.
    const scoped = /\b(in|label|is:sent|is:draft)\s*:/i.test(raw) ? raw : `${DEFAULT_QUERY} ${raw}`;
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
    setSelectedEmails((prev) => {
      const next = prev.length === emails.length ? [] : emails.map((e) => e.messageId);
      setAnnouncement(
        next.length === 0 ? 'Sélection vidée' : `${next.length} email${next.length > 1 ? 's' : ''} sélectionné${next.length > 1 ? 's' : ''}`
      );
      return next;
    });
  };

  const handleAnalyze = async () => {
    const ids = selectedEmails.length > 0 ? selectedEmails : emails.map((e) => e.messageId);
    if (ids.length === 0) {
      toast.error('Aucun email à analyser');
      return;
    }
    const async = ids.length > ASYNC_THRESHOLD;
    track('ai_analyze', { mode: async ? 'async' : 'sync', count: ids.length });
    if (async) runAsyncAnalyze(ids);
    else runSyncAnalyze(ids);
  };

  const announceResult = ({ suggestionsCreated = 0, autoApplied = 0, cachedHits = 0 }) => {
    if (autoApplied > 0) {
      bumpGamify(autoApplied);
      toast.success(`${autoApplied} email${autoApplied > 1 ? 's' : ''} auto-trié${autoApplied > 1 ? 's' : ''} (auto-pilote)`);
    }
    const extra = cachedHits > 0 ? ` · ${cachedHits} depuis le cache` : '';
    toast.success(
      suggestionsCreated
        ? `${suggestionsCreated} suggestion${suggestionsCreated > 1 ? 's' : ''} générée${suggestionsCreated > 1 ? 's' : ''}${extra}`
        : 'Analyse terminée'
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
      if (act !== 'keep') removeEmails(suggestion.emailId);
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
    toast.action('Suggestions ignorées', 'Rétablir', () => restoreSuggestions(batch));
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
        const { data } = await emailService.batchAction(ids, action, labelName);
        const applied = data.applied || [];
        track('bulk_selection', { action, applied: applied.length });
        bumpGamify(applied.length);

        // Archiving and trashing take the message out of the inbox view; a
        // label or a read flag does not.
        if (action === 'archive' || action === 'delete') removeEmails(applied);
        else if (action === 'read') applied.forEach((id) => patchEmail(id, { isRead: true }));
        else if (action === 'unread') applied.forEach((id) => patchEmail(id, { isRead: false }));

        setSelectedEmails([]);

        const parts = [`${applied.length} email${applied.length > 1 ? 's' : ''} ${meta.past.toLowerCase()}`];
        if (data.protectedSkipped) parts.push(`${data.protectedSkipped} protégé${data.protectedSkipped > 1 ? 's' : ''} ignoré${data.protectedSkipped > 1 ? 's' : ''}`);
        const message = parts.join(' · ');

        if (data.reversible && applied.length > 0) {
          toast.action(message, 'Annuler', async () => {
            try {
              await emailService.batchUndo(applied, action);
              toast.success('Action annulée');
              fetchData({ forceRefresh: true, sync: false });
            } catch {
              toast.error("Impossible d'annuler");
            }
          });
        } else {
          toast.success(message);
        }
        if (data.failed) toast.error(`${data.failed} email(s) n'ont pas pu être traités`);
      } catch (err) {
        toast.error(err.response?.data?.trim?.() || "L'action groupée a échoué.");
      } finally {
        setBulkBusy(false);
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [selectedEmails, confirm]
  );

  // Direct action on a single email (used by reader buttons + keyboard).
  const directAction = async (email, action) => {
    // Optimistic: the row leaves immediately so a burst of j/e/j/e actually
    // walks down the list instead of hammering the same message.
    removeEmails(email.messageId);
    try {
      await emailService.action(email.messageId, action);
      bumpGamify(1);
      undoToast(email.messageId, action, action === 'archive' ? 'Email archivé' : 'Email déplacé vers la corbeille');
    } catch (err) {
      toast.error("L'action a échoué");
      fetchData({ forceRefresh: true, sync: false });
    }
  };

  const flagAction = async (email, action) => {
    const patch = action === 'read' ? { isRead: true } : {};
    patchEmail(email.messageId, patch);
    try {
      await emailService.batchAction([email.messageId], action);
    } catch {
      toast.error("L'action a échoué");
      fetchData({ forceRefresh: true, sync: false });
    }
  };

  const handleReaderAction = (email, action) => {
    setSelectedEmail(null);
    directAction(email, action);
  };

  const handleSnooze = async (email, preset) => {
    setSelectedEmail(null);
    removeEmails(email.messageId);
    try {
      await emailService.snooze(email.messageId, preset);
      track('snooze', { preset });
      bumpGamify(1);
      toast.success('Email reporté, il reviendra au bon moment');
    } catch (err) {
      toast.error('Report impossible. Réessayez.');
      fetchData({ forceRefresh: true, sync: false });
    }
  };

  const handleProtect = async (email) => {
    try {
      const { data } = await protectService.add(email.from);
      toast.action(`${data.value} est désormais protégé.`, 'Gérer', () => navigate('/settings'));
    } catch (err) {
      toast.error(err.response?.data?.trim() || 'Protection impossible.');
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
      toast.error(err.response?.data?.trim() || 'Création de la règle impossible');
    }
  };

  // --- Keyboard shortcuts --------------------------------------------------
  useEffect(() => {
    const onKey = (e) => {
      if (e.key === 'Escape') {
        if (selectedEmail) setSelectedEmail(null);
        else if (selectedEmails.length) setSelectedEmails([]);
        return;
      }
      const el = document.activeElement;
      const typing = el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable);
      if (typing || e.metaKey || e.ctrlKey || e.altKey) return;
      // A dialog owns the keyboard while it is open.
      if (document.querySelector('[role="dialog"]')) return;

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
        // and Alt-modified events are filtered out above — so it was simply
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
  }, [emails, focusedIndex, view, visibleSuggestions, selectedEmail, selectedEmails]);

  const allSelected = emails.length > 0 && selectedEmails.length === emails.length;
  const goalHit = gamify.today >= gamify.goal;
  const hasSelection = selectedEmails.length > 0;
  rowRefs.current = [];

  const activeFilter = QUICK_FILTERS.find((f) => f.query === activeQuery);

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

      {/* Stats — each one is also the filter it describes */}
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

      {/* Streak — folded into one compact strip. It used to occupy a full card
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
          <span className={cn('shrink-0 text-xs font-semibold', goalHit ? 'text-positive-600' : 'text-muted')}>
            {gamify.today}/{gamify.goal}
          </span>
        </div>
        {goalHit && <span className="text-xs font-semibold text-positive-600">Objectif atteint 🎉</span>}
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
                view === id ? 'bg-brand-600 text-white shadow-soft' : 'text-muted hover:text-ink-900'
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
                  ? 'bg-brand-600 text-white'
                  : 'bg-ink-100 text-ink-700 hover:bg-ink-200'
              )}
            >
              {label}
            </button>
          ))}
          {!activeFilter && activeQuery !== DEFAULT_QUERY && (
            <span className="chip bg-brand-50 text-brand-700">
              <Search size={12} /> {activeQuery.replace(`${DEFAULT_QUERY} `, '')}
              <button onClick={clearSearch} aria-label="Effacer le filtre" className="ml-0.5 rounded-full p-0.5 hover:bg-brand-100">
                <X size={12} />
              </button>
            </span>
          )}
        </div>
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
                      <span>{from?.split('<')[0]?.trim() || from || 'Expéditeur inconnu'}</span>
                      {suggestion.reasoning ? ` · ${suggestion.reasoning}` : ''}
                    </div>
                  </div>
                  <div className="flex shrink-0 items-center gap-1">
                    <button
                      onClick={() => handleApplySuggestion(suggestion)}
                      className="rounded-lg p-2 text-positive-600 transition-colors hover:bg-positive-50"
                      aria-label={`Appliquer : ${meta.label} — ${subject || 'sans sujet'}`}
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
      <div className={cn('grid gap-4', selectedEmail ? 'lg:grid-cols-[1fr_minmax(380px,460px)]' : 'grid-cols-1')}>
        {view === 'emails' ? (
          <div className="card overflow-hidden">
            {/* List toolbar: selection lives here, and so do the actions on it */}
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
                    allSelected || hasSelection ? 'border-brand-600 bg-brand-600 text-white' : 'border-ink-300'
                  )}
                  aria-hidden
                >
                  {allSelected ? <Check size={11} /> : hasSelection ? <span className="h-0.5 w-2 rounded bg-white" /> : null}
                </span>
                {allSelected ? 'Tout désélectionner' : 'Tout sélectionner'}
              </button>

              {hasSelection ? (
                <div className="flex flex-wrap items-center gap-1">
                  <span className="chip bg-brand-100 text-brand-700">{selectedEmails.length} sélectionné{selectedEmails.length > 1 ? 's' : ''}</span>
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
                {emails.map((email, idx) => {
                  const name = email.from?.split('<')[0]?.trim() || email.from || '?';
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
                          isChecked ? 'border-brand-600 bg-brand-600 text-white' : 'border-ink-300 group-hover:border-ink-400'
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
                        onClick={() => { setFocusedIndex(idx); setSelectedEmail(email); }}
                        className="min-w-0 flex-1 text-left"
                      >
                        <span className="flex items-baseline justify-between gap-2">
                          <span className={cn('truncate text-sm', email.isRead ? 'font-medium text-ink-700' : 'font-bold text-ink-900')}>
                            {name}
                            {!email.isRead && <span className="sr-only"> (non lu)</span>}
                          </span>
                          <span className="shrink-0 text-xs text-muted">{formatDate(email.receivedDate)}</span>
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

                {pagination.nextPageToken && (
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

        {/* Reader — a side panel on a wide screen, a full-screen sheet below it.
            It used to be a grid cell that simply did not exist under lg, so
            tapping an email on a phone appeared to do nothing at all. */}
        {selectedEmail && (
          <>
            <div className="card sticky top-20 hidden h-[calc(100vh-7rem)] overflow-hidden lg:block">
              <ReaderPanel />
            </div>
            <div className="fixed inset-0 z-[90] bg-surface lg:hidden">
              <ReaderPanel />
            </div>
          </>
        )}
      </div>

      {/* Label picker for the bulk "Étiqueter" action */}
      <LabelPicker
        open={labelPickerOpen}
        count={selectedEmails.length}
        onClose={() => setLabelPickerOpen(false)}
        onPick={(name) => {
          setLabelPickerOpen(false);
          runBulk('label', name);
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

  function ReaderPanel() {
    return (
      <EmailReader
        email={selectedEmail}
        onClose={() => setSelectedEmail(null)}
        onRead={(id) => patchEmail(id, { isRead: true })}
        onArchive={() => handleReaderAction(selectedEmail, 'archive')}
        onDelete={() => handleReaderAction(selectedEmail, 'delete')}
        onSnooze={(preset) => handleSnooze(selectedEmail, preset)}
        onProtect={() => handleProtect(selectedEmail)}
        onUnsubscribe={() => handleUnsubscribe({ messageId: selectedEmail.messageId })}
        unsubscribing={unsubscribing === selectedEmail.messageId}
      />
    );
  }
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
          <span className="font-semibold text-caution-700">Nouveau libellé</span> — « {trimmed} » sera créé dans Gmail.
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
