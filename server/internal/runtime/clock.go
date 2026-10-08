// Package runtime holds process-level services: the injected Clock, bounded
// ExecutionHost, execution supervisor and stream hub. Host and supervisor
// lifecycle code is composed by the server root; Clock is the ledger time port.
package runtime

import (
	"sync"
	"time"

	"ant/server/internal/domain"
)

// Clock is the injectable time source; the store writer uses it for lease
// and window math. Clock regression protection (freeze new permits after a
// backwards jump) lands with the supervisor in T2.3 (05-RUNTIME §4).
type Clock struct {
	mu   sync.Mutex
	mili domain.Millis
}

func NewClock() *Clock {
	return &Clock{mili: domain.Millis(time.Now().UnixMilli())}
}

func (c *Clock) Now() domain.Millis {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := domain.Millis(time.Now().UnixMilli())
	if now > c.mili {
		c.mili = now
	}
	return now
}
