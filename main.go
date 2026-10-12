package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	styleUser     = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	styleAssist   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	styleTool     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	styleApproval = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	styleDenied   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleDim      = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	styleErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)

	styleHeader = lipgloss.NewStyle().
			Background(lipgloss.Color("235")).Foreground(lipgloss.Color("255")).
			Padding(0, 1)
	styleStatusBar = lipgloss.NewStyle().
			Background(lipgloss.Color("236")).Foreground(lipgloss.Color("250")).
			Padding(0, 1)
	styleInputBox = lipgloss.NewStyle().
		// Top/bottom only - side borders (true, right, bottom, left) added
		// visual noise without adding information at full terminal width.
		Border(lipgloss.RoundedBorder(), true, false, true, false).
		BorderForeground(lipgloss.Color("63")).
		Padding(0, 1)
	styleUserMark  = lipgloss.NewStyle().Foreground(lipgloss.Color("63")).Bold(true)
	styleUserText  = lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	styleAgentRule = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))

	accentTool     = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	accentApproval = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	accentDenied   = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))

	styleSidebar = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder(), false, false, false, true).
			BorderForeground(lipgloss.Color("236")).Padding(0, 1)
	styleSidebarLabel = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("255"))
	styleSidebarValue = lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
)

// sidebarWidth is the fixed width of the right-hand status panel; the
// transcript gets whatever's left of the terminal.
const sidebarWidth = 34

// humanTokens formats a token count the way the sidebar does: exact under
// 1000, one decimal place in thousands above it (e.g. "21.1k").
func humanTokens(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%.1fk", float64(n)/1000)
}

