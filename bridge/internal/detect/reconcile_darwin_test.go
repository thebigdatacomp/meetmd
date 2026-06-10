//go:build darwin

package detect

import (
	"context"
	"testing"

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
	reconcile(context.Background(), mgr, "bora", ModeAsk, meetCodeSample, "Daily")

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
	reconcile(ctx, mgr, "bora", ModeAuto, meetCodeSample, "Daily")
	if mgr.Status().State != session.StateRecording {
		t.Fatalf("auto mode should start recording")
	}
	reconcile(ctx, mgr, "bora", ModeAuto, "", "") // meeting tab gone
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
	reconcile(ctx, mgr, "", ModeAsk, "", "") // no Meet tab
	if mgr.Status().State != session.StateRecording {
		t.Errorf("manual recording must not be auto-stopped by the detector")
	}
}
