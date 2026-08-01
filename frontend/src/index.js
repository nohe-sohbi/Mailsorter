import React from 'react';
import ReactDOM from 'react-dom/client';
import App from './App';
import './index.css';
import { initAnalytics } from './lib/analytics';
import { applyTheme, readStoredTheme } from './ui/theme';

// Once, at boot, before React mounts. No-op without a website id.
initAnalytics();

// Before the first paint, so a dark-theme user never gets a white flash.
applyTheme(readStoredTheme());

const root = ReactDOM.createRoot(document.getElementById('root'));
root.render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
);
