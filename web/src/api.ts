// Typed client for the goharness web API (web.go).

export interface ToolCall {
  id: string;
  type: string;
  function: { name: string; arguments: string };
}

export interface Message {
  role: "system" | "user" | "assistant" | "tool";
  content: string | null;
  tool_calls?: ToolCall[];
  tool_call_id?: string;
  model?: string;
  images?: string[]; // data: URLs attached to a user message
}

export interface Settings {
  model: string;
  // Which backend this session talks to: "" or "llama-swap" (the default)
  // uses this server's own configured base URL with no auth; any other
  // name is one of GET /api/providers, resolved server-side (see providers.go).
  provider: string;
  plan: boolean;
  tdd: boolean;
  adhd: boolean;
  icm: boolean;
  auto: boolean;
  maxRounds: number;
  /** Model spawn_agent subagents use in this session; empty means the server's default. */
  subagentModel?: string;
}

/** The session's usage telemetry and subagent state (GET /api/sessions/{id}/stats). */
export interface Stats {
  turns: number;
  toolCalls: number;
  promptTokens: number;
  completionTokens: number;
  lastCachePct: number;
  sessionCachePct: number;
  avgTps: number;
  avgTtftSeconds: number;
  endpoint: string;
  lastTps: number;
  contextUsed: number;
  contextBudget: number;
  subagent: {
    model: string;
    running: boolean;
    count: number;
    runningModel: string;
    calls: number;
    elapsedSeconds: number;
    runs: number;
  };
}

export interface CompactResult {
  messagesBefore: number;
  messagesAfter: number;
  tokensBefore: number;
  tokensAfter: number;
}

export interface DiffLine {
  op: "+" | "-" | " ";
  text: string;
}

export interface AgentEvent {
  seq?: number;
  type:
    | "user"
    | "delta"
    | "reasoning"
    | "tool_start"
    | "assistant"
    | "tool_call"
    | "approval"
    | "tool_result"
    | "notice"
    | "error"
    | "cancelled"
    | "done"
    | "resync"
    | "reset"
    | "deleted"
    | "changes";
  text?: string;
  model?: string;
  images?: string[]; // "user" events only: data URLs attached to the message
  id?: string;
  name?: string;
  args?: string;
  action?: "allow" | "ask" | "deny";
  reason?: string;
  diff?: DiffLine[];
  output?: string;
  approved?: boolean;
  turn?: number;
  files?: number;
}

export interface SessionSnapshot {
  id: string;
  messages: Message[];
  settings: Settings;
  running: boolean;
  since: number;
  projectRoot: string;
  canUndo: boolean;
  turns: TurnInfo[];
}

export interface TurnInfo {
  turn: number;
  msgIndex: number;
  files: number;
  hasHistory: boolean;
}

export interface FileChange {
  path: string;
  oldPath?: string;
  status: "added" | "modified" | "deleted" | "renamed";
  added: number;
  removed: number;
  binary?: boolean;
  truncated?: boolean;
  diff: { op: "+" | "-" | " " | "@"; text: string }[];
}

export interface SessionSummary {
  id: string;
  title: string;
  project: string;
  updated: string;
  running: boolean;
  pinned: boolean;
  /** A user-made group's id, or "p:<project>" for another project's group; absent = its own project's group. */
  group?: string;
  match?: string;
}

/** A group the user made in the sidebar. */
export interface ConvGroup {
  id: string;
  name: string;
}

export interface Skill {
  name: string;
  description: string;
  dir: string;
}

export interface DeleteResult {
  deleted: string[];
  skipped: Record<string, string>;
}

export interface Info {
  projectRoot: string;
  defaultModel: string;
  allowAnyDir: boolean;
  stt?: boolean; // a speech-to-text server is configured: show the microphone
}

export interface DirListing {
  path: string;
  parent: string;
  entries: Project[];
  allowed: boolean;
}

export interface Project {
  path: string;
  name: string;
}

export type RuleAction = "allow" | "ask" | "deny";

export interface Rule {
  pattern: string;
  action: RuleAction;
}

export interface ProviderInfo {
  id: string;
  label: string;
  baseUrl: string;
  models?: string[];
  custom: boolean;
  hasApiKey: boolean;
}

export interface Permissions {
  defaults: Rule[];
  rules: Rule[];
  path: string;
}

export interface UndoResult {
  text: string;
  files: string[] | null;
  filesRestored: boolean;
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
  ) {
    super(message);
  }
}

async function call<T>(
  method: string,
  path: string,
  body?: unknown,
): Promise<T> {
  const res = await fetch(path, {
    method,
    credentials: "same-origin",
    headers:
      body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = (await res.json()).error ?? msg;
    } catch {
      /* not JSON */
    }
    throw new ApiError(res.status, msg);
  }
  if (res.status === 204 || res.status === 202) return undefined as T;
  return res.json() as Promise<T>;
}

