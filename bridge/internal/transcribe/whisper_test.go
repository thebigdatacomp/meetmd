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

func TestParseWhisperJSONDropsHallucinations(t *testing.T) {
	data := []byte(`{"transcription":[
		{"offsets":{"from":1000,"to":2000},"text":"Tudo certo do meu lado."},
		{"offsets":{"from":3000,"to":4000},"text":"ლლლლლლლლლლლლლლლლლლ"},
		{"offsets":{"from":5000,"to":6000},"text":"ᄢ ᄢ ᄢ ᄢ ᄢ ᄢ"},
		{"offsets":{"from":7000,"to":8000},"text":"NÖÖÖÖÖÖ"},
		{"offsets":{"from":9000,"to":10000},"text":"Vamos seguir."}
	]}`)

	segs, err := parseWhisperJSON(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2 (3 hallucinations dropped)", len(segs))
	}
	if segs[0].text != "Tudo certo do meu lado." || segs[1].text != "Vamos seguir." {
		t.Errorf("kept wrong segments: %q, %q", segs[0].text, segs[1].text)
	}
}

func TestIsHallucination(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		// glyph spam — dropped
		{"ლლლლლლლლლლლლლლლლლლ", true},
		{"ᄢ ᄢ ᄢ ᄢ ᄢ ᄢ", true},
		{"NÖÖÖÖÖÖ", true},
		{"ὁ ὁ ὁ ὁ ὁ", true},
		// real speech, multiple languages — kept
		{"Olá pessoal, tudo bem?", false},
		{"And that's all from my side.", false},
		{"São Paulo, atenção à informação", false},
		{"こんにちは、世界の皆さん", false},         // Japanese
		{"Привет всем коллегам", false}, // Russian
		{"Καλημέρα σε όλους", false},    // Greek (real, diverse)
		// short utterances — kept (too short to judge)
		{"Yes.", false},
		{"no", false},
		{"2.5 cm.", false},
		// real words with a repeated letter but high diversity — kept
		{"pizza", false},
		{"haha", false},
	}
	for _, c := range cases {
		if got := isHallucination(c.text); got != c.want {
			t.Errorf("isHallucination(%q) = %v, want %v", c.text, got, c.want)
		}
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
