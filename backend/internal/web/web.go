// Package web serves the EpicAI administration UI.
// The UI is embedded into the binary so a single container is enough.
package web

import (
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
)

//go:embed dist/*
var distFS embed.FS

// Handler serves the admin SPA with client-side routing fallback.
func Handler(logger *slog.Logger) http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "admin UI not built", http.StatusServiceUnavailable)
		})
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate, max-age=0")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		path := r.URL.Path
		if path == "/admin" || path == "/admin/" {
			serveFile(w, r, sub, "index.html")
			return
		}
		// Serve static assets under /admin/assets/ or /assets/
		if strings.HasPrefix(path, "/admin/assets/") {
			r2 := new(http.Request)
			*r2 = *r
			r2.URL = new(url.URL)
			*r2.URL = *r.URL
			r2.URL.Path = strings.TrimPrefix(path, "/admin")
			fileServer.ServeHTTP(w, r2)
			return
		}
		// SPA fallback: any unknown /admin/** path renders the app shell.
		if strings.HasPrefix(path, "/admin") {
			if _, err := fs.Stat(sub, strings.TrimPrefix(path, "/")+".html"); err == nil {
				fileServer.ServeHTTP(w, r)
				return
			}
			serveFile(w, r, sub, "index.html")
			return
		}
		if path == "/" {
			http.Redirect(w, r, "/admin/", http.StatusFound)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

func serveFile(w http.ResponseWriter, r *http.Request, sub fs.FS, name string) {
	data, err := fs.ReadFile(sub, name)
	if err != nil {
		http.Error(w, "admin UI not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
