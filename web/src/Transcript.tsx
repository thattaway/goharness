import { isValidElement, memo, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import type { DiffLine } from "./api";
import { TurnEnd } from "./ChangesView";
import { Icon, type IconName } from "./icons";
import type { Item, ToolStatus } from "./transcript";

type ToolItem = Extract<Item, { kind: "tool" }>;

const STATUS_LABEL: Record<ToolStatus, string> = {
  running: "running",
  pending: "needs approval",
  denied: "denied",
  blocked: "blocked",
  cancelled: "cancelled",
  done: "done",
};

// A short, readable line for the card header instead of raw JSON.
function summarize(name: string, rawArgs: string): string {
  let args: Record<string, unknown> = {};
  try {
    args = JSON.parse(rawArgs || "{}");
  } catch {
    return rawArgs;
  }
  const s = (k: string) => (typeof args[k] === "string" ? (args[k] as string) : "");
  switch (name) {
    case "bash":
      return s("command");
    case "read_file":
    case "write_file":
    case "str_replace":
      return s("path");
    case "tavily_search":
      return s("query");
    case "fetch_page":
      return s("url");
    case "skill":
      return s("name") || s("skill");
    case "spawn_agent":
      return s("task");
  }
  return rawArgs;
}

function prettyArgs(raw: string): string {
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}

function CodeBlock({ children }: { children?: ReactNode }) {
  const [copied, setCopied] = useState(false);
  const first = Array.isArray(children) ? children[0] : children;
  const props = (isValidElement(first) ? first.props : {}) as { className?: string; children?: ReactNode };
  const lang = /language-([\w-]+)/.exec(props.className ?? "")?.[1] ?? "";
  const text = typeof props.children === "string" ? props.children.replace(/\n$/, "") : "";
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1400);
    } catch {
      /* clipboard not available (plain http): the text can still be selected */
    }
  };
  return (
    <div className="codeblock">
      <div className="codeblock-head">
        <span>{lang || "text"}</span>
        <button type="button" className="codeblock-copy" onClick={copy} aria-label="Copy code">
          <Icon name={copied ? "check" : "copy"} size={13} />
          {copied ? "Copied" : "Copy"}
        </button>
      </div>
      <pre>{children}</pre>
    </div>
  );
}

const Markdownish = memo(function Markdownish({ text }: { text: string }) {
  return (
    <div className="md">
      <Markdown
        remarkPlugins={[remarkGfm]}
        components={{
          a: ({ node: _node, ...props }) => <a {...props} target="_blank" rel="noreferrer noopener" />,
          pre: ({ node: _node, children }) => <CodeBlock>{children}</CodeBlock>,
        }}
      >
        {text}
      </Markdown>
    </div>
  );
});

const DIFF_PREVIEW = 60;

function DiffView({ lines }: { lines: DiffLine[] }) {
  const [all, setAll] = useState(false);
  const shown = all ? lines : lines.slice(0, DIFF_PREVIEW);
  return (
    <div className="diff" role="table" aria-label="Proposed change">
      {shown.map((l, i) => (
        <div key={i} className={`diff-line diff-${l.op === "+" ? "add" : l.op === "-" ? "del" : "ctx"}`} role="row">
          <span className="diff-op" aria-hidden>
            {l.op}
          </span>
          <span className="diff-text">{l.text || " "}</span>
        </div>
      ))}
      {lines.length > DIFF_PREVIEW && (
        <button className="link-btn" onClick={() => setAll(!all)}>
          {all ? "Show less" : `Show all ${lines.length} lines`}
        </button>
      )}
    </div>
  );
}

const OUTPUT_PREVIEW_LINES = 12;

function Output({ text }: { text: string }) {
  const lines = text.split("\n");
  const long = lines.length > OUTPUT_PREVIEW_LINES;
  const [open, setOpen] = useState(false);
  return (
    <div className="tool-output">
      <pre>{long && !open ? lines.slice(0, OUTPUT_PREVIEW_LINES).join("\n") : text}</pre>
      {long && (
        <button className="link-btn" onClick={() => setOpen(!open)}>
          {open ? "Show less" : `Show all ${lines.length} lines`}
        </button>
      )}
    </div>
  );
}

const TOOL_ICON: Record<string, IconName> = {
  bash: "terminal",
  read_file: "file",
  write_file: "pencil",
  str_replace: "pencil",
  tavily_search: "search",
  fetch_page: "search",
  skill: "zap",
  spawn_agent: "branch",
};

