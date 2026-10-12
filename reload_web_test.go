package main

import (
	"net/http"
	"strings"
	"testing"
)

func TestWebReload(t *testing.T) {
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })

	resp := apiReq(t, srv, "POST", "/api/reload", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body := decode[struct {
		Summary string `json:"summary"`
	}](t, resp)
	for _, want := range []string{"env file", "permission rules", "skills"} {
		if !strings.Contains(body.Summary, want) {
			t.Errorf("summary missing %q: %s", want, body.Summary)
		}
	}

	unauth, err := http.Post(srv.URL+"/api/reload", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated reload status = %d, want 401", unauth.StatusCode)
	}
}
