import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ChangeEvent,
  type ClipboardEvent,
  type FormEvent,
  type KeyboardEvent,
} from "react";
import {
  api,
  ApiError,
  type ConvGroup,
  type Info,
  type Project,
  type SessionSummary,
  type Settings,
  type Skill,
  type Stats,
} from "./api";
import { PROJECT_PREFIX } from "./groups";
import { Icon } from "./icons";
import { useTheme, type Theme } from "./theme";
import {
  helpText,
  isToggle,
  matchCommands,
  parseProviderCommand,
  type Command,
  type CommandName,
  type ProviderCommand,
} from "./commands";
import { ChangesModal } from "./ChangesView";
import {
  canRecord,
  insecureMicMessage,
  joinDictation,
  pickMimeType,
  recordingName,
} from "./dictation";
import { FolderPicker, rememberDir } from "./FolderPicker";
import { ProviderDialog } from "./ProviderDialog";
import { SettingsPanel } from "./SettingsPanel";
import { humanTokens, subagentText, useStats, usageText } from "./stats";
import { Sidebar } from "./Sidebar";
import { TerminalPanel } from "./Terminal";
import { Transcript } from "./Transcript";
import { useSession } from "./useSession";

// ---- routing: /s/<session id> ----

function idFromPath(): string | null {
  const m = location.pathname.match(/^\/s\/([^/]+)$/);
  return m ? decodeURIComponent(m[1]) : null;
}

function useRoute(): [string | null, (id: string, replace?: boolean) => void] {
  const [id, setId] = useState(idFromPath);
  useEffect(() => {
    const onPop = () => setId(idFromPath());
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);
  const go = useCallback((next: string, replace = false) => {
    const url = `/s/${encodeURIComponent(next)}`;
    if (replace) history.replaceState(null, "", url);
    else history.pushState(null, "", url);
    setId(next);
  }, []);
  return [id, go];
}

// ---- login ----

function Login({ onDone }: { onDone: () => void }) {
  const [token, setToken] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await api.login(token.trim());
      onDone();
    } catch (err) {
      setError(
        err instanceof ApiError && err.status === 401
          ? "That token isn't right."
          : String(err),
      );
    } finally {
      setBusy(false);
    }
  };
  return (
    <div className="login">
      <form className="login-card" onSubmit={submit}>
        <div className="brand">
          <span className="logo" aria-hidden>
            ◆
          </span>{" "}
          goharness
        </div>
        <p>
          Open the link that <code>goharness web</code> printed, or paste the
          access token. It is also in <code>~/.config/goharness/web_token</code>
          .
        </p>
        <label htmlFor="token">Access token</label>
        <input
          id="token"
          type="password"
          autoComplete="current-password"
          value={token}
          onChange={(e) => setToken(e.target.value)}
          autoFocus
        />
        {error && <div className="form-error">{error}</div>}
        <button className="btn btn-primary" disabled={busy || !token.trim()}>
          {busy ? "Checking…" : "Log in"}
        </button>
      </form>
    </div>
  );
}

// ---- top bar ----

// PROVIDERS falls back to these two when /api/providers is unreachable;
// otherwise it is populated from GET /api/providers. defaultModel is what
// a provider switch sets the model to, matching the TUI's /provider.
const PROVIDERS_FALLBACK: {
  id: string;
  label: string;
  defaultModel: string | null;
}[] = [
  { id: "llama-swap", label: "llama-swap", defaultModel: null },
  { id: "freellmapi", label: "freellmapi", defaultModel: "auto" },
];

const TOGGLES: {
  key: "plan" | "auto" | "tdd" | "adhd" | "icm";
  label: string;
  title: string;
}[] = [
  {
    key: "plan",
    label: "Plan",
    title: "Read-only: the agent explores and proposes a plan, no file edits",
  },
  {
    key: "auto",
    label: "Auto",
    title: "Run commands that normally need approval without asking",
  },
  { key: "tdd", label: "TDD", title: "Ask for test-first red-green cycles" },
  { key: "adhd", label: "ADHD", title: "Short, action-first replies" },
  {
    key: "icm",
    label: "ICM",
    title:
      "Staged project: work one stages/NN_name/ folder at a time, stop for review between stages",
  },
];

/** The last item in the /provider list: opens the "Add a provider" form instead of switching. */
const ADD_PROVIDER = "__add_provider__";

function provDefaultModel(id: string): string | null {
  if (id === "freellmapi") return "auto";
  return null;
}

