package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type slashCommand struct {
	name  string
	help  string
	skill bool // an installed skill: choosing it fills in "/name " for arguments
}

var slashCommands = []slashCommand{
	{name: "/model", help: "switch the active model"},
	{name: "/subagent", help: "choose the model spawn_agent subagents use"},
	{name: "/sessions", help: "resume a previous session"},
	{name: "/rename", help: "name this session: /rename <title> (/rename - clears the name)"},
	{name: "/new", help: "start a fresh conversation"},
	{name: "/compact", help: "summarize the earlier conversation to free up context"},
	{name: "/undo", help: "undo the last turn: conversation and file changes"},
	{name: "/plan", help: "toggle plan/build mode"},
	{name: "/tdd", help: "toggle test-driven development prompt"},
	{name: "/adhd", help: "toggle ADHD-friendly output style"},
	{name: "/provider", help: "switch backend; /provider add <name> <base-url> <model> [--key KEY|--key-env VAR]"},
	{name: "/icm", help: "toggle staged-project (ICM) mode - work one stages/NN_name/ at a time"},
	{name: "/image", help: "attach a local image: /image <path>, then type your message"},
	{name: "/skills", help: "list installed skills (run one with /<skill-name> <arguments>)"},
	{name: "/reload", help: "re-read the env file, permission rules and model info without restarting"},
	{name: "/auto", help: "toggle auto/manual approval"},
	{name: "/help", help: "list commands"},
	{name: "/quit", help: "exit"},
}

// matchingCommands filters slashCommands the same way NeuralCode's own
// SlashCompleter does: substring match against name and help text, not
// just a prefix, so typing part of what a command does still finds it.
// matchingCommands returns the commands and installed skills matching what
// has been typed after "/".
func matchingCommands(typed, projectRoot string) []slashCommand {
	typed = strings.ToLower(strings.TrimPrefix(typed, "/"))
	all := append([]slashCommand(nil), slashCommands...)
	for _, sk := range discoverSkills(projectRoot) {
		help := sk.Description
		if r := []rune(help); len(r) > 60 {
			help = string(r[:60]) + "…"
		}
		all = append(all, slashCommand{name: "/" + sk.Name, help: "skill: " + help, skill: true})
	}
	var out []slashCommand
	for _, c := range all {
		haystack := strings.ToLower(strings.TrimPrefix(c.name, "/") + " " + c.help)
		if strings.Contains(haystack, typed) {
			out = append(out, c)
		}
	}
	return out
}

// pickerItem is one row in the generic list-and-select overlay used by
// /model and /sessions.
type pickerItem struct {
	label string
	value string
}

type pickerState struct {
	title    string
	items    []pickerItem
	selected int
	onSelect func(model, string) (model, tea.Cmd)
}

type modelsFetchedMsg struct {
	ids []string
	err error
}

// fetchModels returns the model list to show in the /model picker. When
// fixed is non-nil (a provider with a maintained allowlist, e.g.
// freellmapi - see providers.go), it's used as-is with no network call;
// otherwise the provider's own /models endpoint is fetched live.
func fetchModels(baseURL, apiKey string, fixed []string) tea.Cmd {
	return func() tea.Msg {
		if fixed != nil {
			ids := append([]string(nil), fixed...)
			sort.Strings(ids)
			return modelsFetchedMsg{ids: ids}
		}
		ids, err := listModels(baseURL, apiKey)
		return modelsFetchedMsg{ids: ids, err: err}
	}
}

// listModels asks the OpenAI-compatible provider which model ids it serves.
func listModels(baseURL, apiKey string) ([]string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var data struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(data.Data))
	for _, d := range data.Data {
		ids = append(ids, d.ID)
	}
	sort.Strings(ids)
	return ids, nil
}

type sessionFile struct {
	path    string
	modTime time.Time
}

func listSessions() ([]sessionFile, error) {
	dir, err := sessionsDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []sessionFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, sessionFile{path: filepath.Join(dir, e.Name()), modTime: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].modTime.After(out[j].modTime) })
	return out, nil
}

func providerAddHelp() string {
	return "usage: /provider add <name> <base-url> <model> [--key KEY | --key-env VAR]\n  /provider                  switch backend (picker)\n  /provider list             list all providers\n  /provider add              ask step by step\n  /provider add <name> <base-url> <model> [--key KEY | --key-env VAR]   add in one line\n  /provider remove <name>    remove a custom provider\n  names: lowercase, 1-30 chars, a-z0-9._-; base-url must be http(s)://"
}

