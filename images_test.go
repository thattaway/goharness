package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// A minimal valid 1x1 PNG, so http.DetectContentType reports image/png.
var onePixelPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d,
	0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4, 0x89, 0x00, 0x00, 0x00,
	0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49,
	0x45, 0x4e, 0x44, 0xae, 0x42, 0x60, 0x82,
}

func TestImageDataURLPNG(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "pic.png", onePixelPNG)
	url, name, err := imageDataURL(p)
	if err != nil {
		t.Fatal(err)
	}
	if name != "pic.png" {
		t.Errorf("name = %q", name)
	}
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Errorf("url = %q", url)
	}
}

func TestImageDataURLRejectsNonImage(t *testing.T) {
	dir := t.TempDir()
	p := write(t, dir, "notes.txt", []byte("just text, not an image"))
	if _, _, err := imageDataURL(p); err == nil || !strings.Contains(err.Error(), "doesn't look like an image") {
		t.Fatalf("got %v", err)
	}
}

func TestImageDataURLRejectsOversize(t *testing.T) {
	dir := t.TempDir()
	big := append([]byte{}, onePixelPNG...)
	big = append(big, make([]byte, maxImageBytes)...) // padding after the PNG trailer is still "too big" to read as a whole file
	p := write(t, dir, "huge.png", big)
	if _, _, err := imageDataURL(p); err == nil || !strings.Contains(err.Error(), "over the") {
		t.Fatalf("got %v", err)
	}
}

func TestImageDataURLMissingFile(t *testing.T) {
	if _, _, err := imageDataURL("/no/such/file.png"); err == nil {
		t.Fatal("expected an error")
	}
}
