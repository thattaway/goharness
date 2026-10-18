# goharness: improvement recommendations

Suggested improvements to functionality, the web UI, the terminal UI and
safety, based on a read of the code and README (October 2026). Items are
roughly in priority order within each section.

**Start with 1, 2 and 3.** They are small (a few hours each, with tests) and
do the most for the default local 9B model. **7** is the best UI improvement,
because its data already exists.

## Functionality

### 1. Load project instructions automatically

goharness does not read `AGENTS.md` or `CLAUDE.md` from the project. Most
repositories already have one. Adding its contents to the system prompt
teaches the agent each repo's rules without the user repeating them.

- Look for `AGENTS.md`, then `CLAUDE.md`, in the project root.
- Cap the size so a long file cannot fill the context window.
- Show in the sidebar which file was loaded.

### 2. Dedicated search tools: `grep`, `glob`, `list_dir`

Today the agent finds code by writing bash commands. Small models are much
more reliable with simple, bounded tools ("search for X in `*.go`") than with
composing correct `grep`/`find` pipelines.

- Results are capped and sorted, and respect `.gitignore`.
- Read-only, so they need no approval, which means fewer prompts.

### 3. A more forgiving `str_replace`

Small models often get whitespace or indentation slightly wrong, so the edit
fails and costs a round.

- If the exact match fails, retry with whitespace-insensitive matching (only
  when it finds exactly one match).
- When nothing matches, return the closest lines from the file in the error,
  so the model can correct itself on the next try.

### 4. Repair malformed tool calls

Local models sometimes produce broken tool-call JSON (trailing commas,
unquoted keys) or write the call inside the reply text. Quietly repair the
common cases before reporting an error.

### 5. A task-list tool

A simple checklist the model updates as it works, shown in the sidebar. It
keeps small models on track during long tasks and shows progress at a glance.

### 6. MCP support

Connecting to MCP servers would give access to GitHub, databases, browsers and
more without building each tool into goharness. This is a bigger job; do it
after 1–5.

## Web UI

### 7. Turn the Audit placeholder into a decision-log viewer

Every tool call is already recorded in `~/.goharness/decisions.jsonl`, but
nothing reads it.

- A searchable table of tool calls, with denials and failures highlighted.
- A "turn this into a rule" button that adds an allow or deny permission rule.

### 8. Close the gaps with the terminal UI

The web UI lacks `/compact` (the README says so), and resuming old sessions
is described for the terminal only.

### 9. Fill in or hide the Agent and Routines placeholders

Routines could become scheduled prompts, such as "every morning, run the
tests and summarise failures". Until they work, hide them so the app feels
finished.

### 10. Show the diff in the approval prompt

When a file edit needs approval, show its diff right there, with buttons to
approve this edit, approve all edits to this file, or deny.

## Terminal UI

### 11. File picker and prompt history

- `@` opens a fuzzy file picker that attaches a file's contents to the message.
- `Ctrl+R` searches previous prompts.

### 12. Context use breakdown

Split the context figure into system prompt, history, tool output and skills,
so it is clear what fills the window when trimming starts.

## Safety

### 13. Lock down the web terminal

The browser shell is completely unrestricted, and the web UI can listen on
`0.0.0.0` with `--allow-any-dir`.

- Make the terminal opt-in with a flag (off by default).
- Log every terminal session that is opened.

## Distribution

### 14. One-command install from prebuilt binaries

The current `install.sh` needs Go, Node, git and make, builds from source, and
clones from a private network address.

- A GitHub Actions workflow that builds Linux and macOS binaries
  (amd64 and arm64) on each version tag, with a checksum file.
- `install.sh` downloads the right binary, verifies it, and installs it to
  `~/.local/bin`, keeping `--service` and adding `--version` and
  `--from-source`.
- Result: `curl -fsSL https://raw.githubusercontent.com/thattaway/goharness/main/install.sh | sh`
  (requires the repository to be public).
- Optional: rename the module to `github.com/thattaway/goharness` so
  `go install github.com/thattaway/goharness@latest` works.
