import { useCallback, useEffect, useRef, useState } from "react";
import { api, ApiError, type AgentEvent, type Settings, type UndoResult } from "./api";
import { alertUser } from "./notify";
import { applyEvent, fromMessages, type Item } from "./transcript";

export interface SessionState {
  items: Item[];
  settings: Settings | null;
  running: boolean;
  loading: boolean;
  error: string | null;
  connected: boolean;
  projectRoot: string;
  canUndo: boolean;
  /** What the model is doing right now, when it isn't visible as text. */
  activity: string | null;
  /** When the model last sent anything, for "waiting 40s" hints. */
  lastEventAt: number;
}

const initial: SessionState = {
  items: [],
  settings: null,
  running: false,
  loading: true,
  error: null,
  connected: false,
  projectRoot: "",
  canUndo: false,
  activity: null,
  lastEventAt: 0,
};

function activityFor(ev: AgentEvent, prev: string | null): string | null {
  switch (ev.type) {
    case "tool_start":
      return `writing a ${ev.name} call`;
    case "reasoning":
      return "thinking";
    case "delta":
    case "assistant":
    case "tool_call":
    case "done":
      return null;
    case "approval":
    case "tool_result":
      return "waiting for the model";
  }
  return prev;
}

/**
 * Loads a session snapshot, then follows its event stream from exactly
 * where the snapshot ends. EventSource reconnects on its own with
 * Last-Event-ID; "resync" or "reset" from the server reloads the snapshot.
 */
export function useSession(
  id: string | null,
  onUnauthorized: () => void,
  onRunEnd: () => void,
  onDeleted: () => void,
) {
  const [state, setState] = useState<SessionState>(initial);
  const [reloadKey, setReloadKey] = useState(0);
  const onRunEndRef = useRef(onRunEnd);
  onRunEndRef.current = onRunEnd;
  const onDeletedRef = useRef(onDeleted);
  onDeletedRef.current = onDeleted;

  useEffect(() => {
    if (!id) return;
    let es: EventSource | null = null;
    let cancelled = false;
    setState((s) => (reloadKey > 0 ? { ...s, loading: true } : initial));

    api
      .session(id)
      .then((snap) => {
        if (cancelled) return;
        setState({
          items: fromMessages(snap.messages, snap.turns),
          settings: snap.settings,
          running: snap.running,
          loading: false,
          error: null,
          connected: false,
          projectRoot: snap.projectRoot,
          canUndo: snap.canUndo,
          activity: snap.running ? "waiting for the model" : null,
          lastEventAt: Date.now(),
        });
        es = new EventSource(api.eventsURL(id, snap.since));
        es.onopen = () => setState((s) => ({ ...s, connected: true }));
        es.onerror = () => setState((s) => ({ ...s, connected: false }));
        es.onmessage = (msg) => {
          const ev = JSON.parse(msg.data) as AgentEvent;
          if (ev.type === "deleted") {
            es?.close();
            onDeletedRef.current();
            return;
          }
          if (ev.type === "resync" || ev.type === "reset") {
            es?.close();
            setReloadKey((k) => k + 1);
            return;
          }
          setState((s) => ({
            ...s,
            items: applyEvent(s.items, ev),
            running: ev.type === "done" ? false : ev.type === "user" ? true : s.running,
            canUndo: ev.type === "done" ? true : ev.type === "user" ? false : s.canUndo,
            activity: activityFor(ev, s.activity),
            lastEventAt: Date.now(),
          }));
          if (ev.type === "tool_call" && ev.action === "ask") alertUser("Approval needed", `${ev.name}: ${ev.reason ?? ""}`);
          if (ev.type === "done") {
            alertUser("goharness finished", "The agent is done and waiting for you.", { onlyIfHidden: true });
            onRunEndRef.current();
          }
        };
      })
      .catch((err) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 401) onUnauthorized();
        setState((s) => ({ ...s, loading: false, error: String(err.message ?? err) }));
      });

    return () => {
      cancelled = true;
      es?.close();
    };
  }, [id, reloadKey, onUnauthorized]);

  const report = useCallback(
    (err: unknown) => {
      if (err instanceof ApiError && err.status === 401) onUnauthorized();
      setState((s) => ({ ...s, error: err instanceof Error ? err.message : String(err) }));
    },
    [onUnauthorized],
  );

  const send = useCallback(
    async (text: string, images?: string[]) => {
      if (!id) return false;
      try {
        setState((s) => ({ ...s, error: null, running: true, activity: "waiting for the model", lastEventAt: Date.now() }));
        await api.send(id, text, images);
        return true;
      } catch (err) {
        setState((s) => ({ ...s, running: false, activity: null }));
        report(err);
        return false;
      }
    },
    [id, report],
  );

  const cancel = useCallback(() => {
    if (id) api.cancel(id).catch(report);
  }, [id, report]);

  const approve = useCallback(
    (callId: string, ok: boolean) => {
      if (id) api.approve(id, callId, ok).catch(report);
    },
    [id, report],
  );

  const updateSettings = useCallback(
    async (next: Settings) => {
      if (!id) return;
      try {
        const saved = await api.settings(id, next);
        setState((s) => ({ ...s, settings: saved, error: null }));
      } catch (err) {
        report(err);
      }
    },
    [id, report],
  );

  const undo = useCallback(async (): Promise<UndoResult | null> => {
    if (!id) return null;
    try {
      setState((s) => ({ ...s, canUndo: false, error: null }));
      return await api.undo(id); // the server's "reset" event reloads the transcript
    } catch (err) {
      report(err);
      setReloadKey((k) => k + 1);
      return null;
    }
  }, [id, report]);

  const dismissError = useCallback(() => setState((s) => ({ ...s, error: null })), []);

  return { ...state, send, cancel, approve, updateSettings, undo, dismissError };
}
