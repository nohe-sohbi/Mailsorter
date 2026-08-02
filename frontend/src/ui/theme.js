import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';

const STORAGE_KEY = 'mailsorter_theme';
const ThemeContext = createContext(null);

// Three states, not two: "system" is a real choice and the default. Someone who
// never touches the toggle should follow their OS, and keep following it when it
// flips at sunset.
export const THEMES = ['light', 'dark', 'system'];

const prefersDark = () =>
  typeof window !== 'undefined' &&
  window.matchMedia &&
  window.matchMedia('(prefers-color-scheme: dark)').matches;

export const readStoredTheme = () => {
  try {
    const v = localStorage.getItem(STORAGE_KEY);
    return THEMES.includes(v) ? v : 'system';
  } catch {
    // Private browsing can throw on access; fall back rather than crash the app.
    return 'system';
  }
};

export const resolveTheme = (theme) => (theme === 'system' ? (prefersDark() ? 'dark' : 'light') : theme);

// applyTheme is exported so index.js can run it before React mounts, which is
// what stops a dark-theme user from getting a white flash on every page load.
// Held across calls so two toggles less than the transition apart do not have
// the first one's timer strip the class mid-way through the second's animation.
let transitionTimer = 0;

export function applyTheme(theme, { animate = false } = {}) {
  const root = document.documentElement;
  const resolved = resolveTheme(theme);
  if (animate) {
    root.classList.add('theme-transition');
    window.clearTimeout(transitionTimer);
    transitionTimer = window.setTimeout(() => root.classList.remove('theme-transition'), 220);
  }
  root.classList.toggle('dark', resolved === 'dark');
  return resolved;
}

export function ThemeProvider({ children }) {
  const [theme, setThemeState] = useState(readStoredTheme);
  const [resolved, setResolved] = useState(() => resolveTheme(readStoredTheme()));

  // Following the system means following it as it changes, not just at boot.
  useEffect(() => {
    if (theme !== 'system' || !window.matchMedia) return undefined;
    const mq = window.matchMedia('(prefers-color-scheme: dark)');
    const onChange = () => setResolved(applyTheme('system', { animate: true }));
    // Safari < 14 only has the deprecated listener API.
    if (mq.addEventListener) mq.addEventListener('change', onChange);
    else mq.addListener(onChange);
    return () => {
      if (mq.removeEventListener) mq.removeEventListener('change', onChange);
      else mq.removeListener(onChange);
    };
  }, [theme]);

  const setTheme = useCallback((next) => {
    if (!THEMES.includes(next)) return;
    setThemeState(next);
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // Preference is lost on reload, but the session still honours it.
    }
    setResolved(applyTheme(next, { animate: true }));
  }, []);

  // A fixed, predictable rotation: clair → sombre → système → clair. The button
  // labels its own destination, so the order has to be stated once and read from
  // one place — a cycle whose next step depended on the OS preference could not
  // be announced truthfully.
  const nextTheme = theme === 'light' ? 'dark' : theme === 'dark' ? 'system' : 'light';
  const cycleTheme = useCallback(() => setTheme(nextTheme), [nextTheme, setTheme]);

  const value = useMemo(
    () => ({ theme, nextTheme, resolved, setTheme, cycleTheme, isDark: resolved === 'dark' }),
    [theme, nextTheme, resolved, setTheme, cycleTheme]
  );

  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme() {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error('useTheme must be used within a ThemeProvider');
  return ctx;
}
