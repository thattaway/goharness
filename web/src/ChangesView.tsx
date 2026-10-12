import { useEffect, useState } from "react";
import { api, type FileChange } from "./api";

const STATUS: Record<FileChange["status"], { label: string; letter: string }> = {
  added: { label: "added", letter: "A" },
  modified: { label: "modified", letter: "M" },
  deleted: { label: "deleted", letter: "D" },
  renamed: { label: "renamed", letter: "R" },
};

function FileDiff({ file, open, onToggle }: { file: FileChange; open: boolean; onToggle: () => void }) {
  return (
    <div className="file-change">
      <button className="file-head" onClick={onToggle} aria-expanded={open}>
        <span className={`status-badge status-${file.status}`} title={STATUS[file.status].label}>
          {STATUS[file.status].letter}
        </span>
        <span className="file-path" title={file.path}>
          {file.oldPath ? `${file.oldPath} → ${file.path}` : file.path}
        </span>
        <span className="file-stats">
          {file.added > 0 && <span className="stat-add">+{file.added}</span>}
          {file.removed > 0 && <span className="stat-del">−{file.removed}</span>}
        </span>
        <span className="chev" aria-hidden>
          {open ? "▾" : "▸"}
        </span>
      </button>
      {open && (
        <div className="diff file-diff">
          {file.binary && <div className="diff-note">Binary file, no preview.</div>}
          {!file.binary && file.diff.length === 0 && <div className="diff-note">No text changes (permissions or an empty file).</div>}
          {file.diff.map((l, i) =>
            l.op === "@" ? (
              <div key={i} className="diff-line diff-hunk">
                {l.text}
              </div>
            ) : (
              <div key={i} className={`diff-line diff-${l.op === "+" ? "add" : l.op === "-" ? "del" : "ctx"}`}>
                <span className="diff-op" aria-hidden>
                  {l.op}
                </span>
                <span className="diff-text">{l.text || " "}</span>
              </div>
            ),
          )}
          {file.truncated && <div className="diff-note">Diff cut short; it's too long to show here.</div>}
        </div>
      )}
    </div>
  );
}

export function ChangesModal({ sessionId, turn, onClose }: { sessionId: string; turn: number; onClose: () => void }) {
  const [files, setFiles] = useState<FileChange[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState<Set<string>>(new Set());

  useEffect(() => {
    api
      .turnChanges(sessionId, turn)
      .then((res) => {
        setFiles(res.files);
        // Expand everything when it's small enough to read at a glance.
        const lines = res.files.reduce((n, f) => n + f.diff.length, 0);
        setOpen(new Set(res.files.length <= 4 || lines <= 300 ? res.files.map((f) => f.path) : []));
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, [sessionId, turn]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const added = files?.reduce((n, f) => n + f.added, 0) ?? 0;
  const removed = files?.reduce((n, f) => n + f.removed, 0) ?? 0;
  const toggle = (p: string) =>
    setOpen((prev) => {
      const next = new Set(prev);
      if (next.has(p)) next.delete(p);
      else next.add(p);
      return next;
    });

  return (
    <div className="modal-scrim" onClick={onClose}>
      <div className="modal changes-modal" role="dialog" aria-modal="true" aria-label="Files changed" onClick={(e) => e.stopPropagation()}>
        <div className="modal-head">
          <h2>
            Changes in this turn
            {files && (
              <span className="hint">
                {" "}
                · {files.length} file{files.length === 1 ? "" : "s"} <span className="stat-add">+{added}</span>{" "}
                <span className="stat-del">−{removed}</span>
              </span>
            )}
          </h2>
          <button className="icon-btn" onClick={onClose} aria-label="Close">
            ×
          </button>
        </div>
        <div className="modal-body">
          {error && <div className="form-error">{error}</div>}
          {!files && !error && <div className="hint">Loading…</div>}
          {files && files.length === 0 && <div className="hint">This turn didn't change any files.</div>}
          {files && files.length > 1 && (
            <div className="row">
              <button className="link-btn" onClick={() => setOpen(new Set(files.map((f) => f.path)))}>
                Expand all
              </button>
              <button className="link-btn" onClick={() => setOpen(new Set())}>
                Collapse all
              </button>
            </div>
          )}
          {files?.map((f) => (
            <FileDiff key={f.path} file={f} open={open.has(f.path)} onToggle={() => toggle(f.path)} />
          ))}
          <p className="hint changes-foot">
            Shows what the files looked like before this message and when the agent finished it. Changes you made by hand
            afterwards aren't included.
          </p>
        </div>
      </div>
    </div>
  );
}

export function TurnEnd({ files, hasHistory, onReview }: { files: number; hasHistory: boolean; onReview: () => void }) {
  if (!hasHistory || files === 0) return null;
  return (
    <div className="turn-end">
      <button className="turn-changes" onClick={onReview}>
        <span aria-hidden>📝</span> {files} file{files === 1 ? "" : "s"} changed · <span className="link-like">Review</span>
      </button>
    </div>
  );
}
