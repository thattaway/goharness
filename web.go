package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// The web UI server: a JSON + Server-Sent Events API over agent Sessions,
// plus the embedded React build. The agent can run shell commands, so every
// /api request needs the access token.

const (
	tokenCookie     = "goharness_token"
	maxBodyBytes    = 1 << 20
	sseHeartbeat    = 25 * time.Second
	subscriberQueue = 512
)

type webServer struct {
	baseURL      string
	defaultModel string
	projectRoot  string   // the default project: the folder goharness web started in
	projectRoots []string // folders whose subfolders (at any depth) are also selectable projects
	allowAnyDir  bool     // --allow-any-dir: any folder on the machine may be a session's project
	token        string
	static       http.Handler

	mu       sync.Mutex
	sessions map[string]*webSession
}

// webSession wraps a Session with what the browser needs: numbered events
// for the current run (so a reloaded page or a dropped connection can
// catch up), live subscribers, and tool calls waiting for approval.
type webSession struct {
	id   string
	sess *Session

	mu       sync.Mutex
	seq      int64
	running  bool
	runStart int64     // seq of the current (or last) run's first event
	events   []Event   // every event of the current (or last) run
	logPath  string    // where events are persisted; see eventsLogPath
	base     []Message // the conversation as it was when the current run started
	cancel   context.CancelFunc
	subs     map[chan Event]struct{}
	pending  map[string]chan bool // tool call id -> approval answer
	deleted  bool
}

func newWebServer(baseURL, defaultModel, projectRoot, token string, static http.Handler) *webServer {
	return &webServer{
		baseURL:      baseURL,
		defaultModel: defaultModel,
		projectRoot:  projectRoot,
		token:        token,
		static:       static,
		sessions:     map[string]*webSession{},
	}
}

func (s *webServer) routes() http.Handler {
	api := http.NewServeMux()
	api.HandleFunc("GET /api/info", s.handleInfo)
	api.HandleFunc("GET /api/providers", s.handleProviders)
	api.HandleFunc("POST /api/providers", s.handleCreateProvider)
	api.HandleFunc("POST /api/providers/models", s.handleProviderModels)
	api.HandleFunc("DELETE /api/providers/{name}", s.handleDeleteProvider)
	api.HandleFunc("GET /api/models", s.handleModels)
	api.HandleFunc("GET /api/sessions", s.handleListSessions)
	api.HandleFunc("POST /api/sessions", s.handleCreateSession)
	api.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	api.HandleFunc("PATCH /api/sessions/{id}", s.handlePatchSession)
	api.HandleFunc("GET /api/sessions/{id}/turns/{turn}/changes", s.handleTurnChanges)
	api.HandleFunc("GET /api/sessions/{id}/skills", s.handleSkills)
	api.HandleFunc("POST /api/sessions/delete", s.handleDeleteSessions)
	api.HandleFunc("POST /api/sessions/{id}/messages", s.handleSendMessage)
	api.HandleFunc("GET /api/sessions/{id}/events", s.handleEvents)
	api.HandleFunc("GET /api/sessions/{id}/terminal", s.handleTerminal)
	api.HandleFunc("POST /api/sessions/{id}/approvals/{callID}", s.handleApproval)
	api.HandleFunc("POST /api/sessions/{id}/cancel", s.handleCancel)
	api.HandleFunc("PUT /api/sessions/{id}/settings", s.handleSettings)
	api.HandleFunc("POST /api/sessions/{id}/undo", s.handleUndo)
	api.HandleFunc("POST /api/sessions/{id}/compact", s.handleCompact)
	api.HandleFunc("GET /api/sessions/{id}/stats", s.handleStats)
	api.HandleFunc("GET /api/groups", s.handleListGroups)
	api.HandleFunc("POST /api/groups", s.handleCreateGroup)
	api.HandleFunc("PATCH /api/groups/{id}", s.handleRenameGroup)
	api.HandleFunc("DELETE /api/groups/{id}", s.handleDeleteGroup)
	api.HandleFunc("GET /api/projects", s.handleProjects)
	api.HandleFunc("GET /api/dirs", s.handleDirs)
	api.HandleFunc("GET /api/permissions", s.handleGetPermissions)
	api.HandleFunc("PUT /api/permissions", s.handlePutPermissions)
	api.HandleFunc("POST /api/permissions/check", s.handleCheckPermission)
	api.HandleFunc("POST /api/reload", s.handleReload)
	api.HandleFunc("POST /api/transcribe", s.handleTranscribe)
	api.HandleFunc("POST /api/logout", s.handleLogout)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.Handle("/api/", s.requireAuth(api))
	mux.HandleFunc("/", s.handleStatic)
	return securityHeaders(checkOrigin(mux))
}

// ---- auth ----

func (s *webServer) validToken(t string) bool {
	return t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) == 1
}

func (s *webServer) authed(r *http.Request) bool {
	if c, err := r.Cookie(tokenCookie); err == nil && s.validToken(c.Value) {
		return true
	}
	if h, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok && s.validToken(h) {
		return true
	}
	return false
}

func (s *webServer) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *webServer) setTokenCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     tokenCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
}

func (s *webServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !s.validToken(strings.TrimSpace(body.Token)) {
		time.Sleep(500 * time.Millisecond) // slow down guessing
		writeError(w, http.StatusUnauthorized, "wrong token")
		return
	}
	s.setTokenCookie(w, r, s.token, 30*24*3600)
	w.WriteHeader(http.StatusNoContent)
}

