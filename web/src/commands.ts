// Built-in slash commands for the message box: the web counterparts of the
// terminal UI's commands (commands.go). Typing "/" suggests these first,
// then installed skills.

import type { IconName } from "./icons";

export type CommandName =
  | "model"
  | "subagent"
  | "provider"
  | "compact"
  | "undo"
  | "plan"
  | "tdd"
  | "adhd"
  | "icm"
  | "auto"
  | "new"
  | "skills"
  | "reload"
  | "help";

export interface Command {
  name: CommandName;
  icon: IconName;
  help: string;
  /** Opens a list to choose from instead of running at once. */
  picker?: boolean;
}

export const COMMANDS: Command[] = [
  {
    name: "model",
    icon: "layers",
    help: "switch the active model",
    picker: true,
  },
  {
    name: "subagent",
    icon: "branch",
    help: "choose the model spawn_agent subagents use",
    picker: true,
  },
  {
    name: "provider",
    icon: "layers",
    help: "switch backend; /provider add opens a form, or /provider add <name> <base-url> <model> [--key KEY|--key-env VAR]",
    picker: true,
  },
  {
    name: "compact",
    icon: "refresh",
    help: "summarize the earlier conversation to free up context",
  },
  {
    name: "undo",
    icon: "undo",
    help: "undo the last turn: conversation and file changes",
  },
  { name: "plan", icon: "zap", help: "toggle plan/build mode" },
  { name: "tdd", icon: "zap", help: "toggle test-driven development prompt" },
  { name: "adhd", icon: "zap", help: "toggle ADHD-friendly output style" },
  { name: "icm", icon: "zap", help: "toggle staged-project (ICM) mode" },
  { name: "auto", icon: "zap", help: "toggle auto/manual approval" },
  {
    name: "new",
    icon: "plus",
    help: "start a fresh conversation in this project",
  },
  { name: "skills", icon: "file", help: "list installed skills" },
  {
    name: "reload",
    icon: "refresh",
    help: "re-read the env file, permission rules and model info",
  },
  { name: "help", icon: "help", help: "list commands" },
];

/** Commands that flip a boolean setting of the same name. */
export const TOGGLE_COMMANDS = ["plan", "tdd", "adhd", "icm", "auto"] as const;
export type ToggleCommand = (typeof TOGGLE_COMMANDS)[number];

export function isToggle(name: CommandName): name is ToggleCommand {
  return (TOGGLE_COMMANDS as readonly string[]).includes(name);
}

/** Commands whose name or help contains word, those starting with it first. */
export function matchCommands(word: string): Command[] {
  const w = word.toLowerCase();
  return COMMANDS.filter(
    (c) => c.name.includes(w) || c.help.toLowerCase().includes(w),
  ).sort((a, b) => Number(b.name.startsWith(w)) - Number(a.name.startsWith(w)));
}

export function helpText(): string {
  return COMMANDS.map((c) => `/${c.name} - ${c.help}`).join("  ·  ");
}

/** Splits a line into words, keeping 'single' and "double" quoted text together and honoring backslash escapes (as the terminal UI does). */
export function splitArgs(s: string): string[] {
  const out: string[] = [];
  let cur = "";
  let started = false;
  let quote: "'" | '"' | null = null;
  let escaped = false;
  for (const ch of s) {
    if (escaped) {
      cur += ch;
      escaped = false;
    } else if (ch === "\\" && quote !== "'") {
      escaped = true;
      started = true;
    } else if (quote) {
      if (ch === quote) quote = null;
      else cur += ch;
    } else if (ch === "'" || ch === '"') {
      quote = ch;
      started = true;
    } else if (/\s/.test(ch)) {
      if (started) out.push(cur);
      cur = "";
      started = false;
    } else {
      cur += ch;
      started = true;
    }
  }
  if (started) out.push(cur);
  return out;
}

export type ProviderCommand =
  | { action: "list" }
  | { action: "form" }
  | { action: "remove"; name: string }
  | { action: "add"; name: string; baseUrl: string; model: string; apiKey?: string; apiKeyEnv?: string }
  | { action: "error"; message: string };

export const PROVIDER_USAGE =
  "usage: /provider add <name> <base-url> <model> [--key KEY | --key-env VAR]  ·  /provider list  ·  /provider remove <name>";

/**
 * Parses a typed "/provider add|list|remove ..." line, like the terminal UI's command. Returns null for anything
 * that is not "/provider" followed by arguments (plain "/provider" opens the picker instead).
 */
export function parseProviderCommand(line: string): ProviderCommand | null {
  const m = /^\/provider\s+(\S[\s\S]*)$/i.exec(line.trim());
  if (!m) return null;
  const words = splitArgs(m[1]);
  const sub = (words[0] ?? "").toLowerCase();
  if (sub === "list" || sub === "ls") return { action: "list" };
  if (["remove", "rm", "delete", "del"].includes(sub)) {
    return words[1] ? { action: "remove", name: words[1] } : { action: "error", message: "usage: /provider remove <name>" };
  }
  if (sub !== "add") return { action: "error", message: PROVIDER_USAGE };
  if (words.length === 1) return { action: "form" };
  const positional: string[] = [];
  let apiKey: string | undefined;
  let apiKeyEnv: string | undefined;
  for (let i = 1; i < words.length; i++) {
    const w = words[i];
    if (w === "--key" || w === "--key-env") {
      const v = words[++i];
      if (!v) return { action: "error", message: `${w} requires a value` };
      if (w === "--key") apiKey = v;
      else apiKeyEnv = v;
    } else if (w.startsWith("--")) {
      return { action: "error", message: `unknown option ${w}; ${PROVIDER_USAGE}` };
    } else {
      positional.push(w);
    }
  }
  if (positional.length !== 3) return { action: "error", message: PROVIDER_USAGE };
  if (apiKey && apiKeyEnv) return { action: "error", message: "use --key or --key-env, not both" };
  const [name, baseUrl, model] = positional;
  return { action: "add", name, baseUrl, model, ...(apiKey ? { apiKey } : {}), ...(apiKeyEnv ? { apiKeyEnv } : {}) };
}
