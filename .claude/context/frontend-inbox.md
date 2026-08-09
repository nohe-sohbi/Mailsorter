---
subsystem: frontend-inbox
description: The React SPA: routing and boot gate, the EmailContext store, the single axios client in services/api.js, the Tailwind design system, and Inbox.js, the 1068 line triage cockpit.
keywords: [inbox, react, spa, frontend, emailcontext, useemails, api.js, axios, tailwind, design system, icons, toast, react-router, suggestions, keyboard shortcuts]
files:
  - frontend/src/pages/Inbox.js
  - frontend/src/contexts/EmailContext.js
  - frontend/src/services/api.js
  - frontend/src/App.js
  - frontend/src/index.js
  - frontend/src/components/EmailReader.js
  - frontend/src/components/Header.js
  - frontend/src/ui/
  - frontend/src/lib/analytics.js
  - frontend/src/index.css
  - frontend/tailwind.config.js
  - frontend/nginx.conf
  - frontend/Dockerfile
  - frontend/package.json
  - frontend/public/index.html
priority: medium
related: [auth-session-security, ai-triage, gmail-sync, billing-quota]
last-verified: 2026-08-09
---
# Frontend and the Inbox cockpit

Create React App SPA, 4614 lines of JS and CSS across 23 source files, no state
library and no component library. One page holds 23 percent of it: `pages/Inbox.js`
at 1068 lines. Everything else is small on purpose, so the navigation problem in
this subsystem is Inbox.js, and the block map below exists so you never have to
read all of it.

## Overview

| Aspect | Reality |
|---|---|
| Framework | React 18.2 function components, hooks only, no class component in the tree |
| Router | `react-router-dom` 6.21, `BrowserRouter`, 12 `<Route>` elements, no lazy loading |
| Shared state | Exactly one context, `EmailContext`, consumed by exactly one page |
| HTTP | One axios instance in `services/api.js`, 45 methods across 12 service objects |
| Styling | Tailwind 3.4 only, 9 component classes in `index.css`, zero CSS modules |
| Icons | 27 hand written SVG exports in `ui/icons.js`, no icon package |
| Tests | Zero. No `*.test.js`, no `*.spec.js`, no `__tests__` anywhere |
| Build | `react-scripts` 5.0.1, served by nginx alpine, `/api` reverse proxied |

Source file sizes, largest first, so you know what is worth opening:

| File | Lines |
|---|---|
| `pages/Inbox.js` | 1068 |
| `pages/Rules.js` | 506 |
| `pages/Pricing.js` | 380 |
| `pages/Settings.js` | 312 |
| `pages/Account.js` | 272 |
| `pages/Login.js` | 264 |
| `components/EmailReader.js` | 189 |
| `contexts/EmailContext.js` | 185 |
| `pages/History.js` | 178 |
| `ui/icons.js` | 177 |
| `components/Header.js` | 171 |
| `services/api.js` | 151 |
| `pages/Setup.js` | 145 |
| `pages/Snoozed.js` | 123 |
| `App.js` | 113 |
| `ui/Toast.js` | 86 |
| `index.css` | 81 |
| `ui/streak.js` | 61 |
| `lib/analytics.js` | 39 |
| `ui/Spinner.js` | 23 |
| `index.js` | 15 |
| `ui/cn.js` | 4 |

## Boot sequence

`index.js` runs `initAnalytics()` once before React mounts, then renders `<App />`
inside `React.StrictMode`. StrictMode double invokes effects in development, which
matters for anything that fires a request on mount.

`App.js` then blocks the entire app on one request:

`App mount -> configService.getStatus() -> isConfigured true|false -> render Router`

| `isConfigured` | What renders | Source |
|---|---|---|
| `null` (in flight) | `BootScreen` with `Logo` and a spinner | `App.js:47-59` |
| `false` plus an error | `BootScreen` with a retry button | `App.js:61-78` |
| `false`, no error | Router, but every guarded route redirects to `/setup` | `App.js:93-99` |
| `true` | Router, all routes reachable | `App.js:80-110` |

Provider nesting, outermost first: `Router` > `ToastProvider` > `EmailProvider` >
`div.min-h-screen` > `Header` + `Routes`. A toast can therefore be raised from
anywhere below the router, and `EmailProvider` mounts once for the whole session
rather than per route.

Consequence worth knowing: if the backend is unreachable at boot, nothing of the
app renders, not even the marketing landing at `/`. The failure is a full screen,
not a degraded page.

## Routes

Declared in `App.js:86-105`. All page imports are static, so the whole app ships
in one bundle.

| Path | Page component | Gated on `isConfigured` | Header visible |
|---|---|---|---|
| `/` | `pages/Login.js` | yes, redirects to `/setup` | no |
| `/inbox` | `pages/Inbox.js` | yes | yes |
| `/rules` | `pages/Rules.js` | yes | yes |
| `/snoozed` | `pages/Snoozed.js` | yes | yes |
| `/history` | `pages/History.js` | yes | yes |
| `/settings` | `pages/Settings.js` | yes | yes |
| `/account` | `pages/Account.js` | yes | yes |
| `/setup` | `pages/Setup.js` | inverted: redirects to `/` when configured | no |
| `/pricing` | `pages/Pricing.js` | no, always reachable | yes |
| `/auth/callback` | `pages/AuthCallback.js` | no, always reachable | no |
| `/emails` | none, `<Navigate to="/inbox" replace />` | no | n/a |
| `/triage` | none, `<Navigate to="/inbox" replace />` | no | n/a |