/** The one-line steps a subagent reported ("subagent: bash {...}") while a spawn_agent call was running. */
function SubagentSteps({ steps }: { steps: string[] }) {
  return (
    <div className="steps" aria-label="Subagent steps">
      {steps.map((st, i) => {
        const space = st.indexOf(" ");
        const name = space < 0 ? st : st.slice(0, space);
        const text = space < 0 ? "" : summarize(name, st.slice(space + 1));
        return (
          <div key={i} className="step">
            <Icon name={TOOL_ICON[name] ?? "zap"} size={13} />
            <code className="step-text" title={text}>
              {name === "bash" || !text ? text || name : text}
            </code>
          </div>
        );
      })}
    </div>
  );
}

function ToolCard({ item, steps, onApprove }: { item: ToolItem; steps?: string[]; onApprove: (id: string, ok: boolean) => void }) {
  const pending = item.status === "pending";
  // Rows stay one line until you open them, except the ones that need you: an approval, a refusal, a proposed edit.
  const attention = pending || item.status === "denied" || item.status === "blocked" || (item.diff?.length ?? 0) > 0;
  const [open, setOpen] = useState(attention);
  useEffect(() => {
    if (attention) setOpen(true);
  }, [attention]);
  const [showArgs, setShowArgs] = useState(false);
  const summary = summarize(item.name, item.args);
  return (
    <details className={`tool tool-${item.status}`} open={open} onToggle={(e) => setOpen((e.currentTarget as HTMLDetailsElement).open)}>
      <summary className="tool-head">
        <Icon name={TOOL_ICON[item.name] ?? "zap"} size={15} className="tool-icon" />
        <span className="tool-name">{item.name}</span>
        <code className="tool-summary" title={summary}>
          {summary}
        </code>
        <span className={`chip chip-${item.status}`}>
          {item.status === "running" && <span className="spinner" aria-hidden />}
          {item.status === "done" && <Icon name="check" size={13} />}
          {STATUS_LABEL[item.status]}
        </span>
      </summary>
      <div className="tool-body">
        {steps && steps.length > 0 && <SubagentSteps steps={steps} />}
        {item.reason && (item.status === "blocked" || pending) && <div className="tool-reason">{item.reason}</div>}
        {item.diff && item.diff.length > 0 && <DiffView lines={item.diff} />}
        {pending && (
          <div className="approval">
            <button className="btn btn-primary" onClick={() => onApprove(item.id, true)}>
              Approve
            </button>
            <button className="btn" onClick={() => onApprove(item.id, false)}>
              Deny
            </button>
            <span className="hint">or press Y / N</span>
          </div>
        )}
        {item.output !== undefined && <Output text={item.output} />}
        <button className="link-btn args-toggle" onClick={() => setShowArgs(!showArgs)}>
          {showArgs ? "Hide arguments" : "Arguments"}
        </button>
        {showArgs && <pre className="tool-args">{prettyArgs(item.args)}</pre>}
      </div>
    </details>
  );
}

type Row = { item: Item; steps?: string[] };

/**
 * Gathers a subagent's step notices into its spawn_agent row. Notices carry the id of the call they belong to,
 * because several subagents can run at once and their steps interleave; a notice with no id (an older log)
 * attaches to the spawn_agent row just before it.
 */
function withSteps(items: Item[]): Row[] {
  const spawnIds = new Set<string>();
  for (const it of items) if (it.kind === "tool" && it.name === "spawn_agent") spawnIds.add(it.id);
  const isStep = (n: Item): n is Extract<Item, { kind: "notice" }> => n.kind === "notice" && n.text.startsWith("subagent: ");
  const byId = new Map<string, string[]>();
  for (const n of items) {
    if (isStep(n) && n.id && spawnIds.has(n.id)) {
      const list = byId.get(n.id) ?? [];
      list.push(n.text.slice("subagent: ".length));
      byId.set(n.id, list);
    }
  }
  const rows: Row[] = [];
  for (let i = 0; i < items.length; i++) {
    const it = items[i];
    if (isStep(it) && it.id && spawnIds.has(it.id)) continue; // shown in its own row
    if (it.kind === "tool" && it.name === "spawn_agent") {
      const steps = [...(byId.get(it.id) ?? [])];
      let j = i + 1;
      while (j < items.length) {
        const n = items[j];
        if (!isStep(n)) break;
        if (!n.id) steps.push(n.text.slice("subagent: ".length));
        j++;
      }
      rows.push({ item: it, steps });
      // the notices skipped above that had no id were taken here; ones with an id stay hidden either way
      i = j - 1;
    } else {
      rows.push({ item: it });
    }
  }
  return rows;
}

