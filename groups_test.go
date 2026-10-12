package main

import (
	"net/http"
	"testing"
)

func TestGroupStore(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if gs := loadGroups(); gs == nil || len(gs) != 0 {
		t.Fatalf("fresh store = %#v, want an empty non-nil list", gs)
	}
	a, err := createGroup("  Weekend   experiments ")
	if err != nil || a.Name != "Weekend experiments" || a.ID == "" {
		t.Fatalf("create = %+v, %v", a, err)
	}
	if _, err := createGroup("weekend EXPERIMENTS"); err != errGroupDup {
		t.Fatalf("duplicate name (any case): %v", err)
	}
	for _, bad := range []string{"", "   ", string(make([]rune, 61))} {
		if _, err := createGroup(bad); err != errGroupName {
			t.Fatalf("createGroup(%q) = %v, want errGroupName", bad, err)
		}
	}
	b, _ := createGroup("Docs")
	if r, err := renameGroup(b.ID, "Documentation"); err != nil || r.Name != "Documentation" {
		t.Fatalf("rename = %+v, %v", r, err)
	}
	if _, err := renameGroup(b.ID, "Weekend experiments"); err != errGroupDup {
		t.Fatalf("rename onto an existing name: %v", err)
	}
	if _, err := renameGroup("nope", "X"); err != errGroupNotFound {
		t.Fatalf("rename of a missing group: %v", err)
	}
	if !groupExists(a.ID) || groupExists("nope") {
		t.Fatal("groupExists is wrong")
	}
	if err := deleteGroup(a.ID); err != nil || groupExists(a.ID) {
		t.Fatalf("delete: %v", err)
	}
	if err := deleteGroup(a.ID); err != errGroupNotFound {
		t.Fatalf("second delete: %v", err)
	}
	if gs := loadGroups(); len(gs) != 1 || gs[0].Name != "Documentation" {
		t.Fatalf("after all that: %+v", gs)
	}
}

func TestWebGroupsAndMovingSessions(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	snap := createSession(t, srv)
	sessURL := "/api/sessions/" + snap.ID

	g := decode[convGroup](t, apiReq(t, srv, "POST", "/api/groups", map[string]string{"name": "Ideas"}))
	if g.ID == "" || g.Name != "Ideas" {
		t.Fatalf("created group = %+v", g)
	}
	if resp := apiReq(t, srv, "POST", "/api/groups", map[string]string{"name": "ideas"}); resp.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate group: %d", resp.StatusCode)
	}
	if resp := apiReq(t, srv, "POST", "/api/groups", map[string]string{"name": " "}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("blank group: %d", resp.StatusCode)
	}
	if list := decode[[]convGroup](t, apiReq(t, srv, "GET", "/api/groups", nil)); len(list) != 1 {
		t.Fatalf("list = %+v", list)
	}

	// Move the session into it; the list reports the group, and the session remembers it.
	if resp := apiReq(t, srv, "PATCH", sessURL, map[string]string{"group": "gdoesnotexist"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("moving to a missing group: %d", resp.StatusCode)
	}
	got := decode[map[string]any](t, apiReq(t, srv, "PATCH", sessURL, map[string]string{"group": g.ID}))
	if got["group"] != g.ID {
		t.Fatalf("patch result = %v", got)
	}
	find := func() sessionSummary {
		for _, s := range decode[[]sessionSummary](t, apiReq(t, srv, "GET", "/api/sessions", nil)) {
			if s.ID == snap.ID {
				return s
			}
		}
		t.Fatal("session missing from the list")
		return sessionSummary{}
	}
	if s := find(); s.Group != g.ID {
		t.Fatalf("listed group = %q, want %q", s.Group, g.ID)
	}

	// Renaming the group keeps the session in it; patching the title alone doesn't move it.
	apiReq(t, srv, "PATCH", "/api/groups/"+g.ID, map[string]string{"name": "Experiments"}).Body.Close()
	apiReq(t, srv, "PATCH", sessURL, map[string]string{"title": "renamed"}).Body.Close()
	if s := find(); s.Group != g.ID || s.Title != "renamed" {
		t.Fatalf("after rename: %+v", s)
	}

	// Deleting the group sends the session back to its project (an unknown id reads as no group).
	if resp := apiReq(t, srv, "DELETE", "/api/groups/"+g.ID, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete group: %d", resp.StatusCode)
	}
	if s := find(); s.Group != "" {
		t.Fatalf("group after deleting it = %q, want empty", s.Group)
	}
	if resp := apiReq(t, srv, "DELETE", "/api/groups/"+g.ID, nil); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete: %d", resp.StatusCode)
	}

	// "" moves it back to the project explicitly.
	g2 := decode[convGroup](t, apiReq(t, srv, "POST", "/api/groups", map[string]string{"name": "Again"}))
	apiReq(t, srv, "PATCH", sessURL, map[string]string{"group": g2.ID}).Body.Close()
	apiReq(t, srv, "PATCH", sessURL, map[string]string{"group": ""}).Body.Close()
	if s := find(); s.Group != "" {
		t.Fatalf("after moving back: %q", s.Group)
	}
}

func TestMovingToAnotherProjectsGroup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	srv, _ := newTestWeb(t, func(int, map[string]any) fakeReply { return fakeReply{text: "ok"} })
	snap := createSession(t, srv)
	url := "/api/sessions/" + snap.ID
	// A project group needs no stored group: its id is "p:" plus the folder name.
	got := decode[map[string]any](t, apiReq(t, srv, "PATCH", url, map[string]string{"group": "p:sparky-automation"}))
	if got["group"] != "p:sparky-automation" {
		t.Fatalf("patch result = %v", got)
	}
	var listed sessionSummary
	for _, s := range decode[[]sessionSummary](t, apiReq(t, srv, "GET", "/api/sessions", nil)) {
		if s.ID == snap.ID {
			listed = s
		}
	}
	if listed.Group != "p:sparky-automation" {
		t.Fatalf("listed group = %q", listed.Group)
	}
	if resp := apiReq(t, srv, "PATCH", url, map[string]string{"group": "p:"}); resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bare prefix: %d, want 400", resp.StatusCode)
	}
}
