package audio

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// buildWAV writes a RIFF/WAVE file with JUNK + fmt padding chunks (like
// AVAudioFile) and a data chunk holding pcmBytes of audio. When dataSize is 0
// the data/RIFF sizes are left "unfinalized", mimicking a helper that died
// before flushing.
func buildWAV(t *testing.T, pcmBytes int, finalized bool) (path string, want int) {
	t.Helper()
	pcm := make([]byte, pcmBytes)
	for i := range pcm {
		pcm[i] = byte(i)
	}
	var b []byte
	put := func(s string) { b = append(b, s...) }
	put32 := func(v uint32) { b = binary.LittleEndian.AppendUint32(b, v) }

	put("RIFF")
	riffPos := len(b)
	put32(0) // RIFF size — patched below if finalized
	put("WAVE")
	put("JUNK")
	put32(8)
	b = append(b, make([]byte, 8)...) // JUNK body
	put("fmt ")
	put32(16)
	put32(0x00010001) // PCM, mono
	put32(16000)      // sample rate
	put32(32000)      // byte rate
	put32(0x00100002) // block align 2, bits 16
	put("data")
	dataSizePos := len(b)
	put32(0) // data size — patched below if finalized
	b = append(b, pcm...)

	if finalized {
		binary.LittleEndian.PutUint32(b[riffPos:], uint32(len(b)-8))
		binary.LittleEndian.PutUint32(b[dataSizePos:], uint32(pcmBytes))
	}
	path = filepath.Join(t.TempDir(), "rec.wav")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatalf("write wav: %v", err)
	}
	return path, pcmBytes
}

// dataChunkSize reads the size field of the data chunk back from disk.
func dataChunkSize(t *testing.T, path string) uint32 {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for pos := 12; pos+8 <= len(raw); {
		sz := binary.LittleEndian.Uint32(raw[pos+4 : pos+8])
		if string(raw[pos:pos+4]) == "data" {
			return sz
		}
		pos += 8 + int(sz)
		if sz%2 == 1 {
			pos++
		}
	}
	t.Fatal("no data chunk")
	return 0
}

func TestFinalizeWAVRepairsUnfinalizedHeader(t *testing.T) {
	path, want := buildWAV(t, 64000, false) // data/RIFF sizes left 0
	if got := dataChunkSize(t, path); got != 0 {
		t.Fatalf("precondition: data size = %d, want 0 (unfinalized)", got)
	}
	if err := finalizeWAV(path); err != nil {
		t.Fatalf("finalizeWAV: %v", err)
	}
	if got := dataChunkSize(t, path); int(got) != want {
		t.Errorf("data size after repair = %d, want %d", got, want)
	}
	raw, _ := os.ReadFile(path)
	if riff := binary.LittleEndian.Uint32(raw[4:8]); int(riff) != len(raw)-8 {
		t.Errorf("RIFF size = %d, want %d", riff, len(raw)-8)
	}
}

func TestFinalizeWAVNoopWhenAlreadyValid(t *testing.T) {
	path, want := buildWAV(t, 32000, true)
	if err := finalizeWAV(path); err != nil {
		t.Fatalf("finalizeWAV: %v", err)
	}
	if got := dataChunkSize(t, path); int(got) != want {
		t.Errorf("valid header changed: data size = %d, want %d", got, want)
	}
}

func TestFinalizeWAVIgnoresNonWAV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "x.bin")
	os.WriteFile(path, []byte("not a wav file at all, but long enough...................."), 0o644)
	if err := finalizeWAV(path); err != nil {
		t.Errorf("non-WAV should be a no-op, got %v", err)
	}
}
