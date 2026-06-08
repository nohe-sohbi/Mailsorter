// Local gamification engine: tracks how many emails you've triaged today and
// your day streak. Everything lives in localStorage — no backend required.

const KEY = 'mailsorter_gamify';
export const DAILY_GOAL = 20;

function dayKey(d = new Date()) {
  return d.toISOString().slice(0, 10);
}

function isYesterday(dateStr) {
  if (!dateStr) return false;
  const y = new Date();
  y.setDate(y.getDate() - 1);
  return dateStr === dayKey(y);
}

function read() {
  try {
    return JSON.parse(localStorage.getItem(KEY)) || null;
  } catch {
    return null;
  }
}

function write(state) {
  try {
    localStorage.setItem(KEY, JSON.stringify(state));
  } catch {
    /* storage unavailable — gamification is best-effort */
  }
}

// Returns the current display state without mutating the streak count.
export function getStreakState() {
  const today = dayKey();
  const s = read() || { date: today, today: 0, streak: 0 };
  if (s.date !== today) {
    // A new day with no activity yet: streak survives only if last active day
    // was yesterday, otherwise it's broken.
    return { today: 0, streak: isYesterday(s.date) ? s.streak : 0, goal: DAILY_GOAL };
  }
  return { today: s.today, streak: s.streak, goal: DAILY_GOAL };
}

// Records `n` triaged emails, rolling the streak forward on the first activity
// of a new day. Returns the fresh display state.
export function recordTriage(n = 1) {
  if (!n || n < 1) return getStreakState();
  const today = dayKey();
  const s = read() || { date: null, today: 0, streak: 0 };

  if (s.date !== today) {
    s.streak = s.date && isYesterday(s.date) ? s.streak + 1 : 1;
    s.today = 0;
    s.date = today;
  }
  s.today += n;
  write(s);
  return { today: s.today, streak: s.streak, goal: DAILY_GOAL };
}