// handleReload re-reads the env file, permission rules and model info, as
// the terminal UI's /reload does, and returns a summary to show.
func (s *webServer) handleReload(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"summary": reloadConfig(s.projectRoot)})
}

func (s *webServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.setTokenCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

// checkOrigin rejects state-changing requests sent by another site's page
// (cross-site request forgery). Requests with no Origin (curl) still need
// the token like any other.
func checkOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeError(w, http.StatusForbidden, "cross-origin request refused")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// handleStatic serves the React app. Opening /?token=... once swaps the
// token for a cookie and redirects so the token leaves the address bar.
func (s *webServer) handleStatic(w http.ResponseWriter, r *http.Request) {
	if t := r.URL.Query().Get("token"); t != "" {
		if s.validToken(t) {
			s.setTokenCookie(w, r, s.token, 30*24*3600)
		}
		q := r.URL.Query()
		q.Del("token")
		u := *r.URL
		u.RawQuery = q.Encode()
		http.Redirect(w, r, u.RequestURI(), http.StatusSeeOther)
		return
	}
	s.static.ServeHTTP(w, r)
}

// ---- sessions ----

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

func sessionIDFromPath(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".json")
}

func (s *webServer) lookup(id string) (*webSession, error) {
	if !sessionIDPattern.MatchString(id) || strings.Contains(id, "..") {
		return nil, os.ErrNotExist
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ws, ok := s.sessions[id]; ok {
		return ws, nil
	}
	dir, err := sessionsDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, id+".json")
	msgs, err := loadSession(path)
	if err != nil {
		return nil, err
	}
	root := s.projectRoot
	if p, ok := s.resolveProject(loadMeta(path).ProjectRoot); ok {
		root = p
	}
	ws := s.register(id, NewSession(s.baseURL, s.defaultModel, root, path, msgs))
	return ws, nil
}

type project struct {
	Path string `json:"path"`
	Name string `json:"name"`
}

// projects lists the folders a session may work in: the default project,
// then every non-hidden subfolder of each --projects-root.
func (s *webServer) projects() []project {
	out := []project{{Path: s.projectRoot, Name: filepath.Base(s.projectRoot)}}
	seen := map[string]bool{s.projectRoot: true}
	for _, root := range s.projectRoots {
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			p := filepath.Join(root, e.Name())
			// Symlinks are skipped: one could point at / or the home folder.
			if strings.HasPrefix(e.Name(), ".") || seen[p] || !e.IsDir() {
				continue
			}
			out = append(out, project{Path: p, Name: e.Name()})
			seen[p] = true
		}
	}
	return out
}

// browseRoots are the folders the folder browser and project choice are
// confined to when --allow-any-dir is off.
func (s *webServer) browseRoots() []string {
	return append([]string{s.projectRoot}, s.projectRoots...)
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// canBrowse reports whether the folder browser may list dir.
func (s *webServer) canBrowse(dir string) bool {
	if s.allowAnyDir {
		return true
	}
	for _, r := range s.browseRoots() {
		if within(r, dir) {
			return true
		}
	}
	return false
}

// resolveProject checks a folder chosen as a session's project and returns
// its real path (symlinks resolved, so a link can't escape the allowed
// roots). It must be an existing directory, not a secret one like ~/.ssh,
// and - unless --allow-any-dir - inside the default project or a projects root.
func (s *webServer) resolveProject(path string) (string, bool) {
	if !filepath.IsAbs(path) {
		return "", false
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return "", false
	}
	if info, err := os.Stat(real); err != nil || !info.IsDir() {
		return "", false
	}
	if sensitivePathText(real) || sensitivePathText(filepath.Clean(path)) {
		return "", false
	}
	if s.allowAnyDir || s.canBrowse(real) {
		return real, true
	}
	return "", false
}

func (s *webServer) allowedProject(path string) bool {
	_, ok := s.resolveProject(path)
	return ok
}

// handleDirs lists the subfolders of ?path= (default: the default project)
// for the folder browser.
func (s *webServer) handleDirs(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("path")
	if dir == "" {
		dir = s.projectRoot
	}
	dir = resolvePath(dir, s.projectRoot)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		writeError(w, http.StatusNotFound, "no such folder")
		return
	}
	if !s.canBrowse(dir) {
		writeError(w, http.StatusForbidden, "outside the folders goharness web may use (start it with --allow-any-dir to lift this)")
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	var visible, hidden []project
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		p := project{Path: filepath.Join(dir, e.Name()), Name: e.Name()}
		if strings.HasPrefix(e.Name(), ".") {
			hidden = append(hidden, p)
		} else {
			visible = append(visible, p)
		}
	}
	list := append(visible, hidden...)
	if len(list) > 1000 {
		list = list[:1000]
	}
	if list == nil {
		list = []project{}
	}
	parent := filepath.Dir(dir)
	if parent == dir || !s.canBrowse(parent) {
		parent = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": dir, "parent": parent, "entries": list, "allowed": s.allowedProject(dir),
	})
}

func (s *webServer) handleProjects(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.projects())
}

// register must be called with s.mu held.
func (s *webServer) register(id string, sess *Session) *webSession {
	ws := &webSession{id: id, sess: sess, subs: map[chan Event]struct{}{}, pending: map[string]chan bool{}}
	ws.logPath = eventsLogPath(sess.Path())
	if events, seq := loadEvents(ws.logPath); len(events) > 0 {
		ws.events, ws.seq, ws.runStart = events, seq, 1
	}
	s.sessions[id] = ws
	return ws
}

