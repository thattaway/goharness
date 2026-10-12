import type { AgentEvent, DiffLine, Message, TurnInfo } from "./api";

// The transcript is one list of items, built two ways: from a session's
// stored messages (on load), then updated live from run events. Both paths
// produce the same items, so a reload mid-run looks like it never happened.

export type ToolStatus = "running" | "pending" | "denied" | "blocked" | "cancelled" | "done";

export type Item =
  | { kind: "user"; key: string; text: string; images?: string[] }
  | { kind: "agent"; key: string; text: string; model?: string; streaming: boolean }
  | { kind: "thinking"; key: string; text: string; streaming: boolean }
  | {
      kind: "tool";
      key: string;
      id: string;
      name: string;
      args: string;
      status: ToolStatus;
      reason?: string;
      diff?: DiffLine[];
      output?: string;
    }
  | { kind: "turnEnd"; key: string; turn: number; files: number; hasHistory: boolean }
  | { kind: "notice"; key: string; text: string; id?: string }
  | { kind: "error"; key: string; text: string };

let counter = 0;
const key = () => `i${++counter}`;

const SYSTEM_NOTICE = "(system notice";
const BLOCKED = "Blocked by policy: ";
const DENIED = "The user denied this tool call.";
const CANCELLED = "Cancelled by the user.";

export function fromMessages(messages: Message[], turns: TurnInfo[] = []): Item[] {
  const items: Item[] = [];
  const startsTurn = new Map(turns.map((t, i) => [t.msgIndex, i]));
  const endTurn = (i: number) => {
    const t = turns[i];
    if (t) items.push({ kind: "turnEnd", key: key(), turn: t.turn, files: t.files, hasHistory: t.hasHistory });
  };
  let open = -1; // index into turns of the turn being read
  messages.forEach((m, idx) => {
    const starts = startsTurn.get(idx);
    if (starts !== undefined) {
      if (open >= 0) endTurn(open);
      open = starts;
    }
    pushMessage(items, m);
  });
  if (open >= 0) endTurn(open);
  return items;
}

function pushMessage(items: Item[], m: Message) {
  if (m.role === "user" && m.content != null) {
    if (m.content.startsWith(SYSTEM_NOTICE)) {
      items.push({ kind: "notice", key: key(), text: "reached the tool-call limit, asked for a final answer" });
    } else {
      items.push({ kind: "user", key: key(), text: m.content, images: m.images });
    }
  } else if (m.role === "assistant") {
    if (m.content) {
      items.push({ kind: "agent", key: key(), text: m.content, model: m.model, streaming: false });
    }
    for (const tc of m.tool_calls ?? []) {
      items.push({
        kind: "tool",
        key: key(),
        id: tc.id,
        name: tc.function.name,
        args: tc.function.arguments,
        status: "done",
      });
    }
  } else if (m.role === "tool") {
    const out = m.content ?? "";
    updateLastTool(items, m.tool_call_id ?? "", (t) => {
      if (out.startsWith(BLOCKED)) return { ...t, status: "blocked", reason: out.slice(BLOCKED.length) };
      if (out === DENIED) return { ...t, status: "denied" };
      if (out === CANCELLED) return { ...t, status: "cancelled" };
      return { ...t, status: "done", output: out };
    });
  }
}

function updateLastTool(
  items: Item[],
  id: string,
  fn: (t: Extract<Item, { kind: "tool" }>) => Item,
): Item[] {
  for (let i = items.length - 1; i >= 0; i--) {
    const it = items[i];
    if (it.kind === "tool" && it.id === id) {
      items[i] = fn(it);
      break;
    }
  }
  return items;
}

// Reasoning is shown while it streams, then kept collapsed. It isn't saved
// with the session, so it disappears on reload.
function endThinking(items: Item[]): Item[] {
  const last = items[items.length - 1];
  if (last?.kind === "thinking" && last.streaming) items[items.length - 1] = { ...last, streaming: false };
  return items;
}

/** Applies one run event to the transcript, returning a new array. */
export function applyEvent(prev: Item[], ev: AgentEvent): Item[] {
  let items = prev.slice();
  if (ev.type !== "reasoning") items = endThinking(items);
  const last = items[items.length - 1];
  switch (ev.type) {
    case "reasoning":
      if (last?.kind === "thinking" && last.streaming) {
        items[items.length - 1] = { ...last, text: last.text + (ev.text ?? "") };
      } else {
        items.push({ kind: "thinking", key: key(), text: ev.text ?? "", streaming: true });
      }
      break;
    case "tool_start":
      break; // shown as activity, not as an item
    case "changes":
      items.push({ kind: "turnEnd", key: key(), turn: ev.turn ?? 0, files: ev.files ?? 0, hasHistory: true });
      break;
    case "user":
      items.push({ kind: "user", key: key(), text: ev.text ?? "", images: ev.images });
      break;
    case "delta":
      if (last?.kind === "agent" && last.streaming) {
        items[items.length - 1] = { ...last, text: last.text + (ev.text ?? "") };
      } else {
        items.push({ kind: "agent", key: key(), text: ev.text ?? "", streaming: true });
      }
      break;
    case "assistant":
      if (last?.kind === "agent" && last.streaming) {
        if (ev.text) items[items.length - 1] = { ...last, text: ev.text, model: ev.model, streaming: false };
        else items.pop();
      } else if (ev.text) {
        items.push({ kind: "agent", key: key(), text: ev.text, model: ev.model, streaming: false });
      }
      break;
    case "tool_call":
      items.push({
        kind: "tool",
        key: key(),
        id: ev.id ?? "",
        name: ev.name ?? "",
        args: ev.args ?? "",
        reason: ev.reason,
        diff: ev.diff,
        status: ev.action === "deny" ? "blocked" : ev.action === "ask" ? "pending" : "running",
      });
      break;
    case "approval":
      return updateLastTool(items, ev.id ?? "", (t) => ({ ...t, status: ev.approved ? "running" : "denied" }));
    case "tool_result":
      return updateLastTool(items, ev.id ?? "", (t) => ({ ...t, status: "done", output: ev.output ?? "" }));
    case "notice":
      items.push({ kind: "notice", key: key(), text: ev.text ?? "", ...(ev.id ? { id: ev.id } : {}) });
      break;
    case "error":
      items.push({ kind: "error", key: key(), text: ev.text ?? "" });
      break;
    case "cancelled":
      items.push({ kind: "notice", key: key(), text: "cancelled" });
      break;
    case "done":
      // A run that ends mid-stream (error, cancel) leaves no half-finished
      // bubble behind, and no tool card stuck waiting.
      return items
        .filter((it) => !(it.kind === "agent" && it.streaming && it.text === ""))
        .map((it) => {
          if (it.kind === "agent" && it.streaming) return { ...it, streaming: false };
          if (it.kind === "tool" && (it.status === "pending" || it.status === "running"))
            return { ...it, status: "cancelled" as const };
          return it;
        });
  }
  return items;
}
