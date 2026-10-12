# Plan: small-context models - compact early and hand off to a subagent

Status: proposed (2026-10-12). Nothing here is built.

## Problem
A model with a small context window (for example 23k) fills up during one long turn, because tool output piles up.
Today goharness has two defences, and neither fits:
- `trimHistory` (context.go) runs before every request once the history passes about 75% of the window. It shortens
  old tool output and drops whole early turns. It never summarizes, and it cannot help inside a single turn.
- `/compact` (compact.go, `Session.Compact`) summarizes everything *before the latest user message* and keeps the
  current turn verbatim. It is manual and refuses to run while a turn is running (`s.running`).

So a long single turn on a small model has no remedy except trimming the latest tool results.

## Goal
Before the window fills, the main model hands the unfinished work to a read-only subagent, and its own
history is compacted at the same time, so it resumes small.

## Phases (each is useful alone; stop after any of them)

### Phase 1 - auto-compact between turns (about 1 hour)
- Before `Run` starts a turn, if estimated tokens exceed `GOHARNESS_COMPACT_AT` (percent of the prompt budget,
  default 60, 0 = off), call the existing `Compact` logic first.
- `GOHARNESS_COMPACT_MODEL`: optional larger model for the summary (small models write weak summaries).
- Show a notice event ("compacted: 14.2k -> 3.1k tokens") in the TUI and web UI.
- Tests: threshold on and off, no-op when there is no earlier turn, a summary failure leaves history unchanged.

### Phase 2 - handoff nudge inside a turn (about 1 hour)
- At the top of each tool round in `Run` (agent.go, not for subagents), if tokens exceed `GOHARNESS_HANDOFF_AT`
  (default 65% of budget) and spawn_agent is available, append one system notice once per turn:
  "Context is nearly full. Call spawn_agent now with a complete brief (goal, what is done, what is left, exact
  paths). Do not read more files yourself."
- No new model call and no concurrency: the model decides, using the existing delegate-research brief rules.
- Tests: fires once, not in subagents, not when spawn_agent is unavailable.

### Phase 3 - compact the current turn while the subagent runs (about 3-4 hours, the risky one)
- `Compact` leaves the current turn alone, so mid-turn it frees nothing. Add `CompactTurn`: summarize the
  tool rounds of the current turn (from the user message up to, not including, the pending assistant
  tool_calls message) into one progress note, keeping the user message and the pending call.
- When a spawn_agent call starts (`runSubagentsTogether`), start `CompactTurn` in a goroutine, on the other host
  if the summarizer model is set (Phase 1). Do not hold `s.mu` across the model call.
- Splice rule: when both finish, replace the old rounds with the note, then append the subagent result.
  If the summary fails or times out, keep the history as is (`trimHistory` remains the fallback).
- A separate guard (not `s.running`, which is true during the turn) stops two compactions at once and stops
  `/undo` and `/compact` from running mid-splice.
- Fix up the `s.turns` marks (undo indices) like `Compact` does, save the session file, emit one notice.
- Tests (with `-race`): a pending tool_call keeps its result paired; subagent finishing first or second; summary
  error; cancel during the turn; undo after a handoff.

### Phase 4 - not now
- A subagent that can edit files. Subagents are read-only today (Plan mode, 8 rounds), so a handoff is only
  safe for research and review. Editing needs its own permission and approval design.

## Open questions
- Default thresholds per model: should they come from the model context size instead of percentages?
- Which model summarizes by default when `GOHARNESS_COMPACT_MODEL` is unset: the main model or the subagent model?
- Web UI: show the handoff as a normal subagent card plus a "compacted" notice, or a separate marker?

## Recommended order
Phase 1 then 2 (about 2 hours, no concurrency). Run a real small-model session. Only do Phase 3 if turns still
overflow.