func (s *webServer) sessionFor(w http.ResponseWriter, r *http.Request) *webSession {
	ws, err := s.lookup(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "no such session")
		return nil
	}
	return ws
}

type sessionSummary struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Project string    `json:"project"`
	Updated time.Time `json:"updated"`
	Running bool      `json:"running"`
	Pinned  bool      `json:"pinned"`
	// Group is the id of the user-made group the session was moved into;
	// empty means it sits in its project's group.
	Group string `json:"group,omitempty"`
	// Match is a snippet of the message that matched a search, when the
	// title itself didn't.
	Match string `json:"match,omitempty"`
}

// handleListSessions lists saved sessions plus ones not yet saved, pinned
// first, then most recently updated. ?q= keeps only sessions whose title or
// messages contain the text (case-insensitive).
func (s *webServer) handleListSessions(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	files, err := listSessions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	seen := map[string]bool{}
	paths := map[string]string{}
	validGroup := map[string]bool{}
	for _, g := range loadGroups() {
		validGroup[g.ID] = true
	}
	var out []sessionSummary
	for i, f := range files {
		id := sessionIDFromPath(f.path)
		seen[id] = true
		paths[id] = f.path
		meta := loadMeta(f.path)
		sum := sessionSummary{ID: id, Updated: f.modTime, Title: meta.Title, Pinned: meta.Pinned}
		if validGroup[meta.Group] || isProjectGroupID(meta.Group) {
			sum.Group = meta.Group
		}
		if meta.ProjectRoot != "" {
			sum.Project = filepath.Base(meta.ProjectRoot)
		}
		// Deriving a title means reading the whole conversation, so only do
		// it for recent (or pinned) sessions.
		if sum.Title == "" && (i < 150 || sum.Pinned) {
			if msgs, err := loadSession(f.path); err == nil {
				sum.Title = sessionTitle(msgs)
			}
		}
		out = append(out, sum)
	}
	// Sessions created in the browser don't have a file until their first reply.
	s.mu.Lock()
	for id, ws := range s.sessions {
		if !seen[id] {
			title, pinned := ws.sess.Label()
			if title == "" {
				title = sessionTitle(ws.sess.Messages())
			}
			group := ws.sess.Group()
			if !validGroup[group] && !isProjectGroupID(group) {
				group = ""
			}
			out = append(out, sessionSummary{ID: id, Title: title, Pinned: pinned, Group: group, Project: filepath.Base(ws.sess.ProjectRoot), Updated: time.Now()})
		}
	}
	running := map[string]bool{}
	for id, ws := range s.sessions {
		running[id] = ws.isRunning()
	}
	s.mu.Unlock()
	for i := range out {
		out[i].Running = running[out[i].ID]
	}
	if query != "" {
		out = s.searchSessions(out, paths, query)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Pinned != out[j].Pinned {
			return out[i].Pinned
		}
		return out[i].Updated.After(out[j].Updated)
	})
	if out == nil {
		out = []sessionSummary{}
	}
	writeJSON(w, http.StatusOK, out)
}

// searchSessions keeps sessions whose title or conversation mentions query.
func (s *webServer) searchSessions(all []sessionSummary, paths map[string]string, query string) []sessionSummary {
	var out []sessionSummary
	for _, sum := range all {
		if strings.Contains(strings.ToLower(sum.Title), query) {
			out = append(out, sum)
			continue
		}
		var msgs []Message
		if p, ok := paths[sum.ID]; ok {
			msgs, _ = loadSession(p)
		} else {
			s.mu.Lock()
			if ws, ok := s.sessions[sum.ID]; ok {
				msgs = ws.sess.Messages()
			}
			s.mu.Unlock()
		}
		if snip := findSnippet(msgs, query); snip != "" {
			sum.Match = snip
			if sum.Title == "" {
				sum.Title = sessionTitle(msgs)
			}
			out = append(out, sum)
		}
	}
	if out == nil {
		out = []sessionSummary{}
	}
	return out
}

// findSnippet returns ~90 characters around the first user or assistant
// message text matching query, or "".
func findSnippet(msgs []Message, query string) string {
	for _, m := range msgs {
		if (m.Role != "user" && m.Role != "assistant") || m.Content == nil || isSystemNotice(m) {
			continue
		}
		text := strings.Join(strings.Fields(*m.Content), " ")
		lower := strings.ToLower(text)
		i := strings.Index(lower, query)
		if i < 0 {
			continue
		}
		// Work in runes so a multi-byte character is never cut in half.
		runes := []rune(text)
		start := len([]rune(text[:min(i, len(text))])) // lowercasing can change byte lengths
		from, to := max(0, start-35), min(len(runes), start+len([]rune(query))+55)
		snip := string(runes[from:to])
		if from > 0 {
			snip = "…" + snip
		}
		if to < len(runes) {
			snip += "…"
		}
		return snip
	}
	return ""
}

const maxTitleLen = 120

