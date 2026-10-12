package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// Dictation: the browser records speech and posts it here; the audio is passed
// on to a speech-to-text server (the Parakeet Redux server in the homelab, or
// any OpenAI-style /v1/audio/transcriptions endpoint) and the text comes back.
// Going through goharness keeps the STT server's address and token out of the
// browser and out of cross-origin trouble.
//
// GOHARNESS_STT_URL is the server's base URL (unset = dictation is off);
// GOHARNESS_STT_TOKEN, if set, is sent to it as a bearer token.

const maxDictationBytes = 26 << 20 // a little over the STT server's 25 MB limit

func sttURL() string { return strings.TrimRight(os.Getenv("GOHARNESS_STT_URL"), "/") }

func (s *webServer) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	base := sttURL()
	if base == "" {
		writeError(w, http.StatusNotImplemented, "speech-to-text is not configured: set GOHARNESS_STT_URL to the server's address")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxDictationBytes)
	req, err := http.NewRequestWithContext(r.Context(), "POST", base+"/v1/audio/transcriptions", r.Body)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	req.ContentLength = r.ContentLength
	req.Header.Set("Content-Type", r.Header.Get("Content-Type"))
	if tok := os.Getenv("GOHARNESS_STT_TOKEN"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, "could not reach the speech-to-text server: "+err.Error())
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		if d := jsonField(body, "detail"); d != "" {
			msg = d
		}
		writeError(w, resp.StatusCode, msg)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// jsonField returns a top-level string field of a JSON object, or "".
func jsonField(body []byte, name string) string {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		return ""
	}
	v, _ := m[name].(string)
	return v
}