`Setup` receives one prop, `onComplete={() => setIsConfigured(true)}`
(`App.js:90`). No other route takes a prop.

### Header visibility rule

`Header` returns `null` when there is no `userEmail` in localStorage, or when the
path is one of `['/', '/setup', '/auth/callback']` (`Header.js:24-27`). So the
header is not driven by auth alone: a logged in user on `/` still gets no header.

Nav items (`Header.js:29-36`), in order: `/inbox` "Boîte", `/rules` "Règles",
`/snoozed` "Reporté", `/history` "Historique", `/pricing` "Tarifs",
`/settings` "Réglages". `/account` is not in that list, it is reached through the
identity chip, which exists twice, once for desktop (`Header.js:74-89`) and once
inside the mobile drawer (`Header.js:145-154`). Both carry a comment explaining
why: the desktop chip used to be an inert div, and the mobile one must mirror it
or the profile is unreachable on a phone.

## State: EmailContext versus local state

### What lives in EmailContext

`contexts/EmailContext.js`. `CACHE_DURATION = 5 * 60 * 1000` governs three
independent freshness checks.

| Key | Type | Set by | Notes |
|---|---|---|---|
| `emails` | array | `fetchData`, `loadMoreEmails` | appended on load more |
| `senders` | array | `fetchData` | Inbox copies it into local state, see below |
| `subscriptions` | array | `fetchData`, `markUnsubscribed` | |
| `suggestions` | array | `fetchData`, `removeSuggestion`, `removeSuggestions` | optimistic removal |
| `stats` | object or null | `fetchData` | own 5 min timestamp, `lastStatsRef` |
| `pagination` | `{ nextPageToken, resultSizeEstimate }` | `fetchData`, `loadMoreEmails` | |
| `loading` | bool | `fetchData` | |
| `loadingMore` | bool | `loadMoreEmails` | |
| `error` | string | `fetchData` | exported, never consumed, see pitfalls |

Functions on the value object: `fetchData`, `loadMoreEmails`, `removeSuggestion`,
`removeSuggestions`, `markUnsubscribed`, `setError`, `isCacheValid`.

Three `useRef` timestamps deliberately avoid re-renders (`EmailContext.js:19-22`):
`lastFetchRef` (data cache), `lastSyncRef` (Gmail sync throttle),
`lastStatsRef` (stats cache).

### fetchData in detail

`fetchData({ forceRefresh = false, query = 'in:inbox', maxResults = 100 })`,
`EmailContext.js:29-112`.

1. Cache short circuit: returns immediately if `!forceRefresh` and the cache is
   under 5 minutes old and `emails.length > 0`.
2. Gmail sync, throttled: `emailService.syncEmails()` only if `lastSyncRef` is
   empty or older than 5 minutes. Failure is caught and `console.warn`ed only.
3. Stats: refetched if stale or `forceRefresh`. Failure caught and warned only.
4. Four parallel calls through `Promise.allSettled`: emails, senders, pending
   suggestions, subscriptions. Each rejection degrades to `[]` except emails,
   which keeps the previous array.
5. Emails response accepts two shapes: a bare array (legacy) or
   `{ emails, nextPageToken, resultSizeEstimate }` (`EmailContext.js:79-88`).

### What stays local to Inbox

16 `useState` and 3 `useRef`, all declared in `Inbox.js:84-103`. None of it is
shared, and none of it survives a route change.

| Local state | Purpose |
|---|---|
| `view` | which of the three tabs is showing: `'emails'`, `'senders'`, `'subs'` |
| `selectedEmails` | array of checked `messageId` values |
| `selectedEmail` | the email open in the side reader, or null |
| `syncing` | sync button spinner |
| `analyzing` | analyze button spinner, both sync and async paths |
| `applyingAll` | apply all button spinner |
| `searchQuery` | the Gmail query box |
| `localSenders` | a mirror of `senders`, so sender rows can update without a refetch |
| `analyzingSender` | which sender email is mid analysis |
| `highConfOnly` | suggestion filter, threshold `confidence >= 0.8` |
| `unsubscribing` | which row key is mid unsubscribe |
| `focusedIndex` | keyboard cursor into `emails`, `-1` when unset |
| `showShortcuts` | shortcuts modal |
| `gamify` | streak state read from localStorage through `getStreakState` |
| `job` | async analysis job progress, or null |
| `showWelcome` | first run onboarding modal |

| Local ref | Purpose |
|---|---|
| `searchRef` | focused by the `/` shortcut |
| `rowRefs` | per row DOM nodes for `scrollIntoView`, reset to `[]` during render at `Inbox.js:523` |
| `pollRef` | the pending `setTimeout` of the job poller, cleared on unmount at `Inbox.js:124` |

The `localSenders` mirror is the one non obvious piece: `useEffect(() =>
setLocalSenders(senders), [senders])` at `Inbox.js:117` seeds it from context,
and then `handleAnalyzeSender` and `handleToggleAutoApply` write to the local copy
only. Context `senders` goes stale until the next `fetchData`.

## HTTP: everything goes through services/api.js

The rule holds today, verified: `grep -rn "from 'axios'"` over `frontend/src`
returns exactly one hit, `services/api.js:1`. No component, page or context imports
axios.

### The client

```js
const API_URL = process.env.REACT_APP_API_URL || 'http://localhost:8080';
const apiClient = axios.create({
  baseURL: API_URL,
  headers: { 'Content-Type': 'application/json' },
});
```