// renderSidebar builds the right-hand status panel: session activity,
// token usage, compaction budget, attachments, and what tools/skills are
// available. height is the inner content height to pad to, so its border
// lines up with the transcript beside it regardless of content length.
func renderSidebar(sess *Session, pendingImages int, height int) string {
	// sidebarWidth minus the left border (1) and left+right padding (2).
	const innerWidth = sidebarWidth - 3

	stats := sess.Stats()
	set := sess.Settings()
	w := innerWidth - 2 // the box counts its padding inside Width
	var b strings.Builder
	rule := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	bar := lipgloss.NewStyle().Foreground(lipgloss.Color("71")).Render("▌") + " "
	// heading draws "Label ────" across the panel; lines are wrapped before
	// styling so long values flow onto a second line instead of being clipped.
	heading := func(label string) {
		pad := w - lipgloss.Width(label) - 1
		if pad < 0 {
			pad = 0
		}
		b.WriteString(styleSidebarLabel.Render(label) + " " + rule.Render(strings.Repeat("─", pad)) + "\n")
	}
	line := func(l string) {
		b.WriteString(styleSidebarValue.Render(lipgloss.NewStyle().Width(w).Render(l)) + "\n")
	}
	gap := func() { b.WriteString("\n") }

	heading("Session")
	title, _ := sess.Label()
	if title == "" {
		title = "untitled"
	}
	line(title)
	dir := strings.Replace(sess.ProjectRoot, os.Getenv("HOME"), "~", 1)
	if r := []rune(dir); len(r) > w-2 {
		dir = "…" + string(r[len(r)-(w-3):])
	}
	line(bar + dir)
	line(fmt.Sprintf("%d turns, %d tool calls", stats.Turns, stats.ToolCalls))
	if pendingImages > 0 {
		line(fmt.Sprintf("%d image%s pending", pendingImages, plural(pendingImages)))
	}
	gap()

	heading("Token Usage")
	if stats.Turns > 0 {
		line(fmt.Sprintf("%s in, %s out", humanTokens(stats.PromptTokens), humanTokens(stats.CompletionTokens)))
		line(fmt.Sprintf("cache %.0f%% · %.1f tok/s", stats.SessionCachePct, stats.AvgTPS))
		line("latest: " + orDash(hostSpeedLabel(stats)))
	} else {
		line("no calls yet")
	}
	// defaultContextTokens keeps this instant: a live per-model figure would
	// mean a blocking network fetch from View() on every keystroke.
	line("compacts at " + humanTokens(promptBudget(defaultContextTokens)))
	gap()

	heading("Agents")
	provider := set.Provider
	if provider == "" {
		provider = "llama-swap"
	}
	line(styleSidebarLabel.Render("main"))
	line("├ Provider: " + provider)
	line("└ Model: " + set.Model)
	gap()
	line(styleSidebarLabel.Render("subagent"))
	switch {
	case stats.SubRunning:
		line(fmt.Sprintf("├ running: %s", stats.SubModel))
		line(fmt.Sprintf("└ %d tool call%s · %ds", stats.SubCalls, plural(stats.SubCalls), int(stats.SubElapsed.Seconds())))
	case stats.SubRuns > 0:
		line("├ Model: " + subagentModelFor(set))
		line(fmt.Sprintf("└ idle · %d run%s done", stats.SubRuns, plural(stats.SubRuns)))
	default:
		line("├ Model: " + subagentModelFor(set))
		line("└ idle")
	}
	gap()

	heading("Tools")
	line(fmt.Sprintf("%s%d tools available", bar, len(toolSchemas)))
	line(fmt.Sprintf("%s%d skills available", bar, len(discoverSkills(sess.ProjectRoot))))

	content := strings.TrimRight(b.String(), "\n")
	return styleSidebar.Width(innerWidth).Height(height).Render(content)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// accentBlock prefixes every line of s with a colored left bar, giving
// tool/approval/error output a "card" feel instead of plain colored text.
// Content is word-wrapped to width first so long tool output/args don't run
// off the edge of the viewport instead of wrapping like everything else.
func accentBlock(bar lipgloss.Style, s string, width int) string {
	prefix := bar.Render("┃")
	contentWidth := width - 2 // "┃ "
	if contentWidth < 10 {
		contentWidth = 10
	}
	wrapped := lipgloss.NewStyle().Width(contentWidth).Render(strings.TrimRight(s, "\n"))
	lines := strings.Split(wrapped, "\n")
	for i, l := range lines {
		lines[i] = prefix + " " + l
	}
	return strings.Join(lines, "\n")
}

// wrapToolResult word-wraps a raw tool result to width and prefixes the
// first line with an arrow, continuation lines indented to match, so long
// output wraps like everything else instead of running off the viewport.
func wrapToolResult(s string, width int) string {
	contentWidth := width - 4 // "  → "
	if contentWidth < 10 {
		contentWidth = 10
	}
	wrapped := lipgloss.NewStyle().Width(contentWidth).Render(s)
	lines := strings.Split(wrapped, "\n")
	for i, l := range lines {
		if i == 0 {
			lines[i] = "  → " + l
		} else {
			lines[i] = "    " + l
		}
	}
	return strings.Join(lines, "\n")
}

const fallbackBubbleWidth = 80 // used only before the first real terminal size is known

func bubbleWidth(m model) int {
	if m.ready && m.viewport.Width > 0 {
		return m.viewport.Width
	}
	return fallbackBubbleWidth
}

// A user message is a left-aligned header ("❯ message") and an agent reply
// carries a coloured rule down its left edge, so a turn reads top to bottom:
// the question, the work, the answer. The model's name is shown only when it
// differs from the previous reply's, so a mid-conversation /model switch is
// visible without tagging every reply.
func userBubble(width int, text string) string {
	inner := width - 2 // "❯ "
	if inner < 10 {
		inner = 10
	}
	lines := strings.Split(lipgloss.NewStyle().Width(inner).Render(text), "\n")
	for i, l := range lines {
		l = styleUserText.Render(strings.TrimRight(l, " "))
		if i == 0 {
			lines[i] = styleUserMark.Render("❯") + " " + l
		} else {
			lines[i] = "  " + l
		}
	}
	return strings.Join(lines, "\n")
}

// ruled puts the agent's left rule in front of every line, blank ones too, so
// it runs unbroken down the whole reply.
func ruled(lines []string, modelName string, showModel bool) string {
	if showModel && modelName != "" {
		lines = append([]string{styleDim.Render(modelName)}, lines...)
	}
	rule := styleAgentRule.Render("┃") + " "
	for i, l := range lines {
		lines[i] = rule + l
	}
	return strings.Join(lines, "\n")
}

func agentBubble(width int, modelName string, showModel bool, text string) string {
	return ruled(strings.Split(renderMarkdown(text, width-2), "\n"), modelName, showModel)
}

// agentBubblePlain is agentBubble without markdown formatting, for a reply
// that is still streaming in: partial markdown renders badly, so the
// formatted version only replaces it once the reply is complete.
func agentBubblePlain(width int, modelName string, showModel bool, text string) string {
	inner := width - 2
	if inner < 10 {
		inner = 10
	}
	lines := strings.Split(lipgloss.NewStyle().Width(inner).Render(text), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	return ruled(lines, modelName, showModel)
}

// transcriptEntry is a raw, width-independent record of one thing that
// happened - a bubble or a styled line - kept separately from the rendered
// transcript string so the whole thing can be re-rendered from scratch at a
// new width on resize instead of staying baked at whatever width it was
// first drawn at.
type entryKind int

const (
	entryRaw entryKind = iota
	entryUser
	entryAgent
	entryTool    // a tool call, drawn as one line (see toolview.go)
	entryDivider // a thin rule between turns
)

type transcriptEntry struct {
	kind  entryKind
	text  string     // pre-styled string for entryRaw, plain text for bubbles
	model string     // entryAgent only: which model produced this reply, if known
	tool  *toolEntry // entryTool only; updated in place as its approval and result arrive
}

type appState int

const (
	stateIdle appState = iota
	stateStreaming
	stateAwaitingApproval
)

// agentEventMsg carries one Event from the running Session into Bubble Tea.
type agentEventMsg struct{ ev Event }

// approvalRequestMsg is sent by the TUI's Approver: the run goroutine blocks
// on reply until the user presses y or n.
type approvalRequestMsg struct {
	toolCall ToolCall
	reason   string
	reply    chan bool
}

// compactDoneMsg is the result of /compact running in the background.
type compactDoneMsg struct {
	res CompactResult
	err error
}

// runClosedMsg means the run goroutine has exited and closed its channel.
type runClosedMsg struct{}

type model struct {
	sess *Session

	wizard     *providerWizard   // set while "/provider add" is asking its questions
	entries    []transcriptEntry // source of truth, re-rendered into transcript on any width change
	transcript string            // cached render of entries at the current width
	input      textinput.Model
	viewport   viewport.Model
	ready      bool

	state            appState
	approval         *approvalRequestMsg
	events           chan tea.Msg // the current run's events, nil when idle
	streamingContent string
	statusText       string             // transient progress line ("loading model…") shown until the first token arrives
	reasoningChars   int                // how much the model has "thought" since its last reply
	reasoningText    string             // the thinking itself, shown live only while showThinking is on
	showThinking     bool               // Ctrl+T: render the model's reasoning in the transcript
	toolStarting     string             // a tool call the model is writing, if any
	expandTools      bool               // Ctrl+O: show tool output under each tool call line
	turnStart        time.Time          // when the current turn began, for the footer
	turnTools        int                // tool calls in the current turn, for the footer
	cancelFunc       context.CancelFunc // cancels the current run, if any

	showSlashMenu bool
	slashMatches  []slashCommand
	slashSelected int
	picker        *pickerState
	pickSubagent  bool // the next model list is for /subagent, not /model

	pendingImages []string // data URLs attached via /image, sent (and cleared) with the next message
	pendingNames  []string // display names, same order as pendingImages

	showSidebar bool // false on terminals too narrow to fit it beside the transcript
	termWidth   int  // full terminal width - header/status/input span this, not just the transcript
}

func initialModel(sess *Session) model {
	ti := textinput.New()
	ti.Placeholder = "message…"
	ti.Focus()
	ti.CharLimit = 8000

	m := model{sess: sess, input: ti}
	m.loadTranscript("session: " + sess.Path())
	return m
}

// loadTranscript replaces the transcript with a header line plus the
// session's user and assistant messages.
func (m *model) loadTranscript(header string) {
	m.entries = []transcriptEntry{
		{kind: entryRaw, text: styleDim.Render(header)},
		{kind: entryRaw, text: ""},
	}
	for _, msg := range m.sess.Messages() {
		if msg.Content == nil {
			continue
		}
		switch msg.Role {
		case "user":
			if m.hasUserEntry() {
				m.entries = append(m.entries, transcriptEntry{kind: entryDivider})
			}
			m.entries = append(m.entries, transcriptEntry{kind: entryUser, text: *msg.Content}, transcriptEntry{kind: entryRaw, text: ""})
		case "assistant":
			m.entries = append(m.entries, transcriptEntry{kind: entryAgent, text: *msg.Content, model: msg.Model}, transcriptEntry{kind: entryRaw, text: ""})
		}
	}
	m.rebuildTranscript()
	if m.ready {
		m.viewport.GotoBottom()
	}
}

func (m model) Init() tea.Cmd { return nil }

// rebuildTranscript re-renders the entire transcript from m.entries at the
// current terminal width. Called on every append and on every resize, so
// bubbles never stay baked at a stale width.
func (m *model) rebuildTranscript() {
	w := bubbleWidth(*m)
	var b strings.Builder
	lastModel := ""
	for _, e := range m.entries {
		switch e.kind {
		case entryUser:
			b.WriteString(userBubble(w, e.text))
		case entryAgent:
			b.WriteString(agentBubble(w, e.model, e.model != "" && e.model != lastModel, e.text))
			if e.model != "" {
				lastModel = e.model
			}
		case entryTool:
			b.WriteString(renderToolEntry(e.tool, w, m.expandTools))
		case entryDivider:
			b.WriteString(styleDim.Render(strings.Repeat("─", w)))
		default:
			b.WriteString(e.text)
		}
		b.WriteString("\n")
	}
	m.transcript = b.String()
	if m.ready {
		m.viewport.SetContent(m.transcript)
	}
}

func (m *model) appendLine(s string) {
	m.entries = append(m.entries, transcriptEntry{kind: entryRaw, text: s})
	m.rebuildTranscript()
	if m.ready {
		m.viewport.GotoBottom()
	}
}

// lastAgentModel is the model that wrote the most recent reply in the
// transcript, or "" if none did.
func (m *model) lastAgentModel() string {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if e := m.entries[i]; e.kind == entryAgent && e.model != "" {
			return e.model
		}
	}
	return ""
}

// hasUserEntry reports whether the transcript already holds a user message.
func (m *model) hasUserEntry() bool {
	for _, e := range m.entries {
		if e.kind == entryUser {
			return true
		}
	}
	return false
}

func (m *model) appendTool(t *toolEntry) {
	m.entries = append(m.entries, transcriptEntry{kind: entryTool, tool: t})
	m.rebuildTranscript()
	if m.ready {
		m.viewport.GotoBottom()
	}
}

// toolByID finds a tool call's entry, newest first.
func (m *model) toolByID(id string) *toolEntry {
	for i := len(m.entries) - 1; i >= 0; i-- {
		if t := m.entries[i].tool; t != nil && t.id == id {
			return t
		}
	}
	return nil
}

func (m *model) appendUserBubble(text string) {
	if m.hasUserEntry() {
		m.entries = append(m.entries, transcriptEntry{kind: entryDivider})
	}
	m.entries = append(m.entries, transcriptEntry{kind: entryUser, text: text})
	m.rebuildTranscript()
	if m.ready {
		m.viewport.GotoBottom()
	}
}

func (m *model) appendAgentBubble(text, modelName string) {
	m.entries = append(m.entries, transcriptEntry{kind: entryAgent, text: text, model: modelName})
	m.rebuildTranscript()
	if m.ready {
		m.viewport.GotoBottom()
	}
}

func modeLabel(st Settings) string {
	plan := "Build"
	if st.Plan {
		plan = "Plan"
	}
	auto := "Manual"
	if st.Auto {
		auto = "Auto"
	}
	if st.TDD {
		plan += "+TDD"
	}
	if st.ADHD {
		plan += "+ADHD"
	}
	if st.ICM {
		plan += "+ICM"
	}
	return fmt.Sprintf("%s/%s", plan, auto)
}

// updateSettings applies a change to the session's settings, reporting a
// refusal (a run in progress) in the transcript.
func (m *model) updateSettings(change func(*Settings)) {
	st := m.sess.Settings()
	change(&st)
	if err := m.sess.SetSettings(st); err != nil {
		m.appendLine(styleErr.Render("error: " + err.Error()))
	}
}

// attachImage reads a local image file and queues it to go out with the
// next message sent (not this one - the user may still be typing). path is
// resolved against the project root the same way tool calls are.
func (m model) attachImage(path string) (model, tea.Cmd) {
	full := resolvePath(path, m.sess.ProjectRoot)
	dataURL, name, err := imageDataURL(full)
	if err != nil {
		m.appendLine(styleErr.Render("error: " + err.Error()))
		return m, nil
	}
	m.pendingImages = append(m.pendingImages, dataURL)
	m.pendingNames = append(m.pendingNames, name)
	m.appendLine(styleDim.Render(fmt.Sprintf("  attached %s (%d pending; type your message and press Enter to send)", name, len(m.pendingImages))))
	return m, nil
}

// startRun runs the Session in a goroutine, forwarding its events and
// approval requests to Bubble Tea over a channel that is closed when the
// run ends. images are data: URLs to attach; pass nil for a plain message.
func (m model) startRun(text string, images []string) (model, tea.Cmd) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan tea.Msg, 64)
	m.events, m.cancelFunc = ch, cancel
	m.state = stateStreaming
	m.streamingContent = ""
	m.turnStart, m.turnTools = time.Now(), 0

	sess := m.sess
	go func() {
		defer close(ch)
		defer cancel()
		sink := func(ev Event) { ch <- agentEventMsg{ev} }
		approve := func(ctx context.Context, tc ToolCall, reason string) (bool, error) {
			reply := make(chan bool, 1)
			ch <- approvalRequestMsg{toolCall: tc, reason: reason, reply: reply}
			select {
			case ok := <-reply:
				return ok, nil
			case <-ctx.Done():
				return false, ctx.Err()
			}
		}
		sess.Run(ctx, text, images, sink, approve)
	}()
	return m, waitForMsg(ch)
}