/** Sends a recording to the server's speech-to-text endpoint and returns the text. */
async function transcribe(audio: Blob, filename: string): Promise<string> {
  const form = new FormData();
  form.append("file", audio, filename);
  const res = await fetch("/api/transcribe", {
    method: "POST",
    credentials: "same-origin",
    body: form,
  });
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = (await res.json()).error ?? msg;
    } catch {
      /* not JSON */
    }
    throw new ApiError(res.status, msg);
  }
  return ((await res.json()) as { text: string }).text ?? "";
}

export const api = {
  transcribe,
  info: () => call<Info>("GET", "/api/info"),
  login: (token: string) => call<void>("POST", "/api/login", { token }),
  logout: () => call<void>("POST", "/api/logout"),
  reload: () => call<{ summary: string }>("POST", "/api/reload"),
  models: (provider?: string) =>
    call<string[]>(
      "GET",
      `/api/models${provider ? `?provider=${encodeURIComponent(provider)}` : ""}`,
    ),
  sessions: (q?: string) =>
    call<SessionSummary[]>(
      "GET",
      `/api/sessions${q ? `?q=${encodeURIComponent(q)}` : ""}`,
    ),
  skills: (id: string) =>
    call<Skill[]>("GET", `/api/sessions/${encodeURIComponent(id)}/skills`),
  turnChanges: (id: string, turn: number) =>
    call<{ turn: number; files: FileChange[] }>(
      "GET",
      `/api/sessions/${encodeURIComponent(id)}/turns/${turn}/changes`,
    ),
  createSession: (project?: string) =>
    call<SessionSnapshot>(
      "POST",
      "/api/sessions",
      project ? { project } : undefined,
    ),
  projects: () => call<Project[]>("GET", "/api/projects"),
  patchSession: (
    id: string,
    patch: { title?: string; pinned?: boolean; group?: string },
  ) =>
    call<{ title: string; pinned: boolean; group: string }>(
      "PATCH",
      `/api/sessions/${encodeURIComponent(id)}`,
      patch,
    ),
  groups: () => call<ConvGroup[]>("GET", "/api/groups"),
  createGroup: (name: string) =>
    call<ConvGroup>("POST", "/api/groups", { name }),
  renameGroup: (id: string, name: string) =>
    call<ConvGroup>("PATCH", `/api/groups/${encodeURIComponent(id)}`, { name }),
  deleteGroup: (id: string) =>
    call<void>("DELETE", `/api/groups/${encodeURIComponent(id)}`),
  deleteSessions: (ids: string[]) =>
    call<DeleteResult>("POST", "/api/sessions/delete", { ids }),
  dirs: (path?: string) =>
    call<DirListing>(
      "GET",
      `/api/dirs${path ? `?path=${encodeURIComponent(path)}` : ""}`,
    ),
  stats: (id: string) =>
    call<Stats>("GET", `/api/sessions/${encodeURIComponent(id)}/stats`),
  compact: (id: string) =>
    call<CompactResult>(
      "POST",
      `/api/sessions/${encodeURIComponent(id)}/compact`,
    ),
  undo: (id: string) =>
    call<UndoResult>("POST", `/api/sessions/${encodeURIComponent(id)}/undo`),
  providers: () => call<ProviderInfo[]>("GET", "/api/providers"),
  createProvider: (p: {
    name: string;
    baseUrl: string;
    model: string;
    apiKey?: string;
    apiKeyEnv?: string;
  }) => call<{ id: string }>("POST", "/api/providers", p),
  providerModels: (p: { baseUrl: string; apiKey?: string; apiKeyEnv?: string }) =>
    call<{ models: string[] }>("POST", "/api/providers/models", p),
  deleteProvider: (name: string) =>
    call<void>("DELETE", `/api/providers/${encodeURIComponent(name)}`),
  permissions: () => call<Permissions>("GET", "/api/permissions"),
  savePermissions: (rules: Rule[]) =>
    call<{ rules: Rule[] }>("PUT", "/api/permissions", { rules }),
  checkCommand: (command: string) =>
    call<{ action: RuleAction; reason: string }>(
      "POST",
      "/api/permissions/check",
      { command },
    ),
  session: (id: string) =>
    call<SessionSnapshot>("GET", `/api/sessions/${encodeURIComponent(id)}`),
  send: (id: string, text: string, images?: string[]) =>
    call<void>("POST", `/api/sessions/${encodeURIComponent(id)}/messages`, {
      text,
      images,
    }),
  cancel: (id: string) =>
    call<void>("POST", `/api/sessions/${encodeURIComponent(id)}/cancel`),
  approve: (id: string, callId: string, approve: boolean) =>
    call<void>(
      "POST",
      `/api/sessions/${encodeURIComponent(id)}/approvals/${encodeURIComponent(callId)}`,
      { approve },
    ),
  settings: (id: string, s: Settings) =>
    call<Settings>(
      "PUT",
      `/api/sessions/${encodeURIComponent(id)}/settings`,
      s,
    ),
  eventsURL: (id: string, since: number) =>
    `/api/sessions/${encodeURIComponent(id)}/events?since=${since}`,
};
