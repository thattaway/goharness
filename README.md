# goharness

An OpenAI-compatible terminal coding agent. Point it at any OpenAI-compatible
base URL (e.g. a llama-swap gateway) and run a coding agent in your
terminal, with an optional web UI, a plan/undo model, and a skill system
(the open SKILL.md standard shared with Claude Code, Copilot, Amp, Hermes,
and OpenClaw).

## Quick start

```
# Terminal UI - talk to the agent in the current folder
goharness

# Web UI - serves an HTTP + SSE API and the React app on 127.0.0.1:8787
goharness web

# Resume a previous session (terminal UI only)
goharness -resume ./my-session.json
```

There's no `-project-root` flag - both the terminal UI and `goharness web`
use the folder you ran the command from as the project root. `goharness
web` can additionally offer other folders as selectable projects; see
`-projects-root` below.

## Configuration

Defaults: `-base-url http://172.16.10.40:8034/v1`, `-model ornith_1.5_9B`.

### CLI flags

Shared by both the terminal UI and `goharness web`:

- `-base-url` - OpenAI-compatible base URL (llama-swap)
- `-model` - model id to use
- `-max-rounds` - tool-calling rounds per message before asking for a final answer (1-50, default 10)
- `-context-tokens` - context window to assume when the model server doesn't report one

Terminal UI only:

- `-resume` - path to a previous session JSON file to resume

`goharness web` only:

- `-addr` - address to listen on (`127.0.0.1:8787` by default; use `0.0.0.0:8787` or a Tailscale IP to allow other machines)
- `-projects-root` - comma-separated folders whose subfolders can be picked as a session's project (the current folder can always be picked)
- `-allow-any-dir` - let the browser pick any folder on this machine as a session's working directory

Run `goharness -h` or `goharness web -h` for the full, current list.

### Environment variables

