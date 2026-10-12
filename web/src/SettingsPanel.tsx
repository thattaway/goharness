import { useEffect, useState } from "react";
import {
  api,
  type Permissions,
  type ProviderInfo,
  type Rule,
  type RuleAction,
  type Settings,
  type Skill,
} from "./api";
import {
  enableSystemNotifications,
  getPrefs,
  setPrefs,
  systemNotificationsSupported,
} from "./notify";

const ACTIONS: RuleAction[] = ["allow", "ask", "deny"];

function RulesEditor() {
  const [perms, setPerms] = useState<Permissions | null>(null);
  const [rules, setRules] = useState<Rule[]>([]);
  const [status, setStatus] = useState<{
    kind: "ok" | "error";
    text: string;
  } | null>(null);
  const [probe, setProbe] = useState("");
  const [verdict, setVerdict] = useState<{
    action: RuleAction;
    reason: string;
  } | null>(null);

  useEffect(() => {
    api
      .permissions()
      .then((p) => {
        setPerms(p);
        setRules(p.rules);
      })
      .catch((e) => setStatus({ kind: "error", text: String(e.message ?? e) }));
  }, []);

  const dirty =
    perms !== null && JSON.stringify(rules) !== JSON.stringify(perms.rules);

  const save = async () => {
    try {
      const res = await api.savePermissions(
        rules.filter((r) => r.pattern.trim()),
      );
      setRules(res.rules);
      setPerms((p) => (p ? { ...p, rules: res.rules } : p));
      setStatus({
        kind: "ok",
        text: "Saved. New rules apply to the next tool call.",
      });
      if (probe) test(probe);
    } catch (e) {
      setStatus({
        kind: "error",
        text: e instanceof Error ? e.message : String(e),
      });
    }
  };

  const test = async (cmd: string) => {
    if (!cmd.trim()) return setVerdict(null);
    try {
      setVerdict(await api.checkCommand(cmd));
    } catch {
      setVerdict(null);
    }
  };

  const update = (i: number, patch: Partial<Rule>) =>
    setRules(rules.map((r, j) => (j === i ? { ...r, ...patch } : r)));

  return (
    <section className="panel-section">
      <h3>Shell command rules</h3>
      <p className="hint">
        Patterns use <code>*</code> as a wildcard and are matched against each
        part of a command (split on <code>| ; &amp;&amp;</code>). The last
        matching rule wins, and your rules come after the built-in ones. Even an{" "}
        <em>allow</em> rule still asks for commands that redirect output, run
        subcommands with <code>$(…)</code>, or touch secret files.
      </p>

      <div className="rules">
        {rules.length === 0 && <div className="hint">No custom rules yet.</div>}
        {rules.map((r, i) => (
          <div className="rule-row" key={i}>
            <input
              value={r.pattern}
              placeholder="go test*"
              onChange={(e) => update(i, { pattern: e.target.value })}
              aria-label="Command pattern"
              spellCheck={false}
            />
            <select
              value={r.action}
              onChange={(e) =>
                update(i, { action: e.target.value as RuleAction })
              }
              aria-label="Action"
            >
              {ACTIONS.map((a) => (
                <option key={a}>{a}</option>
              ))}
            </select>
            <button
              className="icon-btn"
              aria-label="Remove rule"
              onClick={() => setRules(rules.filter((_, j) => j !== i))}
            >
              ×
            </button>
          </div>
        ))}
      </div>
      <div className="row">
        <button
          className="btn btn-sm"
          onClick={() => setRules([...rules, { pattern: "", action: "allow" }])}
        >
          Add rule
        </button>
        <button
          className="btn btn-sm btn-primary"
          disabled={!dirty}
          onClick={save}
        >
          Save rules
        </button>
        {status && (
          <span className={status.kind === "ok" ? "ok-text" : "form-error"}>
            {status.text}
          </span>
        )}
      </div>

      <label className="field">
        <span>Try a command</span>
        <input
          value={probe}
          placeholder="npm run build"
          spellCheck={false}
          onChange={(e) => {
            setProbe(e.target.value);
            test(e.target.value);
          }}
        />
      </label>
      {verdict && (
        <div className={`verdict verdict-${verdict.action}`}>
          <strong>{verdict.action}</strong> · {verdict.reason}
          {dirty && <span className="hint"> (saved rules only)</span>}
        </div>
      )}

      {perms && (
        <details className="defaults">
          <summary>Built-in rules ({perms.defaults.length})</summary>
          <table>
            <tbody>
              {perms.defaults.map((r) => (
                <tr key={r.pattern}>
                  <td>
                    <code>{r.pattern}</code>
                  </td>
                  <td className={`verdict-${r.action}`}>{r.action}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className="hint">
            Stored in <code>{perms.path}</code>.
          </p>
        </details>
      )}
    </section>
  );
}

function NotificationSettings() {
  const [prefs, setLocal] = useState(getPrefs);
  const [note, setNote] = useState<string | null>(null);
  const supported = systemNotificationsSupported();
  const change = (p: typeof prefs) => {
    setLocal(p);
    setPrefs(p);
  };
  return (
    <section className="panel-section">
      <h3>Alerts</h3>
      <label className="check">
        <input
          type="checkbox"
          checked={prefs.sound}
          onChange={(e) => change({ ...prefs, sound: e.target.checked })}
        />
        Play a chime when approval is needed or a background run finishes
      </label>
      <label className="check">
        <input
          type="checkbox"
          checked={prefs.system && supported}
          disabled={!supported}
          onChange={async (e) => {
            if (!e.target.checked) return change({ ...prefs, system: false });
            const ok = await enableSystemNotifications();
            setNote(
              ok
                ? null
                : "The browser blocked notifications. Allow them in the site settings and try again.",
            );
            change({ ...prefs, system: ok });
          }}
        />
        System notifications when this tab is in the background
      </label>
      {!supported && (
        <p className="hint">
          Browsers only allow notifications over HTTPS. Serve goharness through{" "}
          <code>tailscale serve</code> (or open it on this machine as{" "}
          <code>localhost</code>) to turn them on.
        </p>
      )}
      {note && <p className="form-error">{note}</p>}
    </section>
  );
}

function SessionSettings({
  settings,
  running,
  onChange,
}: {
  settings: Settings;
  running: boolean;
  onChange: (s: Settings) => void;
}) {
  const [rounds, setRounds] = useState(String(settings.maxRounds));
  useEffect(() => setRounds(String(settings.maxRounds)), [settings.maxRounds]);
  const commit = () => {
    const n = Math.max(
      1,
      Math.min(50, parseInt(rounds, 10) || settings.maxRounds),
    );
    setRounds(String(n));
    if (n !== settings.maxRounds) onChange({ ...settings, maxRounds: n });
  };
  return (
    <section className="panel-section">
      <h3>This session</h3>
      <label className="field inline">
        <span>Tool rounds per message</span>
        <input
          type="number"
          min={1}
          max={50}
          value={rounds}
          disabled={running}
          onChange={(e) => setRounds(e.target.value)}
          onBlur={commit}
          onKeyDown={(e) => e.key === "Enter" && commit()}
        />
      </label>
      <p className="hint">
        After this many rounds of tool calls the model is asked to wrap up.
        Raise it for long multi-step tasks; small models sometimes loop, so keep
        it modest.
      </p>
    </section>
  );
}

function ReloadSection({ onReloaded }: { onReloaded: () => void }) {
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<{
    kind: "ok" | "error";
    text: string;
  } | null>(null);
  const reload = async () => {
    setBusy(true);
    try {
      const res = await api.reload();
      setResult({ kind: "ok", text: res.summary });
      onReloaded();
    } catch (e) {
      setResult({
        kind: "error",
        text: e instanceof Error ? e.message : String(e),
      });
    } finally {
      setBusy(false);
    }
  };
  return (
    <section className="panel-section">
      <h3>Reload configuration</h3>
      <p className="hint">
        Re-reads <code>~/.config/goharness/env</code>, the permission rules, the
        model list (context sizes, vision support) and skills, without
        restarting. Use it after editing any of them.
      </p>
      <div className="row">
        <button className="btn btn-sm" disabled={busy} onClick={reload}>
          {busy ? "Reloading…" : "Reload now"}
        </button>
      </div>
      {result && (
        <pre
          className={`reload-result ${result.kind === "ok" ? "ok-text" : "form-error"}`}
        >
          {result.text}
        </pre>
      )}
    </section>
  );
}

function ProvidersEditor() {
  const [providers, setProviders] = useState<ProviderInfo[] | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [model, setModel] = useState("");
  const [apiKey, setApiKey] = useState("");
  const [apiKeyEnv, setApiKeyEnv] = useState("");
  const [busy, setBusy] = useState(false);
  const [msg, setMsg] = useState<{ kind: "ok" | "error"; text: string } | null>(
    null,
  );
  const load = () =>
    api
      .providers()
      .then(setProviders)
      .catch((e) => setErr(String(e.message ?? e)));
  useEffect(() => {
    load();
  }, []);
  const add = async () => {
    setMsg(null);
    if (apiKey && apiKeyEnv)
      return setMsg({
        kind: "error",
        text: "Use --key or --key-env, not both.",
      });
    setBusy(true);
    try {
      await api.createProvider({
        name: name.trim(),
        baseUrl: baseUrl.trim(),
        model: model.trim(),
        apiKey: apiKey || undefined,
        apiKeyEnv: apiKeyEnv || undefined,
      });
      setMsg({ kind: "ok", text: `Provider ${name.trim()} added.` });
      setName("");
      setBaseUrl("");
      setModel("");
      setApiKey("");
      setApiKeyEnv("");
      load();
    } catch (e) {
      setMsg({
        kind: "error",
        text: e instanceof Error ? e.message : String(e),
      });
    } finally {
      setBusy(false);
    }
  };
  const del = async (id: string) => {
    setMsg(null);
    setBusy(true);
    try {
      await api.deleteProvider(id);
      setMsg({ kind: "ok", text: `Provider ${id} removed.` });
      load();
    } catch (e) {
      setMsg({
        kind: "error",
        text: e instanceof Error ? e.message : String(e),
      });
    } finally {
      setBusy(false);
    }
  };
  return (
    <section className="panel-section">
      <h3>Providers</h3>
      <p className="hint">
        Add an OpenAI-compatible provider (base URL + model + optional auth).
        Custom providers are stored in{" "}
        <code>~/.config/goharness/custom_providers.json</code> (0600). Use{" "}
        <code>/provider</code> to switch.
      </p>
      {err && <div className="form-error">{err}</div>}
      {providers && (
        <table>
          <thead>
            <tr>
              <th>name</th>
              <th>base URL</th>
              <th>models</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {providers.map((p) => (
              <tr key={p.id}>
                <td>
                  <code>{p.id}</code>
                  {p.custom && " *"}
                </td>
                <td
                  className="hint"
                  style={{
                    maxWidth: 220,
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                    whiteSpace: "nowrap",
                  }}
                  title={p.baseUrl}
                >
                  {p.baseUrl}
                </td>
                <td className="hint">
                  {p.models?.join(", ") ?? "(live /models)"}
                  {p.hasApiKey ? " · key" : ""}
                </td>
                <td>
                  {p.custom && (
                    <button
                      className="icon-btn"
                      disabled={busy}
                      onClick={() => del(p.id)}
                      aria-label={`Remove ${p.id}`}
                    >
                      ×
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="row" style={{ marginTop: 10, flexWrap: "wrap", gap: 8 }}>
        <input
          placeholder="name"
          value={name}
          onChange={(e) => setName(e.target.value)}
          spellCheck={false}
          style={{ width: 120 }}
        />
        <input
          placeholder="https://api.example.com/v1"
          value={baseUrl}
          onChange={(e) => setBaseUrl(e.target.value)}
          spellCheck={false}
          style={{ flex: "1 1 200px" }}
        />
        <input
          placeholder="model"
          value={model}
          onChange={(e) => setModel(e.target.value)}
          spellCheck={false}
          style={{ width: 160 }}
        />
      </div>
      <div className="row" style={{ marginTop: 6, flexWrap: "wrap", gap: 8 }}>
        <input
          placeholder="api key (optional)"
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
          spellCheck={false}
          type="password"
          style={{ flex: "1 1 160px" }}
        />
        <input
          placeholder="API_KEY_ENV (optional, preferred)"
          value={apiKeyEnv}
          onChange={(e) => setApiKeyEnv(e.target.value)}
          spellCheck={false}
          style={{ flex: "1 1 160px" }}
        />
        <button
          className="btn btn-sm btn-primary"
          disabled={busy || !name.trim() || !baseUrl.trim() || !model.trim()}
          onClick={add}
        >
          {busy ? "…" : "Add"}
        </button>
      </div>
      {msg && (
        <div
          className={msg.kind === "ok" ? "ok-text" : "form-error"}
          style={{ marginTop: 6 }}
        >
          {msg.text}
        </div>
      )}
      <p className="hint">
        Tip: prefer <code>API_KEY_ENV</code> so the secret lives in{" "}
        <code>~/.config/goharness/env</code>, not on disk.
      </p>
    </section>
  );
}

function SkillsList({ skills }: { skills: Skill[] }) {
  return (
    <section className="panel-section">
      <h3>Skills</h3>
      {skills.length === 0 ? (
        <p className="hint">
          No skills installed. A skill is a folder with a <code>SKILL.md</code>.
          Put it in <code>~/.agents/skills/</code> (shared with other agents),{" "}
          <code>~/.claude/skills/</code>, <code>~/.goharness/skills/</code>, or{" "}
          <code>.agents/skills/</code> inside the project.
        </p>
      ) : (
        <>
          <p className="hint">
            The agent sees each skill's name and description and loads a skill
            when a request matches it. Type <code>/name</code> in the message
            box to run one directly.
          </p>
          <ul className="skills-list">
            {skills.map((s) => (
              <li key={s.name}>
                <code>/{s.name}</code> <span>{s.description}</span>
                <div className="hint skill-dir" title={s.dir}>
                  {s.dir}
                </div>
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}

export function SettingsPanel({
  open,
  onClose,
  settings,
  running,
  onChange,
  skills,
  onReloaded,
}: {
  skills: Skill[];
  onReloaded: () => void;
  open: boolean;
  onClose: () => void;
  settings: Settings | null;
  running: boolean;
  onChange: (s: Settings) => void;
}) {
  const [rulesKey, setRulesKey] = useState(0);
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);
  if (!open) return null;
  return (
    <div className="modal-scrim" onClick={onClose}>
      <div
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-label="Settings"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="modal-head">
          <h2>Settings</h2>
          <button className="icon-btn" onClick={onClose} aria-label="Close">
            ×
          </button>
        </div>
        <div className="modal-body">
          {settings && (
            <SessionSettings
              settings={settings}
              running={running}
              onChange={onChange}
            />
          )}
          <ProvidersEditor />
          <SkillsList skills={skills} />
          <NotificationSettings />
          <RulesEditor key={rulesKey} />
          <ReloadSection
            onReloaded={() => {
              setRulesKey((k) => k + 1);
              onReloaded();
            }}
          />
        </div>
      </div>
    </div>
  );
}