Two interceptors, `api.js:15-36`:

| Interceptor | Behaviour |
|---|---|
| request | reads `localStorage.accessToken`, sets `Authorization: Bearer <token>` when present. No header at all when absent |
| response, error path | on HTTP 401 removes `accessToken` and `userEmail`, then `window.location.assign('/')` unless already on `/`. The rejection still propagates |

The comment above the request interceptor states the reason plainly: the server
derives identity from the token, so the SPA no longer sends a spoofable
`X-User-Email`. Do not reintroduce one.

The 401 bounce is a full page load, not a router navigation. Every caller sees the
rejected promise first, so a `catch` in a page can fire a toast that is discarded
milliseconds later by the reload.

### Service objects

| Object | Methods | Endpoints |
|---|---|---|
| `authService` | 2 | `GET /api/auth/url`, `GET /api/auth/callback?code&state` |
| `emailService` | 5 | `GET /api/emails`, `POST /api/emails/sync`, `POST /api/emails/action`, `GET /api/stats`, `POST /api/emails/snooze` |
| `snoozeService` | 2 | `GET /api/snoozes?status`, `POST /api/snoozes/{id}/wake` |
| `protectService` | 3 | `GET/POST /api/protected`, `DELETE /api/protected/{id}` |
| `aiService` | 9 | `/api/ai/analyze`, `analyze-async`, `jobs/{id}`, `analyze-sender`, `apply`, `apply-batch`, `apply-bulk`, `suggestions`, `suggestions/{id}/reject` |
| `senderService` | 3 | `GET /api/senders`, `PUT /api/senders/{id}/preferences`, `POST /api/senders/rule` |
| `accountService` | 9 | `/api/account/profile`, `/api/usage`, `/api/stats/activity`, `GET/PUT /api/account/settings`, `/api/activity/log`, `/api/activity/undo`, `/api/account/export`, `DELETE /api/account` |
| `subscriptionService` | 2 | `GET /api/subscriptions`, `POST /api/unsubscribe` |
| `billingService` | 2 | `POST /api/billing/checkout`, `POST /api/billing/portal` |
| `ruleService` | 6 | `GET/POST /api/rules`, `PUT/DELETE /api/rules/{id}`, `POST /api/rules/apply`, `POST /api/rules/preview` |
| `configService` | 1 | `GET /api/config/status` |
| `waitlistService` | 1 | `POST /api/waitlist` |

Inbox imports five of them (`Inbox.js:4`): `aiService`, `senderService`,
`emailService`, `subscriptionService`, `protectService`.

Adding an endpoint means adding a method to the right object here, never an inline
axios call. Three service objects carry a comment explaining a design choice
(`configService` on why there is nothing to write, `waitlistService` on why it is
public, `ruleService.preview` on the dry run); match that register.

## Inbox.js block map

Line ranges verified against the 1068 line file. Open the block, not the file.

### Module scope, before the component

| Lines | Block | What is there |
|---|---|---|
| 1-14 | Imports | context, 5 services, toast, analytics, streak, `EmailReader`, `Spinner`, `cn`, 14 icons |
| 16-24 | `ACTIONS` | the action design tokens plus `actionMeta(a)` and `isReversible(a)` |
| 26-37 | `SHORTCUTS` | the 10 row keyboard help table, French labels |
| 39-47 | `AVATAR_GRADIENTS` and `gradientFor` | 5 literal classes, hashed by seed |
| 49-67 | `ConfidenceRing` | the only sub component, an SVG donut, radius 13 |
| 69-74 | `STAT_CARDS` | the 4 stat tiles, keyed on `stats` fields |

`ACTIONS` is the canonical example of the static class rule:

```js
const ACTIONS = {
  archive: { label: 'Archiver', Icon: Archive, badge: 'bg-sky-50 text-sky-600', ring: '#0ea5e9' },
  delete:  { label: 'Supprimer', Icon: Trash, badge: 'bg-rose-50 text-rose-600', ring: '#f43f5e' },
  label:   { label: 'Libellé', Icon: Tag, badge: 'bg-amber-50 text-amber-700', ring: '#d97706' },
  keep:    { label: 'Garder', Icon: Pin, badge: 'bg-emerald-50 text-emerald-600', ring: '#10b981' },
};
```

`badge` is a full literal Tailwind chain so the scanner sees it. `ring` is a raw
hex, because it is passed to an SVG `stroke` attribute, not to a class.

### Hooks and handlers

| Lines | Block | Notes |
|---|---|---|
| 76-106 | Setup | context destructure, 16 `useState`, 3 `useRef`, `ASYNC_THRESHOLD = 10` |
| 108-134 | Mount effects | auth guard plus `fetchData()`, `localSenders` mirror, `scrollIntoView` on focus, poll cleanup, first run flag, `dismissWelcome` |
| 136-156 | Derived and formatters | `visibleSuggestions` memo, `bumpGamify`, `formatNumber`, `formatDate` (fr-FR) |
| 158-169 | `undoToast` | the shared undo affordance, maps `archive` to `unarchive` and anything else to `untrash` |
| 171-206 | Basic handlers | `handleSync`, `handleSearch`, `handleSelectEmail`, `handleSelectAll`, `handleAnalyze` |
| 208-277 | Analysis engine | `announceResult`, `runSyncAnalyze`, `runAsyncAnalyze`, `handleQuotaError`, `pollJob` |
| 279-327 | Suggestions | `handleApplySuggestion`, `handleRejectSuggestion`, `handleApplyAll`, `handleRejectAll` |
| 329-359 | Direct actions | `directAction`, `handleReaderAction`, `handleSnooze` |
| 361-373 | `handleProtect` | adds the sender to the VIP list, toast links to `/settings` |
| 375-405 | `handleUnsubscribe` | three outcomes: `done`, `url` (new tab), `mailto` (location change) |
| 407-468 | Sender handlers | `handleAnalyzeSender`, `handleToggleAutoApply`, `handleApplyBulk`, `handleCreateSenderRule` |
| 470-518 | Keyboard shortcuts | one `window` keydown listener, deps `[emails, focusedIndex, view, visibleSuggestions]` |
| 520-523 | Pre render derived | `allSelected`, `progressPct`, `goalHit`, and the `rowRefs.current = []` reset |

