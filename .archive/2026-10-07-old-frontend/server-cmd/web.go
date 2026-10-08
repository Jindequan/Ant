package main

import (
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var errInvalidWebDist = errors.New("web app directory must contain index.html")

type webAppHandler struct {
	root  *os.Root
	files http.Handler
}

func newWebHandler(directory string) (*webAppHandler, error) {
	root, err := filepath.Abs(directory)
	if err != nil {
		return nil, err
	}
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	indexFile, err := rootFS.Open("index.html")
	if err != nil {
		_ = rootFS.Close()
		return nil, errInvalidWebDist
	}
	indexInfo, err := indexFile.Stat()
	_ = indexFile.Close()
	if err != nil || !indexInfo.Mode().IsRegular() {
		_ = rootFS.Close()
		return nil, errInvalidWebDist
	}

	return &webAppHandler{root: rootFS, files: http.FileServerFS(webRootFS{root: rootFS})}, nil
}

func (h *webAppHandler) Close() error {
	if h == nil || h.root == nil {
		return nil
	}
	return h.root.Close()
}

func (h *webAppHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.root == nil || h.files == nil {
		http.NotFound(w, r)
		return
	}
	requestPath := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	if requestPath == "/v1" || strings.HasPrefix(requestPath, "/v1/") ||
		requestPath == "/healthz" || strings.HasPrefix(requestPath, "/healthz/") ||
		requestPath == "/readyz" || strings.HasPrefix(requestPath, "/readyz/") {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	relative := strings.TrimPrefix(requestPath, "/")
	if relative != "" {
		if webFileIsRegular(h.root, relative) {
			h.files.ServeHTTP(w, r)
			return
		}
	}

	// Only browser document navigations receive the SPA shell. Missing
	// scripts, stylesheets and API endpoints remain real 404 responses.
	if requestPath == "/" || (strings.Contains(r.Header.Get("Accept"), "text/html") && path.Ext(requestPath) == "") {
		file, openErr := h.root.Open("index.html")
		if openErr != nil {
			http.Error(w, "web app unavailable", http.StatusInternalServerError)
			return
		}
		defer file.Close()
		info, statErr := file.Stat()
		if statErr != nil || !info.Mode().IsRegular() {
			http.Error(w, "web app unavailable", http.StatusInternalServerError)
			return
		}
		http.ServeContent(w, r, "index.html", info.ModTime(), file)
		return
	}
	http.NotFound(w, r)
}

type webRootFS struct{ root *os.Root }

func (f webRootFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrInvalid
	}
	return f.root.Open(name)
}

func webFileIsRegular(root *os.Root, name string) bool {
	file, err := root.Open(name)
	if err != nil {
		return false
	}
	defer file.Close()
	info, err := file.Stat()
	return err == nil && info.Mode().IsRegular()
}