// handlePatchSession renames and/or pins a session. An empty title goes
// back to naming it after its first message.
func (s *webServer) handlePatchSession(w http.ResponseWriter, r *http.Request) {
	ws := s.sessionFor(w, r)
	if ws == nil {
		return
	}
	var body struct {
		Title  *string `json:"title"`
		Pinned *bool   `json:"pinned"`
		Group  *string `json:"group"` // a group id, or "" to go back to the project's group
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Title != nil {
		t := strings.Join(strings.Fields(*body.Title), " ")
		if len([]rune(t)) > maxTitleLen {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("titles can be at most %d characters", maxTitleLen))
			return
		}
		body.Title = &t
	}
	if body.Group != nil && *body.Group != "" && !validGroupID(*body.Group) {
		writeError(w, http.StatusBadRequest, errGroupNotFound.Error())
		return
	}
	if err := ws.sess.SetLabel(body.Title, body.Pinned); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if body.Group != nil {
		if err := ws.sess.SetGroup(*body.Group); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	title, pinned := ws.sess.Label()
	writeJSON(w, http.StatusOK, map[string]any{"title": title, "pinned": pinned, "group": ws.sess.Group()})
}

func groupError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, errGroupName):
		status = http.StatusBadRequest
	case errors.Is(err, errGroupDup):
		status = http.StatusConflict
	case errors.Is(err, errGroupNotFound):
		status = http.StatusNotFound
	}
	writeError(w, status, err.Error())
}

func (s *webServer) handleListGroups(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, loadGroups())
}

func (s *webServer) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	g, err := createGroup(body.Name)
	if err != nil {
		groupError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, g)
}

func (s *webServer) handleRenameGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	g, err := renameGroup(r.PathValue("id"), body.Name)
	if err != nil {
		groupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *webServer) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	if err := deleteGroup(r.PathValue("id")); err != nil {
		groupError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// trashDir is where deleted sessions go; trash older than trashKeep is
// cleared when the server starts.
const trashKeep = 30 * 24 * time.Hour

func trashDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".goharness", "trash"), nil
}

func pruneTrash() {
	dir, err := trashDir()
	if err != nil {
		return
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > trashKeep {
			os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// handleDeleteSessions deletes several sessions at once, moving their files
// to ~/.goharness/trash. Running sessions and unknown ids are skipped and
// reported, not treated as errors, so "select all, delete" does what it can.
func (s *webServer) handleDeleteSessions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if len(body.IDs) > 5000 {
		writeError(w, http.StatusBadRequest, "too many sessions in one request")
		return
	}
	trash, err := trashDir()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	batch := filepath.Join(trash, time.Now().Format("2006-01-02T15-04-05.000"))

	deleted := []string{}
	skipped := map[string]string{}
	for _, id := range body.IDs {
		ws, err := s.lookup(id)
		if err != nil {
			skipped[id] = "not found"
			continue
		}
		if reason := s.deleteSession(ws, batch); reason != "" {
			skipped[id] = reason
			continue
		}
		deleted = append(deleted, id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "skipped": skipped})
}

// deleteSession removes one session, returning why it couldn't, if so.
func (s *webServer) deleteSession(ws *webSession, trash string) string {
	ws.mu.Lock()
	if ws.running {
		ws.mu.Unlock()
		return "running; stop it first"
	}
	ws.deleted = true // refuse new runs from anyone still holding it
	ws.mu.Unlock()

	path := ws.sess.Path()
	for _, p := range []string{path, metaPath(path)} {
		if _, err := os.Stat(p); err != nil {
			continue // not saved yet
		}
		if err := os.MkdirAll(trash, 0o700); err != nil {
			return err.Error()
		}
		if err := os.Rename(p, filepath.Join(trash, filepath.Base(p))); err != nil {
			return err.Error()
		}
	}
	s.mu.Lock()
	delete(s.sessions, ws.id)
	s.mu.Unlock()
	ws.publish(Event{Type: evDeleted})
	return ""
}

// sessionTitle is the start of the first real user message.
func sessionTitle(msgs []Message) string {
	for _, m := range msgs {
		if m.Role == "user" && m.Content != nil && !strings.HasPrefix(*m.Content, "(system notice") {
			t := strings.Join(strings.Fields(*m.Content), " ")
			if r := []rune(t); len(r) > 80 {
				t = string(r[:80]) + "…"
			}
			return t
		}
	}
	return ""
}

func (s *webServer) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Project string `json:"project"`
	}
	if r.ContentLength != 0 && !readJSON(w, r, &body) {
		return
	}
	root := s.projectRoot
	if body.Project != "" {
		real, ok := s.resolveProject(body.Project)
		if !ok {
			writeError(w, http.StatusBadRequest, "can't use that folder: it must exist, not hold secrets (like ~/.ssh), and be one goharness web is allowed to use")
			return
		}
		root = real
	}
	path, err := newSessionPath()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.mu.Lock()
	// newSessionPath has one-second resolution; keep ids unique.
	id := sessionIDFromPath(path)
	for n := 2; ; n++ {
		if _, taken := s.sessions[id]; !taken {
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), id+".json")); err != nil {
				break
			}
		}
		id = fmt.Sprintf("%s-%d", sessionIDFromPath(path), n)
	}
	path = filepath.Join(filepath.Dir(path), id+".json")
	ws := s.register(id, NewSession(s.baseURL, s.defaultModel, root, path, nil))
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, ws.snapshot())
}

type sessionSnapshot struct {
	ID          string     `json:"id"`
	Messages    []Message  `json:"messages"`
	Settings    Settings   `json:"settings"`
	Running     bool       `json:"running"`
	ProjectRoot string     `json:"projectRoot"`
	CanUndo     bool       `json:"canUndo"`
	Turns       []turnInfo `json:"turns"`
	// Since is the event seq the messages are current as of: subscribe to
	// events with since=Since to pick up exactly where the snapshot ends.
	Since int64 `json:"since"`
}