### JSX, in render order

| Lines | Block | Notes |
|---|---|---|
| 525-557 | Hero command bar | title, subtitle from `stats.inboxCount`, search form, shortcuts button, sync button |
| 559-588 | Streak card | flame badge, daily goal bar, driven entirely by `ui/streak.js` |
| 590-607 | Stats grid | renders only when `stats` is truthy, 2 columns mobile, 4 desktop |
| 609-648 | View toggle and analyze | the three tabs, select all, the primary "Trier ma boîte" button |
| 650-677 | Async job progress | shown only while `job` is set, progress bar from `processed / total` |
| 679-737 | Suggestions panel | header with count, high confidence filter, reject all, apply all, then one row per suggestion |
| 739-740 | Main grid | switches to two columns when a reader is open: `lg:grid-cols-[1fr_minmax(360px,440px)]` |
| 741-836 | Emails view | count bar, 8 row skeleton, empty state, the row list, load more button |
| 837-899 | Senders view | empty state, then one card per sender with preference chips and bulk buttons |
| 900-974 | Subscriptions view | empty state, amber summary banner, then one card per subscription |
| 976-989 | Reader panel | `EmailReader` with 7 props, sticky, `h-[calc(100vh-7rem)]` |
| 992-1033 | Onboarding modal | 3 step first run explainer, `z-[95]` |
| 1035-1063 | Shortcuts modal | renders `SHORTCUTS`, `z-[90]`, click outside to close |

### The analysis flow

The single most load bearing chain in the page:

`handleAnalyze -> ids = selected or all -> ids.length > 10 ? runAsyncAnalyze : runSyncAnalyze`

Sync path: `aiService.analyzeEmails(ids) -> fetchData({forceRefresh:true}) ->
announceResult`.

Async path: `aiService.analyzeAsync(ids) -> setJob({status:'queued'}) ->
pollJob(jobId)` which re-arms `setTimeout` every 1500 ms until
`status === 'done' | 'error'`, then clears `job`, clears `analyzing`, force
refreshes, and either announces or errors.

`announceResult` reads three counters off the response, `suggestionsCreated`,
`autoApplied`, `cachedHits`, and turns them into one or two toasts. `autoApplied`
also feeds `bumpGamify`.

`handleQuotaError` is the shared 402 branch (`Inbox.js:250-256`): it raises an
action toast pointing at `/pricing` and returns `true` so the caller skips its own
generic error toast. Both analysis paths call it.

### Keyboard shortcuts

Registered on `window`, `Inbox.js:471-518`. Guarded: the handler returns early if
the active element is an `INPUT`, a `TEXTAREA` or `isContentEditable`, or if any of
`metaKey`, `ctrlKey`, `altKey` is held.

| Key | Action | Scope |
|---|---|---|
| `Escape` | close reader and shortcuts modal | always, checked before the typing guard |
| `?` | toggle the shortcuts modal | any view |
| `/` | focus the search input, prevents default | any view |
| `r` | `handleSync()` | any view |
| `a` | `handleApplyAll()`, only if `visibleSuggestions.length` | any view |
| `j` | move focus down | emails view only |
| `k` | move focus up | emails view only |
| `Enter` | open the focused email in the reader | emails view only |
| `x` | toggle selection of the focused email | emails view only |
| `e` | `directAction(cur, 'archive')` | emails view only |
| `#` or `Delete` | `directAction(cur, 'delete')` | emails view only |

Everything from `j` onward is behind `if (view !== 'emails' || emails.length === 0)
return;` at line 486. `Escape` is handled before the typing guard, so it works from
inside the search box; nothing else does.

The `SHORTCUTS` help table at lines 26-37 is a separate literal. It is not derived
from the handler, so adding a key means editing two places.

## Design system

### Tailwind config

`frontend/tailwind.config.js`. Content globs: `./src/**/*.{js,jsx,ts,tsx}` and
`./public/index.html`. No plugins.

| Token group | Values |
|---|---|
| `brand` | 11 steps, 50 to 950, `600: '#2563eb'` is the primary. One accent only, the config comment says so |
| `ink` | 11 steps, cool slate neutrals, 50 to 950 |
| `fontFamily.sans` | `"Hanken Grotesk"` then system fallbacks |
| `fontFamily.display` | `"General Sans"` then Hanken Grotesk then system |
| `boxShadow` | `soft`, `card`, `lift`. Three steps, no colored glow |
| `animation` | `fade-in`, `fade-up`, `slide-in-right`, `scale-in`, `shimmer` |

Both fonts load from third party CDNs in `public/index.html:17-24`: Google Fonts
for Hanken Grotesk, `api.fontshare.com` for General Sans. There is no self hosted
copy, so an offline or CDN blocked client silently falls back to `system-ui`.

