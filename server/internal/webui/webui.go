// Package webui is Ant's page, embedded in the server binary: plain HTML,
// CSS and JavaScript, no build step and nothing loaded from the network.
// It only holds the files; api.PageHandler serves them.
package webui

import (
	"embed"
	"io/fs"
)

//go:embed static
var static embed.FS

// Files is the page's files, rooted at the page: index.html, app.css, app.js.
func Files() fs.FS {
	files, err := fs.Sub(static, "static")
	if err != nil {
		panic(err) // "static" is embedded above; this cannot fail
	}
	return files
}
