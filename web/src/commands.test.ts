import { describe, expect, it } from "vitest";
import { COMMANDS, helpText, isToggle, matchCommands, parseProviderCommand, splitArgs } from "./commands";
import { subagentText, usageText } from "./stats";
import type { Stats } from "./api";

describe("matchCommands", () => {
  it("lists every command for a bare slash", () => {
    expect(matchCommands("")).toHaveLength(COMMANDS.length);
  });

  it("puts commands that start with the word first", () => {
    const names = matchCommands("sub").map((c) => c.name);
    expect(names[0]).toBe("subagent");
  });

  it("also matches the help text", () => {
    const names = matchCommands("summarize").map((c) => c.name);
    expect(names).toContain("compact");
  });

  it("finds nothing for an unknown word", () => {
    expect(matchCommands("zzzz")).toEqual([]);
  });

  it("marks the model-style commands as pickers and the toggles as toggles", () => {
    for (const n of ["model", "subagent", "provider"]) {
      expect(COMMANDS.find((c) => c.name === n)?.picker).toBe(true);
    }
    expect(isToggle("plan")).toBe(true);
    expect(isToggle("compact")).toBe(false);
  });

  it("describes every command in the help text", () => {
    const text = helpText();
    for (const c of COMMANDS) expect(text).toContain(`/${c.name}`);
  });
});

const base: Stats = {
  turns: 2,
  toolCalls: 5,
  promptTokens: 63000,
  completionTokens: 2600,
  lastCachePct: 78.6,
  sessionCachePct: 70,
  avgTps: 24,
  avgTtftSeconds: 3.4,
  endpoint: "athena 24.1 tok/s",
  lastTps: 24.1,
  contextUsed: 17500,
  contextBudget: 35400,
  subagent: { model: "minicpm5_2B", running: false, count: 0, runningModel: "", calls: 0, elapsedSeconds: 0, runs: 0 },
};

describe("status line text", () => {
  it("summarizes usage", () => {
    expect(usageText(base)).toBe("63.0k in · 2.6k out · cache 79% · ctx 17.5k/35.4k");
  });

  it("shows nothing before the first call", () => {
    expect(usageText({ ...base, turns: 0, promptTokens: 0, completionTokens: 0 })).toBeNull();
  });

  it("shows an idle subagent with its model", () => {
    expect(subagentText(base)).toBe("subagent minicpm5_2B · idle");
  });

  it("counts finished runs", () => {
    expect(subagentText({ ...base, subagent: { ...base.subagent, runs: 1 } })).toBe("subagent minicpm5_2B · idle · 1 run done");
    expect(subagentText({ ...base, subagent: { ...base.subagent, runs: 3 } })).toBe("subagent minicpm5_2B · idle · 3 runs done");
  });

  it("shows a running subagent with its calls and time", () => {
    const s = { ...base, subagent: { ...base.subagent, running: true, runningModel: "qwen35_9b", calls: 1, elapsedSeconds: 38 } };
    expect(subagentText(s)).toBe("subagent qwen35_9b · running 1 call · 38s");
  });

  it("says how many subagents are running when there are several", () => {
    const s = { ...base, subagent: { ...base.subagent, running: true, count: 2, runningModel: "qwen35_9b", calls: 5, elapsedSeconds: 12 } };
    expect(subagentText(s)).toBe("2 subagents · running 5 calls · 12s");
  });
});

describe("/provider add | list | remove", () => {
  const p = parseProviderCommand;
  it("parses add with a key variable", () => {
    expect(p("/provider add litellm http://h:4000/v1 gpt-x --key-env LK")).toEqual({
      action: "add", name: "litellm", baseUrl: "http://h:4000/v1", model: "gpt-x", apiKeyEnv: "LK",
    });
  });
  it("parses add with a raw key and quoted words", () => {
    expect(p(`/provider add x http://h/v1 "my model" --key 'a b'`)).toEqual({
      action: "add", name: "x", baseUrl: "http://h/v1", model: "my model", apiKey: "a b",
    });
  });
  it("takes the options before or after the positional words", () => {
    expect(p("/provider add --key-env K x http://h/v1 m")).toMatchObject({ action: "add", name: "x", model: "m", apiKeyEnv: "K" });
  });
  it("parses list and remove, with their aliases", () => {
    expect(p("/provider list")).toEqual({ action: "list" });
    expect(p("/provider ls")).toEqual({ action: "list" });
    expect(p("/provider remove litellm")).toEqual({ action: "remove", name: "litellm" });
    expect(p("/provider rm litellm")).toEqual({ action: "remove", name: "litellm" });
  });
  it("a bare /provider add means: show the form", () => {
    expect(p("/provider add")).toEqual({ action: "form" });
    expect(p("  /provider   add  ")).toEqual({ action: "form" });
  });
  it("explains mistakes instead of sending them to the model", () => {
    expect(p("/provider add onlyname")).toMatchObject({ action: "error" });
    expect(p("/provider add a http://h/v1 m --key")).toEqual({ action: "error", message: "--key requires a value" });
    expect(p("/provider add a http://h/v1 m --key k --key-env E")).toEqual({ action: "error", message: "use --key or --key-env, not both" });
    expect(p("/provider add a http://h/v1 m --nope")).toMatchObject({ action: "error" });
    expect(p("/provider remove")).toEqual({ action: "error", message: "usage: /provider remove <name>" });
    expect(p("/provider wat")).toMatchObject({ action: "error" });
  });
  it("leaves everything else alone", () => {
    expect(p("/provider")).toBeNull(); // the picker
    expect(p("/providers add")).toBeNull();
    expect(p("/model add x")).toBeNull();
    expect(p("please /provider add x y z")).toBeNull();
  });
});

describe("splitArgs", () => {
  it("honors quotes and escapes", () => {
    expect(splitArgs(`a "b c" 'd e' f\\ g ""`)).toEqual(["a", "b c", "d e", "f g", ""]);
  });
});
