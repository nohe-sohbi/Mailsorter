import React, { useEffect, useMemo, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { useEmails } from '../contexts/EmailContext';
import { aiService, senderService, emailService } from '../services/api';
import { useToast } from '../ui/Toast';
import EmailReader from '../components/EmailReader';
import Spinner from '../ui/Spinner';
import { cn } from '../ui/cn';
import {
  Sparkles, Archive, Trash, Tag, Pin, Search, Refresh, Inbox as InboxIcon,
  Users, Bolt, Check, X, Mail, Shield,
} from '../ui/icons';

// --- Action design tokens (static classes so Tailwind keeps them) ---
const ACTIONS = {
  archive: { label: 'Archiver', Icon: Archive, badge: 'bg-sky-50 text-sky-600', ring: '#0ea5e9' },
  delete: { label: 'Supprimer', Icon: Trash, badge: 'bg-rose-50 text-rose-600', ring: '#f43f5e' },
  label: { label: 'Libellé', Icon: Tag, badge: 'bg-violet-50 text-violet-600', ring: '#8b5cf6' },
  keep: { label: 'Garder', Icon: Pin, badge: 'bg-emerald-50 text-emerald-600', ring: '#10b981' },
};
const actionMeta = (a) => ACTIONS[a] || ACTIONS.keep;

const AVATAR_GRADIENTS = [
  'from-brand-500 to-fuchsia-500', 'from-sky-500 to-indigo-500',
  'from-emerald-500 to-teal-500', 'from-amber-500 to-orange-500', 'from-rose-500 to-pink-500',
];
const gradientFor = (seed = '') => {
  let h = 0;
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) >>> 0;
  return AVATAR_GRADIENTS[h % AVATAR_GRADIENTS.length];
};

