// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import type { ConvGroup, SessionSummary, Settings } from "./api";
import { Transcript } from "./Transcript";
import type { Item } from "./transcript";

// A fake of the goharness web API with two conversations, to drive the top bar, the sidebar and the theme.

let settings: Settings;
let sessions: SessionSummary[];
let groups: ConvGroup[];
let calls: { method: string; path: string; body: unknown }[];

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
  localStorage.clear();
  delete document.documentElement.dataset.theme;
  settings = { model: "m1", provider: "", plan: false, tdd: false, adhd: false, icm: false, auto: false, maxRounds: 10, subagentModel: "" };
  const now = new Date().toISOString();
  sessions = [
    { id: "t1", title: "Background jobs", project: "higgsfield-api", updated: now, running: false, pinned: false },
    { id: "t2", title: "How the model is called", project: "sparky-automation", updated: now, running: false, pinned: false },
  ];
  groups = [];
  calls = [];
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
      if (path === "/api/sessions" && method === "GET") return json(sessions);
      if (path === "/api/projects") return json([]);
      if (path === "/api/models") return json(["m1", "m2", "other-model"]);
      if (path === "/api/groups" && method === "GET") return json(groups);
      if (path === "/api/groups" && method === "POST") {
        const g = { id: "g" + (groups.length + 1), name: (body as { name: string }).name };
        groups = [...groups, g];
        return json(g, 201);
      }
      if (path === "/api/sessions/t1" && method === "GET") {
        return json({ id: "t1", messages: [], settings, running: false, since: 0, projectRoot: "/p/higgsfield-api", canUndo: false, turns: [] });
      }
      if (/^\/api\/sessions\/t[12]$/.test(path) && method === "PATCH") {
        const s = sessions.find((x) => path.endsWith(x.id))!;
        const patch = body as { pinned?: boolean; group?: string; title?: string };
        if (patch.pinned !== undefined) s.pinned = patch.pinned;
        if (patch.group !== undefined) s.group = patch.group || undefined;
        return json({ title: s.title, pinned: s.pinned, group: s.group ?? "" });
      }
      if (path.endsWith("/skills")) return json([]);
      if (path.endsWith("/stats")) return json(null, 404);
      if (path === "/api/sessions/t1/settings" && method === "PUT") {
        settings = { ...settings, ...(body as Settings) };
        return json(settings);
      }
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
  await screen.findByRole("textbox", { name: "Message" });
  await waitFor(() => expect(screen.getByRole("textbox", { name: "Message" })).toHaveProperty("disabled", false));
  return user;
}

describe("top bar", () => {
  it("shows provider and model in one pill, and switches model from its list", async () => {
    const user = await openApp();
    const pill = await screen.findByRole("button", { name: /Provider and model/ }).catch(() => screen.getByTitle("Provider and model for this session"));
    expect(pill.textContent).toContain("llama-swap");
    expect(pill.textContent).toContain("m1");
    await user.click(pill);
    const dialog = await screen.findByRole("dialog", { name: "Choose a model" });
    await user.type(within(dialog).getByRole("textbox", { name: "Search models" }), "other");
    expect(within(dialog).queryByRole("option", { name: /m2/ })).toBeNull();
    await user.click(within(dialog).getByRole("option", { name: /other-model/ }));
    await waitFor(() => expect(settings.model).toBe("other-model"));
    expect(screen.queryByRole("dialog", { name: "Choose a model" })).toBeNull(); // picking closes the list
  });

  it("switches provider from the same list", async () => {
    const user = await openApp();
    await user.click(screen.getByTitle("Provider and model for this session"));
    await user.click(screen.getByRole("button", { name: "freellmapi" }));
    await waitFor(() => expect(settings.provider).toBe("freellmapi"));
    expect(settings.model).toBe("auto"); // freellmapi's always-valid model, as in the terminal UI
  });

  it("counts the modes that are on and toggles them from one menu", async () => {
    const user = await openApp();
    const modes = screen.getByRole("button", { name: /Modes/ });
    expect(within(modes).queryByText("1")).toBeNull();
    await user.click(modes);
    await user.click(screen.getByRole("menuitemcheckbox", { name: /Plan/ }));
    await waitFor(() => expect(settings.plan).toBe(true));
    await waitFor(() => expect(within(screen.getByRole("button", { name: /Modes/ })).getByText("1")).toBeTruthy());
    expect(screen.getByRole("menuitemcheckbox", { name: /Plan/ }).getAttribute("aria-checked")).toBe("true");
    // The active mode also shows as a chip on the message box; clicking it turns the mode off.
    await user.click(screen.getByRole("button", { name: "Turn off Plan mode" }));
    await waitFor(() => expect(settings.plan).toBe(false));
  });

  it("switches between the light and dark theme and remembers it", async () => {
    const user = await openApp();
    const first = document.documentElement.dataset.theme;
    expect(["light", "dark"]).toContain(first);
    await user.click(screen.getByRole("button", { name: /Switch to the (light|dark) theme/ }));
    const second = document.documentElement.dataset.theme;
    expect(second).not.toBe(first);
    expect(localStorage.getItem("goharness.theme")).toBe(second);
  });
});

