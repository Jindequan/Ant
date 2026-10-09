package api

import (
	"io/fs"
	"net/http"
	"regexp"
)

// logoPath is a provider's logo, bundled with the page (webui/static/logos).
var logoPath = regexp.MustCompile(`^/logos/[a-z0-9-]{1,40}\.png$`)

// PageHandler serves Ant's embedded page (webui.Files): "/" and its own
// files only (the page, its script and style, and the providers' logos),
// GET or HEAD, under a CSP that loads nothing from elsewhere.
func PageHandler(files fs.FS) http.Handler {
	fileServer := http.FileServerFS(files)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		switch p := r.URL.Path; {
		case p == "/" || p == "/index.html" || p == "/app.css" || p == "/app.js":
		case logoPath.MatchString(p):
		default:
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})
}
