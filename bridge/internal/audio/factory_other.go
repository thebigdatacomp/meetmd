//go:build !darwin

package audio

// NewCapturer returns a no-op Stub on platforms without a real capturer yet
// (Windows WASAPI loopback and Linux PipeWire monitor are future work).
func NewCapturer(Options) Capturer {
	return Stub{}
}
