package transcribe

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeWAV builds a 16kHz mono 16-bit WAV from sample blocks and returns its path.
func writeWAV(t *testing.T, blocks ...[]int16) string {
	t.Helper()
	var samples []int16
	for _, b := range blocks {
		samples = append(samples, b...)
	}
	const rate = 16000
	dataLen := len(samples) * 2
	buf := make([]byte, 0, 44+dataLen)
	put4 := func(s string) { buf = append(buf, s...) }
	put32 := func(v uint32) { buf = binary.LittleEndian.AppendUint32(buf, v) }
	put16 := func(v uint16) { buf = binary.LittleEndian.AppendUint16(buf, v) }
	put4("RIFF")
	put32(uint32(36 + dataLen))
	put4("WAVE")
	put4("fmt ")
	put32(16)
	put16(1)        // PCM
	put16(1)        // mono
	put32(rate)     // sample rate
	put32(rate * 2) // byte rate
	put16(2)        // block align
	put16(16)       // bits
	put4("data")
	put32(uint32(dataLen))
	for _, s := range samples {
		put16(uint16(s))
	}
	path := filepath.Join(t.TempDir(), "test.wav")
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("write wav: %v", err)
	}
	return path
}

// tone returns n samples at a constant amplitude (a loudness stand-in).
func tone(n int, amp int16) []int16 {
	s := make([]int16, n)
	for i := range s {
		if i%2 == 0 {
			s[i] = amp // alternate sign → nonzero RMS
		} else {
			s[i] = -amp
		}
	}
	return s
}

// A mic that broke but kept emitting buffers writes digital silence, which whisper
// transcribes into zero segments — indistinguishable from a quiet user unless we
// ask the audio itself. HasAudio is what makes that call.
func TestDropMuted(t *testing.T) {
	segs := []seg{
		{start: 2 * time.Second, text: "before mute"},
		{start: 6 * time.Second, text: "2.5 kg of flour"},  // hallucination while muted
		{start: 8 * time.Second, text: "still muted"},       // inside the muted range
		{start: 15 * time.Second, text: "back and talking"}, // after unmute
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "mic.wav.muted")
	// Muted from 5s to 10s.
	if err := os.WriteFile(path, []byte("5000 10000\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := dropMuted(path, segs)
	if len(got) != 2 || got[0].text != "before mute" || got[1].text != "back and talking" {
		t.Fatalf("dropMuted kept the wrong segments: %+v", got)
	}

	// No sidecar → nothing dropped.
	if len(dropMuted(filepath.Join(dir, "missing"), segs)) != len(segs) {
		t.Error("dropMuted dropped segments with no muted-ranges file")
	}
}

func TestHasAudio(t *testing.T) {
	rate := 16000
	if !HasAudio(writeWAV(t, tone(rate, 800))) { // quiet, but a real noise floor
		t.Error("HasAudio = false for a quiet-but-real recording; the mic worked")
	}
	if HasAudio(writeWAV(t, tone(rate, 0))) { // a broken mic: exact zeroes
		t.Error("HasAudio = true for a digitally silent WAV; the mic captured nothing")
	}
	if HasAudio(filepath.Join(t.TempDir(), "missing.wav")) {
		t.Error("HasAudio = true for an unreadable file")
	}
}

func TestDropSilent(t *testing.T) {
	const rate = 16000
	// [0,1s] loud, [1,2s] near-silent, [2,3s] loud.
	wav := writeWAV(t, tone(rate, 5000), tone(rate, 50), tone(rate, 5000))
	segs := []seg{
		{0, time.Second, "loud one"},
		{time.Second, 2 * time.Second, "phantom"},
		{2 * time.Second, 3 * time.Second, "loud two"},
	}
	kept := dropSilent(wav, segs)
	if len(kept) != 2 {
		t.Fatalf("kept %d segments, want 2: %+v", len(kept), kept)
	}
	if kept[0].text != "loud one" || kept[1].text != "loud two" {
		t.Errorf("wrong segments kept: %+v", kept)
	}
}

func TestDropSilentKeepsZeroLengthSegment(t *testing.T) {
	const rate = 16000
	wav := writeWAV(t, tone(rate, 5000)) // 1s of loud audio
	// whisper can emit to == from for short/final words; the segment must still be
	// measured (over a fallback window) and kept, not dropped as silent.
	segs := []seg{{500 * time.Millisecond, 500 * time.Millisecond, "short"}}
	if kept := dropSilent(wav, segs); len(kept) != 1 {
		t.Errorf("zero-length segment over loud audio should be kept, got %d", len(kept))
	}
}

func TestDropSilentTransientKeepsQuietSpeech(t *testing.T) {
	const rate = 16000
	// quiet real speech (RMS ~800) next to a loud transient (RMS ~20000). A
	// max-based threshold (0.15*20000=3000) would drop the 800 speech; the
	// 90th-percentile reference keeps it.
	wav := writeWAV(t, tone(rate, 800), tone(rate, 20000))
	segs := []seg{
		{0, time.Second, "quiet speech"},
		{time.Second, 2 * time.Second, "loud bump"},
	}
	kept := dropSilent(wav, segs)
	if len(kept) != 2 {
		t.Fatalf("kept %d, want 2 (quiet speech must survive a loud transient): %+v", len(kept), kept)
	}
}

func TestDropSilentFailsOpen(t *testing.T) {
	segs := []seg{{0, time.Second, "keep me"}}
	if got := dropSilent("/no/such/file.wav", segs); len(got) != 1 {
		t.Errorf("unreadable WAV should keep all segments, got %d", len(got))
	}
}

func TestWindowRMS(t *testing.T) {
	const rate = 16000
	samples := tone(rate, 1000) // constant |amp| 1000 → RMS 1000
	if got := windowRMS(samples, rate, 0, time.Second); got < 990 || got > 1010 {
		t.Errorf("RMS = %.1f, want ~1000", got)
	}
	if got := windowRMS(samples, rate, 2*time.Second, 3*time.Second); got != 0 {
		t.Errorf("out-of-range window RMS = %.1f, want 0", got)
	}
}