function useProviders(): {
  providers: { id: string; label: string; defaultModel: string | null }[];
} {
  const [providers, setProviders] =
    useState<{ id: string; label: string; defaultModel: string | null }[]>(
      PROVIDERS_FALLBACK,
    );
  const [reloadTick, setReloadTick] = useState(0);
  useEffect(() => {
    const again = () => setReloadTick((n) => n + 1);
    window.addEventListener("goharness-providers", again);
    return () => window.removeEventListener("goharness-providers", again);
  }, []);
  useEffect(() => {
    let cancelled = false;
    api
      .providers()
      .then((list) => {
        if (cancelled) return;
        if (list.length > 0)
          setProviders(
            list.map((p) => ({
              id: p.id,
              label: p.label,
              defaultModel: provDefaultModel(p.id),
            })),
          );
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [reloadTick]);
  return { providers };
}

function usePopover() {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: globalThis.KeyboardEvent) =>
      e.key === "Escape" && setOpen(false);
    window.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [open]);
  return { open, setOpen, ref };
}

/** Provider and model in one pill; opens a searchable list. */
function ModelPill({
  settings,
  models,
  running,
  onChange,
}: {
  settings: Settings;
  models: string[];
  running: boolean;
  onChange: (s: Settings) => void;
}) {
  const pop = usePopover();
  const [filter, setFilter] = useState("");
  const { providers } = useProviders();
  const current = settings.provider || "llama-swap";
  const all = models.includes(settings.model)
    ? models
    : [settings.model, ...models];
  const list = all.filter((m) =>
    m.toLowerCase().includes(filter.trim().toLowerCase()),
  );
  return (
    <div className="pop-wrap" ref={pop.ref}>
      <button
        className="pill"
        aria-haspopup="dialog"
        aria-expanded={pop.open}
        title="Provider and model for this session"
        onClick={() => pop.setOpen(!pop.open)}
      >
        <Icon name="layers" size={15} />
        <span className="pill-text">
          <span className="pill-dim">{current} ·</span> {settings.model}
        </span>
        <Icon name="chevron" size={14} className="pill-chev" />
      </button>
      {pop.open && (
        <div className="popover" role="dialog" aria-label="Choose a model">
          <div className="seg" role="group" aria-label="Provider">
            {providers.map(
              (p: {
                id: string;
                label: string;
                defaultModel: string | null;
              }) => (
                <button
                  key={p.id}
                  className={current === p.id ? "on" : ""}
                  aria-pressed={current === p.id}
                  disabled={running}
                  onClick={() =>
                    onChange({
                      ...settings,
                      provider: p.id,
                      model: p.defaultModel ?? settings.model,
                    })
                  }
                >
                  {p.label}
                </button>
              ),
            )}
          </div>
          <input
            className="pop-search"
            placeholder="Search models…"
            aria-label="Search models"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            autoFocus
          />
          <div className="pop-list" role="listbox" aria-label="Models">
            {list.map((m) => (
              <button
                key={m}
                role="option"
                aria-selected={m === settings.model}
                className={`opt ${m === settings.model ? "on" : ""}`}
                disabled={running}
                onClick={() => {
                  onChange({ ...settings, model: m });
                  pop.setOpen(false);
                }}
              >
                <span className="opt-name">{m}</span>
                <Icon name="check" size={15} className="opt-check" />
              </button>
            ))}
            {list.length === 0 && (
              <div className="pop-empty">No models match.</div>
            )}
          </div>
          {running && (
            <div className="pop-hint">
              Wait for the current run to finish before switching.
            </div>
          )}
        </div>
      )}
    </div>
  );
}

/** The five modes as one menu with checkmarks, and a count of how many are on. */
function ModesMenu({
  settings,
  running,
  onChange,
}: {
  settings: Settings;
  running: boolean;
  onChange: (s: Settings) => void;
}) {
  const pop = usePopover();
  const on = TOGGLES.filter((t) => settings[t.key]).length;
  return (
    <div className="pop-wrap" ref={pop.ref}>
      <button
        className="pill"
        aria-haspopup="menu"
        aria-expanded={pop.open}
        title="Plan, auto-approve, TDD, ADHD, staged project"
        onClick={() => pop.setOpen(!pop.open)}
      >
        <Icon name="zap" size={15} />
        Modes
        {on > 0 && <span className="badge">{on}</span>}
      </button>
      {pop.open && (
        <div className="popover popover-right" role="menu" aria-label="Modes">
          {TOGGLES.map((t) => (
            <button
              key={t.key}
              role="menuitemcheckbox"
              aria-checked={settings[t.key]}
              className={`opt opt-mode ${settings[t.key] ? "on" : ""} ${t.key === "auto" && settings.auto ? "warn" : ""}`}
              disabled={running}
              onClick={() =>
                onChange({ ...settings, [t.key]: !settings[t.key] })
              }
            >
              <span className="mode-box">
                <Icon name="check" size={13} />
              </span>
              <span className="opt-name">
                {t.label}
                <span className="opt-sub">{t.title}</span>
              </span>
            </button>
          ))}
          {running && (
            <div className="pop-hint">
              Wait for the current run to finish before changing modes.
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function TopBar({
  projectRoot,
  settings,
  models,
  running,
  onChange,
  onMenu,
  sidebarHidden,
  onSettings,
  onToggleTerminal,
  terminalOpen,
  connected,
  theme,
  onToggleTheme,
}: {
  projectRoot: string;
  settings: Settings | null;
  models: string[];
  running: boolean;
  onChange: (s: Settings) => void;
  onMenu: () => void;
  sidebarHidden: boolean;
  onSettings: () => void;
  onToggleTerminal: () => void;
  terminalOpen: boolean;
  connected: boolean;
  theme: Theme;
  onToggleTheme: () => void;
}) {
  return (
    <header className="topbar">
      <button
        className={`icon-btn menu-btn ${sidebarHidden ? "" : "menu-btn-open"}`}
        onClick={onMenu}
        aria-label={sidebarHidden ? "Show sidebar" : "Hide sidebar"}
        title={sidebarHidden ? "Show sidebar" : "Hide sidebar"}
      >
        <Icon name="menu" size={18} />
      </button>
      <div className="project" title={projectRoot}>
        {projectRoot.split("/").filter(Boolean).pop() ?? ""}
        {!connected && (
          <span className="offline" title="Reconnecting to the server">
            offline
          </span>
        )}
      </div>
      {settings && (
        <div className="controls">
          <ModelPill
            settings={settings}
            models={models}
            running={running}
            onChange={onChange}
          />
          <ModesMenu
            settings={settings}
            running={running}
            onChange={onChange}
          />
        </div>
      )}
      <div className="topbar-icons">
        <button
          className={`icon-btn ${terminalOpen ? "icon-btn-on" : ""}`}
          onClick={onToggleTerminal}
          aria-label="Terminal"
          aria-pressed={terminalOpen}
          title="Real shell in this project's folder"
        >
          <Icon name="terminal" size={18} />
        </button>
        <button
          className="icon-btn"
          onClick={onToggleTheme}
          aria-label={
            theme === "dark"
              ? "Switch to the light theme"
              : "Switch to the dark theme"
          }
          title={theme === "dark" ? "Light theme" : "Dark theme"}
        >
          <Icon name={theme === "dark" ? "sun" : "moon"} size={18} />
        </button>
        <button
          className="icon-btn"
          onClick={onSettings}
          aria-label="Settings"
          title="Settings"
        >
          <Icon name="cog" size={18} />
        </button>
      </div>
    </header>
  );
}

// ---- composer ----

// maxComposerImageBytes matches goharness's own server-side limit (images.go)
// so a too-large file is rejected here instead of after a slow upload.
const maxComposerImageBytes = 8 * 1024 * 1024;

interface PendingImage {
  url: string; // data: URL
  name: string;
}

function readAsDataURL(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(reader.result as string);
    reader.onerror = () =>
      reject(reader.error ?? new Error("could not read file"));
    reader.readAsDataURL(file);
  });
}

/** A list to choose from for /model, /provider and /subagent. current is the value now in use. */
interface PickerSpec {
  title: string;
  current: string;
  items: { label: string; value: string }[];
}

type Suggestion =
  | { kind: "command"; cmd: Command }
  | { kind: "skill"; skill: Skill };

function StatusLine({ stats }: { stats: Stats }) {
  const usage = usageText(stats);
  return (
    <div
      className="status-line"
      role="status"
      aria-label="Session usage and subagent"
    >
      {usage && <span>{usage}</span>}
      {stats.endpoint && <span>{stats.endpoint}</span>}
      <span
        className={stats.subagent.running ? "status-sub running" : "status-sub"}
      >
        {subagentText(stats)}
      </span>
    </div>
  );
}

function Composer({
  text,
  setText,
  running,
  disabled,
  canUndo,
  skills,
  stt,
  onSend,
  onCancel,
  onUndo,
  onCommand,
  onProviderCommand,
  getPicker,
  settings,
  stats,
}: {
  settings: Settings | null;
  stats: Stats | null;
  onCommand: (name: CommandName, value?: string) => void;
  onProviderCommand: (c: ProviderCommand) => void;
  getPicker: (name: CommandName) => PickerSpec | null;
  skills: Skill[];
  stt: boolean;
  text: string;
  setText: (t: string) => void;
  running: boolean;
  disabled: boolean;
  canUndo: boolean;
  onSend: (text: string, images?: string[]) => Promise<boolean>;
  onCancel: () => void;
  onUndo: () => void;
}) {
  const ref = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const [images, setImages] = useState<PendingImage[]>([]);
  const [attachError, setAttachError] = useState<string | null>(null);

  // Dictation: record, send to the server's speech-to-text, add the words to the box.
  const audioInputRef = useRef<HTMLInputElement>(null);
  const recorderRef = useRef<MediaRecorder | null>(null);
  const [dictation, setDictation] = useState<"idle" | "recording" | "working">(
    "idle",
  );
  const textRef = useRef(text);
  textRef.current = text;
  const transcribeAndAdd = useCallback(
    async (audio: Blob, name: string) => {
      setDictation("working");
      try {
        const spoken = await api.transcribe(audio, name);
        if (spoken.trim()) setText(joinDictation(textRef.current, spoken));
        else setAttachError("didn't hear anything");
      } catch (err) {
        setAttachError(
          `dictation failed: ${err instanceof Error ? err.message : String(err)}`,
        );
      } finally {
        setDictation("idle");
        ref.current?.focus();
      }
    },
    [setText],
  );
  const toggleDictation = async () => {
    setAttachError(null);
    if (dictation === "recording") {
      recorderRef.current?.stop();
      return;
    }
    if (dictation !== "idle") return;
    if (!canRecord()) {
      setAttachError(insecureMicMessage);
      audioInputRef.current?.click();
      return;
    }
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const mime = pickMimeType((t) => MediaRecorder.isTypeSupported(t));
      const rec = new MediaRecorder(
        stream,
        mime ? { mimeType: mime } : undefined,
      );
      const chunks: Blob[] = [];
      rec.ondataavailable = (e) => e.data.size > 0 && chunks.push(e.data);
      rec.onstop = () => {
        stream.getTracks().forEach((t) => t.stop());
        recorderRef.current = null;
        void transcribeAndAdd(
          new Blob(chunks, { type: rec.mimeType || mime }),
          recordingName(rec.mimeType || mime),
        );
      };
      recorderRef.current = rec;
      rec.start();
      setDictation("recording");
    } catch (err) {
      setAttachError(
        `couldn't use the microphone: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  };
  const onAudioPick = (e: ChangeEvent<HTMLInputElement>) => {
    const f = e.target.files?.[0];
    e.target.value = "";
    if (f) void transcribeAndAdd(f, f.name || "dictation.webm");
  };

  // "/" at the start suggests the built-in commands, then installed skills, until there's a space.
  const [pick, setPick] = useState(0);
  const [dismissed, setDismissed] = useState(false);
  const [picker, setPicker] = useState<{
    name: CommandName;
    spec: PickerSpec;
  } | null>(null);
  const [pickerPick, setPickerPick] = useState(0);
  const slashWord = /^\/[\w.-]*$/.test(text)
    ? text.slice(1).toLowerCase()
    : null;
  const suggestions: Suggestion[] =
    slashWord === null || dismissed
      ? []
      : [
          ...matchCommands(slashWord).map((cmd) => ({
            kind: "command" as const,
            cmd,
          })),
          ...skills
            .filter(
              (s) =>
                s.name.includes(slashWord) ||
                s.description.toLowerCase().includes(slashWord),
            )
            .sort(
              (a, b) =>
                Number(b.name.startsWith(slashWord)) -
                Number(a.name.startsWith(slashWord)),
            )
            .slice(0, 6)
            .map((skill) => ({ kind: "skill" as const, skill })),
        ];
  useEffect(() => {
    setPick(0);
    if (!text.startsWith("/")) setDismissed(false);
  }, [text]);
  const choose = (s: Suggestion) => {
    if (s.kind === "skill") {
      setText(`/${s.skill.name} `);
      ref.current?.focus();
      return;
    }
    setText("");
    const spec = s.cmd.picker ? getPicker(s.cmd.name) : null;
    if (spec) {
      setPicker({ name: s.cmd.name, spec });
      setPickerPick(
        Math.max(
          0,
          spec.items.findIndex((i) => i.value === spec.current),
        ),
      );
    } else {
      onCommand(s.cmd.name);
    }
    ref.current?.focus();
  };
  const choosePicker = (value: string) => {
    if (!picker) return;
    onCommand(picker.name, value);
    setPicker(null);
    ref.current?.focus();
  };

  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 240)}px`;
  }, [text]);

  const addFiles = useCallback(async (files: File[]) => {
    setAttachError(null);
    const accepted: PendingImage[] = [];
    for (const f of files) {
      if (!f.type.startsWith("image/")) continue;
      if (f.size > maxComposerImageBytes) {
        setAttachError(`${f.name || "that image"} is over the 8MB limit`);
        continue;
      }
      try {
        accepted.push({
          url: await readAsDataURL(f),
          name: f.name || "pasted image",
        });
      } catch {
        setAttachError(`couldn't read ${f.name || "that image"}`);
      }
    }
    if (accepted.length) setImages((prev) => [...prev, ...accepted]);
  }, []);

  const submit = async () => {
    const t = text.trim();
    const providerCmd = parseProviderCommand(t);
    if (providerCmd && images.length === 0) {
      setText("");
      onProviderCommand(providerCmd);
      return;
    }
    if ((!t && images.length === 0) || running || disabled) return;
    setText("");
    const sent = images;
    setImages([]);
    const urls = sent.map((i) => i.url);
    if (!(await onSend(t, urls.length ? urls : undefined))) {
      setText(t);
      setImages(sent);
    }
  };
  const onKey = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (picker) {
      const n = picker.spec.items.length;
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        setPickerPick((p) => (p + (e.key === "ArrowDown" ? 1 : n - 1)) % n);
      } else if ((e.key === "Enter" && !e.shiftKey) || e.key === "Tab") {
        e.preventDefault();
        choosePicker(picker.spec.items[Math.min(pickerPick, n - 1)].value);
      } else if (e.key === "Escape") {
        e.preventDefault();
        setPicker(null);
      }
      return;
    }
    if (suggestions.length > 0) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const n = suggestions.length;
        setPick((p) => (p + (e.key === "ArrowDown" ? 1 : n - 1)) % n);
        return;
      }
      if ((e.key === "Enter" && !e.shiftKey) || e.key === "Tab") {
        e.preventDefault();
        choose(suggestions[Math.min(pick, suggestions.length - 1)]);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setDismissed(true);
        return;
      }
    }
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      submit();
    }
    if (e.key === "Escape" && running) onCancel();
  };
  const onPaste = (e: ClipboardEvent<HTMLTextAreaElement>) => {
    const files = Array.from(e.clipboardData?.files ?? []).filter((f) =>
      f.type.startsWith("image/"),
    );
    if (files.length === 0) return;
    e.preventDefault(); // pasting an image would otherwise also drop its filename as text
    void addFiles(files);
  };
  const onFilePick = (e: ChangeEvent<HTMLInputElement>) => {
    if (e.target.files) void addFiles(Array.from(e.target.files));
    e.target.value = ""; // let the same file be picked again later
  };

  const activeModes = settings ? TOGGLES.filter((t) => settings[t.key]) : [];
  const ctxPct =
    stats && stats.contextBudget > 0
      ? Math.min(
          100,
          Math.round((100 * stats.contextUsed) / stats.contextBudget),
        )
      : null;
  const firstSkill = suggestions.findIndex((x) => x.kind === "skill");
  return (
    <div className="composer">
      <div className="composer-inner">
        {picker && (
          <ul
            className="skill-suggest"
            role="listbox"
            aria-label={picker.spec.title}
          >
            <li className="skill-suggest-head">
              {picker.spec.title}
              {picker.spec.current ? (
                <span className="hint"> · now: {picker.spec.current}</span>
              ) : null}
            </li>
            {picker.spec.items.map((it, i) => (
              <li
                key={it.value || "(default)"}
                role="option"
                aria-selected={i === pickerPick}
              >
                <button
                  type="button"
                  ref={(el) => {
                    if (el && i === pickerPick)
                      el.scrollIntoView?.({ block: "nearest" });
                  }}
                  className={`skill-option ${i === pickerPick ? "active" : ""}`}
                  onMouseEnter={() => setPickerPick(i)}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => choosePicker(it.value)}
                >
                  <span className="skill-name">{it.label}</span>
                  {it.value === picker.spec.current && (
                    <Icon name="check" size={15} className="skill-check" />
                  )}
                </button>
              </li>
            ))}
            <li className="skill-suggest-hint">
              Up/Down to move, Enter to pick, Esc to close
            </li>
          </ul>
        )}
        {!picker && suggestions.length > 0 && (
          <ul
            className="skill-suggest"
            role="listbox"
            aria-label="Commands and skills"
          >
            {suggestions.map((sg, i) => (
              <li
                key={
                  sg.kind === "command"
                    ? `c:${sg.cmd.name}`
                    : `s:${sg.skill.name}`
                }
                role="option"
                aria-selected={i === pick}
              >
                {i === 0 && sg.kind === "command" && (
                  <div className="skill-suggest-head">Commands</div>
                )}
                {i === firstSkill && (
                  <div className="skill-suggest-head">Skills</div>
                )}
                <button
                  type="button"
                  ref={(el) => {
                    if (el && i === pick)
                      el.scrollIntoView?.({ block: "nearest" });
                  }}
                  className={`skill-option ${i === pick ? "active" : ""}`}
                  onMouseEnter={() => setPick(i)}
                  onMouseDown={(e) => e.preventDefault()}
                  onClick={() => choose(sg)}
                >
                  <span className="skill-ico">
                    <Icon
                      name={sg.kind === "command" ? sg.cmd.icon : "file"}
                      size={15}
                    />
                  </span>
                  <span className="skill-name">
                    /{sg.kind === "command" ? sg.cmd.name : sg.skill.name}
                  </span>
                  <span className="skill-desc">
                    {sg.kind === "command" ? sg.cmd.help : sg.skill.description}
                  </span>
                </button>
              </li>
            ))}
            <li className="skill-suggest-hint">
              <kbd>↑</kbd> <kbd>↓</kbd> move · <kbd>Enter</kbd> run ·{" "}
              <kbd>Esc</kbd> close · skills then take what to run them on
            </li>
          </ul>
        )}
        <input
          ref={fileInputRef}
          type="file"
          accept="image/*"
          multiple
          hidden
          onChange={onFilePick}
        />
        {stt && (
          <input
            ref={audioInputRef}
            type="file"
            accept="audio/*"
            capture
            hidden
            onChange={onAudioPick}
          />
        )}
        <div className="composer-card">
          {(activeModes.length > 0 || images.length > 0 || attachError) && (
            <div className="composer-top">
              {activeModes.map((m) => (
                <button
                  key={m.key}
                  type="button"
                  className={`mode-chip ${m.key === "auto" ? "warn" : ""}`}
                  title={`${m.title}. Click to turn off.`}
                  aria-label={`Turn off ${m.label} mode`}
                  disabled={running}
                  onClick={() => onCommand(m.key)}
                >
                  {m.label} mode
                  <Icon name="x" size={12} />
                </button>
              ))}
              {images.map((img, i) => (
                <div key={i} className="attachment-chip">
                  <img src={img.url} alt={img.name} />
                  <button
                    type="button"
                    className="attachment-remove"
                    aria-label={`Remove ${img.name}`}
                    title={img.name}
                    onClick={() =>
                      setImages((prev) => prev.filter((_, j) => j !== i))
                    }
                  >
                    ×
                  </button>
                </div>
              ))}
              {attachError && (
                <span className="attachment-error">{attachError}</span>
              )}
            </div>
          )}
          <textarea
            ref={ref}
            rows={1}
            value={text}
            placeholder={
              running
                ? "Working… (Esc to stop)"
                : "Message the agent…  or type / for commands"
            }
            onChange={(e) => setText(e.target.value)}
            onKeyDown={onKey}
            onPaste={onPaste}
            disabled={disabled}
            aria-label="Message"
          />
          <div className="composer-bar">
            <button
              type="button"
              className="icon-btn"
              onClick={() => fileInputRef.current?.click()}
              disabled={disabled}
              aria-label="Attach an image"
              title="Attach an image (or paste one)"
            >
              <Icon name="paperclip" size={18} />
            </button>
            {stt && (
              <button
                type="button"
                className={`icon-btn ${dictation === "recording" ? "icon-btn-rec" : ""}`}
                onClick={() => void toggleDictation()}
                disabled={disabled || dictation === "working"}
                aria-label={
                  dictation === "recording"
                    ? "Stop dictating"
                    : "Dictate a message"
                }
                title={
                  dictation === "recording"
                    ? "Stop and transcribe"
                    : dictation === "working"
                      ? "Transcribing…"
                      : "Dictate a message"
                }
              >
                <Icon
                  name={
                    dictation === "recording"
                      ? "stop"
                      : dictation === "working"
                        ? "loader"
                        : "mic"
                  }
                  size={18}
                />
              </button>
            )}
            <span className="composer-spacer" />
            {ctxPct !== null && stats && (
              <span
                className="ctx-meter"
                title={`Conversation context: about ${humanTokens(stats.contextUsed)} of ${humanTokens(stats.contextBudget)} tokens before older messages are trimmed`}
              >
                <span className="ctx-bar">
                  <i
                    className={ctxPct >= 85 ? "hot" : ""}
                    style={{ width: `${ctxPct}%` }}
                  />
                </span>
                {humanTokens(stats.contextUsed)} /{" "}
                {humanTokens(stats.contextBudget)}
              </span>
            )}
            {running ? (
              <button
                className="send send-stop"
                onClick={onCancel}
                aria-label="Stop"
                title="Stop (Esc)"
              >
                <Icon name="stop" size={16} />
              </button>
            ) : (
              <button
                className="send"
                onClick={submit}
                disabled={(!text.trim() && images.length === 0) || disabled}
                aria-label="Send"
                title="Send (Enter)"
              >
                <Icon name="send" size={18} />
              </button>
            )}
          </div>
        </div>
      </div>
      <div className="composer-foot">
        {stats ? (
          <StatusLine stats={stats} />
        ) : (
          <span className="composer-hint">
            Enter to send · Shift+Enter for a new line · / for commands
          </span>
        )}
        {canUndo && !running && (
          <button
            className="link-btn undo-link"
            onClick={onUndo}
            title="Restore files and remove the last message and its reply"
          >
            <Icon name="undo" size={13} /> Undo last turn
          </button>
        )}
      </div>
    </div>
  );
}

