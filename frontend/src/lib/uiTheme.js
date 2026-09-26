// Which landing this browser renders on /.
//
// The server decides (UI_THEME, served by GET /api/config/status), so the
// opinionated Grand Hotel theme can be unplugged by changing one variable and
// restarting the backend, without a rebuild. `?ui=hotel` or `?ui=classic`
// overrides it for this browser tab only, to compare both on a live instance
// before flipping the switch for everyone; `?ui=default` drops the override.

export const UI_THEMES = ['hotel', 'classic'];

const PREVIEW_KEY = 'mailsorter_ui_preview';

// An absent or unknown server value falls back to classic: a backend that does
// not send the field predates the hotel theme, and classic is the landing it
// shipped with.
export function serverUiTheme(value) {
  return UI_THEMES.includes(value) ? value : 'classic';
}

// Pure: the query string wins, then a preview remembered for this tab, then
// the server.
export function resolveUiTheme(serverValue, search, remembered) {
  const asked = new URLSearchParams(search || '').get('ui');
  if (UI_THEMES.includes(asked)) return asked;
  if (asked !== 'default' && UI_THEMES.includes(remembered)) return remembered;
  return serverUiTheme(serverValue);
}

// Remembers or forgets the preview for this tab, then resolves. Storage can
// throw (private browsing, blocked site data): the preview is then simply not
// remembered, and the query still applies to this page.
export function effectiveUiTheme(serverValue, search) {
  const asked = new URLSearchParams(search || '').get('ui');
  let remembered = null;
  try {
    if (asked === 'default') sessionStorage.removeItem(PREVIEW_KEY);
    else if (UI_THEMES.includes(asked)) sessionStorage.setItem(PREVIEW_KEY, asked);
    remembered = sessionStorage.getItem(PREVIEW_KEY);
  } catch {
    remembered = null;
  }
  return resolveUiTheme(serverValue, search, remembered);
}
