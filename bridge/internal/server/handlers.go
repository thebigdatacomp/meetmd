package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/thebigdatacomp/meetmd/internal/model"
	"github.com/thebigdatacomp/meetmd/internal/session"
)

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