// ---- undo ----

function UndoConfirm({
  onConfirm,
  onClose,
}: {
  onConfirm: () => void;
  onClose: () => void;
}) {
  const confirmRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    confirmRef.current?.focus();
    const onKey = (e: globalThis.KeyboardEvent) =>
      e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="modal-scrim" onClick={onClose}>
      <div
        className="modal modal-sm"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="undo-title"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="modal-head">
          <h2 id="undo-title">Undo the last turn?</h2>
        </div>
        <div className="modal-body">
          <p>
            Your last message and everything the agent did after it are removed,
            and the project's files go back to how they were before that
            message. That includes edits you made by hand since then.
          </p>
          <p className="hint">
            Files ignored by .gitignore (and folders like node_modules) aren't
            touched.
          </p>
          <div className="row end">
            <button className="btn" onClick={onClose}>
              Cancel
            </button>
            <button
              ref={confirmRef}
              className="btn btn-danger"
              onClick={onConfirm}
            >
              Undo
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

// ---- app ----

export default function App() {
  const [auth, setAuth] = useState<"checking" | "login" | "ok">("checking");
  const [serverInfo, setInfo] = useState<Info | null>(null);
  const [sessions, setSessions] = useState<SessionSummary[]>([]);
  const [models, setModels] = useState<string[]>([]);
  const [drawer, setDrawer] = useState(false);
  const [terminalOpen, setTerminalOpen] = useState(false);
  const [groups, setGroups] = useState<ConvGroup[]>([]);
  const [sidebarHidden, setSidebarHidden] = useState(() => {
    try {
      return localStorage.getItem("goharness.sidebarHidden") === "1";
    } catch {
      return false;
    }
  });
  const [projects, setProjects] = useState<Project[]>([]);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [confirmUndo, setConfirmUndo] = useState(false);
  const [picking, setPicking] = useState(false);
  const [reviewTurn, setReviewTurn] = useState<number | null>(null);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [query, setQuery] = useState("");
  const queryRef = useRef("");
  const [draft, setDraft] = useState("");
  const [info, setInfoBanner] = useState<string | null>(null);
  const [id, go] = useRoute();
  const [theme, toggleTheme] = useTheme();

  const onUnauthorized = useCallback(() => setAuth("login"), []);

  // The list always reflects the current search; a stale response for an
  // older query is dropped.
  const refreshSessions = useCallback(() => {
    const q = queryRef.current;
    api
      .sessions(q)
      .then((list) => q === queryRef.current && setSessions(list))
      .catch((err) => {
        if (err instanceof ApiError && err.status === 401) setAuth("login");
      });
  }, []);

  useEffect(() => {
    queryRef.current = query.trim();
    const t = setTimeout(refreshSessions, query ? 250 : 0);
    return () => clearTimeout(t);
  }, [query, refreshSessions]);

  const boot = useCallback(() => {
    api
      .info()
      .then((i) => {
        setInfo(i);
        setAuth("ok");
        refreshSessions();
        api
          .projects()
          .then(setProjects)
          .catch(() => setProjects([]));
        api
          .groups()
          .then(setGroups)
          .catch(() => setGroups([]));
      })
      .catch((err) =>
        setAuth(err instanceof ApiError && err.status === 401 ? "login" : "ok"),
      );
  }, [refreshSessions]);

  useEffect(boot, [boot]);

  // Returns an error message, or null on success.
  const newSession = useCallback(
    async (project?: string, group?: string): Promise<string | null> => {
      try {
        const snap = await api.createSession(project);
        if (project) rememberDir(snap.projectRoot);
        if (group) await api.patchSession(snap.id, { group });
        setPicking(false);
        go(snap.id);
        setDrawer(false);
        refreshSessions();
        return null;
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) setAuth("login");
        return err instanceof Error ? err.message : String(err);
      }
    },
    [go, refreshSessions],
  );

  // With no session in the URL, open a new one (once, even under StrictMode).
  const creating = useRef(false);
  useEffect(() => {
    if (auth !== "ok" || id || creating.current) return;
    creating.current = true;
    api
      .createSession()
      .then(
        (snap) => go(snap.id, true),
        () => {},
      )
      .finally(() => (creating.current = false));
  }, [auth, id, go]);

  // Where to go when the open session disappears: the newest other one, or a new one.
  const leaveSession = useCallback(
    (gone: Set<string>) => {
      api
        .sessions()
        .then((list) => {
          setSessions(list);
          const next = list.find((s) => !gone.has(s.id));
          if (next) go(next.id, true);
          else
            api.createSession().then(
              (snap) => go(snap.id, true),
              () => {},
            );
        })
        .catch(() => {});
    },
    [go],
  );

  // Sessions this tab is deleting: their "deleted" event is ours, already handled.
  const deleting = useRef(new Set<string>());
  // Skills depend on the session's project, and a run can install new ones
  // (book-to-skill writes them), so they're reloaded after every run too.
  const loadSkills = useCallback(() => {
    if (id) api.skills(id).then(setSkills, () => setSkills([]));
  }, [id]);
  useEffect(loadSkills, [loadSkills]);

  const onRunEnd = useCallback(() => {
    refreshSessions();
    loadSkills();
  }, [refreshSessions, loadSkills]);
  const session = useSession(
    auth === "ok" ? id : null,
    onUnauthorized,
    onRunEnd,
    () => {
      if (id && !deleting.current.has(id)) leaveSession(new Set([id]));
    },
  );

  // Refetch the model list whenever the active session's provider changes,
  // so the model dropdown always matches the backend actually selected -
  // freellmapi's models are a different set from llama-swap's.
  const provider = session.settings?.provider;
  useEffect(() => {
    if (auth !== "ok") return;
    api
      .models(provider)
      .then(setModels)
      .catch(() => setModels([]));
  }, [auth, provider]);

  const renameSession = useCallback(
    (sid: string, title: string) => {
      setSessions((list) =>
        list.map((s) =>
          s.id === sid ? { ...s, title: title.trim() || s.title } : s,
        ),
      );
      api.patchSession(sid, { title }).then(refreshSessions, refreshSessions);
    },
    [refreshSessions],
  );

  const pinSession = useCallback(
    (sid: string, pinned: boolean) => {
      setSessions((list) =>
        list.map((s) => (s.id === sid ? { ...s, pinned } : s)),
      );
      api.patchSession(sid, { pinned }).then(refreshSessions, refreshSessions);
    },
    [refreshSessions],
  );

  // Moves a conversation to a group ("" = back to its project's group).
  const moveSession = useCallback(
    (sid: string, group: string) => {
      setSessions((list) =>
        list.map((s) =>
          s.id === sid ? { ...s, group: group || undefined } : s,
        ),
      );
      api.patchSession(sid, { group }).then(refreshSessions, (err) => {
        setInfoBanner(
          `Couldn't move it: ${err instanceof Error ? err.message : String(err)}`,
        );
        refreshSessions();
      });
    },
    [refreshSessions],
  );

  const createGroup = useCallback(
    async (name: string): Promise<ConvGroup | null> => {
      try {
        const g = await api.createGroup(name);
        setGroups((list) => [...list, g]);
        setInfoBanner(`Created group “${g.name}”.`);
        return g;
      } catch (err) {
        setInfoBanner(
          `Couldn't create the group: ${err instanceof Error ? err.message : String(err)}`,
        );
        return null;
      }
    },
    [],
  );

  const renameGroup = useCallback(async (gid: string, name: string) => {
    try {
      const g = await api.renameGroup(gid, name);
      setGroups((list) => list.map((x) => (x.id === gid ? g : x)));
    } catch (err) {
      setInfoBanner(
        `Couldn't rename the group: ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }, []);

  const deleteGroup = useCallback(
    async (gid: string) => {
      try {
        await api.deleteGroup(gid);
        setGroups((list) => list.filter((x) => x.id !== gid));
        setInfoBanner(
          "Deleted the group. Its conversations went back to their projects.",
        );
        refreshSessions();
      } catch (err) {
        setInfoBanner(
          `Couldn't delete the group: ${err instanceof Error ? err.message : String(err)}`,
        );
      }
    },
    [refreshSessions],
  );

  // A new conversation inside a group: a project's group starts one in that folder; one of the user's
  // groups starts one in the current project and files it there.
  const newInGroup = useCallback(
    async (key: string) => {
      if (key.startsWith(PROJECT_PREFIX)) {
        const name = key.slice(PROJECT_PREFIX.length);
        const p = projects.find((x) => x.name === name);
        if (!p)
          return setInfoBanner(
            `Can't start a conversation in “${name}”: that folder isn't in your project list. Use New, then Choose folder.`,
          );
        const err = await newSession(p.path);
        if (err) setInfoBanner(`Couldn't start a new session: ${err}`);
        return;
      }
      const err = await newSession(undefined, key);
      if (err) setInfoBanner(`Couldn't start a new session: ${err}`);
    },
    [projects, newSession],
  );

  const toggleSidebar = useCallback(() => {
    if (window.matchMedia?.("(max-width: 760px)").matches)
      return setDrawer((v) => !v);
    setSidebarHidden((v) => {
      try {
        localStorage.setItem("goharness.sidebarHidden", v ? "0" : "1");
      } catch {
        /* not remembered */
      }
      return !v;
    });
  }, []);

  const deleteSessions = useCallback(
    async (ids: string[]) => {
      ids.forEach((x) => deleting.current.add(x));
      try {
        const res = await api.deleteSessions(ids);
        const skipped = Object.entries(res.skipped);
        const deleted = new Set(res.deleted);
        setSessions((list) => list.filter((s) => !deleted.has(s.id)));
        setInfoBanner(
          `Deleted ${res.deleted.length} session${res.deleted.length === 1 ? "" : "s"}.` +
            (skipped.length
              ? ` Skipped ${skipped.length}: ${skipped
                  .map(([, why]) => why)
                  .filter((v, i, a) => a.indexOf(v) === i)
                  .join("; ")}.`
              : ""),
        );
        if (id && deleted.has(id)) leaveSession(deleted);
        else refreshSessions();
      } catch (err) {
        if (err instanceof ApiError && err.status === 401) setAuth("login");
        else
          setInfoBanner(
            `Couldn't delete: ${err instanceof Error ? err.message : String(err)}`,
          );
      } finally {
        setTimeout(() => ids.forEach((x) => deleting.current.delete(x)), 5000);
      }
    },
    [id, leaveSession, refreshSessions],
  );

  // Drafts and open dialogs belong to a session.
  useEffect(() => {
    setDraft("");
    setReviewTurn(null);
  }, [id]);

  const undo = useCallback(async () => {
    setConfirmUndo(false);
    const res = await session.undo();
    if (!res) return;
    setDraft(res.text);
    const files = res.files ?? [];
    setInfoBanner(
      !res.filesRestored
        ? "Removed the last turn. Files were not restored (that turn had no checkpoint)."
        : files.length === 0
          ? "Removed the last turn. No files had changed."
          : `Removed the last turn and restored ${files.length} file${files.length === 1 ? "" : "s"}: ${files.slice(0, 6).join(", ")}${files.length > 6 ? "…" : ""}`,
    );
    refreshSessions();
  }, [session.undo, refreshSessions]);

  const send = useCallback(
    async (text: string, images?: string[]) => {
      setInfoBanner(null);
      const ok = await session.send(text, images);
      if (ok) refreshSessions();
      return ok;
    },
    [session.send, refreshSessions],
  );

  const stats = useStats(auth === "ok" ? id : null, session.running);

  // What a picker command (/model, /provider, /subagent) offers.
  const { providers: providerChoices } = useProviders(); // the live list, so custom providers show up
  const getPicker = useCallback(
    (name: CommandName): PickerSpec | null => {
      const st = session.settings;
      if (!st || session.running) return null;
      if (name === "model") {
        const list = models.includes(st.model) ? models : [st.model, ...models];
        return {
          title: "Select a model",
          current: st.model,
          items: list.map((m) => ({ label: m, value: m })),
        };
      }
      if (name === "provider") {
        return {
          title: "Select a provider",
          current: st.provider || "llama-swap",
          items: [
            ...providerChoices.map((p) => ({ label: p.label, value: p.id })),
            { label: "+ Add a provider…", value: ADD_PROVIDER },
          ],
        };
      }
      if (name === "subagent") {
        return {
          title: "Select the subagent model",
          current: st.subagentModel ?? "",
          items: [
            {
              label:
                "(default: GOHARNESS_SUBAGENT_MODEL, else the session model)",
              value: "",
            },
            ...models.map((m) => ({ label: m, value: m })),
          ],
        };
      }
      return null;
    },
    [session.settings, session.running, models, providerChoices],
  );

  // Runs a slash command from the message box. Feedback goes in the info banner.
  // "/provider add|list|remove ...": manage custom providers from the message box, like the terminal UI.
  const [providerDialog, setProviderDialog] = useState(false);
  const runProviderCommand = useCallback(async (c: ProviderCommand) => {
    const note = (t: string) => setInfoBanner(t);
    const why = (err: unknown) => (err instanceof Error ? err.message : String(err));
    try {
      if (c.action === "form") {
        setProviderDialog(true);
        return;
      }
      if (c.action === "error") return note(c.message);
      if (c.action === "list") {
        const list = await api.providers();
        return note(
          "Providers: " +
            list.map((p) => `${p.id}${p.custom ? ` (custom${p.hasApiKey ? ", key set" : ""})` : ""}`).join(", ") +
            ". /provider to switch.",
        );
      }
      if (c.action === "remove") {
        await api.deleteProvider(c.name);
        window.dispatchEvent(new Event("goharness-providers"));
        return note(`Provider "${c.name}" removed.`);
      }
      await api.createProvider({ name: c.name, baseUrl: c.baseUrl, model: c.model, apiKey: c.apiKey, apiKeyEnv: c.apiKeyEnv });
      window.dispatchEvent(new Event("goharness-providers"));
      note(`Provider "${c.name}" added with model "${c.model}". Type /provider to switch to it.`);
    } catch (err) {
      note(`/provider ${c.action}: ${why(err)}`);
    }
  }, []);

  const runCommand = useCallback(
    async (name: CommandName, value?: string) => {
      const note = (t: string) => setInfoBanner(t);
      const failed = (what: string, err: unknown) =>
        note(`${what}: ${err instanceof Error ? err.message : String(err)}`);
      const st = session.settings;
      if (name === "help") return note(helpText());
      if (name === "skills") {
        return note(
          skills.length
            ? `Installed skills (type / and a name): ${skills.map((s) => "/" + s.name).join(", ")}`
            : "No skills installed.",
        );
      }
      if (name === "reload") {
        try {
          note((await api.reload()).summary);
          loadSkills();
        } catch (err) {
          failed("Reload failed", err);
        }
        return;
      }
      if (name === "new") {
        const err = await newSession(session.projectRoot || undefined);
        if (err) note(`Couldn't start a new session: ${err}`);
        return;
      }
      if (session.running)
        return note("Wait for the current run to finish first.");
      if (name === "undo") {
        if (!session.canUndo) return note("Nothing to undo.");
        setConfirmUndo(true);
        return;
      }
      if (!st || !id) return;
      if (name === "compact") {
        note("Compacting the conversation…");
        try {
          const r = await api.compact(id);
          note(
            `Compacted: about ${humanTokens(r.tokensBefore)} → ${humanTokens(r.tokensAfter)} tokens of history.`,
          );
        } catch (err) {
          failed("Compact", err);
        }
        return;
      }
      if (isToggle(name)) {
        const on = !st[name];
        await session.updateSettings({ ...st, [name]: on });
        return note(`${name} ${on ? "on" : "off"}.`);
      }
      if (name === "model" && value) {
        await session.updateSettings({ ...st, model: value });
        return note(`Model set to ${value}.`);
      }
      if (name === "provider" && value === ADD_PROVIDER) {
        setProviderDialog(true);
        return;
      }
      if (name === "provider" && value) {
        const p = providerChoices.find(
          (x: { id: string }) => x.id === value,
        );
        await session.updateSettings({
          ...st,
          provider: value,
          model: p?.defaultModel ?? st.model,
        });
        return note(
          `Provider set to ${value}.${p?.defaultModel ? ` Model set to ${p.defaultModel}.` : " Pick a model with /model."}`,
        );
      }
      if (name === "subagent" && value !== undefined) {
        await session.updateSettings({ ...st, subagentModel: value });
        return note(
          value
            ? `Subagent model set to ${value}.`
            : "Subagent model reset to the default.",
        );
      }
    },
    [
      session.settings,
      session.running,
      session.canUndo,
      session.projectRoot,
      session.updateSettings,
      id,
      skills,
      loadSkills,
      providerChoices,
      newSession,
    ],
  );

  useEffect(() => {
    const pending = session.items.some(
      (i) => i.kind === "tool" && i.status === "pending",
    );
    document.title = pending
      ? "● Approval needed · goharness"
      : session.running
        ? "… goharness"
        : "goharness";
  }, [session.items, session.running]);

  if (auth === "checking") return <div className="boot" />;
  if (auth === "login") return <Login onDone={boot} />;

  const logout = async () => {
    await api.logout().catch(() => {});
    setAuth("login");
  };

  return (
    <div className={`app ${sidebarHidden ? "sidebar-hidden" : ""}`}>
      <Sidebar
        sessions={sessions}
        projects={projects}
        groups={groups}
        current={id}
        collapsed={sidebarHidden}
        onToggleCollapsed={toggleSidebar}
        onMove={moveSession}
        onCreateGroup={createGroup}
        onRenameGroup={renameGroup}
        onDeleteGroup={deleteGroup}
        onNewInGroup={newInGroup}
        onSelect={(s) => {
          go(s);
          setDrawer(false);
        }}
        onNew={newSession}
        query={query}
        onQuery={setQuery}
        onRename={renameSession}
        onPin={pinSession}
        onDelete={deleteSessions}
        onChooseFolder={() => {
          setDrawer(false);
          setPicking(true);
        }}
        onLogout={logout}
        open={drawer}
        onClose={() => setDrawer(false)}
      />
      <main className="main">
        <TopBar
          projectRoot={session.projectRoot}
          settings={session.settings}
          models={models}
          running={session.running}
          onChange={session.updateSettings}
          onMenu={toggleSidebar}
          sidebarHidden={sidebarHidden}
          theme={theme}
          onToggleTheme={toggleTheme}
          onSettings={() => setSettingsOpen(true)}
          onToggleTerminal={() => setTerminalOpen((v) => !v)}
          terminalOpen={terminalOpen}
          connected={session.connected || session.loading}
        />
        {session.error && (
          <div className="banner" role="alert">
            <span>{session.error}</span>
            <button
              className="icon-btn"
              onClick={session.dismissError}
              aria-label="Dismiss"
            >
              ×
            </button>
          </div>
        )}
        {info && (
          <div className="banner banner-info" role="status">
            <span>{info}</span>
            <button
              className="icon-btn"
              onClick={() => setInfoBanner(null)}
              aria-label="Dismiss"
            >
              ×
            </button>
          </div>
        )}
        <Transcript
          items={session.items}
          running={session.running}
          activity={session.activity}
          lastEventAt={session.lastEventAt}
          onApprove={session.approve}
          onReviewChanges={setReviewTurn}
        />
        <Composer
          settings={session.settings}
          stats={stats}
          onCommand={runCommand}
          onProviderCommand={runProviderCommand}
          getPicker={getPicker}
          skills={skills}
          stt={serverInfo?.stt ?? false}
          text={draft}
          setText={setDraft}
          running={session.running}
          disabled={session.loading || !id}
          canUndo={session.canUndo}
          onSend={send}
          onCancel={session.cancel}
          onUndo={() => setConfirmUndo(true)}
        />
      </main>
      {terminalOpen && id && (
        <aside className="terminal-pane">
          <div className="terminal-pane-head">
            <span>Terminal</span>
            <button
              className="icon-btn"
              onClick={() => setTerminalOpen(false)}
              aria-label="Close terminal"
            >
              ×
            </button>
          </div>
          <TerminalPanel key={id} sessionId={id} />
        </aside>
      )}
      <ProviderDialog
        open={providerDialog}
        onClose={() => setProviderDialog(false)}
        onAdded={(n, m) => {
          window.dispatchEvent(new Event("goharness-providers"));
          setInfoBanner(`Provider "${n}" added with model "${m}". Type /provider to switch to it.`);
        }}
      />
      <SettingsPanel
        open={settingsOpen}
        onClose={() => setSettingsOpen(false)}
        settings={session.settings}
        running={session.running}
        onChange={session.updateSettings}
        skills={skills}
        onReloaded={loadSkills}
      />
      {confirmUndo && (
        <UndoConfirm onConfirm={undo} onClose={() => setConfirmUndo(false)} />
      )}
      {reviewTurn !== null && id && (
        <ChangesModal
          sessionId={id}
          turn={reviewTurn}
          onClose={() => setReviewTurn(null)}
        />
      )}
      {picking && (
        <FolderPicker
          projects={projects}
          onChoose={(p) => newSession(p)}
          onClose={() => setPicking(false)}
        />
      )}
    </div>
  );
}
