//go:build darwin

package detect

import (
	"context"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"time"

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
const safariScript = `tell application "Safari"
	repeat with w in windows
		repeat with t in tabs of w
			if (URL of t) contains "meet.google.com/" then
				return (URL of t) & linefeed & (name of t)
			end if
		end repeat
	end repeat
	return ""
end tell`

// Start launches the Safari meeting detector in a background goroutine.
func Start(ctx context.Context, mgr *session.Manager, opts Options) {
	if opts.Interval <= 0 {
		opts.Interval = defaultInterval
	}
	if opts.Mode == "" {
		opts.Mode = ModeAsk
	}
	go loop(ctx, mgr, opts)
}

func loop(ctx context.Context, mgr *session.Manager, opts Options) {
	ticker := time.NewTicker(opts.Interval)
	defer ticker.Stop()
	log.Printf("auto-detect (Safari) ativo: modo=%s, a cada %s", opts.Mode, opts.Interval)

	warned := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			code, title, err := detectMeet()
			if err != nil {
				if !warned {
					log.Printf("auto-detect indisponível (permissão de Automation do Safari?): %v", err)
					warned = true
				}
				continue
			}
			warned = false
			reconcile(ctx, mgr, opts, code, title)
		}
	}
}

// reconcile drives detection/recording based on whether a Meet tab is present:
//   - no Meet tab + a Meet recording active → auto-stop (the meeting ended)
//   - Meet tab + idle, "auto" mode          → auto-start
//   - Meet tab + idle, "ask" mode           → surface it for the UI to prompt
//
// Auto-stop keys off the recording's platform (google-meet), so manual
// recordings are never touched.
func reconcile(ctx context.Context, mgr *session.Manager, opts Options, code, title string) {
	st := mgr.Status()
	recording := st.State == session.StateRecording || st.State == session.StatePaused
	meetRecording := recording && st.Meeting != nil && st.Meeting.Platform == model.PlatformGoogleMeet

	if code == "" {
		mgr.ClearDetected()
		if meetRecording {
			if _, err := mgr.Stop(ctx, ""); err != nil {
				log.Printf("auto-stop falhou: %v", err)
			} else {
				log.Printf("auto-parado (reunião encerrada)")
			}
		}
		return
	}

	if recording { // already recording — nothing to prompt or start
		mgr.ClearDetected()
		return
	}

	if opts.Mode == ModeAuto {
		if _, err := mgr.Start(ctx, session.StartRequest{
			Title:    title,
			Project:  opts.Project,
			Platform: model.PlatformGoogleMeet,
		}); err != nil {
			log.Printf("auto-start falhou: %v", err)
		} else {
			log.Printf("auto-iniciado: %s", title)
		}
		return
	}
	mgr.SetDetected(code, title) // ModeAsk: let the UI prompt
}

func detectMeet() (code, title string, err error) {
	out, err := exec.Command("osascript", "-e", safariScript).Output()
	if err != nil {
		return "", "", err
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
