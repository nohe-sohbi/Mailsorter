import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react';
import {
  emailService,
  aiService,
  senderService,
  subscriptionService,
  mailboxService,
  whenMailboxChanged,
} from '../services/api';
import { setMailboxGeneration } from '../lib/session';

const EmailContext = createContext(null);

const CACHE_DURATION = 5 * 60 * 1000; // 5 minutes
// How long a screen left open waits before asking again which mailbox this is,
// after GET /api/mailbox failed.
const MAILBOX_RETRY_MS = 30 * 1000;
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
        // Whether the caller shows what the sync has to say (syncNotice): only
        // then does it ask for the mails the rules gave up on, which the
        // server tells once.
        notice = false,
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

      // Set when the round-trip to the mailbox failed. The stored mailbox is
      // still worth showing, so it does not stop the listing below, but it is
      // handed back to the caller: the "Synchroniser" button used to announce
      // "Boîte synchronisée" after a sync that had failed.
      let syncError = '';
      // What a sync that worked has to say besides (over IMAP, rules that
      // could not run on some new mails yet): information, never an error.
      let syncNotice = '';
      try {
        const now = Date.now();
        if (sync || !lastSyncRef.current || now - lastSyncRef.current > CACHE_DURATION) {
          try {
            const syncRes = await emailService.syncEmails(notice);
            syncNotice = syncRes?.data?.rulesMessage || '';
            lastSyncRef.current = now;
          } catch (syncErr) {
            console.warn('Sync failed:', syncErr);
            syncError = errorMessage(syncErr, { sync: true });
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

        if (isStale()) return { emails, senders, suggestions, stats: newStats, syncError, syncNotice };

        // The listing is the one call whose failure the user must see: without
        // it the screen falls back to an empty list, which the inbox used to
        // celebrate as an empty inbox, the exact opposite of the truth.
        if (emailsRes.status === 'rejected') {
          reportError(emailsRes.reason);
          setLoading(false);
          return { emails, senders, suggestions, stats: newStats, syncError, syncNotice, listError: true };
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

        return {
          emails: newEmails,
          senders: newSenders,
          suggestions: newSuggestions,
          stats: newStats,
          syncError,
          syncNotice,
        };
      } catch (err) {
        if (!isStale()) reportError(err);
        return { emails, senders, suggestions, stats, syncError, syncNotice, listError: true };
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
  // burst keyboard triage (j, e, j, e...) works against a list that never moves.
  const removeEmails = useCallback((ids) => {
    const set = new Set(Array.isArray(ids) ? ids : [ids]);
    setEmails((prev) => prev.filter((e) => !set.has(e.messageId)));
  }, []);

  const patchEmail = useCallback((messageId, patch) => {
    setEmails((prev) => prev.map((e) => (e.messageId === messageId ? { ...e, ...patch } : e)));
  }, []);

  const addSuggestion = useCallback((sug) => {
    if (!sug) return;
    const id = sug.id || sug._id;
    setSuggestions((prev) => [sug, ...prev.filter((s) => (s.id || s._id) !== id)]);
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

  // The mailbox this account reads (GET /api/mailbox), fetched once and only
  // when a screen asks for it through useMailbox(): this provider also sits
  // above the public pages, where there is no session to ask with. `undefined`
  // means not known yet, `null` means no mailbox.
  //
  // A failed call is NOT an answer. It used to set `null`, which read as "no
  // mailbox, so not IMAP" for the rest of the session: one passing outage and
  // an IMAP user got the Gmail-only controls back until the next reload. So a
  // failure leaves the mailbox unknown and raises `mailboxFailed`, and the
  // question is asked again by the next screen that needs it, or after
  // MAILBOX_RETRY_MS on the one still open.
  const [mailbox, setMailbox] = useState(undefined);
  const [mailboxFailed, setMailboxFailed] = useState(false);
  const mailboxRequestRef = useRef(null);
  const mailboxRetryRef = useRef(null);
  // Only the latest request may answer: a forced reload (after a connect or a
  // disconnect) must not be overwritten by a slower request about the mailbox
  // as it was before.
  const mailboxSeqRef = useRef(0);
  // The mailbox what this provider holds was loaded from (its generation),
  // which every request names (setMailboxGeneration).
  const generationRef = useRef(null);
  // Forgets what was loaded of a mailbox: the mails, the proposals and the
  // rest were another mailbox's, and their numbers name, in the next one,
  // other mails. A request still on its way answers into nothing
  // (requestSeqRef), and the next screen loads everything again.
  const forgetMailbox = useCallback(() => {
    requestSeqRef.current += 1;
    setEmails([]);
    setSenders([]);
    setSubscriptions([]);
    setSuggestions([]);
    setStats(null);
    setPagination({ nextPageToken: null, resultSizeEstimate: 0 });
    setLoading(false);
    setError('');
    lastFetchRef.current = null;
    lastSyncRef.current = null;
    lastStatsRef.current = null;
  }, []);
  const loadMailbox = useCallback(({ force = false } = {}) => {
    if (mailboxRequestRef.current && !force) return mailboxRequestRef.current;
    clearTimeout(mailboxRetryRef.current);
    // A forced reload follows a change of mailbox (a connection, a
    // disconnection): until it answers, what was known is no longer true,
    // and what was loaded of the mailbox before goes.
    if (force) {
      setMailbox(undefined);
      forgetMailbox();
      generationRef.current = null;
      setMailboxGeneration('');
    }
    const seq = ++mailboxSeqRef.current;
    mailboxRequestRef.current = mailboxService
      .get()
      .then(({ data }) => {
        if (seq !== mailboxSeqRef.current) return;
        const generation = data?.generation || '';
        if (generationRef.current !== null && generationRef.current !== generation) forgetMailbox();
        generationRef.current = generation;
        setMailboxGeneration(generation);
        setMailbox(data?.mailbox || null);
        setMailboxFailed(false);
      })
      .catch(() => {
        if (seq !== mailboxSeqRef.current) return;
        mailboxRequestRef.current = null;
        setMailboxFailed(true);
        mailboxRetryRef.current = setTimeout(() => loadMailbox(), MAILBOX_RETRY_MS);
      });
    return mailboxRequestRef.current;
  }, [forgetMailbox]);
  useEffect(() => () => clearTimeout(mailboxRetryRef.current), []);
  // A request refused because the mailbox changed since this was loaded
  // (409, api.js): ask again which mailbox it is, and forget the other one's.
  useEffect(() => whenMailboxChanged(() => loadMailbox({ force: true })), [loadMailbox]);

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
    addSuggestion,
    removeSuggestion,
    removeSuggestions,
    restoreSuggestions,
    markUnsubscribed,
    isCacheValid,
    mailbox,
    mailboxFailed,
    loadMailbox,
  };

  return <EmailContext.Provider value={value}>{children}</EmailContext.Provider>;
}

// The API returns plain-text errors for most failures; surface something a
// French-speaking user can act on rather than a raw googleapi string.
//
// Only the listing, the sync and the counters come through here, and the
// server never answers them 502, 503 or 504: a mail provider that does not
// answer fails them with a 500 (handlers.go). So a gateway status on these
// routes is the proxy in front of Mailsorter, with Mailsorter itself down or
// too slow, and naming the mail provider for it sent people to check the wrong
// service. Neither message names the provider.
//
// `sync` words the failure of a sync, which the inbox says after "La
// synchronisation a échoué." while the list stays on screen: "Votre boîte n'a
// pas pu être chargée" contradicted the emails right under it.
function errorMessage(err, { sync = false } = {}) {
  const status = err?.response?.status;
  const raw = err?.response?.data;
  // writeError sends { error, status }; older routes send a bare string.
  const serverSaid = (typeof raw === 'string' ? raw : raw?.error)?.trim?.() || '';

  if (status === 429) return 'Trop de requêtes vers votre boîte mail. Patientez quelques instants.';
  if (status === 402) return 'Quota mensuel atteint.';
  if (status === 401) return 'Session expirée. Reconnectez-vous.';
  // 501 is checked BEFORE the 5xx catch-all, and it is the one 5xx whose message
  // must reach the user verbatim. It means the feature does not exist on their
  // transport yet, so "réessayez" would send them back to a button that can
  // never work, and the server's sentence names the filter that is missing.
  if (status === 501) {
    return serverSaid || "Cette action n'est pas encore disponible sur cette boîte.";
  }
  if (status === 502 || status === 503 || status === 504) {
    return "Le serveur ne répond pas pour le moment. Réessayez dans un instant.";
  }
  if (status >= 500) return sync ? 'Réessayez dans un instant.' : "Votre boîte n'a pas pu être chargée. Réessayez.";
  if (err?.code === 'ERR_NETWORK') return 'Connexion au serveur impossible.';
  if (serverSaid) return serverSaid;
  // axios's own message is English and technical ("Request failed with status
  // code 400"): never shown as is.
  return 'Une erreur inattendue est survenue.';
}

export function useEmails() {
  const context = useContext(EmailContext);
  if (!context) {
    throw new Error('useEmails must be used within an EmailProvider');
  }
  return context;
}

// The connected mailbox and what it can do, for the signed-in screens. One
// request per session, shared by every screen that asks.
//
// `isImap` is true only once the server has said so: it is what a sentence
// about IMAP checks ("pas encore disponible sur les boîtes IMAP").
// `fullFeatures` is what a Gmail-only control checks (see GMAIL_ONLY in
// components/landing/features.js): true once the answer is in and the mailbox
// is not IMAP. While the answer is not in, or the request failed, it is false,
// so a control the server may refuse is never offered on a guess.
// `known` is the answer being in, `failed` the last request having failed,
// and `settled` either of the two: what a screen waits for before drawing the
// controls that depend on it (RequireMailbox in App.js), so they do not appear
// or vanish under the user's eyes.
export function useMailbox() {
  const { mailbox, mailboxFailed, loadMailbox } = useEmails();
  useEffect(() => {
    if (mailbox === undefined) loadMailbox();
  }, [mailbox, loadMailbox]);
  const known = mailbox !== undefined;
  const isImap = mailbox?.transport === 'imap';
  return {
    mailbox: mailbox || null,
    known,
    failed: !known && mailboxFailed,
    settled: known || mailboxFailed,
    transport: mailbox?.transport || null,
    isImap,
    isGmail: mailbox?.transport === 'gmail',
    fullFeatures: known && !isImap,
    reload: () => loadMailbox({ force: true }),
  };
}
