import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { api, type DirListing, type Project } from "./api";

// Recently chosen folders, per browser. Only a convenience: the server
// decides what's allowed.
const RECENT_KEY = "goharness.recentDirs";

function recentDirs(): string[] {
  try {
    return JSON.parse(localStorage.getItem(RECENT_KEY) ?? "[]");
  } catch {
    return [];
  }
}

export function rememberDir(path: string) {
  try {
    const list = [path, ...recentDirs().filter((p) => p !== path)].slice(0, 6);
    localStorage.setItem(RECENT_KEY, JSON.stringify(list));
  } catch {
    /* storage unavailable */
  }
}

const baseName = (p: string) => p.split("/").filter(Boolean).pop() ?? "/";

export function FolderPicker({
  projects,
  onChoose,
  onClose,
}: {
  projects: Project[];
  onChoose: (path: string) => Promise<string | null>;
  onClose: () => void;
}) {
  const [listing, setListing] = useState<DirListing | null>(null);
  const [typed, setTyped] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [showHidden, setShowHidden] = useState(false);
  const [filter, setFilter] = useState("");
  const listRef = useRef<HTMLUListElement>(null);

  const open = useCallback(async (path?: string) => {
    try {
      const l = await api.dirs(path);
      setListing(l);
      setTyped(l.path);
      setFilter("");
      setError(null);
      listRef.current?.scrollTo(0, 0);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => {
    open(recentDirs()[0]);
  }, [open]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const go = (e: FormEvent) => {
    e.preventDefault();
    const t = typed.trim();
    if (t) open(t.startsWith("~") || t.startsWith("/") ? t : `${listing?.path ?? ""}/${t}`);
  };

  const quick = [
    ...recentDirs().map((p) => ({ path: p, name: baseName(p) })),
    ...projects.filter((p) => !recentDirs().includes(p.path)),
  ].slice(0, 10);

  const entries = (listing?.entries ?? []).filter(
    (e) => (showHidden || !e.name.startsWith(".")) && e.name.toLowerCase().includes(filter.toLowerCase()),
  );

  return (
    <div className="modal-scrim" onClick={onClose}>
      <div className="modal folder-picker" role="dialog" aria-modal="true" aria-label="Choose a working directory" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <h2>New session in…</h2>
          <button className="icon-btn" onClick={onClose} aria-label="Close">
            ×
          </button>
        </div>
        <div className="modal-body">
          {quick.length > 0 && (
            <div className="quick-picks" aria-label="Recent and project folders">
              {quick.map((q) => (
                <button key={q.path} className="chip-btn" title={q.path} onClick={() => open(q.path)}>
                  {q.name}
                </button>
              ))}
            </div>
          )}

          <form className="path-row" onSubmit={go}>
            <button
              type="button"
              className="btn btn-sm"
              disabled={!listing?.parent}
              onClick={() => listing?.parent && open(listing.parent)}
              aria-label="Parent folder"
              title="Parent folder"
            >
              ↑
            </button>
            <input
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              spellCheck={false}
              aria-label="Folder path"
              placeholder="/path/to/folder or ~/projects/x"
            />
            <button className="btn btn-sm">Go</button>
          </form>
          {error && <div className="form-error">{error}</div>}

          <div className="dir-tools">
            <input
              className="dir-filter"
              value={filter}
              onChange={(e) => setFilter(e.target.value)}
              placeholder="Filter"
              aria-label="Filter folders"
            />
            <label className="check">
              <input type="checkbox" checked={showHidden} onChange={(e) => setShowHidden(e.target.checked)} />
              Hidden
            </label>
          </div>

          <ul className="dir-list" ref={listRef}>
            {entries.map((e) => (
              <li key={e.path}>
                <button className="dir-item" onClick={() => open(e.path)} title={e.path}>
                  <span aria-hidden>📁</span> {e.name}
                </button>
              </li>
            ))}
            {listing && entries.length === 0 && <li className="hint pad">No subfolders.</li>}
          </ul>

          <div className="row end">
            {listing && !listing.allowed && (
              <span className="hint grow">This folder can't be used (it holds secrets, or is outside what the server allows).</span>
            )}
            <button className="btn" onClick={onClose}>
              Cancel
            </button>
            <button
              className="btn btn-primary"
              disabled={!listing?.allowed}
              onClick={async () => {
                if (!listing) return;
                const err = await onChoose(listing.path);
                if (err) setError(err);
              }}
            >
              Start in {listing ? baseName(listing.path) : "…"}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
