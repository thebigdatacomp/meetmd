package transcribe

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAudioDuration(t *testing.T) {
	const rate = 16000
	wav := writeWAV(t, tone(3*rate, 1000)) // 3 seconds
	got, err := audioDuration(wav)
	if err != nil {
		t.Fatalf("audioDuration: %v", err)
	}
	if got < 2900*time.Millisecond || got > 3100*time.Millisecond {
		t.Errorf("duration = %v, want ~3s", got)
	}
	// Reading the header must not depend on loading the samples.
	if _, err := audioDuration(filepath.Join(t.TempDir(), "missing.wav")); err == nil {
		t.Error("audioDuration should fail on a missing file")
	}
	notWav := filepath.Join(t.TempDir(), "x.wav")
	if err := os.WriteFile(notWav, []byte("not a wav at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := audioDuration(notWav); err == nil {
		t.Error("audioDuration should reject a non-WAV file")
	}
}

func TestSpeechCoverage(t *testing.T) {
	segs := []seg{
		{start: 0, end: 10 * time.Second},
		{start: 30 * time.Second, end: 40 * time.Second},
		{start: time.Minute, end: 0}, // whisper emits these; they must not count negative
	}
	if got := speechSeconds(segs); got != 20*time.Second {
		t.Errorf("speechSeconds = %v, want 20s", got)
	}
	if got := speechCoverage(segs, 100*time.Second); got < 0.19 || got > 0.21 {
		t.Errorf("coverage = %.3f, want ~0.20", got)
	}
	// An unknown recording length must read as "cannot judge", not as zero speech.
	if got := speechCoverage(segs, 0); got != 0 {
		t.Errorf("coverage with unknown duration = %.3f, want 0", got)
	}
}

// The whole point of Result is telling a quiet meeting from a lost one, so the
// defaults must never accuse the pipeline without evidence.
func TestResultPlausible(t *testing.T) {
	cases := []struct {
		name string
		r    Result
		want bool
	}{
		{"unaudited short clip is trusted", Result{Coverage: 0, Audited: false}, true},
		{"a normal meeting is trusted", Result{Coverage: 0.62, Audited: true}, true},
		{"just above the floor is trusted", Result{Coverage: MinPlausibleCoverage, Audited: true}, true},
		{"44 min of audio, 2 min of speech is not", Result{Coverage: 0.045, Audited: true}, false},
		{"nothing at all is not", Result{Coverage: 0, Audited: true}, false},
	}
	for _, c := range cases {
		if got := c.r.Plausible(); got != c.want {
			t.Errorf("%s: Plausible() = %v, want %v", c.name, got, c.want)
		}
	}
}
