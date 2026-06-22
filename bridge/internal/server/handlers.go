package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/thebigdatacomp/meetmd/internal/model"
	"github.com/thebigdatacomp/meetmd/internal/session"
)

// shutdownStopTimeout caps how long /shutdown waits for an in-progress recording
// to finalize+transcribe before exiting anyway, so a stuck transcription can't
// keep the bridge (and its capture helper) alive after the app has quit.
const shutdownStopTimeout = 2 * time.Minute

// startRequest is the JSON body for POST /sessions/start.
type startRequest struct {
	Title        string   `json:"title"`
	Project      string   `json:"project"` // optional; routes output per project
	Platform     string   `json:"platform"`
	Participants []string `json:"participants"`
	StartedAt    string   `json:"startedAt"` // optional RFC3339
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "service": "meetmd-bridge"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, http.StatusOK, s.mgr.Status())
}

func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req startRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	started, err := parseStartedAt(req.StartedAt)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid startedAt: must be RFC3339")
		return
	}

	meeting, err := s.mgr.Start(ctx(r), session.StartRequest{
		Title:        req.Title,
		Project:      req.Project,
		Platform:     model.Platform(req.Platform),
		Participants: req.Participants,
		StartedAt:    started,
	})
	if err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"sessionId": meeting.ID,
		"dir":       meeting.DirName(),
	})
}

// handleNoteStart begins a quick voice note (mic-only). It takes no body — a
// note carries no title/project/participants, just the transcribed text.
func (s *Server) handleNoteStart(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	note, err := s.mgr.StartNote(ctx(r))
	if err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"sessionId": note.ID})
}

// handleSleep / handleWake toggle snooze: while asleep the detector does nothing
// (no prompts, no auto-record). Both are idempotent POSTs with no body.
func (s *Server) handleSleep(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	s.mgr.Sleep()
	writeJSON(w, http.StatusOK, map[string]bool{"asleep": true})
}

func (s *Server) handleWake(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	s.mgr.Wake()
	writeJSON(w, http.StatusOK, map[string]bool{"asleep": false})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request, id string) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	res, err := s.mgr.Stop(ctx(r), id)
	if err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessionDir": res.SessionDir,
		"files":      res.Files,
	})
}

func (s *Server) handlePause(w http.ResponseWriter, r *http.Request, id string) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if err := s.mgr.Pause(id); err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "paused"})
}

func (s *Server) handleResume(w http.ResponseWriter, r *http.Request, id string) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if err := s.mgr.Resume(id); err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "resumed"})
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request, id string) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	if err := s.mgr.Cancel(id); err != nil {
		writeManagerError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// handleShutdown finalizes any active recording and exits the bridge process.
// The menu-bar app calls this on quit so no capture helper is left orphaned
// (which would keep the macOS screen-capture indicator lit). It acks immediately
// so the quitting app isn't blocked, then stops + exits in the background.
func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "shutting down"})
	go func() {
		time.Sleep(150 * time.Millisecond) // let the HTTP response flush before exit
		if id, ok := s.mgr.ActiveID(); ok {
			// Stop also SIGTERMs the capture helper, so the indicator clears within
			// seconds even though transcription continues. Capped so it can't hang.
			done := make(chan struct{})
			go func() {
				if _, err := s.mgr.Stop(context.Background(), id); err != nil {
					log.Printf("shutdown: stop active recording: %v", err)
				}
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(shutdownStopTimeout):
				log.Printf("shutdown: stop timed out after %s — exiting anyway", shutdownStopTimeout)
			}
		}
		log.Printf("shutdown requested — exiting")
		os.Exit(0)
	}()
}

// --- helpers ----------------------------------------------------------------

func parseStartedAt(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, raw)
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": http.StatusText(status), "message": message})
}

// writeManagerError maps domain errors to HTTP status codes.
func writeManagerError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrBusy):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, session.ErrNoSession), errors.Is(err, session.ErrUnknownID):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, session.ErrEmptyOutput):
		writeError(w, http.StatusInternalServerError, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}
