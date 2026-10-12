package main

import (
	"os"
	"testing"
)

// TestMain points HOME at a temp dir so no test touches the real
// ~/.goharness (sessions, checkpoints) or ~/.config/goharness.
func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "goharness-test-home")
	if err != nil {
		panic(err)
	}
	os.Setenv("HOME", home)
	code := m.Run()
	os.RemoveAll(home)
	os.Exit(code)
}
