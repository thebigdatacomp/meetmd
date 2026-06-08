// Package server exposes the bridge's local HTTP API (127.0.0.1 only). The
// browser extension calls it to start/stop recordings. See
// docs/specs/2026-06-08-architecture.md §5.
package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/thebigdatacomp/meetmd/internal/session"
)

const (
	loopbackHost  = "127.0.0.1"
	readTimeout   = 10 * time.Second
	writeTimeout  = 5 * time.Minute // transcription can take a while
	sessionPrefix = "/sessions/"
)

// Server wires the session manager to HTTP routes.
type Server struct {
	mgr *session.Manager
}

// New builds a Server backed by the given manager.
func New(mgr *session.Manager) *Server {
	return &Server{mgr: mgr}
}

// Handler returns the configured HTTP handler (exported for testing).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/sessions/start", s.handleStart)
	mux.HandleFunc(sessionPrefix, s.handleSessionByID) // /sessions/{id}/{action}
	return mux
}

// ListenAndServe starts the bridge on the loopback interface at the given port.
func (s *Server) ListenAndServe(port int) error {
	srv := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", loopbackHost, port),
		Handler:      s.Handler(),
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
	}
	return srv.ListenAndServe()
}

// handleSessionByID routes /sessions/{id}/stop and /sessions/{id}/cancel.
func (s *Server) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, sessionPrefix)
	id, action, ok := strings.Cut(rest, "/")
	if !ok || id == "" {
		writeError(w, http.StatusNotFound, "route not found")
		return
	}
	switch action {
	case "stop":
		s.handleStop(w, r, id)
	case "cancel":
		s.handleCancel(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "unknown action: "+action)
	}
}

// ctx returns the request context (placeholder for future cancellation wiring).
func ctx(r *http.Request) context.Context { return r.Context() }
