// Run with: node --test .claude/hooks/lock-scenarios.test.mjs
// The lock is only worth something if it denies what it should and lets the
// rest through, so both halves are pinned here.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

const hook = fileURLToPath(new URL('./lock-scenarios.mjs', import.meta.url));
const ROOT = '/repo';

function decide(input) {
  const out = spawnSync(process.execPath, [hook], {
    input: JSON.stringify({ cwd: ROOT, ...input }),
    env: { ...process.env, CLAUDE_PROJECT_DIR: ROOT },
    encoding: 'utf8',
  });
  assert.equal(out.status, 0, out.stderr);
  if (!out.stdout) return 'allow';
  return JSON.parse(out.stdout).hookSpecificOutput.permissionDecision;
}

const scenarioFile = `${ROOT}/backend/internal/api/scenarios_3_sender_test.go`;
const harnessFile = `${ROOT}/backend/internal/api/acceptance_harness_test.go`;
const codeFile = `${ROOT}/backend/internal/api/unsubscribe.go`;

const cases = [
  ['codeur edits a scenario', { agent_type: 'codeur', tool_name: 'Edit', tool_input: { file_path: scenarioFile } }, 'deny'],
  ['codeur rewrites the harness', { agent_type: 'codeur', tool_name: 'Write', tool_input: { file_path: harnessFile } }, 'deny'],
  ['main session adds a scenario file', { tool_name: 'Write', tool_input: { file_path: `${ROOT}/backend/internal/api/scenarios_9_new_test.go` } }, 'deny'],
  ['relative path', { agent_type: 'codeur', tool_name: 'Edit', tool_input: { file_path: 'backend/internal/api/scenarios_1_single_message_test.go' } }, 'deny'],
  ['windows path', { agent_type: 'codeur', tool_name: 'Edit', tool_input: { file_path: 'C:\\repo\\backend\\internal\\api\\scenarios_1_x_test.go' } }, 'deny'],
  ['scenariste edits a scenario', { agent_type: 'scenariste', tool_name: 'Edit', tool_input: { file_path: scenarioFile } }, 'allow'],
  ['codeur edits production code', { agent_type: 'codeur', tool_name: 'Edit', tool_input: { file_path: codeFile } }, 'allow'],
  ['codeur edits an ordinary test', { agent_type: 'codeur', tool_name: 'Edit', tool_input: { file_path: `${ROOT}/backend/internal/api/unsubscribe_test.go` } }, 'allow'],
  ['sed -i on a scenario', { agent_type: 'codeur', tool_name: 'Bash', tool_input: { command: `sed -i 's/a/b/' backend/internal/api/scenarios_3_sender_test.go` } }, 'deny'],
  ['redirect into the harness', { agent_type: 'codeur', tool_name: 'Bash', tool_input: { command: `echo x > backend/internal/api/acceptance_harness_test.go` } }, 'deny'],
  ['git checkout of a scenario', { agent_type: 'codeur', tool_name: 'Bash', tool_input: { command: `git checkout -- backend/internal/api/scenarios_1_single_message_test.go` } }, 'deny'],
  ['rm with a glob', { agent_type: 'codeur', tool_name: 'Bash', tool_input: { command: `rm backend/internal/api/scenarios_*_test.go` } }, 'deny'],
  ['python rewriting a scenario', { agent_type: 'codeur', tool_name: 'Bash', tool_input: { command: `python3 fix.py backend/internal/api/scenarios_2_selection_test.go` } }, 'deny'],
  ['reading a scenario', { agent_type: 'codeur', tool_name: 'Bash', tool_input: { command: `cat backend/internal/api/scenarios_3_sender_test.go` } }, 'allow'],
  ['grep to /dev/null', { agent_type: 'codeur', tool_name: 'Bash', tool_input: { command: `grep -n Tout backend/internal/api/scenarios_3_sender_test.go > /dev/null` } }, 'allow'],
  ['running the scenarios', { agent_type: 'codeur', tool_name: 'Bash', tool_input: { command: `make scenarios 2>&1 | tail -40` } }, 'allow'],
  ['vet with the tag', { agent_type: 'controleur', tool_name: 'Bash', tool_input: { command: `cd backend && go vet -tags acceptance ./internal/api/ 2>&1` } }, 'allow'],
  ['diff against the lock', { agent_type: 'controleur', tool_name: 'Bash', tool_input: { command: `git diff --stat abc123..HEAD -- 'backend/internal/api/scenarios_*_test.go'` } }, 'allow'],
];

for (const [name, input, want] of cases) {
  test(name, () => assert.equal(decide(input), want));
}
