//go:build darwin

package audio

// NewCapturer returns the macOS ScreenCaptureKit-based capturer.
func NewCapturer(opts Options) Capturer {
	return newMacCapturer(opts)
}
