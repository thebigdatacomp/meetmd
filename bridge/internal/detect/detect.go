// Package detect auto-detects active meetings in the browser and drives the
// session manager, so the user doesn't need to start/stop recordings manually.
// The real implementation is macOS-only (AppleScript over Safari); other
// platforms get a no-op.
package detect

import "time"

// Detection modes (config auto_detect.mode).
const (
	ModeAsk  = "ask"  // surface detected meetings for a UI to prompt the user
	ModeAuto = "auto" // start/stop recording automatically
)

const defaultInterval = 3 * time.Second
