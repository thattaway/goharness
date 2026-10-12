// The status line under the transcript: usage numbers and the subagent.

import { useEffect, useState } from "react";
import { api, type Stats } from "./api";

export function humanTokens(n: number): string {
  if (n < 1000) return String(n);
  return `${(n / 1000).toFixed(1)}k`;
}

/** "63.0k in · 2.6k out · cache 79% · ctx 17.5k/35.4k", or null before the first call. */
export function usageText(s: Stats): string | null {
  if (s.turns === 0 && s.promptTokens === 0) return null;
  const parts = [`${humanTokens(s.promptTokens)} in`, `${humanTokens(s.completionTokens)} out`];
  if (s.promptTokens > 0) parts.push(`cache ${Math.round(s.lastCachePct)}%`);
  if (s.contextBudget > 0) parts.push(`ctx ${humanTokens(s.contextUsed)}/${humanTokens(s.contextBudget)}`);
  return parts.join(" · ");
}

/** What the subagent is doing: "subagent minicpm5_2B · running 4 calls · 38s", or idle with a run count. */
export function subagentText(s: Stats): string {
  const sub = s.subagent;
  if (sub.running && sub.count > 1) {
    return `${sub.count} subagents · running ${sub.calls} call${sub.calls === 1 ? "" : "s"} · ${sub.elapsedSeconds}s`;
  }
  if (sub.running) {
    return `subagent ${sub.runningModel || sub.model} · running ${sub.calls} call${sub.calls === 1 ? "" : "s"} · ${sub.elapsedSeconds}s`;
  }
  const runs = sub.runs > 0 ? ` · ${sub.runs} run${sub.runs === 1 ? "" : "s"} done` : "";
  return `subagent ${sub.model || "-"} · idle${runs}`;
}

/** Loads the session's stats now, when a run starts or ends, and every 2s while one is going. */
export function useStats(id: string | null, running: boolean): Stats | null {
  const [stats, setStats] = useState<Stats | null>(null);
  useEffect(() => {
    if (!id) return;
    let dead = false;
    const load = () =>
      api
        .stats(id)
        .then((s) => !dead && setStats(s))
        .catch(() => {});
    load();
    if (!running) return () => void (dead = true);
    const t = setInterval(load, 2000);
    return () => {
      dead = true;
      clearInterval(t);
    };
  }, [id, running]);
  return stats;
}
