package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/security"
)

const (
	operationEventPollInterval = 500 * time.Millisecond
	operationEventHeartbeat    = 15 * time.Second
	operationEventAuthInterval = 60 * time.Second
	operationEventMaxLifetime  = 30 * time.Minute
	operationEventMaxStreams   = 8
	operationEventWriteLimit   = 5 * time.Second
)

type OperationEventCommands interface {
	Events(context.Context, domain.AuthContext, domain.ID, int64, int) (application.OperationEventPage, error)
}

type operationEventLimiter struct {
	mu     sync.Mutex
	active map[string]int
}

func (l *operationEventLimiter) acquire(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active == nil {
		l.active = map[string]int{}
	}
	if l.active[key] >= operationEventMaxStreams {
		return false
	}
	l.active[key]++
	return true
}

func (l *operationEventLimiter) release(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.active[key] <= 1 {
		delete(l.active, key)
	} else {
		l.active[key]--
	}
}

// OperationEventHandler serves a reconnectable view of committed events. The
// database is the source of truth; polling only discovers new rows and never
// affects the running operation.
func OperationEventHandler(events OperationEventCommands, authProvider *security.AuthProvider) http.Handler {
	limiter := &operationEventLimiter{active: map[string]int{}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if events == nil || authProvider == nil {
			writeConfigError(w, http.StatusServiceUnavailable, "Unavailable", "ant.error.unavailable", true, "retry_later")
			return
		}
		principal, ok := PrincipalFrom(r.Context())
		if !ok {
			errUnauthenticated.write(w)
			return
		}
		if !security.Can(principal, domain.PermRead, principal.Namespace) {
			errForbidden.write(w)
			return
		}
		opID := domain.ID(r.PathValue("operationId"))
		after, ok := operationEventCursor(r)
		if !ok || opID == "" {
			writeConfigError(w, http.StatusBadRequest, "InvalidInput", "ant.error.invalidInput", false, "fix_fields")
			return
		}
		ctx := r.Context()
		authContext := domain.AuthContext{PrincipalID: principal.PrincipalID, Namespace: principal.Namespace, Permissions: principal.Permissions}
		page, err := events.Events(ctx, authContext, opID, after, application.MaxOperationEventPageSize)
		if err != nil {
			writeOperationQueryError(w, err)
			return
		}
		limitKey := string(principal.Namespace) + "/" + string(principal.PrincipalID)
		if !limiter.acquire(limitKey) {
			writeConfigError(w, http.StatusTooManyRequests, "RateLimited", "ant.error.unavailable", true, "retry_later")
			return
		}
		defer limiter.release(limitKey)

		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			return
		}
		if _, err := fmt.Fprint(w, "retry: 1000\n\n"); err != nil {
			return
		}
		flusher.Flush()

		cursor := after
		if !writeOperationEvents(w, flusher, page.Events) {
			return
		}
		if len(page.Events) > 0 {
			cursor = page.Events[len(page.Events)-1].Sequence
		}
		if page.State != domain.Running || hasTerminalOperationEvent(page.Events) {
			return
		}

		poll := time.NewTicker(operationEventPollInterval)
		defer poll.Stop()
		heartbeat := time.NewTicker(operationEventHeartbeat)
		defer heartbeat.Stop()
		recheck := time.NewTicker(operationEventAuthInterval)
		defer recheck.Stop()
		lifetime := time.NewTimer(operationEventMaxLifetime)
		defer lifetime.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-lifetime.C:
				return // EventSource reconnects with its last delivered sequence.
			case <-recheck.C:
				if !requestStillAuthorized(r, principal, authProvider) {
					return
				}
			case <-heartbeat.C:
				if !writeSSEComment(w, flusher, "heartbeat") {
					return
				}
			case <-poll.C:
				page, err = events.Events(ctx, authContext, opID, cursor, application.MaxOperationEventPageSize)
				if err != nil {
					return // client reconnects and receives a fresh HTTP error if the fault persists.
				}
				if len(page.Events) == 0 {
					poll.Reset(operationEventPollInterval)
					if page.State != domain.Running {
						return
					}
					continue
				}
				if !writeOperationEvents(w, flusher, page.Events) {
					return
				}
				cursor = page.Events[len(page.Events)-1].Sequence
				if page.State != domain.Running || hasTerminalOperationEvent(page.Events) {
					return
				}
				if len(page.Events) == application.MaxOperationEventPageSize {
					// Drain a full page immediately; the next poll will be naturally
					// scheduled after the sequence backlog is caught up.
					poll.Reset(time.Millisecond)
				} else {
					poll.Reset(operationEventPollInterval)
				}
			}
		}
	})
}

func operationEventCursor(r *http.Request) (int64, bool) {
	queryValues := r.URL.Query()["afterSequence"]
	headerValues := r.Header.Values("Last-Event-ID")
	if len(queryValues) > 1 || len(headerValues) > 1 {
		return 0, false
	}
	queryValue, headerValue := "", ""
	if len(queryValues) == 1 {
		queryValue = queryValues[0]
	}
	if len(headerValues) == 1 {
		headerValue = headerValues[0]
	}
	raw := queryValue
	if headerValue != "" {
		raw = headerValue // browser reconnect state is newer than the initial URL cursor.
	}
	if raw == "" {
		return 0, true
	}
	if strings.TrimSpace(raw) != raw {
		return 0, false
	}
	sequence, err := strconv.ParseInt(raw, 10, 64)
	return sequence, err == nil && sequence >= 0
}

func writeOperationEvents(w http.ResponseWriter, flusher http.Flusher, events []domain.OperationEvent) bool {
	for _, event := range events {
		data, err := json.Marshal(event)
		if err != nil {
			return false
		}
		controller := http.NewResponseController(w)
		if err := controller.SetWriteDeadline(time.Now().Add(operationEventWriteLimit)); err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, data); err != nil {
			return false
		}
		flusher.Flush()
		_ = controller.SetWriteDeadline(time.Time{})
	}
	return true
}

func writeSSEComment(w http.ResponseWriter, flusher http.Flusher, comment string) bool {
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(operationEventWriteLimit)); err != nil {
		return false
	}
	if _, err := fmt.Fprintf(w, ": %s\n\n", comment); err != nil {
		return false
	}
	flusher.Flush()
	_ = controller.SetWriteDeadline(time.Time{})
	return true
}

func hasTerminalOperationEvent(events []domain.OperationEvent) bool {
	for _, event := range events {
		if event.Type == "operation_terminal" {
			return true
		}
	}
	return false
}

func requestStillAuthorized(r *http.Request, original security.Principal, authProvider *security.AuthProvider) bool {
	if authProvider == nil {
		return false
	}
	token, ok := bearerToken(r)
	if !ok {
		cookie, err := r.Cookie(sessionCookieName)
		if err != nil || cookie.Value == "" {
			return false
		}
		token = cookie.Value
	}
	current, err := authProvider.Authenticate(token)
	if err != nil || current.PrincipalID != original.PrincipalID || current.Namespace != original.Namespace || !security.Can(current, domain.PermRead, current.Namespace) || (current.ExpiresAt > 0 && current.ExpiresAt <= domain.Millis(time.Now().UnixMilli())) {
		return false
	}
	if len(current.Permissions) != len(original.Permissions) {
		return false
	}
	for i, permission := range current.Permissions {
		if permission != original.Permissions[i] {
			return false
		}
	}
	return true
}