// snapshot returns the conversation plus the event seq it matches. During
// a run it returns the conversation from before the run, with Since set so
// the run's events are replayed on top of it.
func (ws *webSession) snapshot() sessionSnapshot {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	snap := sessionSnapshot{ID: ws.id, Settings: ws.sess.Settings(), Running: ws.running, ProjectRoot: ws.sess.ProjectRoot, CanUndo: !ws.running && ws.sess.CanUndo()}
	if ws.running {
		snap.Messages, snap.Since = ws.base, ws.runStart-1
	} else {
		snap.Messages, snap.Since = ws.sess.Messages(), ws.seq
	}
	offset := 0
	if len(snap.Messages) > 0 && snap.Messages[0].Role == "system" {
		snap.Messages = snap.Messages[1:]
		offset = 1
	}
	snap.Turns = turnInfos(ws.sess.Turns(), offset, len(snap.Messages))
	return snap
}

// turnInfo tells the browser where each turn starts in the snapshot's
// messages and whether its file changes can be shown.
type turnInfo struct {
	Turn       int  `json:"turn"`     // 1-based, as used by the changes endpoint
	MsgIndex   int  `json:"msgIndex"` // index of the turn's user message in the snapshot
	Files      int  `json:"files"`
	HasHistory bool `json:"hasHistory"`
}

func turnInfos(turns []turnMark, offset, count int) []turnInfo {
	out := []turnInfo{}
	for i, t := range turns {
		idx := t.MsgIndex - offset
		if idx < 0 || idx >= count {
			continue // the turn in progress isn't in the snapshot yet
		}
		has := t.Checkpoint != "" && (t.After != "" || i+1 < len(turns))
		out = append(out, turnInfo{Turn: i + 1, MsgIndex: idx, Files: t.Files, HasHistory: has})
	}
	return out
}

// handleSkills lists the skills available to a session: the project's own
// plus the user-wide ones.
func (s *webServer) handleSkills(w http.ResponseWriter, r *http.Request) {
	ws := s.sessionFor(w, r)
	if ws == nil {
		return
	}
	skills := discoverSkills(ws.sess.ProjectRoot)
	if skills == nil {
		skills = []Skill{}
	}
	writeJSON(w, http.StatusOK, skills)
}

// handleTurnChanges returns a turn's file changes with diffs.
func (s *webServer) handleTurnChanges(w http.ResponseWriter, r *http.Request) {
	ws := s.sessionFor(w, r)
	if ws == nil {
		return
	}
	turn, err := strconv.Atoi(r.PathValue("turn"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "turn must be a number")
		return
	}
	files, err := ws.sess.Changes(turn)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"turn": turn, "files": files})
}

func (ws *webSession) isRunning() bool {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.running
}

func (s *webServer) handleGetSession(w http.ResponseWriter, r *http.Request) {
	if ws := s.sessionFor(w, r); ws != nil {
		writeJSON(w, http.StatusOK, ws.snapshot())
	}
}

// ---- runs, events, approvals ----

// eventsLogPath is where a session's events are persisted, so a run
// survives the goharness process itself restarting - not just the browser
// disconnecting, which already works via the in-memory replay buffer above
// and SSE's Last-Event-ID reconnect. Only the latest run is kept: this file
// is truncated at the start of each new run (see start, below).
func eventsLogPath(sessionPath string) string {
	return sessionPath + ".events.jsonl"
}

// appendEvent persists one event as a line of JSON. Best-effort: a
// failure here must not interrupt a live run, so errors are dropped.
func appendEvent(path string, ev Event) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	f.Write(b)
	f.Write([]byte("\n"))
}

// loadEvents reads a persisted event log back for a session being loaded
// fresh (after a restart, there is no in-memory copy). If the run it
// recorded never reached EvDone, the process died mid-run: an EvNotice is
// appended - both returned and persisted - so a reconnecting client sees
// why the run stopped instead of it looking silently stuck forever.
func loadEvents(path string) ([]Event, int64) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0
	}
	var events []Event
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		events = append(events, ev)
	}
	if len(events) == 0 {
		return nil, 0
	}
	seq := events[len(events)-1].Seq
	if events[len(events)-1].Type != EvDone {
		seq++
		notice := Event{Seq: seq, Type: EvNotice, Text: "goharness restarted before this run finished - interrupted"}
		events = append(events, notice)
		appendEvent(path, notice)
	}
	return events, seq
}

// publish numbers an event, keeps it for replay and fans it out. A
// subscriber that has fallen too far behind is dropped; its EventSource
// reconnects with Last-Event-ID and catches up from the replay buffer.
func (ws *webSession) publish(ev Event) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	ws.seq++
	ev.Seq = ws.seq
	ws.events = append(ws.events, ev)
	appendEvent(ws.logPath, ev)
	for ch := range ws.subs {
		select {
		case ch <- ev:
		default:
			delete(ws.subs, ch)
			close(ch)
		}
	}
}