### The @layer components classes

`frontend/src/index.css:44-81`. Nine classes, with current usage counts across
`frontend/src`:

| Class | Uses | Definition summary |
|---|---|---|
| `.btn` | base | inline flex, `gap-2`, `rounded-xl`, `px-4 py-2.5`, focus ring, disabled at 50 percent opacity |
| `.btn-primary` | 20 | `.btn` plus `bg-brand-600 text-white shadow-soft`. The comment states the rule: one primary action per screen, no gradient, no glow |
| `.btn-secondary` | 18 | `.btn` plus white background, `border-ink-200` |
| `.btn-ghost` | 21 | `.btn` plus transparent, `hover:bg-ink-100` |
| `.btn-danger` | 1 | `.btn` plus `bg-rose-50 text-rose-600` |
| `.card` | 45 | `rounded-2xl border border-ink-200/80 bg-white shadow-soft` |
| `.input` | 22 | full width, `rounded-xl`, `focus:ring-4 focus:ring-brand-500/10` |
| `.chip` | 27 | `rounded-full px-2.5 py-1 text-xs font-semibold`, carries no color of its own |
| `.skeleton` | 3 | gradient plus `animate-shimmer` |

`.chip` deliberately ships without a color: every call site pairs it with an
explicit pair, for example `chip bg-emerald-100 text-emerald-700`. That is why the
`ACTIONS.badge` values exist.

The `@layer base` block above them (lines 5-42) sets `color-scheme: light`, the
body background, the selection color, and a full custom scrollbar. There is no dark
mode anywhere in the app.

### Icons

`frontend/src/ui/icons.js`, 27 exports, imported by 13 of the 23 source files. A
`make(paths)` factory (lines 15-19) wraps a shared `base` props object: 24 by 24
viewBox, `fill: none`, `stroke: currentColor`, `strokeWidth: 2`, round caps and
joins. Every generated icon takes `{ size = 20, className = '', ...rest }`.

Exports: `Logo`, `Sparkles`, `Archive`, `Trash`, `Tag`, `Pin`, `Inbox`, `Search`,
`Refresh`, `Check`, `X`, `Menu`, `ChevronRight`, `Shield`, `Bolt`, `Settings`,
`LogOut`, `Users`, `Mail`, `Alert`, `Flame`, `Keyboard`, `Undo`, `Clock`,
`History`, `BellOff`, `Google`.

Two are not built by `make` because they are multicolor: `Logo` (lines 21-33, a
linear gradient with `id="ms-logo"`) and `Google` (lines 170-177, the four official
brand colors). Add a new monochrome icon with `make`; only break the pattern when
the mark genuinely needs more than `currentColor`.

`Inbox` and `History` are both an icon name and a page name. `Inbox.js:12` imports
it as `Inbox as InboxIcon` for exactly that reason.

### The static class constraint

Tailwind scans source text, so a class name that only exists after string
concatenation is never generated. Two patterns in this codebase satisfy the rule:

- Full literal chains stored in a lookup object, then selected at runtime:
  `ACTIONS[a].badge` in `Inbox.js:17-22`.
- Full literal chains in an array, indexed by a hash: `AVATAR_GRADIENTS` in
  `Inbox.js:39-42` and `EmailReader.js:15-21`.

Both are safe because the literal appears in the file. What is forbidden is
building the token itself, for example `` `bg-${color}-500` ``. Interpolating an
already literal class into a longer string is fine and is what `EmailReader.js:124`
does.

`ui/cn.js` is the whole class merging story: 4 lines,
`args.filter(Boolean).join(' ')`. No `clsx`, no `tailwind-merge`, so conflicting
utilities are not deduplicated. Order matters, last wins per CSS rules.

### Toast

`ui/Toast.js` exposes `ToastProvider` and `useToast()`, used by 8 pages. Four
methods on the returned object:

| Method | Variant | Default duration | Visual |
|---|---|---|---|
| `toast.success(m, o)` | success | 3500 ms | `Check` icon, `bg-emerald-500` |
| `toast.error(m, o)` | error | 3500 ms | `Alert` icon, `bg-rose-500` |
| `toast.info(m, o)` | info | 3500 ms | `Sparkles` icon, `bg-brand-500` |
| `toast.action(m, label, onClick, o)` | info by default | 5500 ms | adds a bold text button, dismisses on click |

`VARIANTS` (lines 7-11) has three entries. `action` is not a variant, it is an
option, and the spread order `{ variant: 'info', duration: 5500, ...o, action }`
means a caller can override the variant. `handleQuotaError` in `Inbox.js:252` does
exactly that with `{ variant: 'error' }`.

`toast.action` is how undo is offered. `undoToast` (`Inbox.js:159-169`) is the only
producer of undo affordances in the page.

### Gamification

`ui/streak.js`, 61 lines, entirely localStorage, key `mailsorter_gamify`,
`DAILY_GOAL = 20`. Two exports: `getStreakState()` reads without mutating,
`recordTriage(n)` rolls the streak forward on the first activity of a new day and
returns the fresh state. Both reads and writes are wrapped in try/catch, because
storage can be unavailable and gamification is explicitly best effort.

Inbox calls `recordTriage` through `bumpGamify(n)` on: suggestion applied (1),
apply all (the server reported count), direct action (1), snooze (1), bulk by
sender (the server reported count), and auto applied emails from an analysis run.

### UI copy language

