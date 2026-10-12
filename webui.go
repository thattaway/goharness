package main

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// The React app's production build (npm --prefix web run build), compiled
// into the binary so `goharness web` is still a single file.
//
//go:embed all:web/dist
var webDist embed.FS

// staticHandler serves the built app. Paths that aren't files fall back to
// index.html so client-side routes survive a reload. Vite's hashed assets
// are cached for good; index.html is always revalidated.
func staticHandler() http.Handler {
	dist, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		if _, err := fs.Stat(dist, name); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
			name = "index.html"
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
