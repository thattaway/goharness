// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ConvGroup, SessionSummary } from "./api";
import { Sidebar } from "./Sidebar";
import { buildSections, groupKeyOf, moveTargets } from "./groups";

afterEach(cleanup);
beforeEach(() => localStorage.clear());

const now = new Date().toISOString();
const sessions: SessionSummary[] = [
  { id: "s1", title: "First chat", project: "goharness", updated: now, running: false, pinned: false },
  { id: "s2", title: "Second chat", project: "goharness", updated: now, running: false, pinned: true },
  { id: "s3", title: "Third chat", project: "tictactoe", updated: now, running: true, pinned: false },
];
const custom: ConvGroup[] = [{ id: "g1", name: "Ideas" }];

function setup(over: { sessions?: SessionSummary[]; groups?: ConvGroup[]; onCreateGroup?: () => Promise<ConvGroup | null> } = {}) {
  const props = {
    onSelect: vi.fn(),
    onNew: vi.fn(),
    onNewInGroup: vi.fn(),
    onChooseFolder: vi.fn(),
    onRename: vi.fn(),
    onPin: vi.fn(),
    onMove: vi.fn(),
    onCreateGroup: vi.fn(over.onCreateGroup ?? (async () => ({ id: "g9", name: "Made" }) as ConvGroup | null)),
    onRenameGroup: vi.fn(),
    onDeleteGroup: vi.fn(),
    onDelete: vi.fn(async () => {}),
    onLogout: vi.fn(),
    onClose: vi.fn(),
    onQuery: vi.fn(),
    onToggleCollapsed: vi.fn(),
  };
  render(<Sidebar sessions={over.sessions ?? sessions} projects={[]} groups={over.groups ?? custom} current="s1" open query="" {...props} />);
  return { props, user: userEvent.setup() };
}

function dataTransfer() {
  const data: Record<string, string> = {};
  return {
    data,
    setData: (k: string, v: string) => void (data[k] = v),
    getData: (k: string) => data[k] ?? "",
    dropEffect: "none",
    effectAllowed: "none",
  };
}

describe("grouping rules", () => {
  it("puts a conversation in its project unless it was moved", () => {
    const ids = new Set(["g1"]);
    expect(groupKeyOf(sessions[0], ids)).toBe("p:goharness");
    expect(groupKeyOf({ ...sessions[0], group: "g1" }, ids)).toBe("g1");
    expect(groupKeyOf({ ...sessions[0], group: "p:tictactoe" }, ids)).toBe("p:tictactoe");
    expect(groupKeyOf({ ...sessions[0], group: "gdeleted" }, ids)).toBe("p:goharness"); // an unknown id reads as no group
  });

  it("lists pinned ones once, then projects, then the user's groups (empty ones too)", () => {
    const { pinned, groups } = buildSections(sessions, custom, false);
    expect(pinned.map((s) => s.id)).toEqual(["s2"]);
    expect(groups.map((g) => g.name)).toEqual(["goharness", "tictactoe", "Ideas"]);
    expect(groups[0].sessions.map((s) => s.id)).toEqual(["s1"]);
    expect(groups[0].total).toBe(2); // the pinned one still counts
    expect(groups[2].sessions).toEqual([]);
  });

  it("hides empty groups while searching", () => {
    const { groups } = buildSections([sessions[2]], custom, true);
    expect(groups.map((g) => g.name)).toEqual(["tictactoe"]);
  });

  it("offers every project with conversations and every user group as a move target", () => {
    const moved = { ...sessions[0], group: "p:tictactoe" };
    expect(moveTargets([moved, sessions[2]], custom).map((t) => t.key)).toEqual(["p:goharness", "p:tictactoe", "g1"]);
  });
});

