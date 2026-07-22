package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thebigdatacomp/meetmd/internal/audio"
	"github.com/thebigdatacomp/meetmd/internal/config"
	"github.com/thebigdatacomp/meetmd/internal/transcribe"
)

// fakeCapturer hands back WAV paths that really exist, so the test can assert
// what Stop does to them.
type fakeCapturer struct{ rec audio.Recording }

func (fakeCapturer) Start(context.Context, string) error        { return nil }
func (fakeCapturer) StartMicOnly(context.Context, string) error { return nil }
func (c fakeCapturer) Stop() (audio.Recording, error)           { return c.rec, nil }
func (fakeCapturer) Cancel() error                              { return nil }
func (fakeCapturer) Pause() error                               { return nil }
func (fakeCapturer) Resume() error                              { return nil }

// fixedTranscriber returns a canned Result, standing in for a whisper run whose
// coverage says whether the transcript can be trusted.
type fixedTranscriber struct{ res transcribe.Result }

func (f fixedTranscriber) Transcribe(context.Context, string) (transcribe.Result, error) {
	return f.res, nil
}

// stopWith runs a whole meeting whose transcription returns res, and reports
// where the audio ended up.
func stopWith(t *testing.T, res transcribe.Result) (root string, rec audio.Recording, dir string) {
	t.Helper()
	root = t.TempDir()
	audioDir := t.TempDir() // stands in for the volatile temp dir
	rec = audio.Recording{
		SystemWav: filepath.Join(audioDir, "m.wav"),
		MicWav:    filepath.Join(audioDir, "m.mic.wav"),
	}
	for _, p := range []string{rec.SystemWav, rec.MicWav} {
		if err := os.WriteFile(p, []byte("RIFF....WAVE"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{RecordingsRoot: root}
	cfg.Audio.DeleteWavOnFinish = true
	mgr := New(config.NewStore(cfg), fakeCapturer{rec: rec},
		func(config.Config, bool) transcribe.Transcriber { return fixedTranscriber{res: res} })

	ctx := context.Background()
	if _, err := mgr.Start(ctx, StartRequest{Title: "Long Call"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	out, err := mgr.Stop(ctx, "")
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	return root, rec, out.SessionDir
}

// The failure this guards against cost a real 44-minute meeting: the transcript
// came back nearly empty, the meeting was written as if that were the truth, and
// delete-on-finish then destroyed the only copy of the audio. A transcript that
// cannot account for its recording must never be grounds for deleting it.
func TestStopKeepsAudioWhenTranscriptIsImplausible(t *testing.T) {
	root, rec, sessionDir := stopWith(t, transcribe.Result{Coverage: 0.045, Audited: true})

	if _, err := os.Stat(rec.SystemWav); !os.IsNotExist(err) {
		t.Error("audio should have been moved out of the temp dir, not left behind")
	}
	kept := filepath.Join(root, "recovery")
	entries, err := os.ReadDir(kept)
	if err != nil || len(entries) == 0 {
		t.Fatalf("raw audio was not kept under %s (err=%v)", kept, err)
	}
	found, _ := filepath.Glob(filepath.Join(kept, "*", "m.wav"))
	if len(found) == 0 {
		t.Error("system WAV missing from recovery — the meeting would be unrecoverable")
	}

	body, err := os.ReadFile(filepath.Join(sessionDir, "meeting.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "⚠️") {
		t.Errorf("meeting.md must warn that the transcript is partial:\n%s", body)
	}
}

// The flip side: a healthy meeting must still clean up after itself, or every
// recording accumulates forever.
func TestStopDeletesAudioAfterAPlausibleTranscript(t *testing.T) {
	root, rec, sessionDir := stopWith(t, transcribe.Result{Coverage: 0.62, Audited: true})

	if _, err := os.Stat(rec.SystemWav); !os.IsNotExist(err) {
		t.Error("a trusted transcript should let the WAV be deleted")
	}
	if _, err := os.Stat(filepath.Join(root, "recovery")); err == nil {
		t.Error("healthy meeting should not leave anything in recovery/")
	}
	body, err := os.ReadFile(filepath.Join(sessionDir, "meeting.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "Transcrição possivelmente incompleta") ||
		strings.Contains(string(body), "Transcript may be incomplete") {
		t.Errorf("healthy meeting must not be flagged as suspect:\n%s", body)
	}
}

// A short recording has no meaningful coverage, so it must not be accused —
// otherwise every quick voice note would be flagged and hoard its audio.
func TestStopTrustsUnauditedShortRecording(t *testing.T) {
	root, _, _ := stopWith(t, transcribe.Result{Coverage: 0, Audited: false})
	if _, err := os.Stat(filepath.Join(root, "recovery")); err == nil {
		t.Error("an unaudited short recording should not be treated as a failure")
	}
}
