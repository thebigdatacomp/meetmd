package transcribe

import (
	"testing"
	"time"
)

func TestParseWhisperJSON(t *testing.T) {
	data := []byte(`{"transcription":[
		{"offsets":{"from":4000,"to":8000},"text":" Olá pessoal."},
		{"offsets":{"from":9000,"to":9000},"text":"   "},
		{"offsets":{"from":12000,"to":15000},"text":"Vamos começar."}
	]}`)

	segs, err := parseWhisperJSON(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segs) != 2 { // the whitespace-only segment is dropped
		t.Fatalf("got %d segments, want 2", len(segs))
	}
	if segs[0].start != 4*time.Second || segs[0].end != 8*time.Second || segs[0].text != "Olá pessoal." {
		t.Errorf("seg0 = %v–%v / %q", segs[0].start, segs[0].end, segs[0].text)
	}
	if segs[1].start != 12*time.Second || segs[1].text != "Vamos começar." {
		t.Errorf("seg1 = %v / %q", segs[1].start, segs[1].text)
	}
}

func TestNewFallsBackToStub(t *testing.T) {
	tr, note := New(Options{Engine: "api"}) // non-local engine
	if _, ok := tr.(Stub); !ok {
		t.Errorf("non-local engine should fall back to Stub, got %T", tr)
	}
	if note == "" {
		t.Errorf("expected a note explaining the fallback")
	}
}
