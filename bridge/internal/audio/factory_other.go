//go:build !darwin

package audio

import "github.com/thebigdatacomp/meetmd/internal/config"

// NewCapturer returns a no-op Stub on platforms without a real capturer yet
// (Windows WASAPI loopback and Linux PipeWire monitor are future work).
func NewCapturer(*config.Store) Capturer {
	return Stub{}
}
