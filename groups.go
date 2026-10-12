package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Sidebar groups. Every conversation sits in its project's group unless the
// user moved it into one of these groups (kept in ~/.goharness/groups.json).
// A session records only the group's id in its meta file; deleting a group
// sends its conversations back to their projects, because an id that no
// longer exists simply reads as "no group".

const maxGroupNameLen = 60

type convGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

var (
	errGroupName     = errors.New("group names must be 1-60 characters")
	errGroupDup      = errors.New("a group with that name already exists")
	errGroupNotFound = errors.New("no such group")
	groupsMu         sync.Mutex
)

func groupsPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".goharness", "groups.json"), nil
}

// readGroups loads the groups file; a missing or unreadable file is no groups.
// The caller holds groupsMu.
func readGroups() []convGroup {
	path, err := groupsPath()
	if err != nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var gs []convGroup
	if json.Unmarshal(data, &gs) != nil {
		return nil
	}
	return gs
}

func writeGroups(gs []convGroup) error {
	path, err := groupsPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(gs, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func loadGroups() []convGroup {
	groupsMu.Lock()
	defer groupsMu.Unlock()
	gs := readGroups()
	if gs == nil {
		gs = []convGroup{}
	}
	return gs
}

// A conversation can also be moved into another project's group. Those groups
// are not stored: their id is "p:" followed by the project folder's name.
const projectGroupPrefix = "p:"

func isProjectGroupID(id string) bool {
	return strings.HasPrefix(id, projectGroupPrefix) && len(id) > len(projectGroupPrefix) && len(id) <= 200
}

// validGroupID reports whether id names a group a session can be moved into:
// a stored custom group or a project group.
func validGroupID(id string) bool {
	return isProjectGroupID(id) || groupExists(id)
}

func groupExists(id string) bool {
	groupsMu.Lock()
	defer groupsMu.Unlock()
	for _, g := range readGroups() {
		if g.ID == id {
			return true
		}
	}
	return false
}

func cleanGroupName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if n := len([]rune(name)); n == 0 || n > maxGroupNameLen {
		return "", errGroupName
	}
	return name, nil
}

func nameTaken(gs []convGroup, name, exceptID string) bool {
	for _, g := range gs {
		if g.ID != exceptID && strings.EqualFold(g.Name, name) {
			return true
		}
	}
	return false
}

func createGroup(name string) (convGroup, error) {
	name, err := cleanGroupName(name)
	if err != nil {
		return convGroup{}, err
	}
	groupsMu.Lock()
	defer groupsMu.Unlock()
	gs := readGroups()
	if nameTaken(gs, name, "") {
		return convGroup{}, errGroupDup
	}
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return convGroup{}, err
	}
	g := convGroup{ID: "g" + hex.EncodeToString(b), Name: name}
	return g, writeGroups(append(gs, g))
}

func renameGroup(id, name string) (convGroup, error) {
	name, err := cleanGroupName(name)
	if err != nil {
		return convGroup{}, err
	}
	groupsMu.Lock()
	defer groupsMu.Unlock()
	gs := readGroups()
	if nameTaken(gs, name, id) {
		return convGroup{}, errGroupDup
	}
	for i := range gs {
		if gs[i].ID == id {
			gs[i].Name = name
			return gs[i], writeGroups(gs)
		}
	}
	return convGroup{}, errGroupNotFound
}

func deleteGroup(id string) error {
	groupsMu.Lock()
	defer groupsMu.Unlock()
	gs := readGroups()
	out := make([]convGroup, 0, len(gs))
	found := false
	for _, g := range gs {
		if g.ID == id {
			found = true
			continue
		}
		out = append(out, g)
	}
	if !found {
		return errGroupNotFound
	}
	return writeGroups(out)
}