func waitForMsg(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return runClosedMsg{}
		}
		return msg
	}
}

// handleEvent renders one Event from the running Session.
func (m model) handleEvent(ev Event) model {
	switch ev.Type {
	case EvUser:
		// already shown when Enter was pressed
	case EvStatus:
		m.statusText = ev.Text
	case EvDelta:
		m.statusText = ""
		m.streamingContent += ev.Text
		m.refreshStream()
	case EvReasoning:
		m.statusText = ""
		m.reasoningChars += len(ev.Text)
		m.reasoningText += ev.Text
		m.refreshStream()
	case EvToolStart:
		m.statusText = ""
		m.toolStarting = ev.Name
	case EvAssistant:
		m.statusText = ""
		m.reasoningChars, m.reasoningText, m.toolStarting = 0, "", ""
		m.streamingContent = ""
		if ev.Text != "" {
			m.appendAgentBubble(ev.Text, ev.Model)
			m.appendLine("")
		} else {
			m.rebuildTranscript() // drop the in-progress bubble
		}
	case EvToolCall:
		m.turnTools++
		t := &toolEntry{id: ev.ID, name: ev.Name, args: ev.Args, reason: ev.Reason, diff: ev.Diff, started: time.Now()}
		switch ev.Action {
		case "deny":
			t.status = toolBlocked
		case "ask":
			t.status = toolPending
		}
		m.appendTool(t)
	case EvApproval:
		if t := m.toolByID(ev.ID); t != nil {
			t.status = toolRunning
			if !ev.Approved {
				t.status = toolDenied
			}
			t.started = time.Now()
			m.rebuildTranscript()
		}
	case EvToolResult:
		if t := m.toolByID(ev.ID); t != nil {
			t.output, t.dur = ev.Output, time.Since(t.started)
			t.status = toolDone
			if outcomeOf(ev.Output) == "error" || strings.HasPrefix(ev.Output, "Blocked by policy") {
				t.status = toolFailed
			}
			m.rebuildTranscript()
		}
	case EvNotice:
		m.appendLine(styleDim.Render("  (" + ev.Text + ")"))
	case EvError:
		m.statusText = ""
		text := ev.Text
		if !strings.HasPrefix(text, "warning:") {
			text = "error: " + text
		}
		m.appendLine(styleErr.Render(text))
	case EvCancelled:
		m.statusText = ""
		m.appendLine(styleDim.Render("  (generation cancelled)"))
	case EvDone:
		m.statusText = ""
		for _, e := range m.entries {
			if t := e.tool; t != nil && (t.status == toolRunning || t.status == toolPending) {
				t.status = toolCancelled
			}
		}
		m.rebuildTranscript()
		if !m.turnStart.IsZero() {
			m.appendLine(styleDim.Render(turnFooter(m.turnTools, time.Since(m.turnStart), hostSpeedLabel(m.sess.Stats()))))
			m.turnStart = time.Time{}
		}
		m.reasoningChars, m.reasoningText, m.toolStarting = 0, "", ""
		m.state = stateIdle
		m.approval = nil
		m.cancelFunc = nil
		if m.streamingContent != "" {
			m.streamingContent = ""
			m.rebuildTranscript()
		}
	}
	return m
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		// header (1) + status bar (1) + bordered input box (3: border/content/border)
		chromeHeight := 6
		vpHeight := msg.Height - chromeHeight
		if vpHeight < 3 {
			vpHeight = 3
		}
		// The sidebar sits beside the transcript, not the input/status
		// bar/header - only the transcript gives up width for it. Below a
		// width where that would leave an unusably narrow transcript, drop
		// the sidebar entirely rather than render something unreadable.
		// Threshold kept low deliberately: the web UI's terminal panel is
		// only ~42% of the browser viewport (see .terminal-pane in
		// styles.css), so a typical laptop-width browser window lands
		// well under what a generous cutoff would require.
		m.termWidth = msg.Width
		vpWidth := msg.Width
		m.showSidebar = msg.Width-sidebarWidth-1 >= 24
		if m.showSidebar {
			vpWidth = msg.Width - sidebarWidth - 1
		}
		if !m.ready {
			m.viewport = viewport.New(vpWidth, vpHeight)
			m.ready = true
			m.rebuildTranscript()
			m.viewport.GotoBottom()
		} else {
			m.viewport.Width = vpWidth
			m.viewport.Height = vpHeight
			// Re-render every bubble/line at the new width instead of
			// leaving old content baked at whatever width it was first
			// drawn at.
			m.rebuildTranscript()
		}
		m.input.Width = msg.Width - 2
		return m, nil

	case agentEventMsg:
		m = m.handleEvent(msg.ev)
		return m, waitForMsg(m.events)

	case approvalRequestMsg:
		m.state = stateAwaitingApproval
		m.approval = &msg
		return m, waitForMsg(m.events)

	case compactDoneMsg:
		m.state, m.cancelFunc, m.statusText = stateIdle, nil, ""
		switch {
		case msg.err != nil && strings.HasPrefix(msg.err.Error(), "nothing to compact"):
			m.appendLine(styleDim.Render("  " + msg.err.Error()))
		case msg.err != nil:
			m.appendLine(styleErr.Render("compact: " + msg.err.Error()))
		default:
			m.loadTranscript("session: " + m.sess.Path())
			m.appendLine(styleDim.Render(fmt.Sprintf("  compacted: %d messages → %d, ~%s → ~%s tokens (earlier turns can no longer be undone)",
				msg.res.MessagesBefore, msg.res.MessagesAfter, humanTokens(msg.res.TokensBefore), humanTokens(msg.res.TokensAfter))))
		}
		return m, nil

	case runClosedMsg:
		m.events = nil
		return m, nil

	case tea.KeyMsg:
		if m.wizard != nil && m.state == stateIdle {
			switch msg.String() {
			case "esc":
				m.cancelWizard()
				return m, nil
			case "enter":
				return m.wizardEnter()
			}
		}
		if m.state == stateAwaitingApproval && m.approval != nil {
			switch msg.String() {
			case "y", "Y":
				m.approval.reply <- true
				if t := m.toolByID(m.approval.toolCall.ID); t != nil {
					t.status, t.started = toolRunning, time.Now()
					m.rebuildTranscript()
				}
				m.approval = nil
				m.state = stateStreaming
				return m, nil
			case "n", "N", "esc":
				m.approval.reply <- false
				if t := m.toolByID(m.approval.toolCall.ID); t != nil {
					t.status = toolDenied
					m.rebuildTranscript()
				}
				m.approval = nil
				m.state = stateStreaming
				return m, nil
			case "ctrl+c":
				return m, tea.Quit
			}
			return m, nil
		}

		if m.picker != nil {
			switch msg.String() {
			case "up":
				if m.picker.selected > 0 {
					m.picker.selected--
				}
				return m, nil
			case "down":
				if m.picker.selected < len(m.picker.items)-1 {
					m.picker.selected++
				}
				return m, nil
			case "enter":
				if len(m.picker.items) == 0 {
					m.picker = nil
					return m, nil
				}
				p := m.picker
				m.picker = nil
				return p.onSelect(m, p.items[p.selected].value)
			case "esc":
				m.picker = nil
				return m, nil
			case "ctrl+c":
				return m, tea.Quit
			}
			return m, nil
		}

		if m.showSlashMenu {
			switch msg.String() {
			case "up":
				if m.slashSelected > 0 {
					m.slashSelected--
				}
				return m, nil
			case "down":
				if m.slashSelected < len(m.slashMatches)-1 {
					m.slashSelected++
				}
				return m, nil
			case "tab", "enter":
				if len(m.slashMatches) > 0 {
					chosen := m.slashMatches[m.slashSelected]
					m.showSlashMenu = false
					if chosen.skill {
						// Leave "/name " in the box for the arguments.
						m.input.SetValue(chosen.name + " ")
						m.input.CursorEnd()
						return m, nil
					}
					m.input.Reset()
					return m.executeCommand(chosen.name)
				}
				return m, nil
			case "esc":
				m.showSlashMenu = false
				return m, nil
			case "ctrl+c":
				return m, tea.Quit
			}
			// any other key (typing, backspace) falls through to normal
			// input handling below, which also keeps the menu in sync
		}

		switch msg.String() {
		case "ctrl+c":
			if m.state == stateStreaming && m.cancelFunc != nil {
				// The run reports EvCancelled and then EvDone, which is
				// what returns the TUI to idle.
				m.cancelFunc()
				m.cancelFunc = nil
				return m, nil
			}
			if m.state == stateStreaming {
				return m, nil // already cancelling
			}
			return m, tea.Quit
		case "tab":
			if m.state == stateIdle {
				m.updateSettings(func(st *Settings) { st.Plan = !st.Plan })
			}
			return m, nil
		case "ctrl+o":
			m.expandTools = !m.expandTools
			m.rebuildTranscript()
			return m, nil
		case "ctrl+t":
			m.showThinking = !m.showThinking
			m.refreshStream()
			return m, nil
		case "ctrl+a":
			if m.state == stateIdle {
				m.updateSettings(func(st *Settings) { st.Auto = !st.Auto })
			}
			return m, nil
		case "pgup", "pgdown", "up", "down", "home", "end":
			// textinput is single-line and doesn't use these, so they're
			// free to repurpose for scrolling the transcript.
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		case "enter":
			if m.state != stateIdle || strings.TrimSpace(m.input.Value()) == "" {
				return m, nil
			}
			text := m.input.Value()
			if rest, ok := strings.CutPrefix(strings.TrimSpace(text), "/image "); ok {
				m.input.Reset()
				return m.attachImage(strings.TrimSpace(rest))
			}
			if f := strings.Fields(text); len(f) > 1 && strings.HasPrefix(f[0], "/") {
				for _, c := range slashCommands {
					if c.name == f[0] {
						m.input.Reset()
						m.showSlashMenu = false
						return m.executeCommand(strings.TrimSpace(text))
					}
				}
			}
			m.input.Reset()
			images, names := m.pendingImages, m.pendingNames
			m.pendingImages, m.pendingNames = nil, nil
			bubbleText := text
			if len(names) > 0 {
				bubbleText = fmt.Sprintf("📎 %s\n%s", strings.Join(names, ", "), text)
			}
			m.appendUserBubble(bubbleText)
			m.appendLine("")
			return m.startRun(text, images)
		default:
			if m.state == stateIdle {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				val := m.input.Value()
				// The menu completes the command word; once there's a space the
				// rest is arguments, and Enter sends the line as typed.
				if strings.HasPrefix(val, "/") && !strings.ContainsAny(val, " \t") {
					m.slashMatches = matchingCommands(val, m.sess.ProjectRoot)
					if len(m.slashMatches) > 10 {
						m.slashMatches = m.slashMatches[:10]
					}
					m.showSlashMenu = len(m.slashMatches) > 0
					if m.slashSelected >= len(m.slashMatches) {
						m.slashSelected = 0
					}
				} else {
					m.showSlashMenu = false
				}
				return m, cmd
			}
			return m, nil
		}

	case modelsFetchedMsg:
		if msg.err != nil {
			m.appendLine(styleErr.Render("error fetching models: " + msg.err.Error()))
			return m, nil
		}
		if len(msg.ids) == 0 {
			m.appendLine(styleDim.Render("  provider returned no models"))
			return m, nil
		}
		if m.pickSubagent {
			m.pickSubagent = false
			cur := m.sess.Settings().SubagentModel
			if cur == "" {
				cur = "default"
			}
			items := []pickerItem{{label: "(default: GOHARNESS_SUBAGENT_MODEL, else the session model)", value: ""}}
			for _, id := range msg.ids {
				items = append(items, pickerItem{label: id, value: id})
			}
			m.picker = &pickerState{
				title: "select subagent model (now: " + cur + ")", items: items,
				onSelect: func(m model, id string) (model, tea.Cmd) {
					m.updateSettings(func(st *Settings) { st.SubagentModel = id })
					now := m.sess.Settings()
					m.appendLine(styleDim.Render("  subagent model set to " + subagentModelFor(now)))
					return m, nil
				},
			}
			return m, nil
		}
		items := make([]pickerItem, len(msg.ids))
		for i, id := range msg.ids {
			items[i] = pickerItem{label: id, value: id}
		}
		m.picker = &pickerState{
			title: "select model", items: items,
			onSelect: func(m model, id string) (model, tea.Cmd) {
				m.updateSettings(func(st *Settings) { st.Model = id })
				m.appendLine(styleDim.Render("  model set to " + m.sess.Settings().Model))
				return m, nil
			},
		}
		return m, nil
	}

	return m, nil
}

