import React, { createContext, useCallback, useContext, useRef, useState } from 'react';
import { emailService, aiService, senderService, subscriptionService } from '../services/api';

const EmailContext = createContext(null);

const CACHE_DURATION = 5 * 60 * 1000; // 5 minutes
export const DEFAULT_QUERY = 'in:inbox';

export function EmailProvider({ children }) {
  const [emails, setEmails] = useState([]);
  const [senders, setSenders] = useState([]);
  const [subscriptions, setSubscriptions] = useState([]);
  const [suggestions, setSuggestions] = useState([]);
  const [stats, setStats] = useState(null);
  const [pagination, setPagination] = useState({ nextPageToken: null, resultSizeEstimate: 0 });
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState('');
  // Whether retrying the same call could ever succeed. A 501 means the feature
  // does not exist on this user's transport, so a "Réessayer" button would send
  // them back to a filter that can never work.
  const [errorRetryable, setErrorRetryable] = useState(true);

  const reportError = useCallback((err) => {
    setError(errorMessage(err));
    setErrorRetryable(err?.response?.status !== 501);
  }, []);
  // The query the emails on screen actually came from. "Charger plus" and any
  // refresh must reuse THIS, not whatever is currently typed in the search box:
  // paging with a half-typed query silently mixed two different result sets.
  const [activeQuery, setActiveQuery] = useState(DEFAULT_QUERY);

  // Use refs for timestamps to avoid re-renders
  const lastFetchRef = useRef(null);
  const lastSyncRef = useRef(null);
  const lastStatsRef = useRef(null);
  const activeQueryRef = useRef(DEFAULT_QUERY);
  // Guards against an older in-flight fetch overwriting a newer one (type fast,
  // hit enter twice, and the slower response used to win).
  const requestSeqRef = useRef(0);

  const isCacheValid = useCallback(() => {
    if (!lastFetchRef.current) return false;
    return Date.now() - lastFetchRef.current < CACHE_DURATION;
  }, []);

  const fetchData = useCallback(
    async (options = {}) => {
      const {
        forceRefresh = false,
        query = activeQueryRef.current || DEFAULT_QUERY,
        maxResults = 100,
        // A real sync hits Gmail. It is throttled on its own timer so that
        // background refreshes stay cheap, but an explicit user action must
        // never be silently swallowed: pressing "Synchroniser" and being told
        // "Boîte synchronisée" when nothing was fetched is a lie the UI told
        // every time the button was used twice within five minutes.
        sync = forceRefresh,
      } = options;

      // Return cached data if valid, same query, and not forcing refresh.
      // Checked BEFORE taking a sequence number: a call answered from cache
      // performs no request, so bumping the counter would mark a slower request
      // still in flight as stale and leave `loading` stuck at true.
      if (!forceRefresh && isCacheValid() && emails.length > 0 && query === activeQueryRef.current) {
        return { emails, senders, suggestions, stats };
      }

      const seq = ++requestSeqRef.current;
      const isStale = () => seq !== requestSeqRef.current;

      activeQueryRef.current = query;
      setActiveQuery(query);
      setLoading(true);
      setError('');
      setErrorRetryable(true);

      try {
        const now = Date.now();
        if (sync || !lastSyncRef.current || now - lastSyncRef.current > CACHE_DURATION) {
          try {
            await emailService.syncEmails();
            lastSyncRef.current = now;
          } catch (syncErr) {
            // A failed sync is not fatal: the stored mailbox is still worth
            // showing. It becomes visible only if the listing below also fails.
            console.warn('Sync failed:', syncErr);
          }
        }

        let newStats = stats;
        if (!lastStatsRef.current || now - lastStatsRef.current > CACHE_DURATION || forceRefresh) {
          try {
            const statsRes = await emailService.getStats();
            newStats = statsRes.data;
            if (!isStale()) setStats(newStats);
            lastStatsRef.current = now;
          } catch (statsErr) {
            console.warn('Stats fetch failed:', statsErr);
          }
        }

        const [emailsRes, sendersRes, suggestionsRes, subscriptionsRes] = await Promise.allSettled([
          emailService.getEmails(query, { maxResults }),
          senderService.getSenders(),
          aiService.getSuggestions('pending'),
          subscriptionService.getSubscriptions(),
        ]);

        if (isStale()) return { emails, senders, suggestions, stats: newStats };

        // The listing is the one call whose failure the user must see: without
        // it the screen falls back to an empty list, which the inbox used to
        // celebrate as "Inbox Zero atteint 🎉", the exact opposite of the truth.
        if (emailsRes.status === 'rejected') {
          reportError(emailsRes.reason);
          setLoading(false);
          return { emails, senders, suggestions, stats: newStats };
        }

        let newEmails = [];
        let newPagination = { nextPageToken: null, resultSizeEstimate: 0 };
        const data = emailsRes.value.data;
        // Handle both old format (array) and new format (object with emails array)
        if (Array.isArray(data)) {
          newEmails = data;
        } else if (data && data.emails) {
          newEmails = data.emails;
          newPagination = {
            nextPageToken: data.nextPageToken || null,
            resultSizeEstimate: data.resultSizeEstimate || 0,
          };
        }

        const newSenders = sendersRes.status === 'fulfilled' ? sendersRes.value.data || [] : [];
        const newSuggestions = suggestionsRes.status === 'fulfilled' ? suggestionsRes.value.data || [] : [];
        const newSubscriptions =
          subscriptionsRes.status === 'fulfilled' ? subscriptionsRes.value.data || [] : [];

        setEmails(newEmails);
        setPagination(newPagination);
        setSenders(newSenders);
        setSuggestions(newSuggestions);
        setSubscriptions(newSubscriptions);
        lastFetchRef.current = Date.now();

        return { emails: newEmails, senders: newSenders, suggestions: newSuggestions, stats: newStats };
      } catch (err) {
        if (!isStale()) reportError(err);
        return { emails, senders, suggestions, stats };
      } finally {
        if (!isStale()) setLoading(false);
      }
    },
    [emails, senders, suggestions, stats, isCacheValid, reportError]
  );

  const loadMoreEmails = useCallback(async () => {
    if (!pagination.nextPageToken || loadingMore) return;

    // The page being fetched belongs to the query active when the click
    // happened. If a filter change lands first, appending this page would splice
    // results from two different searches into one list.
    const queryAtCall = activeQueryRef.current;
    const seqAtCall = requestSeqRef.current;

    setLoadingMore(true);
    try {
      const res = await emailService.getEmails(queryAtCall, {
        maxResults: 100,
        pageToken: pagination.nextPageToken,
      });
      if (seqAtCall !== requestSeqRef.current || queryAtCall !== activeQueryRef.current) return;
      const data = res.data;
      if (data && data.emails) {
        // De-duplicate: Gmail can repeat a message across page boundaries when
        // the mailbox changes mid-pagination, and a duplicate key breaks React's
        // list reconciliation.
        setEmails((prev) => {
          const seen = new Set(prev.map((e) => e.messageId));
          return [...prev, ...data.emails.filter((e) => !seen.has(e.messageId))];
        });
        setPagination({
          nextPageToken: data.nextPageToken || null,
          resultSizeEstimate: data.resultSizeEstimate || 0,
        });
      }
    } catch (err) {
      reportError(err);
    } finally {
      setLoadingMore(false);
    }
  }, [pagination.nextPageToken, loadingMore, reportError]);

  // Optimistic removal: a triaged email must leave the list immediately, or
  // burst keyboard triage (j, e, j, e…) works against a list that never moves.
  const removeEmails = useCallback((ids) => {
    const set = new Set(Array.isArray(ids) ? ids : [ids]);
    setEmails((prev) => prev.filter((e) => !set.has(e.messageId)));
  }, []);

  const patchEmail = useCallback((messageId, patch) => {
    setEmails((prev) => prev.map((e) => (e.messageId === messageId ? { ...e, ...patch } : e)));
  }, []);

  const removeSuggestion = useCallback((suggestionId) => {
    setSuggestions((prev) => prev.filter((s) => (s.id || s._id) !== suggestionId));
  }, []);

  const removeSuggestions = useCallback((ids) => {
    const set = new Set(ids);
    setSuggestions((prev) => prev.filter((s) => !set.has(s.id || s._id)));
  }, []);

  // Put a suggestion back where it was. Applying one is optimistic, so a failure
  // has to be able to undo the optimism: otherwise a suggestion whose Gmail call
  // failed vanished from the screen for good while the email stayed put.
  const restoreSuggestions = useCallback((items) => {
    setSuggestions((prev) => {
      const known = new Set(prev.map((s) => s.id || s._id));
      const missing = items.filter((s) => !known.has(s.id || s._id));
      if (missing.length === 0) return prev;
      return [...missing, ...prev];
    });
  }, []);

  const markUnsubscribed = useCallback((senderEmail) => {
    setSubscriptions((prev) =>
      prev.map((s) => (s.senderEmail === senderEmail ? { ...s, unsubscribed: true } : s))
    );
  }, []);

  const value = {
    emails,
    senders,
    subscriptions,
    suggestions,
    stats,
    pagination,
    loading,
    loadingMore,
    error,
    setError,
    errorRetryable,
    activeQuery,
    fetchData,
    loadMoreEmails,
    removeEmails,
    patchEmail,
    removeSuggestion,
    removeSuggestions,
    restoreSuggestions,
    markUnsubscribed,
    isCacheValid,
  };

  return <EmailContext.Provider value={value}>{children}</EmailContext.Provider>;
}