describe("Sidebar", () => {
  it("renames from the row menu", async () => {
    const { props, user } = setup();
    await user.click(screen.getByRole("button", { name: "Actions for First chat" }));
    await user.click(screen.getByRole("menuitem", { name: /Rename/ }));
    const input = screen.getByRole("textbox", { name: "Session name" });
    expect(input).toHaveProperty("value", "First chat");
    expect(document.activeElement).toBe(input);
    await user.clear(input);
    await user.type(input, "Renamed chat{Enter}");
    expect(props.onRename).toHaveBeenCalledWith("s1", "Renamed chat");
    expect(screen.queryByRole("textbox", { name: "Session name" })).toBeNull();
  });

  it("renames on double-click, and Escape cancels", async () => {
    const { props, user } = setup();
    await user.dblClick(screen.getByText("First chat"));
    await user.type(screen.getByRole("textbox", { name: "Session name" }), " edited{Escape}");
    expect(props.onRename).not.toHaveBeenCalled();
  });

  it("pins and unpins from the row menu", async () => {
    const { props, user } = setup();
    await user.click(screen.getByRole("button", { name: "Actions for First chat" }));
    await user.click(screen.getByRole("menuitem", { name: /Pin to top/ }));
    expect(props.onPin).toHaveBeenCalledWith("s1", true);
    await user.click(screen.getByRole("button", { name: "Actions for Second chat" }));
    await user.click(screen.getByRole("menuitem", { name: /Unpin/ }));
    expect(props.onPin).toHaveBeenCalledWith("s2", false);
  });

  it("pins with the pin button on the row", async () => {
    const { props, user } = setup();
    await user.click(screen.getByRole("button", { name: "Pin First chat" }));
    expect(props.onPin).toHaveBeenCalledWith("s1", true);
    await user.click(screen.getByRole("button", { name: "Unpin Second chat" }));
    expect(props.onPin).toHaveBeenCalledWith("s2", false);
  });

  it("shows Pinned first, then each project, then the user's groups", () => {
    setup();
    const names = screen.getAllByRole("region").map((r) => r.getAttribute("aria-label"));
    expect(names).toEqual(["Pinned", "goharness", "tictactoe", "Ideas"]);
    // Second chat is pinned, so it is listed once, under Pinned, tagged with its project.
    const pinned = screen.getByRole("region", { name: "Pinned" });
    expect(within(pinned).getByText("Second chat")).toBeTruthy();
    expect(within(pinned).getByText("goharness")).toBeTruthy();
    expect(within(screen.getByRole("region", { name: "goharness" })).queryByText("Second chat")).toBeNull();
    expect(within(screen.getByRole("region", { name: "Ideas" })).getByText("Drag a conversation here")).toBeTruthy();
  });

  it("collapses a group, and remembers it", async () => {
    const { user } = setup();
    const grp = screen.getByRole("region", { name: "tictactoe" });
    expect(within(grp).getByText("Third chat")).toBeTruthy();
    await user.click(within(grp).getByRole("button", { name: /^tictactoe\s*\d/ }));
    expect(within(screen.getByRole("region", { name: "tictactoe" })).queryByText("Third chat")).toBeNull();
    expect(JSON.parse(localStorage.getItem("goharness.collapsedGroups") ?? "[]")).toContain("p:tictactoe");
    cleanup();
    setup(); // a fresh render reads it back
    expect(within(screen.getByRole("region", { name: "tictactoe" })).queryByText("Third chat")).toBeNull();
  });

  it("selects all and deletes after confirming", async () => {
    const { props, user } = setup();
    await user.click(screen.getByRole("button", { name: "Select" }));
    await user.click(screen.getByRole("checkbox", { name: "Select all" }));
    expect(screen.getByText("3 selected")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Delete" }));
    const dialog = screen.getByRole("alertdialog");
    expect(within(dialog).getByText("Delete 3 sessions?")).toBeTruthy();
    expect(within(dialog).getByText(/still running and will be skipped/)).toBeTruthy();
    await user.click(within(dialog).getByRole("button", { name: "Delete" }));
    expect([...(props.onDelete.mock.calls[0] as unknown as [string[]])[0]].sort()).toEqual(["s1", "s2", "s3"]);
  });

  it("selects a range with shift-click, and cancel keeps everything", async () => {
    const { props, user } = setup();
    await user.click(screen.getByRole("button", { name: "Select" }));
    await user.click(screen.getByRole("checkbox", { name: "Select Second chat" }));
    await user.keyboard("{Shift>}");
    await user.click(screen.getByRole("checkbox", { name: "Select Third chat" }));
    await user.keyboard("{/Shift}");
    expect(screen.getByText("3 selected")).toBeTruthy();
    await user.click(screen.getByRole("checkbox", { name: "Select First chat" }));
    expect(screen.getByText("2 selected")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Delete" }));
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    expect(props.onDelete).not.toHaveBeenCalled();
  });

  it("searches, with Ctrl+K to jump to the box", async () => {
    const { props, user } = setup();
    await user.keyboard("{Control>}k{/Control}");
    const box = screen.getByRole("searchbox", { name: "Search sessions" });
    expect(document.activeElement).toBe(box);
    await user.type(box, "pg");
    expect(props.onQuery).toHaveBeenLastCalledWith("g"); // controlled: parent owns the value
    expect(props.onQuery).toHaveBeenCalledTimes(2);
  });

  it("shows where a search matched inside a session", () => {
    render(
      <Sidebar
        sessions={[{ ...sessions[0], match: "…set up postgres on the box…" }]}
        projects={[]}
        groups={[]}
        current={null}
        open
        query="postgres"
        onQuery={vi.fn()}
        onSelect={vi.fn()}
        onNew={vi.fn()}
        onNewInGroup={vi.fn()}
        onChooseFolder={vi.fn()}
        onRename={vi.fn()}
        onPin={vi.fn()}
        onMove={vi.fn()}
        onCreateGroup={vi.fn(async () => null)}
        onRenameGroup={vi.fn()}
        onDeleteGroup={vi.fn()}
        onDelete={vi.fn(async () => {})}
        onLogout={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    expect(screen.getByText("…set up postgres on the box…")).toBeTruthy();
  });

  it("deletes one session from its menu", async () => {
    const { props, user } = setup();
    await user.click(screen.getByRole("button", { name: "Actions for Third chat" }));
    await user.click(screen.getByRole("menuitem", { name: /Delete…/ }));
    await user.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Delete" }));
    expect(props.onDelete).toHaveBeenCalledWith(["s3"]);
  });
});

describe("moving conversations between groups", () => {
  it("moves from the row menu: other projects and your groups are offered", async () => {
    const { props, user } = setup();
    await user.click(screen.getByRole("button", { name: "Actions for First chat" }));
    await user.click(screen.getByRole("menuitem", { name: /Move to group/ }));
    const names = screen.getAllByRole("menuitem").map((m) => m.textContent?.replace(/✓/, "").trim());
    expect(names).toEqual(["goharness", "tictactoe", "Ideas", "New group…"]);
    await user.click(screen.getByRole("menuitem", { name: /Ideas/ }));
    expect(props.onMove).toHaveBeenCalledWith("s1", "g1");
  });

  it("moving to another project's group files it under that project", async () => {
    const { props, user } = setup();
    await user.click(screen.getByRole("button", { name: "Actions for First chat" }));
    await user.click(screen.getByRole("menuitem", { name: /Move to group/ }));
    await user.click(screen.getByRole("menuitem", { name: /tictactoe/ }));
    expect(props.onMove).toHaveBeenCalledWith("s1", "p:tictactoe");
  });

  it("offers 'Move back' for a conversation that was moved, and lists it under its new group", async () => {
    const moved = sessions.map((s) => (s.id === "s1" ? { ...s, group: "g1" } : s));
    const { props, user } = setup({ sessions: moved });
    expect(within(screen.getByRole("region", { name: "Ideas" })).getByText("First chat")).toBeTruthy();
    // Its folder is still shown, since it isn't the group it sits in.
    expect(within(screen.getByRole("region", { name: "Ideas" })).getByText("goharness")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Actions for First chat" }));
    await user.click(screen.getByRole("menuitem", { name: /Move back to goharness/ }));
    expect(props.onMove).toHaveBeenCalledWith("s1", "");
  });

  it("makes a new group from the footer", async () => {
    const { props, user } = setup();
    await user.click(screen.getByRole("button", { name: /New group/ }));
    await user.type(screen.getByRole("textbox", { name: "New group name" }), "Research{Enter}");
    expect(props.onCreateGroup).toHaveBeenCalledWith("Research");
    expect(props.onMove).not.toHaveBeenCalled();
  });

  it("'New group…' in the move menu creates the group and moves the conversation into it", async () => {
    const { props, user } = setup();
    await user.click(screen.getByRole("button", { name: "Actions for Third chat" }));
    await user.click(screen.getByRole("menuitem", { name: /Move to group/ }));
    await user.click(screen.getByRole("menuitem", { name: /New group…/ }));
    await user.type(screen.getByRole("textbox", { name: "New group name" }), "Docs{Enter}");
    expect(props.onCreateGroup).toHaveBeenCalledWith("Docs");
    await vi.waitFor(() => expect(props.onMove).toHaveBeenCalledWith("s3", "g9"));
  });

  it("does not move anything when group creation fails", async () => {
    const { props, user } = setup({ onCreateGroup: async () => null });
    await user.click(screen.getByRole("button", { name: "Actions for Third chat" }));
    await user.click(screen.getByRole("menuitem", { name: /Move to group/ }));
    await user.click(screen.getByRole("menuitem", { name: /New group…/ }));
    await user.type(screen.getByRole("textbox", { name: "New group name" }), "Docs{Enter}");
    await vi.waitFor(() => expect(props.onCreateGroup).toHaveBeenCalled());
    expect(props.onMove).not.toHaveBeenCalled();
  });

  it("drags a conversation onto a group, onto Pinned, and back onto its own project", () => {
    const moved = sessions.map((s) => (s.id === "s3" ? { ...s, group: "g1" } : s));
    const { props } = setup({ sessions: moved });
    const row = (title: string) => screen.getByText(title).closest("li")!;

    let dt = dataTransfer();
    fireEvent.dragStart(row("First chat"), { dataTransfer: dt });
    fireEvent.dragOver(screen.getByRole("region", { name: "Ideas" }), { dataTransfer: dt });
    expect(screen.getByRole("region", { name: "Ideas" }).className).toContain("drop");
    fireEvent.drop(screen.getByRole("region", { name: "Ideas" }), { dataTransfer: dt });
    expect(props.onMove).toHaveBeenLastCalledWith("s1", "g1");

    dt = dataTransfer();
    fireEvent.dragStart(row("First chat"), { dataTransfer: dt });
    fireEvent.drop(screen.getByRole("region", { name: "Pinned" }), { dataTransfer: dt });
    expect(props.onPin).toHaveBeenCalledWith("s1", true);

    // Third chat was moved into Ideas; dropping it on its own project sends it back.
    dt = dataTransfer();
    fireEvent.dragStart(row("Third chat"), { dataTransfer: dt });
    fireEvent.drop(screen.getByRole("region", { name: "tictactoe" }), { dataTransfer: dt });
    expect(props.onMove).toHaveBeenLastCalledWith("s3", "");
  });

  it("ignores a drop on the group it is already in", () => {
    const { props } = setup();
    const dt = dataTransfer();
    fireEvent.dragStart(screen.getByText("First chat").closest("li")!, { dataTransfer: dt });
    fireEvent.drop(screen.getByRole("region", { name: "goharness" }), { dataTransfer: dt });
    expect(props.onMove).not.toHaveBeenCalled();
  });
});

describe("group actions", () => {
  it("starts a conversation in a group from the + button", async () => {
    const { props, user } = setup();
    await user.click(screen.getByRole("button", { name: "New conversation in tictactoe" }));
    expect(props.onNewInGroup).toHaveBeenCalledWith("p:tictactoe");
    await user.click(screen.getByRole("button", { name: "New conversation in Ideas" }));
    expect(props.onNewInGroup).toHaveBeenLastCalledWith("g1");
  });

  it("renames and deletes a group you made, but not a project's", async () => {
    const { props, user } = setup();
    await user.click(screen.getByRole("button", { name: "Actions for group Ideas" }));
    await user.click(screen.getByRole("menuitem", { name: /Rename group/ }));
    const input = screen.getByRole("textbox", { name: "Group name" });
    await user.clear(input);
    await user.type(input, "Plans{Enter}");
    expect(props.onRenameGroup).toHaveBeenCalledWith("g1", "Plans");

    await user.click(screen.getByRole("button", { name: "Actions for group Ideas" }));
    await user.click(screen.getByRole("menuitem", { name: /Delete group/ }));
    expect(props.onDeleteGroup).toHaveBeenCalledWith("g1");

    await user.click(screen.getByRole("button", { name: "Actions for group tictactoe" }));
    expect(screen.queryByRole("menuitem", { name: /Delete group/ })).toBeNull();
    expect(screen.queryByRole("menuitem", { name: /Rename group/ })).toBeNull();
    expect(screen.getByRole("menuitem", { name: /New conversation here/ })).toBeTruthy();
  });

  it("collapses the whole sidebar from its button, and Ctrl+K opens it again", async () => {
    const { props, user } = setup();
    await user.click(screen.getByRole("button", { name: "Collapse sidebar" }));
    expect(props.onToggleCollapsed).toHaveBeenCalledTimes(1);
    cleanup();
    const props2 = { onToggleCollapsed: vi.fn() };
    render(
      <Sidebar
        sessions={sessions}
        projects={[]}
        groups={[]}
        current={null}
        open={false}
        collapsed
        query=""
        onQuery={vi.fn()}
        onSelect={vi.fn()}
        onNew={vi.fn()}
        onNewInGroup={vi.fn()}
        onChooseFolder={vi.fn()}
        onRename={vi.fn()}
        onPin={vi.fn()}
        onMove={vi.fn()}
        onCreateGroup={vi.fn(async () => null)}
        onRenameGroup={vi.fn()}
        onDeleteGroup={vi.fn()}
        onDelete={vi.fn(async () => {})}
        onLogout={vi.fn()}
        onClose={vi.fn()}
        {...props2}
      />,
    );
    await userEvent.setup().keyboard("{Control>}k{/Control}");
    expect(props2.onToggleCollapsed).toHaveBeenCalledTimes(1); // asks the parent to show the sidebar
  });
});
