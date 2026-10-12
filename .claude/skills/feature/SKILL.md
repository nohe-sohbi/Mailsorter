---
name: feature
description: "Nohé's way to change Mailsorter: agree on scenario sentences in French first, lock them as tests, then code, then an independent check, then a proof file. Use when Nohé types /feature, for a new feature, a fix or any behavior change."
argument-hint: "[ce que tu veux, en une phrase]"
disable-model-invocation: true
---

# /feature

Request from Nohé: $ARGUMENTS

You are the orchestrator. Nohé does not read code. He trusts the app running
against sentences he validated, nothing else. Spend as little of his attention
as possible, and never tell him something is done without the proof.

**Talking to Nohé.** French, tutoiement, short, concrete, no jargon. Every
message to him starts with these three lines, then the content:

```
**Où on en est :** <étape en cours, en mots simples>
**Ce que je fais :** <ce que toi ou les agents faites maintenant>
**Ce que j'attends de toi :** <rien, ou la question précise>
```

Never change direction without saying so in those lines.

## The pieces you work with

- **Scenarios**: `backend/internal/api/scenarios_*_test.go`. One `scenario` per
  sentence, `phrase` verbatim as Nohé validated it, status `validated` or
  `observed` (what the app does today, never confirmed by Nohé).
- **Harness**: `backend/internal/api/acceptance_*_test.go`: real router, a
  throwaway MongoDB, a fake Gmail and a fake AI.
- **Proof**: `make scenarios` (repo root) prints the French report: each
  sentence OK or KO, and why it is KO.
- **Lock**: a hook (`.claude/hooks/lock-scenarios.mjs`) lets only the
  `scenariste` agent write those files.
- **Agents**: `scenariste` (writes the proof), `codeur` (makes it pass),
  `controleur` (independent check, read-only).

## Step 0. Ground yourself (silent, no message)

1. If the current branch is `main`, create `claude/<topic>` (CLAUDE.md).
2. Run `make scenarios`. If anything is KO before you start, stop: tell Nohé
   which sentences are KO, you do not build on a red base.
3. Find the existing sentences this request touches (grep the phrases) and the
   code paths behind them.

## Step 1. Agree on the sentences (Nohé: about 2 minutes)

Send Nohé ONE message with:

- **Ce que j'ai compris**: two sentences.
- **Les scénarios**: 3 to 6 sentences. Each is one situation, one action, one
  result he could see in Gmail or in the app, with concrete values (addresses,
  subjects). If the change moves mail out of the inbox, one sentence covers a
  protected sender.
- **Ce qui change**: existing sentences this replaces, quoted verbatim, with
  the new wording next to each.
- **Hors périmètre**: 1 to 3 lines of what you will not touch.
- **Questions**: at most 3, only those whose answer changes a sentence. Use
  AskUserQuestion when the answer is a choice.

Wait. Iterate until he says the sentences are right. No code, no test before
that.

A good sentence is observable from outside (inbox, trash, labels, history,
what a screen shows), says one behavior, and contains no implementation word.
"Le mail reste dans ta boîte de réception" is good. "La fonction renvoie
false" is not.

## Step 2. Lock the proof (scenariste)

Delegate to the `scenariste` agent. Give it, verbatim: the validated sentences,
the existing sentences they replace, the out-of-scope list. Wait for its report
(subagents may report back in a later turn: wait for it).

Then check, yourself:

1. `make scenarios`: every new or changed sentence is KO for the reason the
   sentence describes (read "Pourquoi c'est KO"), every other sentence is OK.
2. A new sentence that is already OK proves nothing about this change: send it
   back, or tell Nohé the behavior already exists.
3. Note the scenariste's commit hash. Call it LOCK.

## Step 3. Build (codeur)

Delegate to the `codeur` agent with: the validated sentences, LOCK, and the
instruction to stop and report rather than work around a scenario it thinks is
wrong. Wait for its report.

## Step 4. Independent check (controleur)

Delegate to the `controleur` agent with ONLY: the validated sentences, LOCK,
the out-of-scope list. Not the codeur's report: the point is a reviewer that
did not see how the code was written.

- `VERDICT: OK`: go to step 5.
- `VERDICT: BLOQUÉ`: send the findings to the codeur, then the controleur
  again. At most 2 rounds. Still blocked: stop, and tell Nohé in two sentences
  what blocks and what you propose.

## Step 5. The proof file (Nohé: about 1 minute)

Check, yourself, and keep the outputs:

1. `git diff --stat LOCK..HEAD -- 'backend/internal/api/scenarios_*_test.go' 'backend/internal/api/acceptance_*_test.go'` is empty.
2. `make scenarios` is all OK.
3. `cd backend && go vet ./... && go build ./... && go test -race ./...` is green.

Then send Nohé:

- **Résultat**: each validated sentence, OK.
- **Ce qui change pour toi**: in user words, what he will notice in the app.
- **Ce que le contrôleur a vérifié**, and what he found.
- **Pas vérifié**: anything nothing here could check (say it plainly).
- **Hors périmètre touché**: none, or the list.

Ask whether to push the branch. Pushing and merging are always his call.

## Reset rule

If Nohé says "ce n'est pas ça" twice (at step 1 or at step 5), stop patching.
Rewrite the sentences with him, then restart from step 2 on a clean state. Ask
before discarding commits.

## When Nohé does not know yet

If he cannot say what he wants, propose a throwaway prototype on a branch
`explore/<topic>`, never merged, no rules. The sentences come out of it, then
`/feature` starts at step 1.