describe("sidebar", () => {
  it("hides and shows from the top bar button, and remembers it", async () => {
    const user = await openApp();
    const app = document.querySelector(".app")!;
    expect(app.className).not.toContain("sidebar-hidden");
    await user.click(screen.getByRole("button", { name: "Hide sidebar" }));
    expect(app.className).toContain("sidebar-hidden");
    expect(localStorage.getItem("goharness.sidebarHidden")).toBe("1");
    await user.click(screen.getByRole("button", { name: "Show sidebar" }));
    expect(app.className).not.toContain("sidebar-hidden");
    expect(localStorage.getItem("goharness.sidebarHidden")).toBe("0");
  });

  it("groups conversations by project", async () => {
    await openApp();
    const nav = await screen.findByRole("navigation", { name: "Sessions" });
    await within(nav).findByRole("region", { name: "higgsfield-api" });
    expect(within(nav).getByRole("region", { name: "sparky-automation" })).toBeTruthy();
    expect(within(nav).getByRole("region", { name: "Pinned" })).toBeTruthy();
  });

  it("creates a group and moves a conversation into it", async () => {
    const user = await openApp();
    const nav = await screen.findByRole("navigation", { name: "Sessions" });
    await within(nav).findByText("How the model is called");
    await user.click(within(nav).getByRole("button", { name: "Actions for How the model is called" }));
    await user.click(screen.getByRole("menuitem", { name: /Move to group/ }));
    await user.click(screen.getByRole("menuitem", { name: /New group…/ }));
    await user.type(within(nav).getByRole("textbox", { name: "New group name" }), "Research{Enter}");
    await waitFor(() => expect(calls.some((c) => c.method === "POST" && c.path === "/api/groups")).toBe(true));
    await waitFor(() => expect(sessions[1].group).toBe("g1"));
    const research = await within(nav).findByRole("region", { name: "Research" });
    expect(within(research).getByText("How the model is called")).toBeTruthy();
  });

  it("pins a conversation from its row", async () => {
    const user = await openApp();
    const nav = await screen.findByRole("navigation", { name: "Sessions" });
    await user.click(await within(nav).findByRole("button", { name: "Pin Background jobs" }));
    await waitFor(() => expect(sessions[0].pinned).toBe(true));
    const pinned = await within(nav).findByRole("region", { name: "Pinned" });
    await waitFor(() => expect(within(pinned).getByText("Background jobs")).toBeTruthy());
  });
});

