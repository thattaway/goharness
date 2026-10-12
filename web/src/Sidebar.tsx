import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type DragEvent, type MouseEvent as ReactMouseEvent } from "react";
import { createPortal } from "react-dom";
import type { ConvGroup, Project, SessionSummary } from "./api";
import { buildSections, groupKeyOf, groupNameOf, moveTargets, ownGroupKey, type GroupSection } from "./groups";
import { Icon } from "./icons";

function relativeTime(iso: string): string {
  const d = new Date(iso);
  const mins = Math.round((Date.now() - d.getTime()) / 60000);
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.round(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  return d.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

const COLLAPSED_KEY = "goharness.collapsedGroups";

function loadCollapsed(): Set<string> {
  try {
    const raw = JSON.parse(localStorage.getItem(COLLAPSED_KEY) ?? "[]");
    return new Set(Array.isArray(raw) ? raw.filter((x): x is string => typeof x === "string") : []);
  } catch {
    return new Set();
  }
}

// Closes a popover when clicking anywhere outside refs.
function useClickOutside(open: boolean, ref: React.RefObject<HTMLElement | null>, close: () => void) {
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) close();
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && close();
    window.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [open, ref, close]);
}

function NewSessionButton({
  projects,
  onNew,
  onChooseFolder,
}: {
  projects: Project[];
  onNew: (project?: string) => void;
  onChooseFolder: () => void;
}) {
  const [menu, setMenu] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useClickOutside(menu, ref, () => setMenu(false));
  return (
    <div className="menu-wrap" ref={ref}>
      <button className="btn btn-primary btn-sm" aria-haspopup="menu" aria-expanded={menu} onClick={() => setMenu(!menu)}>
        <Icon name="plus" size={14} />
        New
      </button>
      {menu && (
        <div className="menu" role="menu">
          <button
            role="menuitem"
            className="menu-item menu-item-strong"
            onClick={() => {
              setMenu(false);
              onChooseFolder();
            }}
          >
            Choose folder…
          </button>
          <div className="menu-label">Projects</div>
          {projects.map((p) => (
            <button
              key={p.path}
              role="menuitem"
              className="menu-item"
              title={p.path}
              onClick={() => {
                setMenu(false);
                onNew(p.path);
              }}
            >
              {p.name}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

function RenameInput({
  initial,
  onDone,
  label = "Session name",
  placeholder = "Name (empty = first message)",
}: {
  initial: string;
  onDone: (title: string | null) => void;
  label?: string;
  placeholder?: string;
}) {
  const [value, setValue] = useState(initial);
  const ref = useRef<HTMLInputElement>(null);
  const done = useRef(false);
  useEffect(() => {
    ref.current?.focus();
    ref.current?.select();
  }, []);
  const finish = (title: string | null) => {
    if (done.current) return;
    done.current = true;
    onDone(title);
  };
  return (
    <input
      ref={ref}
      className="rename-input"
      value={value}
      maxLength={120}
      aria-label={label}
      placeholder={placeholder}
      onChange={(e) => setValue(e.target.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter") finish(value);
        if (e.key === "Escape") finish(null);
      }}
      onBlur={() => finish(value)}
    />
  );
}

function DeleteConfirm({
  sessions,
  onConfirm,
  onClose,
}: {
  sessions: SessionSummary[];
  onConfirm: () => void;
  onClose: () => void;
}) {
  const confirmRef = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    confirmRef.current?.focus();
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  const n = sessions.length;
  const running = sessions.filter((s) => s.running).length;
  return (
    <div className="modal-scrim" onClick={onClose}>
      <div className="modal modal-sm" role="alertdialog" aria-modal="true" aria-labelledby="del-title" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <h2 id="del-title">
            Delete {n === 1 ? "this session" : `${n} sessions`}?
          </h2>
        </div>
        <div className="modal-body">
          <ul className="delete-list">
            {sessions.slice(0, 6).map((s) => (
              <li key={s.id}>{s.title || "New session"}</li>
            ))}
            {n > 6 && <li className="hint">and {n - 6} more</li>}
          </ul>
          <p className="hint">
            They disappear from the list. The files move to <code>~/.goharness/trash</code> and are cleared after 30 days.
            Project files and undo history for the folder aren't affected.
          </p>
          {running > 0 && (
            <p className="hint">
              {running === 1 ? "One is" : `${running} are`} still running and will be skipped.
            </p>
          )}
          <div className="row end">
            <button className="btn" onClick={onClose}>
              Cancel
            </button>
            <button ref={confirmRef} className="btn btn-danger" onClick={onConfirm}>
              Delete
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

type MenuState =
  | { kind: "row"; id: string; view: "main" | "move"; rect: DOMRect }
  | { kind: "group"; key: string; rect: DOMRect };

/** A menu placed on the viewport next to an anchor (the list scrolls and clips, so it is portalled to <body>). */
function FloatingMenu({ rect, onClose, children }: { rect: DOMRect; onClose: () => void; children: React.ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ top: rect.bottom + 4, left: Math.max(8, rect.right - 220) });
  useLayoutEffect(() => {
    const h = ref.current?.offsetHeight ?? 0;
    const top = rect.bottom + 4 + h > window.innerHeight ? Math.max(8, rect.top - h - 4) : rect.bottom + 4;
    setPos({ top, left: Math.max(8, Math.min(rect.right - 220, window.innerWidth - 232)) });
  }, [rect, children]);
  useClickOutside(true, ref, onClose);
  useEffect(() => {
    window.addEventListener("resize", onClose);
    window.addEventListener("scroll", onClose, true);
    return () => {
      window.removeEventListener("resize", onClose);
      window.removeEventListener("scroll", onClose, true);
    };
  }, [onClose]);
  return createPortal(
    <div ref={ref} className="menu menu-fixed" role="menu" style={{ top: pos.top, left: pos.left }}>
      {children}
    </div>,
    document.body,
  );
}

function MenuItem({
  icon,
  label,
  onClick,
  danger = false,
  hint,
}: {
  icon: Parameters<typeof Icon>[0]["name"];
  label: string;
  onClick: () => void;
  danger?: boolean;
  hint?: string;
}) {
  return (
    <button
      role="menuitem"
      className={`menu-item ${danger ? "menu-item-danger" : ""}`}
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
    >
      <Icon name={icon} size={15} />
      <span className="menu-item-label">{label}</span>
      {hint && <span className="menu-item-hint">{hint}</span>}
    </button>
  );
}

export function Sidebar({
  sessions,
  projects,
  groups,
  current,
  onSelect,
  onNew,
  onNewInGroup,
  onChooseFolder,
  query,
  onQuery,
  onRename,
  onPin,
  onMove,
  onCreateGroup,
  onRenameGroup,
  onDeleteGroup,
  onDelete,
  onLogout,
  open,
  onClose,
  collapsed = false,
  onToggleCollapsed,
}: {
  query: string;
  onQuery: (q: string) => void;
  sessions: SessionSummary[];
  projects: Project[];
  groups: ConvGroup[];
  current: string | null;
  onSelect: (id: string) => void;
  onNew: (project?: string) => void;
  /** Start a conversation in a group (a project's own, or one the user made). */
  onNewInGroup: (groupKey: string) => void;
  onChooseFolder: () => void;
  onRename: (id: string, title: string) => void;
  onPin: (id: string, pinned: boolean) => void;
  /** Move a conversation to a group; "" puts it back in its project. */
  onMove: (id: string, groupKey: string) => void;
  /** Create a group; resolves to it, or null when that failed (the parent reports why). */
  onCreateGroup: (name: string) => Promise<ConvGroup | null>;
  onRenameGroup: (id: string, name: string) => void;
  onDeleteGroup: (id: string) => void;
  onDelete: (ids: string[]) => Promise<void>;
  onLogout: () => void;
  open: boolean;
  onClose: () => void;
  collapsed?: boolean;
  onToggleCollapsed?: () => void;
}) {
  const [selecting, setSelecting] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [anchor, setAnchor] = useState<string | null>(null);
  const [renaming, setRenaming] = useState<string | null>(null);
  const [renamingGroup, setRenamingGroup] = useState<string | null>(null);
  const [confirm, setConfirm] = useState<SessionSummary[] | null>(null);
  const [menu, setMenu] = useState<MenuState | null>(null);
  const [collapsedGroups, setCollapsedGroups] = useState<Set<string>>(loadCollapsed);
  const [creating, setCreating] = useState<{ moveId: string | null } | null>(null);
  const [dropKey, setDropKey] = useState<string | null>(null);
  const selectAllRef = useRef<HTMLInputElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const focusSearch = useRef(false);

  const searching = query.trim() !== "";
  const customIds = useMemo(() => new Set(groups.map((g) => g.id)), [groups]);
  const { pinned, groups: sections } = useMemo(() => buildSections(sessions, groups, searching), [sessions, groups, searching]);
  const targets = useMemo(() => moveTargets(sessions, groups), [sessions, groups]);
  const closeMenu = useCallback(() => setMenu(null), []);

  // Ctrl/Cmd+K jumps to search from anywhere, opening the sidebar if it is collapsed.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        if (collapsed) {
          focusSearch.current = true;
          onToggleCollapsed?.();
        } else {
          searchRef.current?.focus();
          searchRef.current?.select();
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [collapsed, onToggleCollapsed]);
  useEffect(() => {
    if (!collapsed && focusSearch.current) {
      focusSearch.current = false;
      searchRef.current?.focus();
    }
  }, [collapsed]);

  // Forget selections of sessions that no longer exist.
  useEffect(() => {
    const ids = new Set(sessions.map((s) => s.id));
    setSelected((prev) => {
      const next = new Set([...prev].filter((id) => ids.has(id)));
      return next.size === prev.size ? prev : next;
    });
  }, [sessions]);

  const allSelected = sessions.length > 0 && selected.size === sessions.length;
  useEffect(() => {
    if (selectAllRef.current) selectAllRef.current.indeterminate = selected.size > 0 && !allSelected;
  }, [selected, allSelected]);

  const exitSelect = () => {
    setSelecting(false);
    setSelected(new Set());
    setAnchor(null);
  };
  useEffect(() => {
    if (!selecting) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && !confirm && exitSelect();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [selecting, confirm]);

  const toggleGroup = (key: string) => {
    setCollapsedGroups((prev) => {
      const next = new Set(prev);
      if (!next.delete(key)) next.add(key);
      try {
        localStorage.setItem(COLLAPSED_KEY, JSON.stringify([...next]));
      } catch {
        /* private mode: the state just isn't remembered */
      }
      return next;
    });
  };
  const isCollapsed = (key: string) => !searching && collapsedGroups.has(key);

  // Click toggles one; Shift+click selects the range from the last click, in the order on screen.
  const visibleOrder = () => [...pinned, ...sections.flatMap((g) => (isCollapsed(g.key) ? [] : g.sessions))].map((s) => s.id);
  const toggle = (id: string, e: ReactMouseEvent | { shiftKey: boolean }) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (e.shiftKey && anchor) {
        const ids = visibleOrder();
        const [a, b] = [ids.indexOf(anchor), ids.indexOf(id)].sort((x, y) => x - y);
        if (a >= 0) ids.slice(a, b + 1).forEach((x) => next.add(x));
      } else if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
    setAnchor(id);
  };

  // ---- drag and drop: a conversation onto a group moves it, onto Pinned pins it ----
  const dropOn = (key: string) => (e: DragEvent) => {
    e.preventDefault();
    setDropKey(null);
    const id = e.dataTransfer.getData("application/x-goharness-session") || e.dataTransfer.getData("text/plain");
    const s = sessions.find((x) => x.id === id);
    if (!s) return;
    if (key === "pinned") {
      if (!s.pinned) onPin(s.id, true);
      return;
    }
    // Dropping on the conversation's own project means "back to the project".
    const target = key === ownGroupKey(s) ? "" : key;
    if ((s.group ?? "") !== target) onMove(s.id, target);
  };
  const dragOverOn = (key: string) => (e: DragEvent) => {
    if (selecting) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = "move";
    setDropKey(key);
  };
  const dragLeave = (e: DragEvent) => {
    if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setDropKey(null);
  };

  const finishCreate = async (name: string | null) => {
    const ctx = creating;
    setCreating(null);
    if (name === null || !name.trim() || !ctx) return;
    const g = await onCreateGroup(name.trim());
    if (g && ctx.moveId) onMove(ctx.moveId, g.id);
  };

  const row = (s: SessionSummary, inPinned: boolean, groupName: string) => {
    const isSelected = selected.has(s.id);
    const own = ownGroupKey(s);
    const here = groupKeyOf(s, customIds);
    // The folder it works in, when that isn't the group it is listed under.
    const folderTag = !inPinned && here !== own && s.project ? s.project : null;
    return (
      <li
        key={s.id}
        className={`session-row ${s.id === current ? "active" : ""} ${isSelected ? "selected" : ""}`}
        draggable={!selecting && renaming !== s.id}
        onDragStart={(e) => {
          e.dataTransfer.setData("application/x-goharness-session", s.id);
          e.dataTransfer.setData("text/plain", s.id);
          e.dataTransfer.effectAllowed = "move";
        }}
        onDragEnd={() => setDropKey(null)}
      >
        {selecting && (
          <input
            type="checkbox"
            className="session-check"
            checked={isSelected}
            onChange={() => {}}
            onClick={(e) => toggle(s.id, e)}
            aria-label={`Select ${s.title || "session"}`}
          />
        )}
        {renaming === s.id ? (
          <RenameInput
            initial={s.title}
            onDone={(title) => {
              setRenaming(null);
              if (title !== null && title.trim() !== s.title) onRename(s.id, title);
            }}
          />
        ) : (
          <button
            className="session"
            onClick={(e) => (selecting ? toggle(s.id, e) : onSelect(s.id))}
            onDoubleClick={() => !selecting && setRenaming(s.id)}
            aria-current={s.id === current ? "page" : undefined}
          >
            <span className="session-title">{s.title || "New session"}</span>
            {s.match && <span className="session-match">{s.match}</span>}
            <span className="session-meta">
              {s.running && <span className="dot" title="running" />}
              {inPinned && <span className="project-tag">{groupName}</span>}
              {folderTag && (
                <span className="project-tag" title={`Works in the ${folderTag} folder`}>
                  {folderTag}
                </span>
              )}
              {relativeTime(s.updated)}
            </span>
          </button>
        )}
        {!selecting && renaming !== s.id && (
          <div className="row-actions">
            <button
              className={`icon-btn row-btn ${s.pinned ? "row-btn-on" : ""}`}
              aria-label={`${s.pinned ? "Unpin" : "Pin"} ${s.title || "session"}`}
              aria-pressed={s.pinned}
              title={s.pinned ? "Unpin" : "Pin to top"}
              onClick={() => onPin(s.id, !s.pinned)}
            >
              <Icon name="pin" size={15} />
            </button>
            <button
              className="icon-btn row-btn"
              aria-label={`Actions for ${s.title || "session"}`}
              aria-haspopup="menu"
              aria-expanded={menu?.kind === "row" && menu.id === s.id}
              onClick={(e) => setMenu({ kind: "row", id: s.id, view: "main", rect: e.currentTarget.getBoundingClientRect() })}
            >
              <Icon name="more" size={15} />
            </button>
          </div>
        )}
      </li>
    );
  };

  const groupSection = (g: GroupSection) => {
    const folded = isCollapsed(g.key);
    return (
      <section
        key={g.key}
        className={`grp ${folded ? "collapsed" : ""} ${dropKey === g.key ? "drop" : ""}`}
        aria-label={g.name}
        onDragOver={dragOverOn(g.key)}
        onDragLeave={dragLeave}
        onDrop={dropOn(g.key)}
      >
        <div className="grp-head">
          {renamingGroup === g.key ? (
            <RenameInput
              initial={g.name}
              label="Group name"
              placeholder="Group name"
              onDone={(name) => {
                setRenamingGroup(null);
                if (name !== null && name.trim() && name.trim() !== g.name) onRenameGroup(g.key, name.trim());
              }}
            />
          ) : (
            <>
              <button className="grp-toggle" aria-expanded={!folded} onClick={() => toggleGroup(g.key)} title={g.name}>
                <Icon name="chevron" size={14} className="grp-chev" />
                <Icon name={g.kind === "custom" ? "tag" : "folder"} size={15} className={g.kind === "custom" ? "grp-icon custom" : "grp-icon"} />
                <span className="grp-name">{g.name}</span>
                <span className="grp-count">{g.total}</span>
              </button>
              <div className="grp-actions">
                <button className="icon-btn row-btn" aria-label={`New conversation in ${g.name}`} title="New conversation here" onClick={() => onNewInGroup(g.key)}>
                  <Icon name="plus" size={15} />
                </button>
                <button
                  className="icon-btn row-btn"
                  aria-label={`Actions for group ${g.name}`}
                  aria-haspopup="menu"
                  onClick={(e) => setMenu({ kind: "group", key: g.key, rect: e.currentTarget.getBoundingClientRect() })}
                >
                  <Icon name="more" size={15} />
                </button>
              </div>
            </>
          )}
        </div>
        {!folded && (
          <ul className="session-list">
            {g.sessions.map((s) => row(s, false, g.name))}
            {g.sessions.length === 0 && (
              <li className="grp-empty">{g.total > 0 ? "Everything here is pinned" : "Drag a conversation here"}</li>
            )}
          </ul>
        )}
      </section>
    );
  };

  // ---- the open menu ----
  const menuSession = menu?.kind === "row" ? sessions.find((s) => s.id === menu.id) : undefined;
  const menuGroup = menu?.kind === "group" ? sections.find((g) => g.key === menu.key) : undefined;
  const renderMenu = () => {
    if (menu?.kind === "row" && menuSession) {
      const s = menuSession;
      const here = groupKeyOf(s, customIds);
      if (menu.view === "move") {
        return (
          <FloatingMenu rect={menu.rect} onClose={closeMenu}>
            <div className="menu-label">Move to group</div>
            {targets.map((t) => (
              <MenuItem
                key={t.key}
                icon={t.kind === "custom" ? "tag" : "folder"}
                label={t.name}
                hint={t.key === here ? "✓" : undefined}
                onClick={() => {
                  closeMenu();
                  if (t.key !== here) onMove(s.id, t.key === ownGroupKey(s) ? "" : t.key);
                }}
              />
            ))}
            <div className="menu-sep" />
            <MenuItem
              icon="folder-plus"
              label="New group…"
              onClick={() => {
                closeMenu();
                setCreating({ moveId: s.id });
              }}
            />
          </FloatingMenu>
        );
      }
      const own = ownGroupKey(s);
      return (
        <FloatingMenu rect={menu.rect} onClose={closeMenu}>
          <MenuItem icon="pencil" label="Rename" onClick={() => (closeMenu(), setRenaming(s.id))} />
          <MenuItem icon="pin" label={s.pinned ? "Unpin" : "Pin to top"} onClick={() => (closeMenu(), onPin(s.id, !s.pinned))} />
          <MenuItem icon="move" label="Move to group…" onClick={() => setMenu({ ...menu, view: "move" })} hint={groupNameOf(here, groups).slice(0, 14)} />
          {here !== own && (
            <MenuItem icon="folder" label={`Move back to ${groupNameOf(own, groups)}`} onClick={() => (closeMenu(), onMove(s.id, ""))} />
          )}
          <div className="menu-sep" />
          <MenuItem icon="trash" label="Delete…" danger onClick={() => (closeMenu(), setConfirm([s]))} />
        </FloatingMenu>
      );
    }
    if (menu?.kind === "group" && menuGroup) {
      const g = menuGroup;
      return (
        <FloatingMenu rect={menu.rect} onClose={closeMenu}>
          <MenuItem icon="plus" label="New conversation here" onClick={() => (closeMenu(), onNewInGroup(g.key))} />
          {g.kind === "custom" && (
            <>
              <MenuItem icon="pencil" label="Rename group" onClick={() => (closeMenu(), setRenamingGroup(g.key))} />
              <div className="menu-sep" />
              <MenuItem icon="trash" label="Delete group" danger hint="keeps its conversations" onClick={() => (closeMenu(), onDeleteGroup(g.key))} />
            </>
          )}
        </FloatingMenu>
      );
    }
    return null;
  };

  const empty = sessions.length === 0;
  return (
    <>
      <div className={`scrim ${open ? "show" : ""}`} onClick={onClose} aria-hidden />
      <nav className={`sidebar ${open ? "open" : ""}`} aria-label="Sessions" aria-hidden={collapsed || undefined}>
        <div className="sidebar-head">
          <div className="brand">
            <span className="logo" aria-hidden>
              g
            </span>
            goharness
          </div>
          <div className="sidebar-head-actions">
            <NewSessionButton projects={projects} onNew={onNew} onChooseFolder={onChooseFolder} />
            {onToggleCollapsed && (
              <button className="icon-btn" aria-label="Collapse sidebar" title="Collapse sidebar" onClick={onToggleCollapsed}>
                <Icon name="panel" size={17} />
              </button>
            )}
          </div>
        </div>

        <div className="search-box">
          <Icon name="search" size={15} />
          <input
            ref={searchRef}
            type="search"
            value={query}
            onChange={(e) => onQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Escape" && query) {
                e.stopPropagation();
                onQuery("");
              }
            }}
            placeholder="Search sessions"
            aria-label="Search sessions"
            spellCheck={false}
          />
          <kbd>Ctrl K</kbd>
        </div>

        <div className="select-bar">
          {selecting ? (
            <>
              <label className="check select-all">
                <input
                  ref={selectAllRef}
                  type="checkbox"
                  checked={allSelected}
                  onChange={() => setSelected(allSelected ? new Set() : new Set(sessions.map((s) => s.id)))}
                />
                {selected.size > 0 ? `${selected.size} selected` : query.trim() ? "Select all matches" : "Select all"}
              </label>
              <button
                className="btn btn-sm btn-danger"
                disabled={selected.size === 0}
                onClick={() => setConfirm(sessions.filter((s) => selected.has(s.id)))}
              >
                Delete
              </button>
              <button className="link-btn" onClick={exitSelect}>
                Done
              </button>
            </>
          ) : (
            sessions.length > 0 && (
              <button className="link-btn" onClick={() => setSelecting(true)}>
                Select
              </button>
            )
          )}
        </div>

        <div className="session-scroll">
          {(pinned.length > 0 || !searching) && (
            <section
              className={`grp grp-pinned ${isCollapsed("pinned") ? "collapsed" : ""} ${dropKey === "pinned" ? "drop" : ""}`}
              aria-label="Pinned"
              onDragOver={dragOverOn("pinned")}
              onDragLeave={dragLeave}
              onDrop={dropOn("pinned")}
            >
              <div className="grp-head">
                <button className="grp-toggle" aria-expanded={!isCollapsed("pinned")} onClick={() => toggleGroup("pinned")}>
                  <Icon name="chevron" size={14} className="grp-chev" />
                  <Icon name="pin" size={15} className="grp-icon pin" />
                  <span className="grp-name">Pinned</span>
                  <span className="grp-count">{pinned.length}</span>
                </button>
              </div>
              {!isCollapsed("pinned") && (
                <ul className="session-list">
                  {pinned.map((s) => row(s, true, groupNameOf(groupKeyOf(s, customIds), groups)))}
                  {pinned.length === 0 && <li className="grp-empty">Pin a conversation, or drop one here</li>}
                </ul>
              )}
            </section>
          )}
          {sections.map(groupSection)}
          {creating && (
            <div className="new-group">
              <RenameInput initial="" label="New group name" placeholder="Group name, then Enter" onDone={finishCreate} />
            </div>
          )}
          {empty && <div className="hint pad">{searching ? `No sessions mention “${query.trim()}”.` : "No sessions yet."}</div>}
          {selecting && <div className="hint pad">Shift+click to select a range.</div>}
        </div>

        <div className="sidebar-foot">
          <button className="btn btn-ghost btn-block" onClick={() => setCreating({ moveId: null })}>
            <Icon name="folder-plus" size={15} />
            New group
          </button>
          <button className="link-btn" onClick={onLogout}>
            Log out
          </button>
        </div>
      </nav>
      {renderMenu()}
      {confirm && (
        <DeleteConfirm
          sessions={confirm}
          onClose={() => setConfirm(null)}
          onConfirm={async () => {
            const ids = confirm.map((s) => s.id);
            setConfirm(null);
            await onDelete(ids);
            if (selecting) exitSelect();
          }}
        />
      )}
    </>
  );
}
