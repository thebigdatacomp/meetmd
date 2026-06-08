// Package audio captures meeting audio at the OS level (system loopback for all
// participants + the user's mic), which is what makes MeetMD browser-agnostic.
// Real capture is platform-specific (M1): ScreenCaptureKit/BlackHole on macOS,
// WASAPI loopback on Windows, PipeWire/PulseAudio monitor on Linux.
package audio

import (
	"context"
	"errors"
)

// ErrNotImplemented is returned by the stub on platforms without a real
// capturer yet (Windows/Linux). macOS is implemented in mac_darwin.go.
var ErrNotImplemented = errors.New("audio capture not implemented on this platform yet")

// Options configures a platform Capturer. NewCapturer (build-tagged per OS)
// consumes it.
type Options struct {
	HelperPath string // path to the OS capture helper binary (empty = look up in PATH)
	WorkDir    string // directory for temporary WAV files
}

// Capturer records a session's audio to a WAV file on disk.
type Capturer interface {
	// Start begins capturing for sessionID. It returns immediately; capture
	// runs until Stop or Cancel.
	Start(ctx context.Context, sessionID string) error
	// Stop ends capture and returns the path to the recorded WAV.
	Stop() (wavPath string, err error)
	// Cancel aborts capture and discards any recorded audio.
	Cancel() error
}

// Stub is a no-op Capturer so the bridge runs end-to-end before M1. It records
// nothing and reports that capture is unavailable on Stop.
type Stub struct{}

func (Stub) Start(context.Context, string) error { return nil }
func (Stub) Stop() (string, error)               { return "", ErrNotImplemented }
func (Stub) Cancel() error                       { return nil }
