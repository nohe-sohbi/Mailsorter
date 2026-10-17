import React, { createContext, useContext, useEffect, useMemo, useState } from 'react';
import { useLocation } from 'react-router-dom';
import { useInstance } from '../../contexts/InstanceContext';
import { effectiveUiTheme, rememberUiTheme } from '../../lib/uiTheme';
import { useHotelFonts } from './useHotelFonts';

// Whether the dashboard wears the Grand Hotel theme. Same switch as the
// landing (UI_THEME, served by GET /api/config/status, with the per-tab ?ui=
// preview on top), read once here so every screen agrees with every other.
const HotelContext = createContext(false);

// The dashboard skin is its own CSS chunk, fetched only when the theme is on:
// a classic instance downloads none of it, fonts included. hotel.css holds the
// tokens and ht- primitives the landing already ships; hotel-app.css maps the
// dashboard's design system onto them.
let stylesPromise = null;
let stylesReady = false;

export function loadHotelStyles() {
  if (!stylesPromise) {
    stylesPromise = Promise.all([import('../../styles/hotel.css'), import('../../styles/hotel-app.css')]).then(
      () => {
        stylesReady = true;
      },
      (err) => {
        // Let the next mount try again rather than caching the failure.
        stylesPromise = null;
        throw err;
      }
    );
  }
  return stylesPromise;
}

// The classes live on <body>, not on a wrapper: dialogs, the toast stack and
// the reader sheet are all fixed-position layers, and the theme has to reach
// them too. <html> carries .dark already, which is what lets the night values
// (.dark .theme-hotel) apply, and gets ht-html so the overscroll area matches.
//
// ht-app, the dashboard skin, stays off the landing: that page was validated
// as drawn, on its own .theme-hotel root, and must not inherit a rule written
// for the app.
function useBodyTheme(on, landing) {
  useEffect(() => {
    if (!on) return undefined;
    const { body, documentElement: html } = document;
    body.classList.add('theme-hotel');
    html.classList.add('ht-html');
    return () => {
      body.classList.remove('theme-hotel');
      html.classList.remove('ht-html');
    };
  }, [on]);
  useEffect(() => {
    if (!on || landing) return undefined;
    document.body.classList.add('ht-app');
    return () => document.body.classList.remove('ht-app');
  }, [on, landing]);
}

export function HotelThemeProvider({ fallback = null, children }) {
  const { uiTheme } = useInstance();
  const { search, pathname } = useLocation();
  const wanted = useMemo(() => effectiveUiTheme(uiTheme, search) === 'hotel', [uiTheme, search]);
  const [ready, setReady] = useState(stylesReady);
  const [failed, setFailed] = useState(false);

  // The server's choice, not this tab's preview: a ?ui= preview must not
  // decide the splash of the next tab, which will not carry it.
  useEffect(() => {
    rememberUiTheme(uiTheme);
  }, [uiTheme]);

  useEffect(() => {
    if (!wanted || ready) return undefined;
    let live = true;
    loadHotelStyles().then(
      () => live && setReady(true),
      // A stylesheet that will not load is not worth a blank screen: the app
      // falls back to the classic look it carries anyway.
      () => live && setFailed(true)
    );
    return () => {
      live = false;
    };
  }, [wanted, ready]);

  const on = wanted && ready && !failed;
  useBodyTheme(on, pathname === '/');
  useHotelFonts(on);

  if (wanted && !ready && !failed) return fallback;
  return <HotelContext.Provider value={on}>{children}</HotelContext.Provider>;
}

export function useHotel() {
  return useContext(HotelContext);
}
