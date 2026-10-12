package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

var termUpgrader = websocket.Upgrader{
	// Cross-origin requests are already rejected by the checkOrigin
	// middleware wrapping the whole /api/ mux before this handler runs.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleTerminal opens a real interactive shell in the session's project
// directory and bridges it to the browser over a WebSocket, for xterm.js on
// the other end. This is a genuine shell - it bypasses the agent's tool-call
// approval system entirely, same trust level as SSH access to this machine
// as the goharness user.
//
// One shell per connection: closing the tab or losing the socket kills it.
// Nothing is persisted or resumable, unlike the agent's own session state -
// this is meant to behave like a normal terminal tab, not a saved one.
func (s *webServer) handleTerminal(w http.ResponseWriter, r *http.Request) {
	ws := s.sessionFor(w, r)
	if ws == nil {
		return
	}

	conn, err := termUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/bash"
	}
	cmd := exec.Command(shell)
	cmd.Dir = ws.sess.ProjectRoot
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		_ = conn.WriteMessage(websocket.TextMessage, []byte("failed to start shell: "+err.Error()))
		return
	}
	defer func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()

	done := make(chan struct{})

	// pty output -> browser
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if werr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// browser -> pty input, plus resize control messages
	type resizeMsg struct {
		Type string `json:"type"`
		Cols uint16 `json:"cols"`
		Rows uint16 `json:"rows"`
	}
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if mt == websocket.TextMessage && len(data) > 0 && data[0] == '{' {
			var m resizeMsg
			if json.Unmarshal(data, &m) == nil && m.Type == "resize" {
				_ = pty.Setsize(ptmx, &pty.Winsize{Cols: m.Cols, Rows: m.Rows})
				continue
			}
		}
		if _, err := ptmx.Write(data); err != nil {
			break
		}
	}
	<-done
}