function Thinking({ text, streaming }: { text: string; streaming: boolean }) {
  const body = useRef<HTMLPreElement>(null);
  useLayoutEffect(() => {
    if (streaming && body.current) body.current.scrollTop = body.current.scrollHeight;
  }, [text, streaming]);
  return (
    <details className="thinking-block" open={streaming || undefined}>
      <summary>
        {streaming && <span className="spinner" aria-hidden />}
        {streaming ? "Thinking…" : "Thought"}
        <span className="hint"> · {text.length.toLocaleString()} chars</span>
      </summary>
      <pre ref={body}>{text.trim()}</pre>
    </details>
  );
}

// Seconds since the model last sent anything, ticking while a run is live.
function useQuietSeconds(active: boolean, since: number) {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    if (!active) return;
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, [active]);
  return active ? Math.max(0, Math.floor((now - since) / 1000)) : 0;
}

function formatSeconds(s: number) {
  return s < 60 ? `${s}s` : `${Math.floor(s / 60)}m ${s % 60}s`;
}

export function Transcript({
  items,
  running,
  activity,
  lastEventAt,
  onApprove,
  onReviewChanges,
}: {
  items: Item[];
  running: boolean;
  activity: string | null;
  lastEventAt: number;
  onApprove: (id: string, ok: boolean) => void;
  onReviewChanges: (turn: number) => void;
}) {
  const scroller = useRef<HTMLDivElement>(null);
  const stick = useRef(true);

  // Follow new output only while the reader is at the bottom.
  const onScroll = () => {
    const el = scroller.current;
    if (el) stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
  };
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
  }, [items]);

  // Y / N answer the oldest pending approval when not typing.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement;
      if (t.closest("textarea, input, select") || e.metaKey || e.ctrlKey || e.altKey) return;
      const k = e.key.toLowerCase();
      if (k !== "y" && k !== "n") return;
      const pending = items.find((i): i is ToolItem => i.kind === "tool" && i.status === "pending");
      if (pending) {
        e.preventDefault();
        onApprove(pending.id, k === "y");
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [items, onApprove]);

  const last = items[items.length - 1];
  const awaitingApproval = items.some((i) => i.kind === "tool" && i.status === "pending");
  const visiblyStreaming = (last?.kind === "agent" || last?.kind === "thinking") && last.streaming;
  const showActivity = running && !awaitingApproval && !visiblyStreaming;
  const quiet = useQuietSeconds(running, lastEventAt);

  return (
    <div className="transcript" ref={scroller} onScroll={onScroll}>
      <div className="transcript-inner">
        {items.length === 0 && (
          <div className="empty">
            <p>Ask for a change, a review, or a search.</p>
            <p className="hint">The agent can read and edit files in the project, run shell commands, and search the web.</p>
          </div>
        )}
        {withSteps(items).map(({ item: it, steps }) => {
          switch (it.kind) {
            case "user":
              return (
                <div key={it.key} className="msg msg-user">
                  {it.images && it.images.length > 0 && (
                    <div className="attachments">
                      {it.images.map((src, i) => (
                        <img key={i} src={src} alt="attached" />
                      ))}
                    </div>
                  )}
                  {it.text && <div className="bubble">{it.text}</div>}
                </div>
              );
            case "agent":
              return (
                <div key={it.key} className="msg msg-agent">
                  <Markdownish text={it.text} />
                  {it.streaming ? <span className="caret" aria-hidden /> : it.model && <div className="model-tag">{it.model}</div>}
                </div>
              );
            case "turnEnd":
              return <TurnEnd key={it.key} files={it.files} hasHistory={it.hasHistory} onReview={() => onReviewChanges(it.turn)} />;
            case "thinking":
              return <Thinking key={it.key} text={it.text} streaming={it.streaming} />;
            case "tool":
              return <ToolCard key={it.key} item={it} steps={steps} onApprove={onApprove} />;
            case "notice":
              return (
                <div key={it.key} className="notice">
                  {it.text}
                </div>
              );
            case "error":
              return (
                <div key={it.key} className="notice notice-error" role="alert">
                  {it.text}
                </div>
              );
          }
        })}
        {showActivity && (
          <div className="thinking" aria-live="polite">
            <span className="spinner" aria-hidden /> {activity ?? "working"}…
            {quiet >= 10 && (
              <span className="hint">
                {" "}
                no output for {formatSeconds(quiet)}
                {quiet >= 60 && " (the model may be loading or busy; Stop cancels)"}
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