function ConfidenceRing({ value = 0, color = '#6366f1' }) {
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
    emails, senders, suggestions, stats, pagination,
    loading, loadingMore, fetchData, loadMoreEmails, removeSuggestion, removeSuggestions,
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

  useEffect(() => {
    if (!localStorage.getItem('userEmail')) {
      navigate('/');
      return;
    }
    fetchData();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [navigate]);

  useEffect(() => setLocalSenders(senders), [senders]);

  const visibleSuggestions = useMemo(
    () => (highConfOnly ? suggestions.filter((s) => (s.confidence || 0) >= 0.8) : suggestions),
    [suggestions, highConfOnly]
  );

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

  // --- Handlers ---
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
    const ids = selectedEmails.length > 0 ? selectedEmails : emails.slice(0, 25).map((e) => e.messageId);
    if (ids.length === 0) {
      toast.error('Aucun email à analyser');
      return;
    }
    setAnalyzing(true);
    try {
      const response = await aiService.analyzeEmails(ids);
      await fetchData({ forceRefresh: true });
      const count = response.data?.length || 0;
      toast.success(count ? `${count} suggestion${count > 1 ? 's' : ''} générée${count > 1 ? 's' : ''}` : 'Analyse terminée');
      setSelectedEmails([]);
    } catch (err) {
      toast.error("L'analyse a échoué. Réessayez.");
    } finally {
      setAnalyzing(false);
    }
  };

  const handleApplySuggestion = async (suggestion) => {
    const id = suggestion.id || suggestion._id;
    removeSuggestion(id);
    try {
      await aiService.applySuggestion(id);
      toast.success(`${actionMeta(suggestion.action).label} appliqué`);
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

  const handleReaderAction = async (email, action) => {
    setSelectedEmail(null);
    try {
      await emailService.action(email.messageId, action);
      toast.success(action === 'archive' ? 'Email archivé' : 'Email déplacé vers la corbeille');
      fetchData({ forceRefresh: true });
    } catch (err) {
      toast.error("L'action a échoué");
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

  const handleApplyBulk = async (sender, action) => {
    try {
      const response = await aiService.applyBulk(sender.senderEmail, action, sender.preference?.defaultLabel || '');
      toast.success(`${response.data.applied} email${response.data.applied > 1 ? 's' : ''} traité${response.data.applied > 1 ? 's' : ''}`);
      fetchData({ forceRefresh: true });
    } catch (err) {
      toast.error('Traitement en masse impossible');
    }
  };

  const allSelected = emails.length > 0 && selectedEmails.length === emails.length;

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
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              placeholder="from:amazon, is:unread…"
              className="input w-44 pl-10 sm:w-64"
            />
          </form>
          <button onClick={handleSync} disabled={syncing} className="btn-secondary px-3" title="Synchroniser">
            <Refresh size={18} className={syncing ? 'animate-spin' : ''} />
          </button>
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
          ].map(({ id, label, Icon }) => (
            <button
              key={id}
              onClick={() => setView(id)}
              className={cn(
                'flex items-center gap-2 rounded-lg px-3.5 py-2 text-sm font-semibold transition-all',
                view === id ? 'bg-brand-gradient text-white shadow-soft' : 'text-ink-500 hover:text-ink-900'
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

      {/* Suggestions panel */}
      {visibleSuggestions.length > 0 && view === 'emails' && (
        <div className="card mb-6 overflow-hidden animate-fade-up">
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-ink-100 bg-gradient-to-r from-brand-50/60 to-fuchsia-50/40 px-5 py-3.5">
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
              <button onClick={handleApplyAll} disabled={applyingAll} className="btn-primary px-3.5 py-1.5 text-xs">
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
                {emails.map((email) => {
                  const name = email.from?.split('<')[0]?.trim() || email.from || '?';
                  const isActive = selectedEmail?.messageId === email.messageId;
                  const isChecked = selectedEmails.includes(email.messageId);
                  return (
                    <div
                      key={email.messageId}
                      className={cn(
                        'group flex items-center gap-3 px-4 py-3 transition-colors',
                        isActive ? 'bg-brand-50/70' : 'hover:bg-ink-50/70',
                        isChecked && 'bg-brand-50/40'
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
                      <span className={cn('relative flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-gradient-to-br text-sm font-bold text-white', gradientFor(email.from))}>
                        {name[0]?.toUpperCase() || '?'}
                        {!email.isRead && (
                          <span className="absolute -right-0.5 -top-0.5 h-3 w-3 rounded-full border-2 border-white bg-brand-500" />
                        )}
                      </span>
                      <button onClick={() => setSelectedEmail(email)} className="min-w-0 flex-1 text-left">
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
        ) : (
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
              localSenders.map((sender) => (
                <div key={sender.senderEmail} className="card flex flex-wrap items-center gap-4 p-4">
                  <span className={cn('flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-gradient-to-br text-sm font-bold text-white', gradientFor(sender.senderEmail))}>
                    {(sender.senderName || sender.senderEmail)[0]?.toUpperCase()}
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-semibold text-ink-900">{sender.senderName || sender.senderEmail.split('@')[0]}</div>
                    <div className="truncate text-xs text-ink-400">{sender.senderEmail}</div>
                  </div>
                  <span className="chip bg-ink-100 text-ink-600">{sender.emailCount} emails</span>
                  {sender.preference ? (
                    <span className={cn('chip', actionMeta(sender.preference.defaultAction).badge)}>
                      {(() => { const M = actionMeta(sender.preference.defaultAction); return <M.Icon size={13} />; })()}
                      {actionMeta(sender.preference.defaultAction).label}
                    </span>
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
                    <button onClick={() => handleApplyBulk(sender, 'archive')} className="btn-ghost px-2.5" title="Tout archiver">
                      <Archive size={18} />
                    </button>
                    <button onClick={() => handleApplyBulk(sender, 'delete')} className="btn-ghost px-2.5 text-rose-500 hover:bg-rose-50" title="Tout supprimer">
                      <Trash size={18} />
                    </button>
                  </div>
                </div>
              ))
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
            />
          </div>
        )}
      </div>
    </div>
  );
}

export default Inbox;
