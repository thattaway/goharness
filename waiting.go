package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// While the model hasn't sent a first byte, the TUI would just say
// "thinking". With llama-swap that wait is often a model load (or swap) that
// takes a minute or more, or a long prompt being processed, so after a short
// delay the agent reports what it can tell about the wait. vars so tests can
// shorten them.
var (
	waitStatusAfter = 3 * time.Second
	waitStatusEvery = 2 * time.Second
)

type runningModel struct {
	Model string `json:"model"`
	State string `json:"state"`
}

// fetchRunning asks llama-swap which models are loaded (GET /running next to
// the /v1 prefix). Returns nil when the server has no such endpoint - Olla
// and the nginx shim in front of it don't - or can't be reached.
func fetchRunning(baseURL string) []runningModel {
	return fetchRunningCtx(context.Background(), baseURL)
}

func fetchRunningCtx(ctx context.Context, baseURL string) []runningModel {
	root := strings.TrimSuffix(strings.TrimRight(baseURL, "/"), "/v1")
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", root+"/running", nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var body struct {
		Running []runningModel `json:"running"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil {
		return nil
	}
	return body.Running
}

// waitingMessage turns what llama-swap reports into one status line.
func waitingMessage(running []runningModel, model string, elapsed time.Duration) string {
	secs := int(elapsed.Round(time.Second) / time.Second)
	loadedOther := false
	for _, r := range running {
		if r.Model != model {
			loadedOther = true
			continue
		}
		if r.State == "starting" {
			return fmt.Sprintf("loading %s… %ds", model, secs)
		}
		return fmt.Sprintf("waiting for %s… %ds (processing the prompt)", model, secs)
	}
	if loadedOther {
		return fmt.Sprintf("swapping to %s… %ds", model, secs)
	}
	return fmt.Sprintf("waiting for %s… %ds", model, secs)
}

// watchFirstByte reports progress to sink until the returned function is
// called (on the first streamed byte, or when the request ends). It stays
// silent when the model answers within waitStatusAfter, and ends with an
// EvStatus with empty Text if it said anything, so the line clears. No event
// is sent after the returned function returns.
func watchFirstByte(ctx context.Context, baseURL, model string, sink func(Event)) (done func()) {
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var once sync.Once
	spoke := false
	wg.Add(1)
	go func() {
		defer wg.Done()
		start := time.Now()
		select {
		case <-time.After(waitStatusAfter):
		case <-ctx.Done():
			return
		}
		tick := time.NewTicker(waitStatusEvery)
		defer tick.Stop()
		for {
			text := waitingMessage(fetchRunningCtx(ctx, baseURL), model, time.Since(start))
			if ctx.Err() != nil {
				return
			}
			spoke = true
			sink(Event{Type: EvStatus, Text: text})
			select {
			case <-tick.C:
			case <-ctx.Done():
				return
			}
		}
	}()
	return func() {
		once.Do(func() {
			cancel()
			wg.Wait()
			if spoke {
				sink(Event{Type: EvStatus})
			}
		})
	}
}
