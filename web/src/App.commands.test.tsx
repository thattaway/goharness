// @vitest-environment jsdom
import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import type { Settings, Stats } from "./api";

// A tiny fake of the goharness web API, enough to drive the message box's slash commands.

let settings: Settings;
let calls: { method: string; path: string; body: unknown }[];
let providerList: { id: string; label: string; baseUrl: string; custom: boolean; hasApiKey: boolean; models?: string[] }[];

const stats: Stats = {
  turns: 2,
  toolCalls: 4,
  promptTokens: 63000,
  completionTokens: 2600,
  lastCachePct: 79,
  sessionCachePct: 70,
  avgTps: 24,
  avgTtftSeconds: 3.4,
  endpoint: "athena 24.1 tok/s",
  lastTps: 24.1,
  contextUsed: 17500,
  contextBudget: 35400,
  subagent: { model: "minicpm5_2B", running: false, count: 0, runningModel: "", calls: 0, elapsedSeconds: 0, runs: 1 },
};

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

class FakeEventSource {
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  constructor(public url: string) {}
  close() {}
}

beforeEach(() => {
  settings = { model: "m1", provider: "", plan: false, tdd: false, adhd: false, icm: false, auto: false, maxRounds: 10, subagentModel: "" };
  calls = [];
  providerList = [
    { id: "llama-swap", label: "llama-swap", baseUrl: "http://x/v1", custom: false, hasApiKey: false },
    { id: "freellmapi", label: "freellmapi", baseUrl: "http://y/v1", custom: false, hasApiKey: true },
  ];
  history.replaceState(null, "", "/s/t1");
  vi.stubGlobal("EventSource", FakeEventSource);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const method = init?.method ?? "GET";
      const path = url.split("?")[0];
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      calls.push({ method, path, body });
      if (path === "/api/info") return json({ projectRoot: "/p", defaultModel: "m1", allowAnyDir: false });
      if (path === "/api/sessions" && method === "GET") return json([]);
      if (path === "/api/projects") return json([]);
      if (path === "/api/models") return json(["m1", "m2", "m3"]);
      if (path === "/api/sessions/t1" && method === "GET") {
        return json({ id: "t1", messages: [], settings, running: false, since: 0, projectRoot: "/p", canUndo: false, turns: [] });
      }
      if (path === "/api/sessions/t1/skills") return json([{ name: "demo", description: "a demo skill", dir: "/x" }]);
      if (path === "/api/sessions/t1/stats") return json(stats);
      if (path === "/api/sessions/t1/settings" && method === "PUT") {
        settings = { ...settings, ...(body as Settings) };
        return json(settings);
      }
      if (path === "/api/providers/models" && method === "POST") {
        const b = body as { baseUrl: string };
        if (b.baseUrl.includes("bad")) return json({ error: "could not reach http://bad/v1: connection refused" }, 502);
        return json({ models: ["claude-y", "gpt-x"] });
      }
      if (path === "/api/providers" && method === "GET") return json(providerList);
      if (path === "/api/providers" && method === "POST") {
        const b = body as { name: string; baseUrl: string; model: string; apiKey?: string; apiKeyEnv?: string };
        if (b.name === "bad name") return json({ error: `provider name "${b.name}" must match ^[a-z0-9][a-z0-9._-]{0,29}$` }, 400);
        providerList = [...providerList, { id: b.name, label: b.name, baseUrl: b.baseUrl, custom: true, hasApiKey: !!(b.apiKey || b.apiKeyEnv), models: [b.model] }];
        return json({ id: b.name }, 201);
      }
      if (path.startsWith("/api/providers/") && method === "DELETE") {
        providerList = providerList.filter((p) => p.id !== decodeURIComponent(path.slice("/api/providers/".length)));
        return new Response(null, { status: 204 });
      }
      if (path === "/api/reload") return json({ summary: "reloaded the env file" });
      if (path === "/api/sessions/t1/compact") return json({ messagesBefore: 9, messagesAfter: 5, tokensBefore: 12000, tokensAfter: 3000 });
      return json({ error: `unexpected ${method} ${path}` }, 404);
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

async function openApp() {
  const user = userEvent.setup();
  render(<App />);
  const box = await screen.findByRole("textbox", { name: "Message" });
  await waitFor(() => expect(box).toHaveProperty("disabled", false));
  return { user, box };
}

describe("slash commands in the message box", () => {
  it("suggests the built-in commands before skills", async () => {
    const { user, box } = await openApp();
    await user.type(box, "/");
    const list = await screen.findByRole("listbox", { name: "Commands and skills" });
    const text = list.textContent ?? "";
    expect(text).toContain("/subagent");
    expect(text).toContain("/compact");
    expect(text).toContain("/demo"); // the installed skill still shows up
    expect(text.indexOf("/subagent")).toBeLessThan(text.indexOf("/demo"));
  });

  it("/subagent opens a model list and saves the choice", async () => {
    const { user, box } = await openApp();
    await user.type(box, "/subagent{Enter}");
    await screen.findByRole("listbox", { name: "Select the subagent model" });
    await user.click(screen.getByRole("button", { name: "m2" }));
    await waitFor(() => expect(settings.subagentModel).toBe("m2"));
    expect(await screen.findByText("Subagent model set to m2.")).toBeTruthy();
    const put = calls.filter((c) => c.method === "PUT").pop();
    expect(put?.body).toMatchObject({ model: "m1", subagentModel: "m2" }); // other settings are kept
  });

  it("toggles a mode from the box", async () => {
    const { user, box } = await openApp();
    await user.type(box, "/plan{Enter}");
    expect(await screen.findByText("plan on.")).toBeTruthy();
    expect(settings.plan).toBe(true);
    expect(box).toHaveProperty("value", "");
  });

  it("/compact calls the server and reports the saving", async () => {
    const { user, box } = await openApp();
    await user.type(box, "/compact{Enter}");
    expect(await screen.findByText(/Compacted: about 12\.0k → 3\.0k tokens/)).toBeTruthy();
    expect(calls.some((c) => c.method === "POST" && c.path === "/api/sessions/t1/compact")).toBe(true);
  });

  it("/reload and /help answer in the banner", async () => {
    const { user, box } = await openApp();
    await user.type(box, "/reload{Enter}");
    expect(await screen.findByText("reloaded the env file")).toBeTruthy();
    await user.type(box, "/help{Enter}");
    expect(await screen.findByText(/\/compact - summarize/)).toBeTruthy();
  });

  it("shows usage and the subagent under the transcript", async () => {
    await openApp();
    const line = await screen.findByRole("status", { name: "Session usage and subagent" });
    expect(line.textContent).toContain("63.0k in");
    expect(line.textContent).toContain("subagent minicpm5_2B · idle · 1 run done");
  });

  it("leaves ordinary messages and skill names alone", async () => {
    const { user, box } = await openApp();
    await user.type(box, "/demo");
    await user.keyboard("{Tab}");
    // Picking a skill fills in its name for arguments, as before; it is not run as a command.
    expect(box).toHaveProperty("value", "/demo ");
    expect(calls.some((c) => c.method === "PUT")).toBe(false);
  });

  it("/provider add saves the provider through the providers API and does not send it to the model", async () => {
    const { user, box } = await openApp();
    await user.type(box, "/provider add litellm http://h:4000/v1 gpt-x --key-env LK{Enter}");
    expect(await screen.findByText(/Provider "litellm" added with model "gpt-x"/)).toBeTruthy();
    const post = calls.find((c) => c.method === "POST" && c.path === "/api/providers");
    expect(post?.body).toEqual({ name: "litellm", baseUrl: "http://h:4000/v1", model: "gpt-x", apiKeyEnv: "LK" });
    expect(calls.some((c) => c.path.endsWith("/messages"))).toBe(false); // it was a command, not chat
    expect((box as HTMLTextAreaElement).value).toBe("");
  });

  it("/provider add shows the server's reason when it refuses", async () => {
    const { user, box } = await openApp();
    await user.type(box, '/provider add "bad name" http://h/v1 m{Enter}');
    expect(await screen.findByText(/must match/)).toBeTruthy();
    expect(calls.some((c) => c.path.endsWith("/messages"))).toBe(false);
  });

  it("/provider add with missing words prints the usage and calls nothing", async () => {
    const { user, box } = await openApp();
    await user.type(box, "/provider add onlyname{Enter}");
    expect(await screen.findByText(/usage: \/provider add <name> <base-url> <model>/)).toBeTruthy();
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });

  it("/provider list names the custom providers", async () => {
    providerList.push({ id: "litellm", label: "litellm", baseUrl: "http://h/v1", custom: true, hasApiKey: true });
    const { user, box } = await openApp();
    await user.type(box, "/provider list{Enter}");
    expect(await screen.findByText(/llama-swap, freellmapi, litellm \(custom, key set\)/)).toBeTruthy();
  });

  it("/provider remove deletes it", async () => {
    providerList.push({ id: "litellm", label: "litellm", baseUrl: "http://h/v1", custom: true, hasApiKey: false });
    const { user, box } = await openApp();
    await user.type(box, "/provider remove litellm{Enter}");
    expect(await screen.findByText('Provider "litellm" removed.')).toBeTruthy();
    expect(calls.some((c) => c.method === "DELETE" && c.path === "/api/providers/litellm")).toBe(true);
  });

  it("a provider you add shows up in the /provider picker without a reload", async () => {
    const { user, box } = await openApp();
    await user.type(box, "/provider add litellm http://h/v1 gpt-x{Enter}");
    await screen.findByText(/Provider "litellm" added/);
    await user.type(box, "/provider{Enter}");
    expect(await screen.findByRole("button", { name: "litellm" })).toBeTruthy();
  });

  it("plain /provider still opens the picker, and ordinary text is still sent", async () => {
    const { user, box } = await openApp();
    await user.type(box, "/provider{Enter}");
    expect(await screen.findByRole("listbox", { name: /provider/i })).toBeTruthy();
  });

  it("/provider add with nothing after it opens a form; Fetch models offers a list; Add saves it", async () => {
    const { user, box } = await openApp();
    await user.type(box, "/provider add{Enter}");
    const dialog = await screen.findByRole("dialog", { name: "Add a provider" });
    expect(calls.some((c) => c.path.endsWith("/messages"))).toBe(false);
    const add = within(dialog).getByRole("button", { name: "Add" });
    expect(add).toHaveProperty("disabled", true);
    await user.type(within(dialog).getByLabelText("Name"), "litellm");
    await user.type(within(dialog).getByLabelText("Base URL"), "http://h:4000/v1");
    await user.type(within(dialog).getByLabelText("API key variable"), "LK");
    expect(add).toHaveProperty("disabled", true); // still no model
    await user.click(within(dialog).getByRole("button", { name: "Fetch models" }));
    const list = await within(dialog).findByLabelText("Models on the server");
    expect(calls.find((c) => c.path === "/api/providers/models")?.body).toEqual({ baseUrl: "http://h:4000/v1", apiKeyEnv: "LK" });
    await user.selectOptions(list, "gpt-x");
    expect(within(dialog).getByLabelText("Model")).toHaveProperty("value", "gpt-x");
    expect(add).toHaveProperty("disabled", false);
    await user.click(add);
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add a provider" })).toBeNull());
    expect(calls.find((c) => c.method === "POST" && c.path === "/api/providers")?.body).toEqual({
      name: "litellm", baseUrl: "http://h:4000/v1", model: "gpt-x", apiKeyEnv: "LK",
    });
    expect(await screen.findByText(/Provider "litellm" added with model "gpt-x"/)).toBeTruthy();
  });

  it("the form shows why fetching models failed, and you can still type the model", async () => {
    const { user, box } = await openApp();
    await user.type(box, "/provider add{Enter}");
    const dialog = await screen.findByRole("dialog", { name: "Add a provider" });
    await user.type(within(dialog).getByLabelText("Base URL"), "http://bad/v1");
    await user.click(within(dialog).getByRole("button", { name: "Fetch models" }));
    expect((await within(dialog).findByRole("alert")).textContent).toContain("could not reach");
    expect(within(dialog).queryByLabelText("Models on the server")).toBeNull();
    await user.type(within(dialog).getByLabelText("Model"), "by-hand");
    expect(within(dialog).getByLabelText("Model")).toHaveProperty("value", "by-hand");
  });

  it("the form refuses a key variable and a key together, and Escape closes it", async () => {
    const { user, box } = await openApp();
    await user.type(box, "/provider add{Enter}");
    const dialog = await screen.findByRole("dialog", { name: "Add a provider" });
    await user.type(within(dialog).getByLabelText("Name"), "x");
    await user.type(within(dialog).getByLabelText("Base URL"), "http://h/v1");
    await user.type(within(dialog).getByLabelText("Model"), "m");
    await user.type(within(dialog).getByLabelText("API key variable"), "E");
    await user.type(within(dialog).getByLabelText("API key"), "k");
    await user.click(within(dialog).getByRole("button", { name: "Add" }));
    expect((await within(dialog).findByRole("alert")).textContent).toContain("not both");
    expect(calls.some((c) => c.method === "POST" && c.path === "/api/providers")).toBe(false);
    await user.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "Add a provider" })).toBeNull());
  });

  it("the /provider list ends with an 'Add a provider…' choice that opens the form", async () => {
    const { user, box } = await openApp();
    await user.type(box, "/provider{Enter}");
    await user.click(await screen.findByRole("button", { name: "+ Add a provider…" }));
    expect(await screen.findByRole("dialog", { name: "Add a provider" })).toBeTruthy();
    expect(settings.provider).toBe(""); // choosing it did not switch the provider
  });
});
