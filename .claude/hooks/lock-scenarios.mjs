#!/usr/bin/env node
// PreToolUse hook: the acceptance scenarios are locked.
//
// The scenario tests (backend/internal/api/scenarios_*_test.go) and the harness
// they run on (backend/internal/api/acceptance_*_test.go) are the definition of
// done of every change made through /feature. If the agent that writes the
// code could also rewrite them, "the scenarios pass" would prove nothing, and
// deleting or weakening a failing test is exactly what coding agents do under
// pressure. So only the "scenariste" subagent may write them, and it only does
// so after Nohé validated the sentences.
//
// File tools are checked on their path. Bash is checked on a heuristic (the
// command names a locked file AND looks like it writes); the computational
// backstop is the git diff the orchestrator and the controleur run against the
// commit that locked the tests.
//
// Plain Node, no dependency: the frontend toolchain already requires Node.

import { readFileSync } from 'node:fs';

const WRITER = 'scenariste';
const LOCKED = [
  /(^|\/)backend\/internal\/api\/scenarios_[^/]*_test\.go$/,
  /(^|\/)backend\/internal\/api\/acceptance_[^/]*_test\.go$/,
];
const MENTIONS_LOCKED = /(scenarios|acceptance)_[A-Za-z0-9_*]*_test\.go|internal\/api\/(scenarios|acceptance)_\*/;
const LOOKS_LIKE_A_WRITE = new RegExp([
  String.raw`(^|[^>0-9&])>{1,2}\s*(?!/dev/null)[^&\s]`, // redirection into a file
  String.raw`\btee\b`,
  String.raw`\bsed\s+(-[a-zA-Z]*i|--in-place)`,
  String.raw`\bperl\s+-[a-zA-Z]*i`,
  String.raw`\b(rm|mv|cp|truncate|dd|ln|install|unlink)\s`,
  String.raw`\bgit\s+(checkout|restore|reset|rm|mv|stash|apply|am|revert|clean)\b`,
  String.raw`\bpatch\b`,
  String.raw`\b(gofmt|goimports)\s+(-[a-z]+\s+)*-w`,
  String.raw`\bpython3?\b|\bnode\s+-e\b`,
].join('|'));

let input;
try {
  input = JSON.parse(readFileSync(0, 'utf8'));
} catch {
  process.exit(0); // not our business: no decision
}

if (input.agent_type === WRITER) process.exit(0);

const root = String(process.env.CLAUDE_PROJECT_DIR || input.cwd || '').replace(/\\/g, '/').replace(/\/$/, '');
const relative = (p) => {
  let s = String(p || '').replace(/\\/g, '/');
  if (root && s.startsWith(root + '/')) s = s.slice(root.length + 1);
  return s;
};

const tool = input.tool_name;
const args = input.tool_input || {};
let target = null;

if (tool === 'Bash' || tool === 'PowerShell') {
  const cmd = String(args.command || '');
  if (MENTIONS_LOCKED.test(cmd) && LOOKS_LIKE_A_WRITE.test(cmd)) target = cmd;
} else {
  const p = args.file_path || args.notebook_path || args.path;
  if (p && LOCKED.some((re) => re.test(relative(p)))) target = relative(p);
}

if (!target) process.exit(0);

const reason =
  `Verrou des scénarios : "${target}" touche un test de scénario ou leur harnais. ` +
  `Seul l'agent "${WRITER}" peut les modifier, et seulement après que Nohé a validé un changement de comportement. ` +
  `Si un scénario te semble faux ou impossible, arrête-toi et explique pourquoi au lieu de le contourner.`;

process.stdout.write(JSON.stringify({
  hookSpecificOutput: {
    hookEventName: 'PreToolUse',
    permissionDecision: 'deny',
    permissionDecisionReason: reason,
  },
}));
