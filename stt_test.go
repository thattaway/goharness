package main

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func audioUpload(t *testing.T, srv *httptest.Server) *http.Response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "rec.webm")
	fw.Write([]byte("fake audio bytes"))
	mw.Close()
	req, _ := http.NewRequest("POST", srv.URL+"/api/transcribe", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// fakeSTT stands in for the speech-to-text server and records what it was sent.
func fakeSTT(t *testing.T, status int, reply string) (url string, got *struct{ auth, ctype, body string }) {
	got = &struct{ auth, ctype, body string }{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got.auth, got.ctype, got.body = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), string(b)
		if r.URL.Path != "/v1/audio/transcriptions" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, got
}

func TestTranscribeForwardsTheRecordingAndReturnsTheText(t *testing.T) {
	stt, got := fakeSTT(t, 200, `{"text":"hello there"}`)
	t.Setenv("GOHARNESS_STT_URL", stt+"/")
	t.Setenv("GOHARNESS_STT_TOKEN", "")
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "x"} })
	resp := audioUpload(t, srv)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	out := decode[map[string]string](t, resp)
	if out["text"] != "hello there" {
		t.Fatalf("got %v", out)
	}
	if !strings.HasPrefix(got.ctype, "multipart/form-data") || !strings.Contains(got.body, "fake audio bytes") {
		t.Fatalf("the upload was not forwarded intact: %q / %q", got.ctype, got.body)
	}
	if got.auth != "" {
		t.Fatalf("the browser's goharness token must never be sent upstream, got %q", got.auth)
	}
}

func TestTranscribeSendsTheConfiguredSTTToken(t *testing.T) {
	stt, got := fakeSTT(t, 200, `{"text":"ok"}`)
	t.Setenv("GOHARNESS_STT_URL", stt)
	t.Setenv("GOHARNESS_STT_TOKEN", "s3cret")
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "x"} })
	audioUpload(t, srv).Body.Close()
	if got.auth != "Bearer s3cret" {
		t.Fatalf("upstream auth = %q", got.auth)
	}
}

func TestTranscribeWithoutAnSTTServerSaysSo(t *testing.T) {
	t.Setenv("GOHARNESS_STT_URL", "")
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "x"} })
	resp := audioUpload(t, srv)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if msg := decode[map[string]string](t, resp)["error"]; !strings.Contains(msg, "GOHARNESS_STT_URL") {
		t.Fatalf("error should say how to configure it: %q", msg)
	}
}

func TestTranscribePassesAnUpstreamRejectionOn(t *testing.T) {
	stt, _ := fakeSTT(t, 400, `{"detail":"could not read that as audio"}`)
	t.Setenv("GOHARNESS_STT_URL", stt)
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "x"} })
	resp := audioUpload(t, srv)
	if resp.StatusCode != 400 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if msg := decode[map[string]string](t, resp)["error"]; !strings.Contains(msg, "could not read that as audio") {
		t.Fatalf("error = %q", msg)
	}
}

func TestTranscribeReportsAnUnreachableSTTServer(t *testing.T) {
	t.Setenv("GOHARNESS_STT_URL", "http://127.0.0.1:1")
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "x"} })
	if resp := audioUpload(t, srv); resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestTranscribeNeedsTheWebToken(t *testing.T) {
	stt, _ := fakeSTT(t, 200, `{"text":"hi"}`)
	t.Setenv("GOHARNESS_STT_URL", stt)
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "x"} })
	req, _ := http.NewRequest("POST", srv.URL+"/api/transcribe", strings.NewReader("x"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestInfoSaysWhetherSpeechToTextIsAvailable(t *testing.T) {
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "x"} })
	t.Setenv("GOHARNESS_STT_URL", "")
	if decode[map[string]any](t, apiReq(t, srv, "GET", "/api/info", nil))["stt"] != false {
		t.Fatal("stt should be false when no server is configured")
	}
	t.Setenv("GOHARNESS_STT_URL", "http://stt.example")
	if decode[map[string]any](t, apiReq(t, srv, "GET", "/api/info", nil))["stt"] != true {
		t.Fatal("stt should be true once configured")
	}
}