- `GOHARNESS_WEB_TOKEN` - overrides the web UI's access token (otherwise one is generated and saved to `~/.config/goharness/web_token` on first run)
- `FREELLMAPI_API_KEY` - bearer token for the `freellmapi` provider (`/provider` in the terminal UI)
- `GOHARNESS_SUBAGENT_MODEL` - model the `spawn_agent` tool's subagents use (default: the session's own model). A smaller model keeps the main model's host from swapping while a subagent runs
- `TAVILY_API_KEY` - enables the web-search tool
- `SCRAPLING_PYTHON` - overrides the Python interpreter used by the page-fetch tool
- `GOHARNESS_PARALLEL` - how many `spawn_agent` subagents from one turn may run at the same time (default 2, at most 4; 1 turns parallel runs off)
- `GOHARNESS_RTK` - set to `off` (or `0`, `false`, `no`) to stop the bash tool running commands through [rtk](https://github.com/rtk-ai/rtk) (see below)
- `GOHARNESS_HOST_NAMES` - friendly names for hosts in the status bar, as `ip-or-host=name` pairs separated by commas
- `GOHARNESS_STT_URL` / `GOHARNESS_STT_TOKEN` - base URL (and optional bearer token) of a speech-to-text server with an OpenAI-style `/v1/audio/transcriptions` endpoint; turns on the microphone button in the web UI (see Dictation)
- `GOHARNESS_DECISION_LOG` - path of the decision log (default `~/.goharness/decisions.jsonl`); set to `off` to disable (see below)

Variables can also go in `~/.config/goharness/env` as `KEY=VALUE` lines (an
optional `export ` prefix and quotes are accepted), which works from launchers
that never source your shell profile. A variable already set in the real
environment always wins over the file.

### Reloading configuration

Edit the env file, the permission rules (`~/.config/goharness/permissions.json`),
or a model server's context sizes, then pick the change up without restarting:
type `/reload` in the terminal UI, or use **Settings -> Reload configuration**
in the web UI. A reload:

- re-reads the env file (edited and removed lines take effect; variables from your real
  environment are never touched, and the summary names changed keys but never values)
- re-reads the permission rules (a file that fails to parse is reported and the previous
  rules stay in force)
- clears the cached model list, so context lengths and vision support are re-fetched on
  the next request
- reports the installed skill count (skills are already re-discovered on every message)

Command-line flags such as `-base-url` and the compiled-in provider list only change
on restart.

### Adding a provider (any OpenAI-compatible server)

Besides the built-in `llama-swap` and `freellmapi`, you can add your own: LiteLLM, vLLM, Ollama,
LM Studio, a cloud API. Type `/provider add`:

- **Web UI:** with nothing after it (or choosing "+ Add a provider..." at the end of the `/provider`
  list) it opens a form: name, base URL, API key variable, model. **Fetch models** asks the server
  what it offers (through goharness, so keys held in environment variables never reach the browser)
  and lets you pick one. The same three commands work as one-liners in the message box.
- **Terminal UI:** with nothing after it, it asks step by step (name, base URL, model, key variable,
  then a hidden key if you gave no variable). Enter answers, Esc cancels, and a bad answer is
  explained and asked again.
- **One line, either UI:** `/provider add <name> <base-url> <model> [--key KEY | --key-env VAR]`,
  plus `/provider list` and `/provider remove <name>`.

Prefer `--key-env`: put `VAR=...` in `~/.config/goharness/env` and only the variable's name is
stored. A raw `--key` is stored in `~/.config/goharness/custom_providers.json` (mode 600). Keys are
never printed, logged or returned by the API.

Providers live in `custom_providers.json` (`name`, `base_url`, `api_key` or `api_key_env`, `models`).
A provider with no `models` list shows whatever the server's `/models` reports, which is the best
fit for a gateway with many models; edit the file by hand to do that, then `/reload`. Switch with
`/provider`, then pick a model with `/model`. Servers that do not report a context size (LiteLLM,
for one) fall back to `-context-tokens`.

## Decision log

Every tool call is appended as one JSON line to `~/.goharness/decisions.jsonl`:
the tool, its input, what the permission policy said (`tier`: allow, ask or
deny), who decided (`by`: policy, plan-mode, auto-mode or user), whether it was
allowed or denied, and the outcome (`ok`, `error`, `cancelled` or `not-run`),
plus the model, session, duration, and whether a subagent made the call. Nothing
in goharness reads it back; it is a record you own, meant to build up labelled
data (approvals and denials, failed calls) for evaluating or training small
decision models later.

Only the bash command, file path, search query or URL is stored - never file
contents - and anything that looks like a credential is replaced with
`[redacted]`. The file is created with mode 0600. Set `GOHARNESS_DECISION_LOG`
to another path, or to `off` to turn the log off. For a quick look:

```sh
jq -r '[.tool, .by, .decision, .outcome, .input] | @tsv' ~/.goharness/decisions.jsonl | tail
```

## Output compression (rtk)

If [rtk](https://github.com/rtk-ai/rtk) is on `PATH`, the bash tool runs each
command through it, which compresses verbose output before it reaches the
model: roughly 85-90% less for `git diff`, 60-75% for `ls` and `docker`
listings, 40% for `git log`, and nothing for `cat`, file reads or most
`grep` output. Commands rtk has no filter for run unchanged. When output was
compressed the tool result ends with a note saying so.

- The rewrite happens only at execution. Permission checks still see the
  command the model asked for.
- The model can pass `raw=true` to the bash tool to get the complete,
  unfiltered output. rtk's filters are lossy (long commit bodies and diff
  hunks can be trimmed), so this is the escape hatch when detail matters.
- Set `GOHARNESS_RTK=off` to disable it. If rtk is missing, fails, or times
  out, the plain command runs instead.
- Install: download the static binary for your platform from the
  [rtk releases](https://github.com/rtk-ai/rtk/releases) into `~/.local/bin`.
  `rtk gain` shows token savings so far (`rtk gain --graph`, `-H` for history).

## Subagents

The agent has a `spawn_agent` tool for self-contained research: it hands a task
brief to a read-only subagent that works in a fresh context and returns only
its final answer, so exploring a large codebase doesn't fill the main
conversation. A subagent gets Plan mode's tools (no file edits), cannot ask for
approval (commands outside the allow-list are refused), and cannot spawn
further subagents. Its tool use shows as one-line notices while it runs, and
its token usage is added to the session totals. Set the model with
`GOHARNESS_SUBAGENT_MODEL`.

When the model makes several `spawn_agent` calls in one reply, they run at the same
time (up to `GOHARNESS_PARALLEL`) and their results come back in the order they were
asked for. Pointed at a gateway that balances several model servers (such as Olla), the
subagents land on different servers. Use a subagent model that every server in the
pool has, or each server swaps models. Each subagent's steps show inside its own
`spawn_agent` row in the web UI.

## Terminal UI

Type normal messages, or one of these slash commands:

- `/model` - switch the active model
- `/provider` - switch backend: llama-swap or freellmapi
- `/sessions` - resume a previous session
- `/new` - start a fresh conversation
- `/compact` - summarize the earlier conversation to free up context (see below)
- `/undo` - undo the last turn: conversation and file changes
- `/plan` - toggle plan/build mode (read-only exploration vs. making edits)
- `/auto` - toggle auto/manual approval
- `/tdd` - toggle test-driven-development prompt
- `/adhd` - toggle ADHD-friendly output style
- `/icm` - toggle staged-project (ICM) mode - work one `stages/NN_name/` folder at a time, stopping for review between stages
- `/image <path>` - attach a local image, then type your message
- `/skills` - list installed skills (run one with `/<skill-name> <arguments>`)
- `/reload` - re-read the env file, permission rules and model info without restarting
- `/help` - list commands
- `/quit` - exit

Keys: `Tab` toggles plan/build, `Ctrl+A` toggles auto/manual approval, `Ctrl+T` shows or
hides the model's reasoning live in the transcript (it is hidden by default; the input
area only shows how much the model has thought), `Ctrl+O` shows or hides tool output,
`PgUp`/`PgDn` scroll, and `Ctrl+C` cancels a running reply (or quits when idle).

A turn reads top to bottom: your message as a `❯` header on the left, the tool calls under
it, then the agent's reply behind a coloured rule down its left edge. The model's name is
shown above a reply only when it differs from the previous reply's (after `/model`).

Each tool call is one line, `▸ bash  git log --oneline | head -3  ✓ 0.4s`, with a
status of running (`…`), awaiting approval (`?`), done (`✓` and how long it took), failed
(`✗`), denied, blocked by policy, or cancelled. Its output stays hidden until you press
`Ctrl+O`, so a loaded skill or a long listing doesn't bury the conversation. File edits
always show their diff, and a call waiting for approval shows why. A thin rule separates
turns, and each turn ends with a footer such as `⎿ 3 tool calls · 12s · artemis 13.4 tok/s`.

Finished replies are rendered as styled markdown (headings, lists, tables, code blocks)
instead of showing the raw markers. A reply that is still streaming stays plain text,
since partial markdown renders badly, and is formatted once it completes. Inline math
that is a single symbol, such as `$\rightarrow$`, is shown as the Unicode character.

On a terminal at least 53 columns wide, a sidebar shows the session at a glance:
turns and tool calls, tokens in and out, prompt-cache hit rate (latest and session),
average tokens/second and time to first token, the history-trim budget, pending image
attachments, the available tools and the installed skills. The numbers come from the
usage the model server reports for each request. The status bar also shows the
conversation size against the trim budget (`ctx used/budget`).

The status bar is always one line: mode, model, context, round (while the model is
calling tools) and host and speed on the left, key hints on the right. On a narrow
terminal the hints are dropped from the end first; `/help` lists every key.

The status bar also names the host that served the latest reply and its generation
speed (`· artemis 13.4 tok/s`). Behind Olla the host comes from its `X-Olla-Endpoint`
header; otherwise it is the host of the base URL, which you can give friendly names with
`GOHARNESS_HOST_NAMES="172.16.10.40=athena,172.16.10.45=artemis"`. While the model has
not started answering, the input area shows what is happening instead of "thinking":
after three seconds it says `loading <model>… 42s` or `swapping to <model>…` when
the server is llama-swap (it reads llama-swap's `/running`), and otherwise
`waiting for <model>…`, with `(processing the prompt)` when the model is already loaded.
Through Olla's shim only the generic wait line is available.

### Compacting

Old messages are trimmed automatically when the conversation outgrows the model's
window, which silently drops detail. `/compact` does it deliberately and keeps the
thread: the model writes a summary of everything before your latest message, and that
summary replaces those messages (the latest turn is kept word for word). It uses the
current model and its own request, so it takes as long as a short reply. The change is
saved with the session. Turns before the compaction can no longer be undone with
`/undo`, since their messages are gone. If the summary fails or comes back empty,
nothing changes. Not available in the web UI yet.

## Skills

goharness discovers skill folders (a folder containing `SKILL.md`) only from
its own locations - a name in an earlier one hides the same name in a later one,
so a project can override a personal skill:

1. `<project>/.goharness/skills`
2. `~/.goharness/skills`

Set `GOHARNESS_ALL_SKILLS=1` to also search the other agents' folders
(`<project>/.agents/skills`, `<project>/.claude/skills`, `~/.agents/skills`,
`~/.claude/skills`, `~/.copilot/skills`, `$HERMES_HOME/skills`,
`$OPENCLAW_STATE_DIR/skills`).

Put a skill folder with a `SKILL.md` in one of these to make it available.
book-to-skill is a compatible skill that converts books and documents (PDF,
EPUB, DOCX, HTML, Markdown, plain text, RTF, MOBI/AZW with Calibre) into
structured agent skills.

## Dictation (web UI)

With `GOHARNESS_STT_URL` set to a speech-to-text server, the message box in the web UI gets a
🎤 button. Click it, speak, click again, and the transcribed words are added to the box for
you to review before sending. The browser's recording goes to goharness (`POST /api/transcribe`),
which forwards it to the server, so the server's address and token stay out of the browser.

The homelab uses Parakeet Redux (`~/projects/parakeet-stt` on artemis, port 8040), which
transcribes on CPU at roughly 60x realtime; any server with that endpoint works.

Browsers only allow microphone access on HTTPS pages and on `localhost`. Over plain `http://`
on a LAN address the 🎤 button instead opens a file picker (on phones, the system recorder)
so you can still dictate by recording a clip. For the live microphone, open the web UI through
an HTTPS address.

## Web UI

`web.go` serves an HTTP API (JSON + Server-Sent Events for streaming agent
output) plus the embedded React build. `goharness web` exposes it on
`127.0.0.1:8787` by default. There's also a per-session WebSocket endpoint
(`/api/sessions/{id}/terminal`) that gives the browser a real interactive
shell in that session's project folder - a genuine, unrestricted shell,
not routed through the agent's own tool-approval system.

Other web UI behaviour worth knowing:

- Runs are detached from the browser: close the tab and come back later, and the page
  catches up. The current run's events are also saved next to the session file
  (`<session>.events.jsonl`), so a run survives a server restart; one that was in
  progress is marked as interrupted rather than looking stuck.
- Settings has the permission-rules editor, the skills list, and **Reload configuration**
  (see above).
- The sidebar rail has Agent, Routines and Audit entries that are placeholders for now.

## Development

This is a Go + React project. From the project directory:

```
make build      # builds the React app, then the Go binary that embeds it
make test       # go test -race, plus the frontend's typecheck and test suite
make dev-web    # frontend dev server on :5173 with hot reload (run ./goharness web alongside it)
```

See the `Makefile` for systemd deployment targets (`install-service`,
`uninstall-service`).

## Installing on a new server

Prerequisites: Go 1.27.1+, Node.js 20+ (the frontend build fails on Node
18 - use `nvm install --lts` if the system Node is older), and `git`.

```
git clone http://172.16.10.24:3000/thattaway/goharness.git
cd goharness
make build

./goharness                              # terminal UI, current folder as project
./goharness web --addr 0.0.0.0:8787      # web UI, reachable from other machines
```

The first run of `goharness web` prints an access token and saves it to
`~/.config/goharness/web_token`.

To find the token again later, read the file (it is a plain text file, not a program, so
do not type its path on its own: the shell would try to run it and say "Permission denied"):

```
cat ~/.config/goharness/web_token
```

or print a ready-made login link for the machine the server runs on (use its address
instead of `localhost` when you open it from another computer):

```
echo "http://localhost:8787/?token=$(cat ~/.config/goharness/web_token)"
```

No `sudo` is needed; the file belongs to the user that runs the server. To use a token of
your own, set `GOHARNESS_WEB_TOKEN` (see the environment variables above).

To run it as a persistent background service instead of a foreground
process:

```
make install-service ADDR=0.0.0.0:8787 PROJECTS_ROOT=$HOME/projects WEB_FLAGS=--allow-any-dir
systemctl --user enable --now goharness-web
loginctl enable-linger $USER   # keeps it running after you log out
```

The default `-base-url` points at this deployment's internal llama-swap
gateway (`172.16.10.40:8034`). A new server needs network access to that
gateway, or an explicit `-base-url` pointing at wherever its model backend
actually lives.

## Running in a Docker sandbox

`sandbox/` runs goharness inside a container that sees only one project folder: no
home directory, no SSH keys, no Docker socket, no other projects. By default it can reach
only your model server (through a small relay), not the rest of the network. That makes it
safe to turn on auto-approve, and to let the agent loose on a copy of a project.

```
sandbox/build-image.sh                        # build the images (needs this repo's built binary, rtk and Node 22)
sandbox/goharness-run ~/projects/my-app       # open the terminal UI inside the sandbox
sandbox/goharness-run --profile reviewer ~/projects/my-app
sandbox/deploy.sh you@newserver --base-url http://model-host:port/v1 --model my-model   # install it on another server
```

Settings (model, context, rounds, subagent model, network mode, skills, rules) live in
profile files under `sandbox/profiles/`. Everything is explained in
[`sandbox/README.md`](sandbox/README.md), including how the network limit works without
root, how to deploy to another machine, and how to verify the isolation.