All user facing strings are French, in the code, verbatim. Code, identifiers and
comments are English. Keep both. Examples straight from `Inbox.js`: the page title
`Votre boîte, sous contrôle.` (line 531), the primary button `"Trier ma boîte"`
(line 644), the empty state `Inbox Zero atteint 🎉` (line 767). Emoji appear in
copy and are intentional.

`public/index.html` sets `lang="fr"` and carries French meta description, og:title
and og:description.

## Analytics

`lib/analytics.js`, 39 lines. `initAnalytics()` injects the self hosted Umami
script once, from `index.js` only. `track(event, props)` is a no op without a
website id.

| Constant | Source | Default |
|---|---|---|
| `WEBSITE_ID` | `REACT_APP_UMAMI_WEBSITE_ID` | none, and no id means no tracking |
| `SRC` | `REACT_APP_UMAMI_SRC` | `https://analytics.sohbi.dev/script.js` |
| `DOMAINS` | `REACT_APP_UMAMI_DOMAINS` | `mailsorter.sohbi.dev` |

`script.dataset.domains` restricts collection to the production host, which keeps
`npm start` out of the stats. Auto track hooks the History API, so router
navigations count themselves: never add a manual pageview, it double counts. The
`window.umami?.track` optional chaining is load bearing and says so in a comment,
because an ad blocker can drop the script while `WEBSITE_ID` is set.

Every event currently emitted, with its properties:

| Event | Properties | Emitted from |
|---|---|---|
| `login_start` | none | `Login.js:79` |
| `login_done` | none | `Login.js:66`, `AuthCallback.js:35` |
| `inbox_sync` | none | `Inbox.js:176` |
| `ai_analyze` | `mode` (`sync` or `async`), `count` | `Inbox.js:200` |
| `suggestion_applied` | `action` | `Inbox.js:285` |
| `apply_all` | `applied` | `Inbox.js:310` |
| `snooze` | `preset` | `Inbox.js:352` |
| `unsubscribe` | `mode` (`oneclick`, `url`, `mailto`) | `Inbox.js:383,386,390` |
| `bulk_by_sender` | `action`, `applied` | `Inbox.js:445` |
| `rule_created` | `source` (`sender` or `editor`) | `Inbox.js:459`, `Rules.js:304` |
| `rules_applied` | `applied`, `scanned` | `Rules.js:339` |
| `upgrade_start` | none | `Pricing.js:125` |
| `waitlist_join` | `loggedIn` | `Pricing.js:161` |
| `data_export` | none | `Account.js:51` |

Every property above is a count or an enum label. No email address, no sender, no
subject. Preserve that.

## localStorage keys

Four keys, and that is the entire client side persistence layer.

| Key | Written by | Read by | Cleared by |
|---|---|---|---|
| `accessToken` | `Login.js:65`, `AuthCallback.js:34` | `api.js:16` request interceptor | `api.js:28` on 401, `Header.js:19` logout, `Account.js:65` |
| `userEmail` | `Login.js:64`, `AuthCallback.js:33` | `Header.js:9`, `Inbox.js:109`, `Account.js`, `Login.js:49`, `Pricing.js` | same three places as above |
| `mailsorter_gamify` | `ui/streak.js` | `ui/streak.js` | never |
| `mailsorter_onboarded` | `Inbox.js:132` | `Inbox.js:128` | never |

`userEmail` is a UI convenience only; the server never trusts it. Inbox uses it as
a cheap auth guard on mount (`Inbox.js:108-115`), redirecting to `/` when it is
missing, before any request goes out.

Neither `mailsorter_gamify` nor `mailsorter_onboarded` is cleared on logout, so the
next user on the same browser inherits the streak and skips onboarding.

## Build and serving

### Scripts

`frontend/package.json`, unmodified CRA:

| Script | Command |
|---|---|
| `start` | `react-scripts start`, dev server on :3000 |
| `build` | `react-scripts build` |
| `test` | `react-scripts test`, has nothing to run |
| `eject` | `react-scripts eject` |

Production build is run as `CI=false npm run build`, because CI treats warnings as
errors otherwise. There is no `proxy` field in `package.json`, which is why the dev
server needs `REACT_APP_API_URL` or the `http://localhost:8080` fallback.

Runtime dependencies, all five of them: `axios`, `dompurify`, `react`, `react-dom`,
`react-router-dom`. Dev: `autoprefixer`, `postcss`, `react-scripts`, `tailwindcss`.
No test library is installed, not even the CRA default `@testing-library/*`.

### The REACT_APP_ build time trap

`REACT_APP_*` values are inlined into the static bundle by webpack at build time.
Changing one on a running container does nothing. This already caused a production
incident, PR #15.

| Var | Read at | Declared as a Docker ARG | Compose default |
|---|---|---|---|
| `REACT_APP_API_URL` | `api.js:3` | yes, `Dockerfile:20-21`, default `/` | `${REACT_APP_API_URL:-/}` |
| `REACT_APP_UMAMI_WEBSITE_ID` | `analytics.js:8` | yes, `Dockerfile:25-26`, default empty | `${REACT_APP_UMAMI_WEBSITE_ID:-}` |
| `REACT_APP_UMAMI_SRC` | `analytics.js:9` | **no** | not passed |
| `REACT_APP_UMAMI_DOMAINS` | `analytics.js:10` | **no** | not passed |

The last two are readable by the code but are not plumbed through the Dockerfile or
`docker-compose.yml`, so in any container build they always take their hardcoded
defaults. Overriding them requires adding the ARG and ENV pair first.

