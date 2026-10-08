package quota

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// --- HTTP ---

// Handler serves the quota API. It answers only requests addressed to this
// machine by name or loopback address, so another site in the browser can't
// reach it through DNS rebinding, and every change needs a header a
// cross-site form can't send and, when the browser names one, a local Origin.
func (s *Service) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/quota", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.Snapshot())
	})
	mux.HandleFunc("POST /api/quota/refresh", s.change(func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), s.readTimeout+5*time.Second)
		defer cancel()
		writeJSON(w, http.StatusOK, s.Refresh(ctx))
	}))
	mux.HandleFunc("GET /api/insights", func(w http.ResponseWriter, r *http.Request) {
		out := map[string][]Insight{}
		if s.demo {
			out = demoInsights(s.Snapshot().Accounts, s.local.Now())
		} else if s.insights != nil {
			got, err := s.insights.Insights(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, "Couldn't read the history: "+err.Error())
				return
			}
			out = got
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /api/providers", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.providers)
	})
	mux.HandleFunc("GET /api/accounts/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, ok := s.view(r.PathValue("id"))
		if !ok {
			writeError(w, http.StatusNotFound, "No such account.")
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
	mux.HandleFunc("POST /api/accounts", s.change(func(w http.ResponseWriter, r *http.Request) {
		var in accountInput
		if !decode(w, r, &in) {
			return
		}
		c, err := s.add(in)
		s.respond(w, r, c.ID, err)
	}))
	mux.HandleFunc("PUT /api/accounts/{id}", s.change(func(w http.ResponseWriter, r *http.Request) {
		var in accountInput
		if !decode(w, r, &in) {
			return
		}
		c, err := s.update(r.PathValue("id"), in)
		s.respond(w, r, c.ID, err)
	}))
	mux.HandleFunc("POST /api/signin", s.change(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Provider string `json:"provider"`
			Mode     string `json:"mode"`
			Way      string `json:"way"`
		}
		if !decode(w, r, &in) {
			return
		}
		// the window stays open if the page reloads meanwhile
		_, err := s.signIn(context.WithoutCancel(r.Context()), in.Provider, in.Mode, in.Way)
		s.respond(w, r, "*"+in.Provider, err)
	}))
	mux.HandleFunc("GET /api/signin/status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, CurrentSignIn())
	})
	mux.HandleFunc("POST /api/signin/finish", s.change(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"open": FinishSignIn()})
	}))
	mux.HandleFunc("POST /api/signin/cancel", s.change(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]bool{"open": CancelSignIn()})
	}))
	mux.HandleFunc("POST /api/notify", s.change(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			On bool `json:"on"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := s.setNotify(in.On); err != nil {
			writeError(w, http.StatusInternalServerError, "Couldn't change notifications: "+err.Error())
			return
		}
		writeJSON(w, http.StatusOK, s.Snapshot())
	}))
	mux.HandleFunc("POST /api/browser", s.change(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Provider string `json:"provider"`
			Mode     string `json:"mode"`
			Way      string `json:"way"`
			Allow    bool   `json:"allow"`
		}
		if !decode(w, r, &in) {
			return
		}
		s.respond(w, r, "*"+in.Provider, s.allowBrowser(in.Provider, in.Mode, in.Way, in.Allow))
	}))
	mux.HandleFunc("DELETE /api/accounts/{id}", s.change(func(w http.ResponseWriter, r *http.Request) {
		s.respond(w, r, "", s.remove(r.PathValue("id")))
	}))
	return LocalOnly(mux)
}

// respond refreshes the changed account (or just the list) and answers
// with the snapshot.
func (s *Service) respond(w http.ResponseWriter, r *http.Request, id string, err error) {
	if errors.Is(err, errBadInput) {
		writeError(w, http.StatusBadRequest, strings.TrimPrefix(err.Error(), errBadInput.Error()+": "))
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Couldn't save: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), s.readTimeout+5*time.Second)
	defer cancel()
	only := map[string]bool{id: true}
	if strings.HasPrefix(id, "*") { // every account of a provider
		only = nil
	}
	writeJSON(w, http.StatusOK, s.refresh(ctx, only))
}

// change guards a request that changes something or makes Ant read
// providers.
func (s *Service) change(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Ant") != "1" {
			writeError(w, http.StatusForbidden, "missing X-Ant header")
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || !isLocalHost(u.Hostname()) {
				writeError(w, http.StatusForbidden, "Ant accepts changes only from its own page")
				return
			}
		}
		next(w, r)
	}
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "send JSON")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "That request couldn't be read.")
		return false
	}
	return true
}

func isLoopbackIP(addr string) bool {
	ip := net.ParseIP(addr)
	return ip != nil && ip.IsLoopback()
}

func isLocalHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// LocalOnly refuses requests whose Host isn't localhost or a loopback IP,
// so another site in the browser can't reach Ant through DNS rebinding,
// and requests from any peer but this machine: when -addr listens beyond
// loopback, another machine could otherwise just send "Host: localhost".
func LocalOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			peer = r.RemoteAddr
		}
		if !isLocalHost(host) || !isLoopbackIP(peer) {
			http.Error(w, "Ant answers only on this machine", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
