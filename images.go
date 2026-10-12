package main

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// maxImageBytes caps a single attached image so one /image or paste can't
// blow past the model's context or balloon the request to llama-swap. 8MB
// covers any real screenshot or photo at web resolution with headroom.
const maxImageBytes = 8 << 20

// imageDataURL reads a local image file and returns it as a data: URL ready
// to attach to a message, plus a short display name. The content is
// sniffed (not just the extension) so a renamed non-image file is caught,
// and anything over maxImageBytes is refused.
func imageDataURL(path string) (dataURL, name string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", "", fmt.Errorf("can't read %s: %w", path, err)
	}
	if info.IsDir() {
		return "", "", fmt.Errorf("%s is a directory", path)
	}
	if info.Size() > maxImageBytes {
		return "", "", fmt.Errorf("%s is %.1fMB, over the %dMB limit", path, float64(info.Size())/(1<<20), maxImageBytes>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("can't read %s: %w", path, err)
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return "", "", fmt.Errorf("%s doesn't look like an image (detected %s)", path, mime)
	}
	return fmt.Sprintf("data:%s;base64,%s", mime, base64.StdEncoding.EncodeToString(data)), filepath.Base(path), nil
}