`REACT_APP_API_URL` defaults to `/` in the image on purpose: the bundle then calls
the API same origin through the nginx proxy, which avoids baking a host specific
URL into a static build and sidesteps CORS entirely in production.

### nginx

`frontend/nginx.conf`, 38 lines, four blocks.

| Block | Directive | Why |
|---|---|---|
| `location /static/` | `try_files $uri =404` plus `Cache-Control: public, max-age=31536000, immutable` | filenames are content hashed, so they can be cached forever. The `=404` is deliberate: a missing bundle must fail as a missing bundle rather than arrive as HTML with a 200 |
| `location /` | `try_files $uri $uri/ /index.html` | the SPA fallback |
| `location = /index.html` | `Cache-Control: no-cache` | index.html is the only unhashed file and it points at the current bundle. Without this, browsers apply their freshness heuristic and can serve a deployment old app for hours. This is what commit `8e35c41` fixed |
| `location /api` | `proxy_pass http://backend:8080` plus upgrade headers | same origin API, no CORS in production |

Dockerfile is a two stage build: `node:18-alpine` builds, `nginx:alpine` serves
`/app/build` at `/usr/share/nginx/html` and copies `nginx.conf` to
`/etc/nginx/conf.d/default.conf`, exposing port 80.

## Known pitfalls

**A failed email fetch is completely invisible.** `fetchData` sets `error` when the
emails call rejects (`EmailContext.js:101-103`), but `error` and `setError` are
never destructured by any consumer. The list simply keeps its previous contents.
`grep -rn "useEmails" frontend/src` returns one consumer, `Inbox.js:82`, and its
destructure at lines 79-82 takes neither `error`, nor `setError`, nor
`isCacheValid`. Those three are dead exports today.

**Three of the four parallel fetches degrade to an empty array in silence.**
`Promise.allSettled` at `EmailContext.js:66-71`, then lines 90-92 map any rejection
of senders, suggestions or subscriptions to `[]`. A backend 500 on `/api/senders`
renders as "no senders yet", not as an error.

**The sync success toast can lie.** `handleSync` awaits `fetchData({forceRefresh:
true})` and then unconditionally shows `'Boîte synchronisée'` (`Inbox.js:177`), but
`fetchData` swallows a Gmail sync failure with a `console.warn`
(`EmailContext.js:47-49`). A user can see the success toast on a sync that never
happened.

**The cache is not keyed by query.** The short circuit at `EmailContext.js:33`
checks only freshness and `emails.length > 0`, ignoring the `query` argument. Every
query bearing call currently passes `forceRefresh: true`, so nothing is broken
today, but a new caller that omits it would get results for a different search.

**`fetchData` changes identity on every data change.** Its `useCallback` deps are
`[emails, senders, suggestions, stats, isCacheValid]` (`EmailContext.js:112`), so
it is a new function after each successful load. Inbox's mount effect works around
this with `[navigate]` and an `eslint-disable-next-line
react-hooks/exhaustive-deps` (`Inbox.js:114-115`). Never add `fetchData` to a
dependency array without checking for a loop; the keyboard effect at line 517 uses
the same escape hatch.

**The async job poller has no ceiling.** On a network error `pollJob` re-arms at
2500 ms with no attempt counter and no backoff (`Inbox.js:274-276`). If the job
endpoint stays unreachable, the page polls forever and `analyzing` stays `true`, so
the analyze button never comes back. Only unmount stops it, through the
`clearTimeout(pollRef.current)` cleanup at line 124.

**`localSenders` and context `senders` drift.** Sender preference toggles and
per sender analyses write only to the local mirror (`Inbox.js:412`, `431-435`).
Leaving `/inbox` and coming back re-seeds from the context copy, which may be
older than what was on screen.

**Two copies of `gradientFor`.** Identical implementations at `Inbox.js:39-47` and
`EmailReader.js:15-27`, including the `AVATAR_GRADIENTS` array. Change one and the
avatar for the same sender differs between the row and the reader.

**The banned punctuation rule is not actually clean in the frontend.** CLAUDE.md
rule 6 forbids em dashes, en dashes, ellipsis characters and curly quotes anywhere.
Verified counts over `frontend/src` today: 0 em dashes, 0 en dashes, 0 curly double
quotes, 0 non breaking spaces, but 32 curly apostrophes and 17 ellipsis characters
remain in French UI copy.

| File | Curly apostrophes | Ellipsis characters |
|---|---|---|
| `pages/Rules.js` | 13 | 3 |
| `pages/Login.js` | 8 | 1 |
| `pages/Pricing.js` | 5 | 3 |
| `pages/Inbox.js` | 3 | 5 |
| `pages/Setup.js` | 3 | 1 |
| `App.js` | 0 | 1 |
| `pages/Account.js` | 0 | 1 |
| `pages/AuthCallback.js` | 0 | 1 |
| `components/EmailReader.js` | 0 | 1 |

Do not introduce new ones. Do not mass rewrite the existing ones as a side effect
of an unrelated change either: that is a separate, reviewable commit.

**The 401 bounce races your error handling.** `api.js:24-36` clears storage and
calls `window.location.assign('/')` before rejecting. A `catch` block that shows a
toast will fire, then the page reloads and the toast is gone. Do not rely on a
visible message for a 401.

**`error_page 404 /index.html` is declared at server level** (`nginx.conf:37`), so
it is inherited by the `/static/` location whose `try_files` ends in `=404`. The
status code stays 404, which is what the `/static/` comment is guarding, but the
body would be the SPA HTML. Not verified against a running container; check it
before relying on the body of a missing asset response.