func humanChars(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d chars", n)
	}
	return fmt.Sprintf("%.1fk chars", float64(n)/1000)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("… (%d more chars; scroll won't reveal them, ask the agent to re-read if needed)", len(s)-n)
}

// renderHeader is the slim top bar: app name and session title on the left,
// project folder and mode on the right. The title is the /rename name, or the
// start of the first message until the session is renamed.
func (m model) renderHeader() string {
	accent := lipgloss.NewStyle().Background(lipgloss.Color("235")).Foreground(lipgloss.Color("99")).Bold(true)
	dim := lipgloss.NewStyle().Background(lipgloss.Color("235")).Foreground(lipgloss.Color("245"))
	title, _ := m.sess.Label()
	if title == "" {
		title = sessionTitle(m.sess.Messages())
	}
	if title == "" {
		title = "new session"
	}
	right := strings.Replace(m.sess.ProjectRoot, os.Getenv("HOME"), "~", 1) + " · " + modeLabel(m.sess.Settings())
	room := m.termWidth - 2 - lipgloss.Width("◆ goharness  ") - lipgloss.Width(right) - 3
	if r := []rune(title); room > 3 && len(r) > room {
		title = string(r[:room-1]) + "…"
	} else if room <= 3 {
		title = ""
	}
	left := accent.Render("◆ goharness") + dim.Render("  "+title)
	gap := m.termWidth - 2 - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	line := left + dim.Render(strings.Repeat(" ", gap)+right)
	return styleHeader.Width(m.termWidth).Render(line) + "\n" + lipgloss.NewStyle().Foreground(lipgloss.Color("57")).Render(strings.Repeat("─", m.termWidth))
}

