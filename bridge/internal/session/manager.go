// Package session coordinates a recording's lifecycle: it drives the audio
// capturer, runs transcription, and writes the structured Markdown output.
// The MVP supports a single active session at a time.
package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/thebigdatacomp/meetmd/internal/audio"
	"github.com/thebigdatacomp/meetmd/internal/config"
	"github.com/thebigdatacomp/meetmd/internal/model"
	"github.com/thebigdatacomp/meetmd/internal/transcribe"
	"github.com/thebigdatacomp/meetmd/internal/writer"
)

// Errors returned by the manager.
var (
	ErrBusy        = errors.New("a recording is already in progress")
	ErrNoSession   = errors.New("no recording in progress")
	ErrUnknownID   = errors.New("session id does not match the active recording")
	ErrEmptyOutput = errors.New("output root is not configured")
)

// State is the manager's high-level status.
type State string

const (
	StateIdle      State = "idle"
	StateRecording State = "recording"
	StatePaused    State = "paused"
)

// DetectedMeeting is a meeting found in the browser but not yet being recorded;
// a UI can prompt the user to start. Set by the detector (see internal/detect).
type DetectedMeeting struct {
	Code  string `json:"code"`
	Title string `json:"title"`
}

// StartRequest is the metadata supplied when a recording begins.
type StartRequest struct {
	Title        string
	Project      string // optional; routes output to output_root/<project>
	Platform     model.Platform
	Participants []string
	StartedAt    time.Time // optional; defaults to now
}

// Status snapshots the manager for the /status endpoint.
type Status struct {
	State      State            `json:"state"`
	Meeting    *model.Meeting   `json:"meeting,omitempty"`
	Detected   *DetectedMeeting `json:"detected,omitempty"`
	OutputRoot string           `json:"outputRoot"`
}

// Manager owns the single active session and its dependencies.
type Manager struct {
	cfg         config.Config
	capturer    audio.Capturer
	transcriber transcribe.Transcriber
	now         func() time.Time // injectable clock for tests

	mu       sync.Mutex
	current  *model.Meeting
	paused   bool
	detected *DetectedMeeting
}

// New builds a Manager from its dependencies.
func New(cfg config.Config, capturer audio.Capturer, transcriber transcribe.Transcriber) *Manager {
	return &Manager{
		cfg:         cfg,
		capturer:    capturer,
		transcriber: transcriber,
		now:         time.Now,
	}
}

// Start begins a recording and returns the created meeting (with its ID).
func (m *Manager) Start(ctx context.Context, req StartRequest) (model.Meeting, error) {
	if m.cfg.OutputRoot == "" {
		return model.Meeting{}, ErrEmptyOutput
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil {
		return model.Meeting{}, ErrBusy
	}

	started := req.StartedAt
	if started.IsZero() {
		started = m.now()
	}
	platform := req.Platform
	if platform == "" {
		platform = model.PlatformManual
	}
	meeting := model.Meeting{
		Title:        req.Title,
		Project:      sanitizeProject(req.Project),
		Platform:     platform,
		Participants: req.Participants,
		StartedAt:    started,
	}
	meeting.ID = meeting.DirName()

	if err := m.capturer.Start(ctx, meeting.ID); err != nil {
		return model.Meeting{}, fmt.Errorf("start capture: %w", err)
	}
	m.current = &meeting
	m.paused = false
	m.detected = nil
	return meeting, nil
}

// Stop ends the active recording, transcribes it, and writes the output.
func (m *Manager) Stop(ctx context.Context, id string) (writer.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	meeting, err := m.takeCurrent(id)
	if err != nil {
		return writer.Result{}, err
	}
	meeting.EndedAt = m.now()

	wavPath, capErr := m.capturer.Stop()
	segments, err := m.transcribeIfAvailable(ctx, wavPath, capErr)
	if err != nil {
		m.current = &meeting // keep session so the caller can retry/cancel
		return writer.Result{}, err
	}

	res, err := writer.Write(outputRoot(m.cfg.OutputRoot, meeting.Project), meeting, segments)
	if err != nil {
		return writer.Result{}, err
	}
	if m.cfg.Audio.DeleteWavOnFinish && wavPath != "" {
		_ = os.Remove(wavPath)
	}
	return res, nil
}

// Cancel aborts the active recording and discards its audio.
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := m.takeCurrent(id); err != nil {
		return err
	}
	return m.capturer.Cancel()
}

// Status returns the current state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{State: StateIdle, OutputRoot: m.cfg.OutputRoot, Detected: m.detected}
	if m.current != nil {
		meeting := *m.current
		st.Meeting = &meeting
		st.State = StateRecording
		if m.paused {
			st.State = StatePaused
		}
	}
	return st
}

// Pause stops capturing without ending the recording. Idempotent.
func (m *Manager) Pause(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkActive(id); err != nil {
		return err
	}
	if m.paused {
		return nil
	}
	if err := m.capturer.Pause(); err != nil {
		return err
	}
	m.paused = true
	return nil
}

// Resume continues a paused recording. Idempotent.
func (m *Manager) Resume(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkActive(id); err != nil {
		return err
	}
	if !m.paused {
		return nil
	}
	if err := m.capturer.Resume(); err != nil {
		return err
	}
	m.paused = false
	return nil
}

// SetDetected records a meeting found in the browser (used by the detector in
// "ask" mode so a UI can prompt the user). ClearDetected removes it.
func (m *Manager) SetDetected(code, title string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.detected = &DetectedMeeting{Code: code, Title: title}
}

// ClearDetected removes any pending detected meeting.
func (m *Manager) ClearDetected() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.detected = nil
}

// checkActive validates id against the active session. Caller holds m.mu.
func (m *Manager) checkActive(id string) error {
	if m.current == nil {
		return ErrNoSession
	}
	if id != "" && id != m.current.ID {
		return ErrUnknownID
	}
	return nil
}

// takeCurrent validates the id against the active session and detaches it.
// Caller must hold m.mu.
func (m *Manager) takeCurrent(id string) (model.Meeting, error) {
	if m.current == nil {
		return model.Meeting{}, ErrNoSession
	}
	if id != "" && id != m.current.ID {
		return model.Meeting{}, ErrUnknownID
	}
	meeting := *m.current
	m.current = nil
	m.paused = false
	return meeting, nil
}

// projectSlug matches characters not allowed in a project folder name.
var projectSlug = regexp.MustCompile(`[^a-z0-9_-]+`)

// sanitizeProject normalizes a project name into a safe single-segment folder
// name (lowercase, no path separators), preventing path traversal.
func sanitizeProject(project string) string {
	s := strings.ToLower(strings.TrimSpace(project))
	s = projectSlug.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// outputRoot returns the base root, or a per-project subfolder when project set.
func outputRoot(base, project string) string {
	if project == "" {
		return base
	}
	return filepath.Join(base, project)
}

// transcribeIfAvailable runs the transcriber when audio was actually captured.
// Until M1 lands, the stub capturer reports ErrNotImplemented; we treat that as
// "no audio" and produce an empty transcript so the pipeline still completes.
func (m *Manager) transcribeIfAvailable(ctx context.Context, wavPath string, capErr error) ([]model.Segment, error) {
	if errors.Is(capErr, audio.ErrNotImplemented) || wavPath == "" {
		return nil, nil
	}
	if capErr != nil {
		return nil, fmt.Errorf("stop capture: %w", capErr)
	}
	return m.transcriber.Transcribe(ctx, wavPath)
}
