package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func withServer(t *testing.T, h http.HandlerFunc) {
	srv := httptest.NewServer(h)
	old := tavilyURL
	tavilyURL = srv.URL
	t.Cleanup(func() { srv.Close(); tavilyURL = old })
	t.Setenv("TAVILY_API_KEY", "k")
}

func TestTavilySuccess(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["max_results"].(float64); !ok {
			t.Errorf("max_results not an integer: %#v", body["max_results"])
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Error("missing auth header")
		}
		w.Write([]byte(`{"results":[{"title":"T","url":"http://u","content":"C"}]}`))
	})
	out := toolTavilySearch("q", 0, "")
	if !strings.Contains(out, "Title: T") || !strings.Contains(out, "Untrusted") {
		t.Fatalf("unexpected: %s", out)
	}
}

func TestTavilyEmpty(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"results":[]}`)) })
	if out := toolTavilySearch("q", 3, ""); !strings.HasPrefix(out, "No results") {
		t.Fatal(out)
	}
}

func TestTavilyUnauthorized(t *testing.T) {
	withServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	if out := toolTavilySearch("q", 3, ""); !strings.Contains(out, "rejected") {
		t.Fatal(out)
	}
}

func TestTavilyRetryOn429(t *testing.T) {
	n := 0
	withServer(t, func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.WriteHeader(429)
			return
		}
		w.Write([]byte(`{"results":[{"title":"T","url":"u","content":"C"}]}`))
	})
	if out := toolTavilySearch("q", 3, ""); !strings.Contains(out, "Title: T") || n != 2 {
		t.Fatalf("n=%d out=%s", n, out)
	}
}

func TestTavilyNoKey(t *testing.T) {
	t.Setenv("TAVILY_API_KEY", "")
	if out := toolTavilySearch("q", 3, ""); !strings.Contains(out, "config/goharness/env") {
		t.Fatal(out)
	}
}
