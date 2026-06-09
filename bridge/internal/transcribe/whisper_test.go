package transcribe

import (
	"testing"
	"time"
)

func TestParseWhisperJSON(t *testing.T) {
	data := []byte(`{"transcription":[
		{"offsets":{"from":4000},"text":" Olá pessoal."},
		{"offsets":{"from":9000},"text":"   "},
		{"offsets":{"from":12000},"text":"Vamos começar."}
	]}`)

	segs, err := parseWhisperJSON(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segs) != 2 { // the whitespace-only segment is dropped
		t.Fatalf("got %d segments, want 2", len(segs))
	}
	if segs[0].Start != 4*time.Second || segs[0].Text != "Olá pessoal." {
		t.Errorf("seg0 = %v / %q", segs[0].Start, segs[0].Text)
	}
	if segs[1].Start != 12*time.Second || segs[1].Text != "Vamos começar." {
		t.Errorf("seg1 = %v / %q", segs[1].Start, segs[1].Text)
	}
	if segs[0].Speaker != "" {
		t.Errorf("speaker should be set by the caller, not the parser")
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
