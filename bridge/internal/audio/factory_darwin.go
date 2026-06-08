//go:build darwin

package audio

import "github.com/thebigdatacomp/meetmd/internal/config"

// NewCapturer returns the macOS ScreenCaptureKit-based capturer. It reads audio
// settings (helper path, mic) live from the store at each Start, so they can be
// hot-reloaded.
func NewCapturer(store *config.Store) Capturer {
	return newMacCapturer(store)
}
