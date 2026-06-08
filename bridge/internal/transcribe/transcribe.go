// Package transcribe turns captured audio into transcript segments. The
// Transcriber interface keeps the engine pluggable: whisper.cpp (local) is the
// MVP default, with a Whisper API backend planned behind the same interface
// (see docs/specs/2026-06-08-architecture.md §3.3).
package transcribe

import (
	"context"
	"os"
	"os/exec"

	"github.com/thebigdatacomp/meetmd/internal/model"
)

// EngineLocal selects the local whisper.cpp backend.
const EngineLocal = "local"

// whisperBinaries are the candidate CLI names brew's whisper-cpp may install.
var whisperBinaries = []string{"whisper-cli", "whisper-cpp"}

// Transcriber converts a recorded WAV into ordered transcript segments.
type Transcriber interface {
	// Transcribe processes wavPath and returns segments ordered by Start.
	Transcribe(ctx context.Context, wavPath string) ([]model.Segment, error)
}

// Options configures which Transcriber New builds.
type Options struct {
	Engine    string // "local" (whisper.cpp). Other engines fall back to Stub.
	BinPath   string // whisper CLI path (empty = look up in PATH)
	ModelPath string // ggml model file for the local engine
	Language  string // e.g. "pt" or "auto"
	VADModel  string // optional ggml VAD model (enables silence skipping)
}

// New returns a Transcriber for the given options, plus a human-readable note
// describing the choice. It degrades gracefully to Stub (empty transcript)
// rather than failing the bridge when whisper.cpp or its model is missing.
func New(o Options) (Transcriber, string) {
	if o.Engine != EngineLocal {
		return Stub{}, "transcrição desabilitada (engine != local)"
	}
	bin := o.BinPath
	if bin == "" {
		bin = lookupWhisper()
	}
	if bin == "" {
		return Stub{}, "whisper CLI não encontrado no PATH — transcript sairá vazio"
	}
	if _, err := os.Stat(o.ModelPath); err != nil {
		return Stub{}, "modelo whisper não encontrado em " + o.ModelPath + " — transcript sairá vazio"
	}
	w := Whisper{BinPath: bin, ModelPath: o.ModelPath, Language: o.Language, VADModel: o.VADModel}
	note := "whisper local: " + bin
	if o.VADModel != "" {
		if _, err := os.Stat(o.VADModel); err == nil {
			note += " (VAD on)"
		}
	}
	return w, note
}

func lookupWhisper() string {
	for _, name := range whisperBinaries {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// Stub is a placeholder transcriber used until whisper.cpp is wired up (M2).
// It returns no segments so the writer emits an explicit "empty" transcript
// rather than failing the session.
type Stub struct{}

func (Stub) Transcribe(context.Context, string) ([]model.Segment, error) {
	return nil, nil
}