describe("conversation", () => {
  const toolItem = (over: Partial<Extract<Item, { kind: "tool" }>>): Item => ({
    kind: "tool",
    key: "k" + Math.random(),
    id: "c1",
    name: "bash",
    args: JSON.stringify({ command: "ls -R lib" }),
    status: "done",
    output: "lib/worker.ts",
    ...over,
  });
  const base = { running: false, activity: null, lastEventAt: 0, onApprove: vi.fn(), onReviewChanges: vi.fn() };

  it("shows a finished tool call as one closed row, with its details inside", () => {
    render(<Transcript {...base} items={[toolItem({})]} />);
    const row = document.querySelector("details.tool") as HTMLDetailsElement;
    expect(row.open).toBe(false);
    expect(within(row).getByText("bash")).toBeTruthy();
    expect(within(row).getByText("ls -R lib")).toBeTruthy();
    expect(within(row).getByText("done")).toBeTruthy();
  });

  it("keeps a call that needs approval open, with its buttons", async () => {
    const onApprove = vi.fn();
    render(<Transcript {...base} onApprove={onApprove} items={[toolItem({ status: "pending", output: undefined, reason: "run: rm x" })]} />);
    const row = document.querySelector("details.tool") as HTMLDetailsElement;
    expect(row.open).toBe(true);
    await userEvent.setup().click(screen.getByRole("button", { name: "Approve" }));
    expect(onApprove).toHaveBeenCalledWith("c1", true);
  });

  it("puts a subagent's steps inside its spawn_agent row", () => {
    const items: Item[] = [
      toolItem({ name: "spawn_agent", args: JSON.stringify({ task: "find the worker" }), output: "done" }),
      { kind: "notice", key: "n1", text: 'subagent: bash {"command":"ls -R lib"}' },
      { kind: "notice", key: "n2", text: 'subagent: read_file {"path":"lib/worker.ts"}' },
      { kind: "notice", key: "n3", text: "an ordinary notice" },
    ];
    render(<Transcript {...base} items={items} />);
    const steps = screen.getByLabelText("Subagent steps");
    expect(within(steps).getByText("ls -R lib")).toBeTruthy();
    expect(within(steps).getByText("lib/worker.ts")).toBeTruthy();
    // The steps are inside the row, not repeated as separate notices; other notices stay.
    expect(screen.queryAllByText(/^subagent: /)).toHaveLength(0);
    expect(screen.getByText("an ordinary notice")).toBeTruthy();
  });

  it("keeps the steps of subagents that run at the same time in their own rows", () => {
    const items: Item[] = [
      toolItem({ id: "c1", name: "spawn_agent", args: JSON.stringify({ task: "review the worker" }), output: "done" }),
      toolItem({ id: "c2", name: "spawn_agent", args: JSON.stringify({ task: "review the db" }), output: "done" }),
      { kind: "notice", key: "n1", id: "c2", text: 'subagent: read_file {"path":"lib/db.ts"}' },
      { kind: "notice", key: "n2", id: "c1", text: 'subagent: read_file {"path":"lib/worker.ts"}' },
      { kind: "notice", key: "n3", id: "c2", text: 'subagent: bash {"command":"rg query lib"}' },
      { kind: "notice", key: "n4", text: "an ordinary notice" },
    ];
    render(<Transcript {...base} items={items} />);
    const rows = Array.from(document.querySelectorAll("details.tool")) as HTMLElement[];
    expect(rows).toHaveLength(2);
    const [first, second] = rows.map((r) => within(r).getByLabelText("Subagent steps"));
    expect(within(first).getByText("lib/worker.ts")).toBeTruthy();
    expect(within(first).queryByText("lib/db.ts")).toBeNull();
    expect(within(second).getByText("lib/db.ts")).toBeTruthy();
    expect(within(second).getByText("rg query lib")).toBeTruthy();
    expect(screen.queryAllByText(/^subagent: /)).toHaveLength(0);
    expect(screen.getByText("an ordinary notice")).toBeTruthy();
  });

  it("shows a copy button on code blocks", async () => {
    const writeText = vi.fn(async () => {});
    vi.stubGlobal("navigator", { ...navigator, clipboard: { writeText } });
    const items: Item[] = [{ kind: "agent", key: "a1", text: "Here:\n\n```ts\nconst x = 1;\n```\n", streaming: false }];
    render(<Transcript {...base} items={items} />);
    expect(screen.getByText("ts")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Copy code" }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith("const x = 1;"));
    expect(await screen.findByText("Copied")).toBeTruthy();
  });
});
