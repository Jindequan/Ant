package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// A taken port moves Ant to the next free one, and only when asked.
func TestListenMovesToTheNextFreePort(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer taken.Close()
	addr := taken.Addr().String()
	if _, err := listenFrom(addr, false); err == nil {
		t.Fatal("listened on a taken port")
	}
	ln, err := listenFrom(addr, true)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, p, _ := net.SplitHostPort(addr)
	_, q, _ := net.SplitHostPort(ln.Addr().String())
	want, _ := strconv.Atoi(p)
	if got, _ := strconv.Atoi(q); got <= want || got > want+50 {
		t.Fatalf("moved from %s to %s", p, q)
	}
	if defaultAddr == "127.0.0.1:8080" {
		t.Fatal("Ant's default port is 8080 again")
	}
}

// The state file says where Ant listens while it runs, and goes with it.
func TestStateFileSaysWhereAntListens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ant", "server.json")
	remove, err := writeState(path, "127.0.0.1:17891")
	if err != nil {
		t.Fatal(err)
	}
	var s serverState
	b, _ := os.ReadFile(path)
	info, _ := os.Stat(path)
	if json.Unmarshal(b, &s) != nil || s.URL != "http://127.0.0.1:17891" || s.PID != os.Getpid() || info.Mode().Perm() != 0o600 {
		t.Fatalf("state = %s (%v)", b, info.Mode())
	}
	remove()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("state file left behind")
	}
}
