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
	"sort"
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
	StateIdle       State = "idle"
	StateRecording  State = "recording"
	StatePaused     State = "paused"
	StateProcessing State = "processing" // stopped; transcribing/writing
)

// Kind distinguishes a full meeting recording from a quick voice note. A note
// is mic-only and writes a lean Markdown file to the inbox.
type Kind string

const (
	KindMeeting Kind = "meeting"
	KindNote    Kind = "note"
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
	Kind       Kind             `json:"kind,omitempty"` // "meeting" | "note" while active
	Meeting    *model.Meeting   `json:"meeting,omitempty"`
	Detected   *DetectedMeeting `json:"detected,omitempty"`
	OutputRoot string           `json:"outputRoot"`
	UILanguage string           `json:"uiLanguage"` // resolved UI language ("pt"/"en")
}

// TranscriberFor builds a transcriber from the current config. It is called per
// recording so config changes (model, language, VAD) take effect without a
// restart — see config hot-reload.
type TranscriberFor func(config.Config) transcribe.Transcriber

// Manager owns the single active session and its dependencies.
type Manager struct {
	store          *config.Store
	capturer       audio.Capturer
	newTranscriber TranscriberFor
	now            func() time.Time // injectable clock for tests

	mu         sync.Mutex
	current    *model.Meeting
	kind       Kind
	paused     bool
	processing bool // stopped, running transcription/write (lock not held meanwhile)
	detected   *DetectedMeeting
}

// New builds a Manager from its dependencies. The config Store is read live so
// transcription/output settings can be hot-reloaded.
func New(store *config.Store, capturer audio.Capturer, newTranscriber TranscriberFor) *Manager {
	return &Manager{
		store:          store,
		capturer:       capturer,
		newTranscriber: newTranscriber,
		now:            time.Now,
	}
}

// Start begins a recording and returns the created meeting (with its ID).
func (m *Manager) Start(ctx context.Context, req StartRequest) (model.Meeting, error) {
	if m.store.Get().OutputRoot == "" {
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
	m.kind = KindMeeting
	m.paused = false
	m.detected = nil
	return meeting, nil
}

// StartNote begins a quick voice note: a mic-only recording (no Screen Recording
// permission) whose output is a lean Markdown file in the inbox.
func (m *Manager) StartNote(ctx context.Context) (model.Meeting, error) {
	if m.store.Get().InboxRoot == "" {
		return model.Meeting{}, ErrEmptyOutput
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current != nil {
		return model.Meeting{}, ErrBusy
	}

	note := model.Meeting{Platform: model.PlatformManual, StartedAt: m.now()}
	note.ID = note.StartedAt.Format("2006-01-02-1504") + "-note"

	if err := m.capturer.StartMicOnly(ctx, note.ID); err != nil {
		return model.Meeting{}, fmt.Errorf("start mic capture: %w", err)
	}
	m.current = &note
	m.kind = KindNote
	m.paused = false
	return note, nil
}

// Stop ends the active recording, transcribes it, and writes the output.
//
// The slow work (capture finalize + transcription + write) runs WITHOUT holding
// the lock, so Status() stays responsive — otherwise the UI would freeze on the
// last state (e.g. the icon stuck on "recording") for the whole transcription.
func (m *Manager) Stop(ctx context.Context, id string) (writer.Result, error) {
	m.mu.Lock()
	kind := m.kind
	meeting, err := m.takeCurrent(id)
	if err != nil {
		m.mu.Unlock()
		return writer.Result{}, err
	}
	meeting.EndedAt = m.now()
	m.processing = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.processing = false
		m.mu.Unlock()
	}()

	cfg := m.store.Get() // read live config (hot-reloadable)
	rec, capErr := m.capturer.Stop()
	segments, err := m.transcribeRecording(ctx, m.newTranscriber(cfg), rec, capErr)
	if err != nil {
		return writer.Result{}, err
	}

	var res writer.Result
	if kind == KindNote {
		res, err = writer.WriteNote(cfg.InboxRoot, meeting, segments, cfg.ResolvedUILang())
	} else {
		res, err = writer.Write(outputRoot(cfg.OutputRoot, meeting.Project), meeting, segments, cfg.ResolvedUILang())
	}
	if err != nil {
		return writer.Result{}, err
	}
	if cfg.Audio.DeleteWavOnFinish {
		for _, p := range []string{rec.SystemWav, rec.MicWav} {
			if p != "" {
				_ = os.Remove(p)
			}
		}
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
	cfg := m.store.Get()
	st := Status{State: StateIdle, Kind: m.kind, OutputRoot: cfg.OutputRoot, UILanguage: cfg.ResolvedUILang(), Detected: m.detected}
	switch {
	case m.current != nil:
		meeting := *m.current
		st.Meeting = &meeting
		st.State = StateRecording
		if m.paused {
			st.State = StatePaused
		}
	case m.processing:
		st.State = StateProcessing
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
	m.kind = ""
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

// transcribeRecording transcribes whichever channels were captured, labels each
// (system = participants, mic = you), and merges them ordered by start time.
// The stub capturer reports ErrNotImplemented until real capture exists; that is
// treated as "no audio" so the pipeline still completes with an empty transcript.
func (m *Manager) transcribeRecording(ctx context.Context, transcriber transcribe.Transcriber, rec audio.Recording, capErr error) ([]model.Segment, error) {
	if errors.Is(capErr, audio.ErrNotImplemented) {
		return nil, nil
	}
	if capErr != nil {
		return nil, fmt.Errorf("stop capture: %w", capErr)
	}

	channels := []struct {
		wav     string
		speaker model.Speaker
	}{
		{rec.SystemWav, model.SpeakerOthers},
		{rec.MicWav, model.SpeakerYou},
	}

	// Transcribe both channels concurrently — each whisper run is the slow part,
	// so this ~halves wall time when both channels have audio.
	results := make([][]model.Segment, len(channels))
	errs := make([]error, len(channels))
	var wg sync.WaitGroup
	for i, ch := range channels {
		if ch.wav == "" {
			continue
		}
		wg.Add(1)
		go func(i int, wav string, speaker model.Speaker) {
			defer wg.Done()
			segs, err := transcriber.Transcribe(ctx, wav)
			if err != nil {
				errs[i] = err
				return
			}
			for j := range segs {
				segs[j].Speaker = speaker
			}
			results[i] = segs
		}(i, ch.wav, ch.speaker)
	}
	wg.Wait()

	var segments []model.Segment
	for i, segs := range results {
		if errs[i] != nil {
			return nil, errs[i]
		}
		segments = append(segments, segs...)
	}
	sort.SliceStable(segments, func(i, j int) bool {
		return segments[i].Start < segments[j].Start
	})
	return segments, nil
}
