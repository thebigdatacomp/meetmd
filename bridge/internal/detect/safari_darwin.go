//go:build darwin

package detect

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/thebigdatacomp/meetmd/internal/config"
	"github.com/thebigdatacomp/meetmd/internal/model"
	"github.com/thebigdatacomp/meetmd/internal/session"
)

// meetCode matches a Google Meet meeting URL and captures its code (the landing
// page meet.google.com/ has no code, so it is ignored).
var meetCode = regexp.MustCompile(`https://meet\.google\.com/([a-z]{3}-[a-z]{4}-[a-z]{3})`)

// titlePrefix strips the leading "Meet - " from a Safari tab title.
var titlePrefix = regexp.MustCompile(`^Meet\s*[–\-—]\s*`)

// safariScript returns the URL and title of the first Safari tab that is in a
// Meet call, or an empty string. Reading tabs needs Automation permission.
//   - It never launches Safari: a closed browser simply means "no meeting".
//   - Each window/tab is read inside its own try, so one unreadable tab (no URL
//     yet, a window closing mid-scan) doesn't fail the whole scan. Listing the
//     windows stays outside any try, so a denied Automation grant (-1743) still
//     surfaces as an error instead of a silent "no meeting".
const safariScript = `if application "Safari" is not running then return ""
tell application "Safari"
	repeat with w in windows
		try
			repeat with t in tabs of w
				try
					set u to URL of t
					if u is not missing value and u contains "meet.google.com/" then
						return u & linefeed & (name of t)
					end if
				end try
			end repeat
		end try
	end repeat
	return ""
end tell`

// endGrace is how long the Meet tab must stay out of sight before an active
// Meet recording is auto-stopped. A single scan can miss the tab (Safari busy,
// tab reloading, a transient AppleScript failure); stopping on the first miss
// split one meeting into several fragments whenever that happened.
const endGrace = 15 * time.Second

// presence debounces the "meeting ended" signal: it counts consecutive scans
// that did not see a Meet tab and reports the meeting as gone only once that
// streak reaches the threshold.
type presence struct {
	threshold int
	misses    int
}

// newPresence sizes the miss threshold so the grace period spans endGrace at
// the given polling interval (at least one scan).
func newPresence(interval time.Duration) *presence {
	n := int((endGrace + interval - 1) / interval)
	if n < 1 {
		n = 1
	}
	return &presence{threshold: n}
}

// seen records a scan that found a Meet tab.
func (p *presence) seen() { p.misses = 0 }

// missed records a scan without a Meet tab and reports whether the meeting has
// now been absent for the whole grace period.
func (p *presence) missed() bool {
	p.misses++
	return p.misses >= p.threshold
}

// Start launches the Safari meeting detector in a background goroutine. It runs
// regardless of the enabled flag and reads auto_detect settings live from the
// store each tick, so toggling detection on/off applies without a restart.
func Start(ctx context.Context, mgr *session.Manager, store *config.Store) {
	interval := time.Duration(store.Get().AutoDetect.IntervalSeconds) * time.Second
	if interval <= 0 {
		interval = defaultInterval
	}
	go loop(ctx, mgr, store, interval)
}

func loop(ctx context.Context, mgr *session.Manager, store *config.Store, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	log.Printf("auto-detect (Safari) ativo a cada %s", interval)

	pres := newPresence(interval)
	warned := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Snooze: do nothing at all — no Safari query, no prompt, no auto-record.
			if mgr.Asleep() {
				mgr.ClearDetected()
				continue
			}
			ad := store.Get().AutoDetect
			if !ad.Enabled {
				mgr.ClearDetected()
				continue
			}
			code, title, err := detectMeet()
			if err != nil {
				if !warned {
					log.Printf("auto-detect indisponível (permissão de Automation do Safari?): %v", err)
					warned = true
				}
				continue
			}
			warned = false
			reconcile(ctx, mgr, pres, ad.Project, ad.Mode, code, title)
		}
	}
}

// reconcile drives detection/recording based on whether a Meet tab is present:
//   - no Meet tab for endGrace + a Meet recording active → auto-stop (ended)
//   - Meet tab + idle, "auto" mode                       → auto-start
//   - Meet tab + idle, "ask" mode                        → surface it for the UI
//
// Auto-stop keys off the recording's platform (google-meet), so manual
// recordings are never touched.
func reconcile(ctx context.Context, mgr *session.Manager, pres *presence, project, mode, code, title string) {
	st := mgr.Status()
	recording := st.State == session.StateRecording || st.State == session.StatePaused
	meetRecording := recording && st.Meeting != nil && st.Meeting.Platform == model.PlatformGoogleMeet

	if code == "" {
		mgr.ClearDetected()
		if pres.missed() && meetRecording {
			if _, err := mgr.Stop(ctx, ""); err != nil {
				log.Printf("auto-stop falhou: %v", err)
			} else {
				log.Printf("auto-parado (reunião encerrada)")
			}
		}
		return
	}
	pres.seen()

	if recording { // already recording — nothing to prompt or start
		mgr.ClearDetected()
		return
	}

	if mode == ModeAuto {
		if _, err := mgr.Start(ctx, session.StartRequest{
			Title:    title,
			Project:  project,
			Platform: model.PlatformGoogleMeet,
		}); err != nil {
			log.Printf("auto-start falhou: %v", err)
		} else {
			log.Printf("auto-iniciado: %s", title)
		}
		return
	}
	mgr.SetDetected(code, title) // ModeAsk (default): let the UI prompt
}

func detectMeet() (code, title string, err error) {
	out, err := exec.Command("osascript", "-e", safariScript).Output()
	if err != nil {
		return "", "", scriptError(err)
	}
	lines := strings.SplitN(strings.TrimSpace(string(out)), "\n", 2)
	if len(lines) == 0 || lines[0] == "" {
		return "", "", nil
	}
	m := meetCode.FindStringSubmatch(lines[0])
	if m == nil {
		return "", "", nil
	}
	title = lines[0] // fall back to URL if the tab has no readable name
	if len(lines) == 2 && strings.TrimSpace(lines[1]) != "" {
		title = titlePrefix.ReplaceAllString(strings.TrimSpace(lines[1]), "")
	}
	return m[1], title, nil
}

// scriptError attaches osascript's stderr (which carries the AppleScript error
// and its code, e.g. "Not authorized to send Apple events to Safari. (-1743)")
// to the bare exit status, so the log says why the scan failed.
func scriptError(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if msg := strings.TrimSpace(string(exitErr.Stderr)); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
	}
	return err
}
