//go:build darwin

package detect

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/thebigdatacomp/meetmd/internal/audio"
	"github.com/thebigdatacomp/meetmd/internal/config"
	"github.com/thebigdatacomp/meetmd/internal/model"
	"github.com/thebigdatacomp/meetmd/internal/session"
	"github.com/thebigdatacomp/meetmd/internal/transcribe"
)

func newManager(t *testing.T) *session.Manager {
	t.Helper()
	return session.New(config.NewStore(config.Config{RecordingsRoot: t.TempDir()}), audio.Stub{},
		func(config.Config, bool) transcribe.Transcriber { return transcribe.Stub{} })
}

const meetCodeSample = "abc-defg-hij"

func TestReconcileAskSurfacesDetection(t *testing.T) {
	mgr := newManager(t)
	reconcile(context.Background(), mgr, newPresence(defaultInterval), "bora", ModeAsk, meetCodeSample, "Daily")

	st := mgr.Status()
	if st.State != session.StateIdle {
		t.Errorf("ask mode must not start recording, got %s", st.State)
	}
	if st.Detected == nil || st.Detected.Code != meetCodeSample {
		t.Errorf("expected detected meeting surfaced, got %+v", st.Detected)
	}
}

func TestReconcileAutoStartsAndStops(t *testing.T) {
	mgr := newManager(t)
	ctx := context.Background()
	pres := newPresence(defaultInterval)
	reconcile(ctx, mgr, pres, "bora", ModeAuto, meetCodeSample, "Daily")
	if mgr.Status().State != session.StateRecording {
		t.Fatalf("auto mode should start recording")
	}
	for i := 0; i < pres.threshold; i++ { // meeting tab gone for the whole grace period
		reconcile(ctx, mgr, pres, "bora", ModeAuto, "", "")
	}
	if mgr.Status().State != session.StateIdle {
		t.Errorf("auto mode should stop when the meeting ends")
	}
}

func TestReconcileDoesNotStopManualRecording(t *testing.T) {
	mgr := newManager(t)
	ctx := context.Background()
	if _, err := mgr.Start(ctx, session.StartRequest{Title: "Solo", Platform: model.PlatformManual}); err != nil {
		t.Fatal(err)
	}
	pres := newPresence(defaultInterval)
	for i := 0; i <= pres.threshold; i++ { // no Meet tab, well past the grace period
		reconcile(ctx, mgr, pres, "", ModeAsk, "", "")
	}
	if mgr.Status().State != session.StateRecording {
		t.Errorf("manual recording must not be auto-stopped by the detector")
	}
}

// A scan that misses the Meet tab mid-call (Safari busy, tab reloading) must not
// split the meeting: the recording survives until the grace period elapses.
func TestReconcileTransientMissKeepsRecording(t *testing.T) {
	mgr := newManager(t)
	ctx := context.Background()
	pres := newPresence(defaultInterval)
	reconcile(ctx, mgr, pres, "bora", ModeAuto, meetCodeSample, "Daily")

	for i := 0; i < pres.threshold-1; i++ {
		reconcile(ctx, mgr, pres, "bora", ModeAuto, "", "")
	}
	if mgr.Status().State != session.StateRecording {
		t.Fatalf("a miss shorter than the grace period must not stop the recording")
	}

	// The tab is back: the streak resets, so another short miss is tolerated too.
	reconcile(ctx, mgr, pres, "bora", ModeAuto, meetCodeSample, "Daily")
	for i := 0; i < pres.threshold-1; i++ {
		reconcile(ctx, mgr, pres, "bora", ModeAuto, "", "")
	}
	if mgr.Status().State != session.StateRecording {
		t.Errorf("seeing the tab again must reset the miss streak")
	}
}

func TestNewPresenceSpansGracePeriod(t *testing.T) {
	cases := []struct {
		interval time.Duration
		want     int
	}{
		{3 * time.Second, 5},
		{4 * time.Second, 4}, // rounds up: 16s >= endGrace
		{endGrace, 1},
		{time.Minute, 1}, // never below one scan
	}
	for _, c := range cases {
		if got := newPresence(c.interval).threshold; got != c.want {
			t.Errorf("newPresence(%s).threshold = %d, want %d", c.interval, got, c.want)
		}
	}
}

func TestScriptErrorIncludesStderr(t *testing.T) {
	// Output (as used by detectMeet) captures stderr into the *exec.ExitError.
	_, err := exec.Command("sh", "-c", "echo 'execution error: boom (-1712)' >&2; exit 1").Output()
	got := scriptError(err)
	if !strings.Contains(got.Error(), "(-1712)") {
		t.Errorf("expected stderr in error, got %q", got)
	}
	var exitErr *exec.ExitError
	if !errors.As(got, &exitErr) {
		t.Errorf("wrapped error must keep the *exec.ExitError")
	}
}
