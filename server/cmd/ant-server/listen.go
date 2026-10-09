package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// defaultAddr is Ant's port: not 8080, which many tools use.
const defaultAddr = "127.0.0.1:17890"

// listenFrom listens on addr; with next, a taken port moves Ant to the next
// free one (up to 50 ports on).
func listenFrom(addr string, next bool) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil || !next || !errors.Is(err, syscall.EADDRINUSE) {
		return ln, err
	}
	host, p, splitErr := net.SplitHostPort(addr)
	port, convErr := strconv.Atoi(p)
	if splitErr != nil || convErr != nil {
		return nil, err
	}
	for i := 1; i <= 50; i++ {
		if ln, e := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port+i))); e == nil {
			return ln, nil
		}
	}
	return nil, fmt.Errorf("ports %d–%d are all taken: %w", port, port+50, err)
}

// serverState is where a running Ant listens, for the menu bar app and
// anyone looking: ~/.config/ant/server.json.
type serverState struct {
	URL       string    `json:"url"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"startedAt"`
}

// writeState records where Ant listens; the returned func removes it.
func writeState(path, addr string) (func(), error) {
	if path == "" {
		return func() {}, nil
	}
	b, _ := json.Marshal(serverState{URL: "http://" + addr, PID: os.Getpid(), StartedAt: time.Now().UTC()})
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return func() { _ = os.Remove(path) }, nil
}
