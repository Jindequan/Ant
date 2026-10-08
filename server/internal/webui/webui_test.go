package webui

import (
	"io/fs"
	"strings"
	"testing"
)

func TestFilesHoldThePage(t *testing.T) {
	for name, want := range map[string]string{"index.html": "<title>Ant</title>", "app.js": "/api/quota", "app.css": "--surface"} {
		b, err := fs.ReadFile(Files(), name)
		if err != nil || !strings.Contains(string(b), want) {
			t.Fatalf("%s: %v, missing %q", name, err, want)
		}
	}
}