**Email bodies must stay sanitized.** `EmailReader.js:62-64` runs
`DOMPurify.sanitize(email.body, { USE_PROFILES: { html: true } })` before the
single `dangerouslySetInnerHTML` in the codebase (line 162). That is the only place
untrusted HTML is rendered. Any new surface showing an email body needs the same
treatment.

## Testing

**There are zero frontend tests.** `find frontend -name '*.test.js' -o -name
'*.spec.js' -o -name '__tests__'` returns nothing, and no testing library is in
`package.json`. `npm test` exists as a CRA default and has nothing to run. CI never
invokes it.

Frontend verification therefore has exactly two legs:

1. `cd frontend && CI=false npm run build` must print "Compiled successfully".
2. Browser navigation through chrome-devtools MCP. Click the thing, look at it.

Never claim a frontend change works on the strength of a unit test, because there
is no unit test to run. A build that compiles proves the bundle links, nothing
about behaviour.

Manual recipe for the Inbox specifically, in the order that catches the most:

| Step | What to check |
|---|---|
| Load `/inbox` cold, no localStorage | redirect to `/` fires before any request |
| Load `/inbox` logged in, first time | the onboarding modal appears once, then never again |
| Sync button | spinner runs, list refreshes, success toast |
| Select 5 emails, analyze | sync path, no job card, suggestions panel appears |
| Select 15 emails, analyze | async path, job card with a moving progress bar |
| Apply one suggestion, archive action | row disappears optimistically, undo toast offered, undo restores it |
| `j`, `k`, `Enter`, `Escape` | focus ring moves, reader opens, reader closes |
| `/` then type | search input focuses, and `j`/`k` no longer move the cursor |
| Switch to Expéditeurs, toggle auto-pilote | chip flips, no full refetch |
| Switch to Abonnements, unsubscribe | one of the three outcomes, and the row marks itself |
| Resize below `sm` | header collapses to the burger, drawer opens, `/account` reachable |

## Key Files

| File | Description |
|---|---|
| `frontend/src/pages/Inbox.js` | The triage cockpit, 1068 lines, see the block map |
| `frontend/src/contexts/EmailContext.js` | The only shared store, 5 min cache and sync throttle |
| `frontend/src/services/api.js` | The only axios instance, 12 service objects, 45 methods |
| `frontend/src/App.js` | Route table and the config gate |
| `frontend/src/index.js` | Mount point, calls `initAnalytics()` before React |
| `frontend/src/components/EmailReader.js` | Side reader, snooze presets, the only sanitized HTML render |
| `frontend/src/components/Header.js` | Nav, identity chip, mobile drawer, self hiding |
| `frontend/src/ui/icons.js` | 27 hand written SVG icons |
| `frontend/src/ui/Toast.js` | `ToastProvider` and `useToast`, 4 methods |
| `frontend/src/ui/streak.js` | localStorage gamification, `DAILY_GOAL = 20` |
| `frontend/src/ui/Spinner.js` | The single spinner |
| `frontend/src/ui/cn.js` | 4 line class joiner |
| `frontend/src/lib/analytics.js` | Umami injection and `track()` |
| `frontend/src/index.css` | `@layer base` and the 9 `@layer components` classes |
| `frontend/tailwind.config.js` | brand and ink palettes, shadows, 5 animations |
| `frontend/public/index.html` | `lang="fr"`, meta tags, both font CDN links |
| `frontend/nginx.conf` | SPA fallback, immutable static, no-cache index.html, `/api` proxy |
| `frontend/Dockerfile` | Two stage build, the two `REACT_APP_*` build args |
| `frontend/package.json` | 5 runtime deps, 4 dev deps, CRA scripts |

## References

### Source Files

- `frontend/src/pages/Inbox.js` : every handler, the keyboard map, the three views
- `frontend/src/contexts/EmailContext.js` : `fetchData`, the cache rules, `useEmails`
- `frontend/src/services/api.js` : the axios instance, both interceptors, all endpoints
- `frontend/src/App.js` : routes, the `isConfigured` gate, provider nesting
- `frontend/src/components/EmailReader.js` : snooze presets, DOMPurify call
- `frontend/src/components/Header.js` : nav items, visibility rule, logout
- `frontend/src/ui/icons.js` : the `make` factory and the icon inventory
- `frontend/src/ui/Toast.js` : variants, durations, the action toast
- `frontend/src/ui/streak.js` : `getStreakState`, `recordTriage`
- `frontend/src/lib/analytics.js` : `initAnalytics`, `track`, the three env constants
- `frontend/src/index.css` : the design system classes
- `frontend/tailwind.config.js` : tokens
- `frontend/nginx.conf` : cache headers and the API proxy
- `frontend/Dockerfile` : where `REACT_APP_*` gets frozen
- `docker-compose.yml` lines 51-64 : the frontend build args

### Related Context Docs

- [auth-session-security.md](auth-session-security.md) : the session token this SPA stores and attaches, and the 401 contract
- [ai-triage.md](ai-triage.md) : what the analyze, suggestion and job endpoints do on the server
- [gmail-sync.md](gmail-sync.md) : what `POST /api/emails/sync` actually performs
- [billing-quota.md](billing-quota.md) : the 402 the Inbox turns into the Pro toast

### Constitution

`CLAUDE.md` holds the map: the route list, the "every HTTP call in api.js" rule,
the design system summary, and the `REACT_APP_*` gotcha. This document is the
terrain under those entries.
