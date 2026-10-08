package quota

import (
	"context"
	"slices"
	"sync"
	"time"
)

// directReader reads synchronously, every time it is asked: what the supply
// engine does through its quota source, minus scheduling and recording.
// Tests use it to drive the Service without the engine.
type directReader struct {
	local Local
	store *Store

	mu     sync.Mutex
	conns  []Connection
	latest map[string]Account
	at     time.Time
}

func newTestService(l Local, store *Store) *Service {
	return NewService(l, store, Options{Reader: &directReader{local: l, store: store, latest: map[string]Account{}}})
}

func (r *directReader) Latest() Reading {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := Reading{Conns: slices.Clone(r.conns), RefreshedAt: r.at}
	for _, c := range r.conns {
		if a, ok := r.latest[c.ID]; ok {
			out.Accounts = append(out.Accounts, a)
		}
	}
	return out
}

func (r *directReader) Refresh(ctx context.Context, only []string) {
	conns, err := Connections(r.local, r.store, Providers())
	if err != nil {
		return
	}
	providers := Providers()
	for _, c := range conns {
		if len(only) > 0 && !slices.Contains(only, c.ID) {
			continue
		}
		a := Read(ctx, providers, r.local, c)
		_ = KeepRenewed(r.store, c, &a)
		r.mu.Lock()
		r.latest[c.ID] = a
		r.mu.Unlock()
	}
	r.mu.Lock()
	r.conns, r.at = conns, r.local.Now()
	r.mu.Unlock()
}