func (m model) View() string {
	if !m.ready {
		return "initializing…"
	}

	header := m.renderHeader()

	st := m.sess.Settings()
	stats := m.sess.Stats()
	left := statusLeft(st, m.sess.ToolRound(), estimateTokens(m.sess.Messages()), promptBudget(defaultContextTokens), hostSpeedLabel(stats))
	if lbl := subagentLabel(stats); lbl != "" {
		left += " · " + lbl
	}
	status := styleStatusBar.Width(m.termWidth).Render(statusBar(m.termWidth, left, keyHints(m.state == stateStreaming)))

	var bottomContent string
	switch {
	case m.state == stateAwaitingApproval:
		bottomContent = styleApproval.Render("approve? [y/n]")
	case m.picker != nil:
		bottomContent = renderPicker(m.picker)
	case m.state == stateStreaming && m.statusText != "":
		bottomContent = styleDim.Render("…" + m.statusText)
	case m.state == stateStreaming && m.toolStarting != "":
		bottomContent = styleDim.Render("…writing a " + m.toolStarting + " call")
	case m.state == stateStreaming && m.reasoningChars > 0:
		bottomContent = styleDim.Render(fmt.Sprintf("…thinking (%s of reasoning)", humanChars(m.reasoningChars)))
	case m.state == stateStreaming:
		bottomContent = styleDim.Render("…thinking")
	default:
		bottomContent = m.input.View()
		if len(m.pendingNames) > 0 {
			bottomContent = styleDim.Render("📎 attached: "+strings.Join(m.pendingNames, ", ")) + "\n" + bottomContent
		}
		if m.showSlashMenu {
			bottomContent += "\n" + renderSlashMenu(m.slashMatches, m.slashSelected)
		}
	}
	// lipgloss's Width() sets the TOTAL rendered width (padding included,
	// not added on top) - confirmed empirically, not just assumed - so this
	// should match viewport.Width exactly, no subtraction.
	bottomWidth := m.termWidth
	if bottomWidth < 1 {
		bottomWidth = 1
	}
	bottom := styleInputBox.Width(bottomWidth).Render(bottomContent)

	middle := m.viewport.View()
	if m.showSidebar {
		sidebar := renderSidebar(m.sess, len(m.pendingImages), m.viewport.Height)
		middle = lipgloss.JoinHorizontal(lipgloss.Top, m.viewport.View(), sidebar)
	}

	return header + "\n" + middle + "\n" + status + "\n" + bottom
}