// The API returns plain-text errors for most failures; surface something a
// French-speaking user can act on rather than a raw googleapi string.
function errorMessage(err) {
  const status = err?.response?.status;
  const raw = err?.response?.data;
  // writeError sends { error, status }; older routes send a bare string.
  const serverSaid = (typeof raw === 'string' ? raw : raw?.error)?.trim?.() || '';

  if (status === 429) return 'Trop de requêtes vers Gmail. Patientez quelques instants.';
  if (status === 402) return 'Quota mensuel atteint.';
  if (status === 401) return 'Session expirée. Reconnectez-vous.';
  // 501 is checked BEFORE the 5xx catch-all, and it is the one 5xx whose message
  // must reach the user verbatim. It means the feature does not exist on their
  // transport yet, so "réessayez" would send them back to a button that can
  // never work, and the server's sentence names the filter that is missing.
  if (status === 501) {
    return serverSaid || "Cette action n'est pas encore disponible sur cette boîte.";
  }
  if (status >= 500 || status === 502) return 'Gmail est momentanément injoignable. Réessayez.';
  if (err?.code === 'ERR_NETWORK') return 'Connexion au serveur impossible.';
  if (serverSaid) return serverSaid;
  return err?.message || 'Une erreur inattendue est survenue.';
}

export function useEmails() {
  const context = useContext(EmailContext);
  if (!context) {
    throw new Error('useEmails must be used within an EmailProvider');
  }
  return context;
}