func splitArgs(s string) []string {
	var out []string
	var cur strings.Builder
	inSingle, inDouble, escaped := false, false, false
	for _, r := range s {
		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && !inSingle {
			escaped = true
			continue
		}
		switch r {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
				continue
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
				continue
			}
		}
		if !inSingle && !inDouble && (r == ' ' || r == '\t') {
			if cur.Len() > 0 {
				out = append(out, cur.String())
				cur.Reset()
			}
			continue
		}
		cur.WriteRune(r)
	}
	if escaped {
		cur.WriteRune('\\')
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func (m model) handleProviderSubcommand(args []string) (model, bool) {
	if len(args) == 0 {
		return m, false
	}
	switch args[0] {
	case "help", "--help", "-h":
		m.appendLine(styleDim.Render("  " + strings.ReplaceAll(providerAddHelp(), "\n", "\n  ")))
		return m, true
	case "list", "ls":
		var b strings.Builder
		b.WriteString("  providers:\n")
		for _, p := range allProviders() {
			models := strings.Join(p.Models, ", ")
			if models == "" {
				models = "(live /models)"
			}
			b.WriteString(fmt.Sprintf("    %-16s %s  models: %s\n", p.Name, p.BaseURL, models))
		}
		// llama-swap is implicit (uses -base-url), show it separately
		b.WriteString(fmt.Sprintf("    %-16s %s  models: (live /models)  [default]\n", "llama-swap", m.sess.BaseURL))
		m.appendLine(styleDim.Render(strings.TrimRight(b.String(), "\n")))
		return m, true
	case "add":
		if len(args) == 1 {
			m.startProviderWizard()
			return m, true
		}
		if len(args) < 4 {
			m.appendLine(styleErr.Render("usage: /provider add <name> <base-url> <model> [--key KEY | --key-env VAR]"))
			return m, true
		}
		name, baseURL, modelName := args[1], args[2], args[3]
		var apiKey, apiKeyEnv string
		for i := 4; i < len(args); i++ {
			switch args[i] {
			case "--key":
				if apiKeyEnv != "" {
					m.appendLine(styleErr.Render("specify --key or --key-env, not both"))
					return m, true
				}
				if i+1 >= len(args) {
					m.appendLine(styleErr.Render("--key requires a value"))
					return m, true
				}
				i++
				apiKey = args[i]
			case "--key-env":
				if apiKey != "" {
					m.appendLine(styleErr.Render("specify --key or --key-env, not both"))
					return m, true
				}
				if i+1 >= len(args) {
					m.appendLine(styleErr.Render("--key-env requires a variable name"))
					return m, true
				}
				i++
				apiKeyEnv = args[i]
			default:
				m.appendLine(styleErr.Render("unknown option " + args[i] + "; usage: /provider add <name> <base-url> <model> [--key KEY | --key-env VAR]"))
				return m, true
			}
		}
		p := provider{Name: name, BaseURL: baseURL, APIKey: apiKey, APIKeyEnv: apiKeyEnv, Models: []string{modelName}}
		if err := saveCustomProvider(p); err != nil {
			m.appendLine(styleErr.Render(err.Error()))
			return m, true
		}
		clearModelInfoCaches()
		if apiKey != "" {
			m.appendLine(styleDim.Render(fmt.Sprintf("  provider %q added with model %q (key stored in ~/.config/goharness/custom_providers.json, 0600). /provider to switch, /model to pick.", name, modelName)))
		} else {
			m.appendLine(styleDim.Render(fmt.Sprintf("  provider %q added with model %q. /provider to switch, /model to pick.", name, modelName)))
		}
		return m, true
	case "remove", "rm", "delete", "del":
		if len(args) < 2 {
			m.appendLine(styleErr.Render("usage: /provider remove <name>"))
			return m, true
		}
		if err := deleteCustomProvider(args[1]); err != nil {
			m.appendLine(styleErr.Render(err.Error()))
			return m, true
		}
		clearModelInfoCaches()
		m.appendLine(styleDim.Render(fmt.Sprintf("  provider %q removed.", args[1])))
		return m, true
	}
	return m, false
}

// executeCommand runs a slash command. name is the full line typed (e.g.
// "/provider add foo ..."); subcommands are parsed from the remainder.
// Returns the updated model and a command to run next (may be nil).
func (m model) executeCommand(name string) (model, tea.Cmd) {
	raw := strings.TrimSpace(name)
	parts := splitArgs(raw)
	if len(parts) == 0 {
		m.appendLine(styleErr.Render("unknown command: " + name))
		return m, nil
	}
	cmd := parts[0]
	args := parts[1:]
	// /provider is special: bare -> picker, with args -> subcommand.
	if cmd == "/provider" && len(args) > 0 {
		if next, handled := m.handleProviderSubcommand(args); handled {
			return next, nil
		}
		m.appendLine(styleErr.Render("unknown /provider subcommand " + args[0] + "; try /provider help"))
		return m, nil
	}
	switch cmd {
	case "/subagent":
		m.pickSubagent = true
		st := m.sess.Settings()
		baseURL, apiKey, fixed := m.sess.resolveProvider(st.Provider)
		if fixed == nil {
			m.appendLine(styleDim.Render("  fetching models…"))
		}
		return m, fetchModels(baseURL, apiKey, fixed)

	case "/model":
		m.pickSubagent = false
		st := m.sess.Settings()
		baseURL, apiKey, fixed := m.sess.resolveProvider(st.Provider)
		if fixed == nil {
			m.appendLine(styleDim.Render("  fetching models…"))
		}
		return m, fetchModels(baseURL, apiKey, fixed)

	case "/provider":
		items := make([]pickerItem, 0, len(providerNames()))
		for _, name := range providerNames() {
			items = append(items, pickerItem{label: name, value: name})
		}
		m.picker = &pickerState{
			title: "select provider", items: items,
			onSelect: func(m model, name string) (model, tea.Cmd) {
				m.updateSettings(func(st *Settings) {
					st.Provider = name
					// The old model almost certainly doesn't exist on the
					// new provider. freellmapi's "auto" always works;
					// llama-swap has no single id that's always valid, so
					// leave it as-is and point at /model instead.
					if name != "llama-swap" {
						st.Model = "auto"
					}
				})
				hint := "  use /model to pick one for it"
				if name != "llama-swap" {
					hint = "  model set to auto -" + hint
				}
				m.appendLine(styleDim.Render("  provider set to " + name + "." + hint))
				return m, nil
			},
		}
		return m, nil

	case "/sessions":
		files, err := listSessions()
		if err != nil {
			m.appendLine(styleErr.Render("error listing sessions: " + err.Error()))
			return m, nil
		}
		if len(files) == 0 {
			m.appendLine(styleDim.Render("  no saved sessions yet"))
			return m, nil
		}
		items := make([]pickerItem, len(files))
		for i, f := range files {
			label := filepath.Base(f.path)
			if meta := loadMeta(f.path); meta.Title != "" {
				label = meta.Title
				if meta.Pinned {
					label = "★ " + label
				}
			}
			items[i] = pickerItem{
				label: fmt.Sprintf("%s  (%s)", label, f.modTime.Format("Jan 2 15:04")),
				value: f.path,
			}
		}
		m.picker = &pickerState{
			title: "resume session", items: items,
			onSelect: func(m model, path string) (model, tea.Cmd) {
				msgs, err := loadSession(path)
				if err != nil {
					m.appendLine(styleErr.Render("error loading session: " + err.Error()))
					return m, nil
				}
				if err := m.sess.Reset(path, msgs); err != nil {
					m.appendLine(styleErr.Render("error: " + err.Error()))
					return m, nil
				}
				m.loadTranscript("resumed: " + path)
				return m, nil
			},
		}
		return m, nil

	case "/new":
		path, err := newSessionPath()
		if err != nil {
			m.appendLine(styleErr.Render("error starting new session: " + err.Error()))
			return m, nil
		}
		if err := m.sess.Reset(path, nil); err != nil {
			m.appendLine(styleErr.Render("error: " + err.Error()))
			return m, nil
		}
		m.loadTranscript("session: " + path)
		return m, nil

	case "/compact":
		ctx, cancel := context.WithCancel(context.Background())
		m.state, m.cancelFunc = stateStreaming, cancel
		m.statusText = "compacting the conversation…"
		sess := m.sess
		return m, func() tea.Msg {
			res, err := sess.Compact(ctx)
			return compactDoneMsg{res: res, err: err}
		}

	case "/undo":
		res, err := m.sess.Undo()
		if err != nil {
			m.appendLine(styleErr.Render("undo: " + err.Error()))
			return m, nil
		}
		m.loadTranscript("session: " + m.sess.Path())
		switch {
		case !res.FilesRestored:
			m.appendLine(styleDim.Render("  undid the last turn (conversation only - it had no file checkpoint)"))
		case len(res.Files) == 0:
			m.appendLine(styleDim.Render("  undid the last turn (no files had changed)"))
		default:
			m.appendLine(styleDim.Render("  undid the last turn and restored: " + strings.Join(res.Files, ", ")))
		}
		m.input.SetValue(res.Text)
		m.input.CursorEnd()
		return m, nil

	case "/skills":
		skills := discoverSkills(m.sess.ProjectRoot)
		if len(skills) == 0 {
			m.appendLine(styleDim.Render("  no skills installed. Put a skill folder (with a SKILL.md) in ~/.agents/skills/,\n  ~/.claude/skills/ or ~/.goharness/skills/, or in .agents/skills/ inside the project."))
			return m, nil
		}
		var b strings.Builder
		b.WriteString("  installed skills (run with /<name> <arguments>, or just ask for the task):\n")
		for _, sk := range skills {
			b.WriteString(fmt.Sprintf("  /%s  %s\n      %s\n", sk.Name, sk.Description, sk.Dir))
		}
		m.appendLine(styleDim.Render(strings.TrimRight(b.String(), "\n")))
		return m, nil

	case "/reload":
		m.appendLine(styleDim.Render("  " + strings.ReplaceAll(reloadConfig(m.sess.ProjectRoot), "\n", "\n  ")))
		return m, nil

	case "/plan":
		m.updateSettings(func(st *Settings) { st.Plan = !st.Plan })
		m.appendLine(styleDim.Render("  mode: " + modeLabel(m.sess.Settings())))
		return m, nil

	case "/tdd":
		m.updateSettings(func(st *Settings) { st.TDD = !st.TDD })
		m.appendLine(styleDim.Render("  mode: " + modeLabel(m.sess.Settings())))
		return m, nil

	case "/adhd":
		m.updateSettings(func(st *Settings) { st.ADHD = !st.ADHD })
		m.appendLine(styleDim.Render("  mode: " + modeLabel(m.sess.Settings())))
		return m, nil

	case "/icm":
		m.updateSettings(func(st *Settings) { st.ICM = !st.ICM })
		m.appendLine(styleDim.Render("  mode: " + modeLabel(m.sess.Settings())))
		return m, nil

	case "/image":
		m.appendLine(styleDim.Render("  usage: /image <path>  (attaches it; type your message after and press Enter to send)"))
		return m, nil

	case "/rename":
		if len(args) == 0 {
			cur, _ := m.sess.Label()
			if cur == "" {
				cur = "(none)"
			}
			m.appendLine(styleDim.Render("  session name: " + cur + " - use /rename <title> to change it"))
			return m, nil
		}
		title := strings.TrimSpace(strings.TrimPrefix(raw, cmd))
		if title == "-" {
			title = ""
		}
		if err := m.sess.SetLabel(&title, nil); err != nil {
			m.appendLine(styleErr.Render("rename failed: " + err.Error()))
			return m, nil
		}
		m.appendLine(styleDim.Render("  session renamed"))
		return m, nil

	case "/auto":
		m.updateSettings(func(st *Settings) { st.Auto = !st.Auto })
		m.appendLine(styleDim.Render("  mode: " + modeLabel(m.sess.Settings())))
		return m, nil

	case "/help":
		var b strings.Builder
		for _, c := range slashCommands {
			b.WriteString(fmt.Sprintf("  %-10s %s\n", c.name, c.help))
		}
		b.WriteString("\n  keys: Tab plan/build · Ctrl+A auto/manual approval · Ctrl+T show thinking · Ctrl+O show tool output\n" +
			"        PgUp/PgDn scroll · Ctrl+C cancel a reply (quit when idle)\n")
		m.appendLine(styleDim.Render(strings.TrimRight(b.String(), "\n")))
		return m, nil

	case "/quit":
		return m, tea.Quit
	}

	m.appendLine(styleErr.Render("unknown command: " + name))
	return m, nil
}
