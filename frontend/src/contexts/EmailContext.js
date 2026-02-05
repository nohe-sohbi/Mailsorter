import React, { createContext, useContext, useState, useCallback, useRef } from 'react';
import { emailService, aiService, senderService } from '../services/api';

const EmailContext = createContext(null);

const CACHE_DURATION = 5 * 60 * 1000; // 5 minutes

export function EmailProvider({ children }) {
  const [emails, setEmails] = useState([]);
  const [senders, setSenders] = useState([]);
  const [suggestions, setSuggestions] = useState([]);
  const [stats, setStats] = useState(null);
  const [pagination, setPagination] = useState({ nextPageToken: null, resultSizeEstimate: 0 });
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState('');

  // Use refs for timestamps to avoid re-renders
  const lastFetchRef = useRef(null);
  const lastSyncRef = useRef(null);
  const lastStatsRef = useRef(null);

  const isCacheValid = useCallback(() => {
    if (!lastFetchRef.current) return false;
    return (Date.now() - lastFetchRef.current) < CACHE_DURATION;
  }, []);

  const fetchData = useCallback(async (options = {}) => {
    const { forceRefresh = false, query = 'in:inbox', maxResults = 100 } = options;

    // Return cached data if valid and not forcing refresh
    if (!forceRefresh && isCacheValid() && emails.length > 0) {
      console.log('[Cache] Using cached data');
      return { emails, senders, suggestions, stats };
    }

    console.log('[Cache] Fetching fresh data...');

    setLoading(true);
    setError('');

    try {
      // Sync with Gmail if needed (only every 5 min)
      const now = Date.now();
      if (!lastSyncRef.current || (now - lastSyncRef.current) > CACHE_DURATION) {
        try {
          console.log('[Cache] Syncing with Gmail...');
          await emailService.syncEmails();
          lastSyncRef.current = now;
        } catch (syncErr) {
          console.warn('Sync failed:', syncErr);
        }
      }

      // Fetch stats if needed (cache for 5 min)
      let newStats = stats;
      if (!lastStatsRef.current || (now - lastStatsRef.current) > CACHE_DURATION || forceRefresh) {
        try {
          console.log('[Cache] Fetching mailbox stats...');
          const statsRes = await emailService.getStats();
          newStats = statsRes.data;
          setStats(newStats);
          lastStatsRef.current = now;
        } catch (statsErr) {
          console.warn('Stats fetch failed:', statsErr);
        }
      }

      // Fetch all data
      const [emailsRes, sendersRes, suggestionsRes] = await Promise.allSettled([
        emailService.getEmails(query, { maxResults }),
        senderService.getSenders(),
        aiService.getSuggestions('pending'),
      ]);

      // Handle new response format with pagination
      let newEmails = emails;
      let newPagination = { nextPageToken: null, resultSizeEstimate: 0 };
      if (emailsRes.status === 'fulfilled') {
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
      }

      const newSenders = sendersRes.status === 'fulfilled' ? (sendersRes.value.data || []) : [];
      const newSuggestions = suggestionsRes.status === 'fulfilled' ? (suggestionsRes.value.data || []) : [];

      setEmails(newEmails);
      setPagination(newPagination);
      setSenders(newSenders);
      setSuggestions(newSuggestions);
      lastFetchRef.current = Date.now();

      if (emailsRes.status === 'rejected') {
        setError('Erreur: ' + (emailsRes.reason?.response?.data || emailsRes.reason?.message));
      }

      return { emails: newEmails, senders: newSenders, suggestions: newSuggestions, stats: newStats };
    } catch (err) {
      setError('Erreur: ' + (err.response?.data || err.message));
      return { emails, senders, suggestions, stats };
    } finally {
      setLoading(false);
    }
  }, [emails, senders, suggestions, stats, isCacheValid]);

  const loadMoreEmails = useCallback(async (query = 'in:inbox') => {
    if (!pagination.nextPageToken || loadingMore) return;

    setLoadingMore(true);
    try {
      const res = await emailService.getEmails(query, {
        maxResults: 100,
        pageToken: pagination.nextPageToken
      });
      const data = res.data;
      if (data && data.emails) {
        setEmails(prev => [...prev, ...data.emails]);
        setPagination({
          nextPageToken: data.nextPageToken || null,
          resultSizeEstimate: data.resultSizeEstimate || 0,
        });
      }
    } catch (err) {
      console.error('Failed to load more emails:', err);
    } finally {
      setLoadingMore(false);
    }
  }, [pagination.nextPageToken, loadingMore]);

  const refreshSuggestions = useCallback(async () => {
    try {
      const res = await aiService.getSuggestions('pending');
      setSuggestions(res.data || []);
    } catch (err) {
      console.warn('Failed to refresh suggestions:', err);
    }
  }, []);

  const removeSuggestion = useCallback((suggestionId) => {
    setSuggestions(prev => prev.filter(s => (s.id || s._id) !== suggestionId));
  }, []);

  const clearCache = useCallback(() => {
    setEmails([]);
    setSenders([]);
    setSuggestions([]);
    setStats(null);
    setPagination({ nextPageToken: null, resultSizeEstimate: 0 });
    lastFetchRef.current = null;
    lastSyncRef.current = null;
    lastStatsRef.current = null;
  }, []);

  const value = {
    emails,
    senders,
    suggestions,
    stats,
    pagination,
    loading,
    loadingMore,
    error,
    setError,
    fetchData,
    loadMoreEmails,
    refreshSuggestions,
    removeSuggestion,
    clearCache,
    isCacheValid,
  };

  return (
    <EmailContext.Provider value={value}>
      {children}
    </EmailContext.Provider>
  );
}

export function useEmails() {
  const context = useContext(EmailContext);
  if (!context) {
    throw new Error('useEmails must be used within an EmailProvider');
  }
  return context;
}