func renderPicker(p *pickerState) string {
	var b strings.Builder
	b.WriteString(styleApproval.Render(p.title))
	b.WriteString("  (↑/↓ enter, esc to cancel)\n")
	for i, item := range p.items {
		if i == p.selected {
			b.WriteString(styleUser.Render("> " + item.label))
		} else {
			b.WriteString(styleDim.Render("  " + item.label))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderSlashMenu(matches []slashCommand, selected int) string {
	var b strings.Builder
	for i, c := range matches {
		line := fmt.Sprintf("%-10s %s", c.name, c.help)
		if i == selected {
			b.WriteString(styleUser.Render("> " + line))
		} else {
			b.WriteString(styleDim.Render("  " + line))
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

const defaultBaseURL = "http://172.16.10.40:8034/v1"
const defaultModel = "ornith_1.5_9B"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "web" {
		runWeb(os.Args[2:])
		return
	}

	baseURL, modelName := agentFlags(flag.CommandLine)
	resume := flag.String("resume", "", "path to a previous session JSON file to resume")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: goharness [flags]      terminal UI\n       goharness web [flags]  web UI (goharness web -h for its flags)\n\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	loadEnvFile()
	if err := loadCustomProviders(); err != nil {
		fmt.Fprintln(os.Stderr, "warning: ignoring custom providers:", err)
	}
	if err := loadUserRules(); err != nil {
		fmt.Fprintln(os.Stderr, "warning: ignoring permission rules:", err)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not determine working directory:", err)
		os.Exit(1)
	}

	var resumed []Message
	sessionPath := *resume
	if sessionPath != "" {
		resumed, err = loadSession(sessionPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "could not load session:", err)
			os.Exit(1)
		}
	} else {
		sessionPath, err = newSessionPath()
		if err != nil {
			fmt.Fprintln(os.Stderr, "could not create session file:", err)
			os.Exit(1)
		}
	}

	m := initialModel(NewSession(*baseURL, *modelName, cwd, sessionPath, resumed))
	// No mouse mode: enabling it would let PgUp/PgDn/arrow-key scrolling
	// gain wheel support too, but it also hands mouse control to the app in
	// most terminals, breaking native click-drag text selection/copy - a
	// worse trade for a coding tool than not having wheel scroll.
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	// The alternate screen leaves nothing behind, so say where the
	// conversation went.
	if fm, ok := final.(model); ok {
		if path := fm.sess.Path(); fileExists(path) {
			fmt.Printf("session saved: %s\nresume with: goharness -resume %s\n", path, path)
		}
	}
}

// agentFlags registers the flags shared by the TUI and the web server.
func agentFlags(fs *flag.FlagSet) (baseURL, modelName *string) {
	baseURL = fs.String("base-url", defaultBaseURL, "OpenAI-compatible base URL (llama-swap)")
	modelName = fs.String("model", defaultModel, "model id to use")
	fs.IntVar(&defaultMaxRounds, "max-rounds", defaultMaxRounds, "tool-calling rounds per message before asking for a final answer (1-50)")
	fs.IntVar(&defaultContextTokens, "context-tokens", defaultContextTokens, "context window to assume when the model server doesn't report one")
	return baseURL, modelName
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// refreshStream redraws the in-progress reply under the finished transcript:
// the model's reasoning (dim, only while Ctrl+T has it on) then its answer.
func (m *model) refreshStream() {
	if !m.ready {
		return
	}
	w := bubbleWidth(*m)
	out := m.transcript
	if m.showThinking && m.reasoningText != "" {
		out += styleDim.Render(lipgloss.NewStyle().Width(w).Render("thinking: "+strings.TrimSpace(m.reasoningText))) + "\n\n"
	}
	if m.streamingContent != "" {
		model := m.sess.Settings().Model
		out += agentBubblePlain(w, model, model != "" && model != m.lastAgentModel(), m.streamingContent)
	}
	m.viewport.SetContent(out)
	m.viewport.GotoBottom()
}

// orDash is s, or "-" when it is empty.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