func (ws *webSession) approve(ctx context.Context, tc ToolCall, reason string) (bool, error) {
	reply := make(chan bool, 1)
	ws.mu.Lock()
	ws.pending[tc.ID] = reply
	ws.mu.Unlock()
	defer func() {
		ws.mu.Lock()
		delete(ws.pending, tc.ID)
		ws.mu.Unlock()
	}()
	select {
	case ok := <-reply:
		return ok, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (ws *webSession) start(text string, images []string) error {
	ws.mu.Lock()
	if ws.running {
		ws.mu.Unlock()
		return ErrBusy
	}
	if ws.deleted {
		ws.mu.Unlock()
		return errors.New("this session was deleted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ws.running, ws.cancel = true, cancel
	ws.base = ws.sess.Messages()
	ws.events = nil
	ws.runStart = ws.seq + 1
	// Only the latest run's events are kept on disk: truncate here to
	// match the in-memory reset above, rather than growing without bound.
	os.WriteFile(ws.logPath, nil, 0o644)
	ws.mu.Unlock()

	go func() {
		defer cancel()
		ws.sess.Run(ctx, text, images, ws.publish, ws.approve)
		ws.mu.Lock()
		ws.running, ws.cancel = false, nil
		ws.mu.Unlock()
	}()
	return nil
}

func (s *webServer) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	ws := s.sessionFor(w, r)
	if ws == nil {
		return
	}
	var body struct {
		Text   string   `json:"text"`
		Images []string `json:"images"` // data: URLs from the browser (attach/paste)
	}
	if !readJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Text) == "" {
		writeError(w, http.StatusBadRequest, "text is required")
		return
	}
	if err := ws.start(body.Text, body.Images); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *webServer) handleCancel(w http.ResponseWriter, r *http.Request) {
	ws := s.sessionFor(w, r)
	if ws == nil {
		return
	}
	ws.mu.Lock()
	if ws.cancel != nil {
		ws.cancel()
	}
	ws.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *webServer) handleApproval(w http.ResponseWriter, r *http.Request) {
	ws := s.sessionFor(w, r)
	if ws == nil {
		return
	}
	var body struct {
		Approve bool `json:"approve"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	ws.mu.Lock()
	reply, ok := ws.pending[r.PathValue("callID")]
	delete(ws.pending, r.PathValue("callID"))
	ws.mu.Unlock()
	if !ok {
		writeError(w, http.StatusNotFound, "no tool call is waiting for that approval")
		return
	}
	reply <- body.Approve
	w.WriteHeader(http.StatusNoContent)
}

func (s *webServer) handleSettings(w http.ResponseWriter, r *http.Request) {
	ws := s.sessionFor(w, r)
	if ws == nil {
		return
	}
	st := ws.sess.Settings() // fields the client omits (e.g. subagentModel) keep their value
	if !readJSON(w, r, &st) {
		return
	}
	if err := ws.sess.SetSettings(st); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ws.sess.Settings())
}

// evReset tells browsers the conversation changed outside a run (an undo)
// and they should reload it.
const evReset = "reset"

// evDeleted tells browsers showing a session that it was deleted.
const evDeleted = "deleted"

func (s *webServer) handleUndo(w http.ResponseWriter, r *http.Request) {
	ws := s.sessionFor(w, r)
	if ws == nil {
		return
	}
	if ws.isRunning() {
		writeError(w, http.StatusConflict, ErrBusy.Error())
		return
	}
	res, err := ws.sess.Undo()
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrBusy) || err.Error() == "nothing to undo" {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	ws.publish(Event{Type: evReset})
	writeJSON(w, http.StatusOK, res)
}

// handleCompact summarizes the earlier conversation to free up context
// (the terminal UI's /compact). The session counts as running while the
// summary is written, so nothing else can start or change it, and Cancel works.
func (s *webServer) handleCompact(w http.ResponseWriter, r *http.Request) {
	ws := s.sessionFor(w, r)
	if ws == nil {
		return
	}
	ws.mu.Lock()
	if ws.running {
		ws.mu.Unlock()
		writeError(w, http.StatusConflict, ErrBusy.Error())
		return
	}
	if ws.deleted {
		ws.mu.Unlock()
		writeError(w, http.StatusConflict, "this session was deleted")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	ws.running, ws.cancel = true, cancel
	ws.mu.Unlock()

	res, err := ws.sess.Compact(ctx)

	cancel()
	ws.mu.Lock()
	ws.running, ws.cancel = false, nil
	ws.mu.Unlock()
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrBusy) || strings.HasPrefix(err.Error(), "nothing to compact") {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	ws.publish(Event{Type: evReset})
	writeJSON(w, http.StatusOK, map[string]int{
		"messagesBefore": res.MessagesBefore, "messagesAfter": res.MessagesAfter,
		"tokensBefore": res.TokensBefore, "tokensAfter": res.TokensAfter,
	})
}

// statsResponse is the session's usage telemetry for the page's status line:
// the same numbers the terminal sidebar shows, including the subagent.
type statsResponse struct {
	Turns            int     `json:"turns"`
	ToolCalls        int     `json:"toolCalls"`
	PromptTokens     int     `json:"promptTokens"`
	CompletionTokens int     `json:"completionTokens"`
	LastCachePct     float64 `json:"lastCachePct"`
	SessionCachePct  float64 `json:"sessionCachePct"`
	AvgTPS           float64 `json:"avgTps"`
	AvgTTFTSeconds   float64 `json:"avgTtftSeconds"`
	Endpoint         string  `json:"endpoint"`
	LastTPS          float64 `json:"lastTps"`
	ContextUsed      int     `json:"contextUsed"`
	ContextBudget    int     `json:"contextBudget"`
	Subagent         struct {
		Model          string `json:"model"` // the model a subagent will use (configured)
		Running        bool   `json:"running"`
		Count          int    `json:"count"` // how many are running at once
		RunningModel   string `json:"runningModel"`
		Calls          int    `json:"calls"`
		ElapsedSeconds int    `json:"elapsedSeconds"`
		Runs           int    `json:"runs"`
	} `json:"subagent"`
}

func (s *webServer) handleStats(w http.ResponseWriter, r *http.Request) {
	ws := s.sessionFor(w, r)
	if ws == nil {
		return
	}
	st := ws.sess.Stats()
	// The window of this session's own model (llama-swap reports it), not the global default.
	settings := ws.sess.Settings()
	baseURL, apiKey, _ := ws.sess.resolveProvider(settings.Provider)
	out := statsResponse{
		Turns: st.Turns, ToolCalls: st.ToolCalls,
		PromptTokens: st.PromptTokens, CompletionTokens: st.CompletionTokens,
		LastCachePct: st.LastCachePct, SessionCachePct: st.SessionCachePct,
		AvgTPS: st.AvgTPS, AvgTTFTSeconds: st.AvgTTFT.Seconds(),
		Endpoint: hostSpeedLabel(st), LastTPS: st.LastTPS,
		ContextUsed:   estimateTokens(ws.sess.Messages()),
		ContextBudget: promptBudget(modelContextTokens(baseURL, apiKey, settings.Model)),
	}
	out.Subagent.Model = subagentModelFor(ws.sess.Settings())
	out.Subagent.Running = st.SubRunning
	out.Subagent.Count = st.SubActive
	out.Subagent.RunningModel = st.SubModel
	out.Subagent.Calls = st.SubCalls
	out.Subagent.ElapsedSeconds = int(st.SubElapsed.Seconds())
	out.Subagent.Runs = st.SubRuns
	writeJSON(w, http.StatusOK, out)
}

func (s *webServer) handleGetPermissions(w http.ResponseWriter, r *http.Request) {
	path, _ := rulesPath()
	rules := currentUserRules()
	if rules == nil {
		rules = []Rule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"defaults": defaultRules(), "rules": rules, "path": path})
}

func (s *webServer) handlePutPermissions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Rules []Rule `json:"rules"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	rules, err := saveUserRules(body.Rules)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

// handleCheckPermission shows what a bash command would get, for trying
// out rules before relying on them.
func (s *webServer) handleCheckPermission(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Command string `json:"command"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	action, reason := check("bash", map[string]any{"command": body.Command}, s.projectRoot)
	writeJSON(w, http.StatusOK, map[string]string{"action": action, "reason": reason})
}

// handleEvents streams a session's events as Server-Sent Events, starting
// after the seq given by Last-Event-ID (on reconnect) or ?since=. If those
// events are no longer available, it sends a "resync" event and the client
// reloads the session snapshot.
func (s *webServer) handleEvents(w http.ResponseWriter, r *http.Request) {
	ws := s.sessionFor(w, r)
	if ws == nil {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	sinceStr := r.Header.Get("Last-Event-ID")
	if sinceStr == "" {
		sinceStr = r.URL.Query().Get("since")
	}
	since, _ := strconv.ParseInt(sinceStr, 10, 64)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	ws.mu.Lock()
	if since > ws.seq || (since < ws.runStart-1 && since != ws.seq) {
		ws.mu.Unlock()
		writeSSE(w, Event{Type: "resync"})
		flusher.Flush()
		return
	}
	var backlog []Event
	for _, ev := range ws.events {
		if ev.Seq > since {
			backlog = append(backlog, ev)
		}
	}
	ch := make(chan Event, subscriberQueue)
	ws.subs[ch] = struct{}{}
	ws.mu.Unlock()
	defer func() {
		ws.mu.Lock()
		if _, still := ws.subs[ch]; still {
			delete(ws.subs, ch)
			close(ch)
		}
		ws.mu.Unlock()
	}()

	for _, ev := range backlog {
		writeSSE(w, ev)
	}
	fmt.Fprint(w, "retry: 2000\n\n")
	flusher.Flush()

	heartbeat := time.NewTicker(sseHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-ch:
			if !open {
				return
			}
			writeSSE(w, ev)
			flusher.Flush()
		case <-heartbeat.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func writeSSE(w http.ResponseWriter, ev Event) {
	data, _ := json.Marshal(ev)
	if ev.Seq > 0 {
		fmt.Fprintf(w, "id: %d\n", ev.Seq)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}

// ---- misc endpoints ----

func (s *webServer) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"projectRoot":  s.projectRoot,
		"allowAnyDir":  s.allowAnyDir,
		"defaultModel": s.defaultModel,
		"stt":          sttURL() != "", // dictation: show the microphone button
	})
}

func (s *webServer) handleProviders(w http.ResponseWriter, r *http.Request) {
	type provInfo struct {
		ID        string   `json:"id"`
		Label     string   `json:"label"`
		BaseURL   string   `json:"baseUrl"`
		Models    []string `json:"models,omitempty"`
		Custom    bool     `json:"custom"`
		HasAPIKey bool     `json:"hasApiKey"`
	}
	out := []provInfo{{ID: "llama-swap", Label: "llama-swap", BaseURL: s.baseURL}}
	for _, p := range allProviders() {
		out = append(out, provInfo{
			ID: p.Name, Label: p.Name, BaseURL: p.BaseURL, Models: p.Models,
			Custom:    func() bool { _, ok := lookupCustomProvider(p.Name); return ok }(),
			HasAPIKey: p.APIKey != "" || p.APIKeyEnv != "",
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func lookupCustomProvider(name string) (provider, bool) {
	customMu.RLock()
	defer customMu.RUnlock()
	for _, p := range customProviders {
		if p.Name == name {
			return p, true
		}
	}
	return provider{}, false
}

func (s *webServer) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      string   `json:"name"`
		BaseURL   string   `json:"baseUrl"`
		Model     string   `json:"model"`
		APIKey    string   `json:"apiKey"`
		APIKeyEnv string   `json:"apiKeyEnv"`
		Models    []string `json:"models"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	body.BaseURL = strings.TrimSpace(body.BaseURL)
	body.Model = strings.TrimSpace(body.Model)
	models := body.Models
	if len(models) == 0 && body.Model != "" {
		models = []string{body.Model}
	}
	p := provider{Name: body.Name, BaseURL: body.BaseURL, APIKey: body.APIKey, APIKeyEnv: body.APIKeyEnv, Models: models}
	if err := saveCustomProvider(p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	clearModelInfoCaches()
	writeJSON(w, http.StatusCreated, map[string]string{"id": p.Name})
}

func (s *webServer) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := deleteCustomProvider(name); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	clearModelInfoCaches()
	w.WriteHeader(http.StatusNoContent)
}

// handleModels lists the model ids the browser can offer in its model
// picker. ?provider= selects which backend to ask - see providers.go;
// omitted or unrecognized falls back to llama-swap (this server's own
// configured -base-url), matching how a session with no Settings.Provider
// behaves.
func (s *webServer) handleModels(w http.ResponseWriter, r *http.Request) {
	baseURL, apiKey, fixed := resolveProviderFrom(s.baseURL, r.URL.Query().Get("provider"))
	if fixed != nil {
		ids := append([]string(nil), fixed...)
		sort.Strings(ids)
		writeJSON(w, http.StatusOK, ids)
		return
	}
	ids, err := listModels(baseURL, apiKey)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not list models: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ids)
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ---- startup ----

// loadOrCreateToken returns GOHARNESS_WEB_TOKEN if set, otherwise the token
// saved in ~/.config/goharness/web_token, creating it on first use.
func loadOrCreateToken() (string, error) {
	if t := strings.TrimSpace(os.Getenv("GOHARNESS_WEB_TOKEN")); t != "" {
		return t, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "goharness")
	path := filepath.Join(dir, "web_token")
	if data, err := os.ReadFile(path); err == nil {
		if t := strings.TrimSpace(string(data)); t != "" {
			return t, nil
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	t := hex.EncodeToString(buf)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(t+"\n"), 0o600); err != nil {
		return "", err
	}
	return t, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func runWeb(args []string) {
	fs := flag.NewFlagSet("web", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:8787", "address to listen on; use 0.0.0.0:8787 (or a Tailscale IP) to allow other machines")
	baseURL, modelName := agentFlags(fs)
	projectsRoot := fs.String("projects-root", "", "comma-separated folders whose subfolders can be picked as a session's project (the current folder always can)")
	allowAnyDir := fs.Bool("allow-any-dir", false, "let the browser pick any folder on this machine as a session's working directory")
	fs.Parse(args)
	loadEnvFile()
	if err := loadCustomProviders(); err != nil {
		fmt.Fprintln(os.Stderr, "warning: ignoring custom providers:", err)
	}
	if err := loadUserRules(); err != nil {
		fmt.Fprintln(os.Stderr, "warning: ignoring permission rules:", err)
	}
	pruneTrash()

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not determine working directory:", err)
		os.Exit(1)
	}
	token, err := loadOrCreateToken()
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not set up the access token:", err)
		os.Exit(1)
	}
	host, port, err := net.SplitHostPort(*addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid -addr:", err)
		os.Exit(1)
	}

	ws := newWebServer(*baseURL, *modelName, cwd, token, staticHandler())
	ws.allowAnyDir = *allowAnyDir
	for _, r := range strings.Split(*projectsRoot, ",") {
		if r = strings.TrimSpace(r); r != "" {
			abs, err := filepath.Abs(resolvePath(r, cwd))
			if err != nil {
				fmt.Fprintln(os.Stderr, "invalid -projects-root:", err)
				os.Exit(1)
			}
			ws.projectRoots = append(ws.projectRoots, abs)
		}
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           ws.routes(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	shown := host
	if host == "" || host == "0.0.0.0" || host == "::" {
		shown = "<this-machine's-ip>"
	}
	fmt.Printf("goharness web: default project %s\n", cwd)
	if n := len(ws.projects()); n > 1 {
		fmt.Printf("               %d projects selectable per session\n", n)
	}
	fmt.Printf("open http://%s/?token=%s\n", net.JoinHostPort(shown, port), token)
	if !isLoopback(host) {
		fmt.Println("warning: listening beyond this machine over plain HTTP. Anyone with the token can run")
		fmt.Println("         commands here. Prefer Tailscale (tailscale serve) or an SSH tunnel for HTTPS.")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if srv.Shutdown(shutdownCtx) != nil {
			srv.Close() // open event streams don't end on their own
		}
	}()
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// handleProviderModels lists the models an OpenAI-compatible server offers, for the "add a provider" form. The
// request is made from this server, not the browser, so keys held in environment variables never leave it.
func (s *webServer) handleProviderModels(w http.ResponseWriter, r *http.Request) {
	var body struct {
		BaseURL   string `json:"baseUrl"`
		APIKey    string `json:"apiKey"`
		APIKeyEnv string `json:"apiKeyEnv"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	key := body.APIKey
	if key != "" && body.APIKeyEnv != "" {
		writeError(w, http.StatusBadRequest, "use an API key or a key variable, not both")
		return
	}
	if body.APIKeyEnv != "" {
		if !envVarRe.MatchString(body.APIKeyEnv) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("%q is not a valid environment variable name", body.APIKeyEnv))
			return
		}
		if key = os.Getenv(body.APIKeyEnv); key == "" {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("the variable %s is not set where goharness runs (put it in ~/.config/goharness/env and restart)", body.APIKeyEnv))
			return
		}
	}
	ids, err := probeModels(strings.TrimSpace(body.BaseURL), key)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": ids})
}
