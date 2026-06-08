// Package transcribe turns captured audio into transcript segments. The
// Transcriber interface keeps the engine pluggable: whisper.cpp (local) is the
// MVP default, with a Whisper API backend planned behind the same interface
// (see docs/specs/2026-06-08-architecture.md §3.3).
package transcribe

import (
	"context"

	"github.com/thebigdatacomp/meetmd/internal/model"
)

// Transcriber converts a recorded WAV into ordered transcript segments.
type Transcriber interface {
	// Transcribe processes wavPath and returns segments ordered by Start.
	Transcribe(ctx context.Context, wavPath string) ([]model.Segment, error)
}

// Stub is a placeholder transcriber used until whisper.cpp is wired up (M2).
// It returns no segments so the writer emits an explicit "empty" transcript
// rather than failing the session.
type Stub struct{}

func (Stub) Transcribe(context.Context, string) ([]model.Segment, error) {
	return nil, nil
}
