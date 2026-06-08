package transcribe

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/thebigdatacomp/meetmd/internal/model"
)

// LanguageAuto lets whisper.cpp detect the spoken language per recording.
const LanguageAuto = "auto"

// Whisper transcribes audio with the local whisper.cpp CLI (whisper-cli).
type Whisper struct {
	BinPath   string
	ModelPath string
	Language  string // ISO code (e.g. "pt", "en") or "auto" to detect
	VADModel  string // optional ggml VAD model; enables silence skipping when present
}

// Transcribe runs whisper.cpp over wavPath and parses its JSON output into
// timestamped segments.
func (w Whisper) Transcribe(ctx context.Context, wavPath string) ([]model.Segment, error) {
	lang := w.Language
	if lang == "" {
		lang = LanguageAuto
	}
	outBase := wavPath + ".whisper"
	jsonPath := outBase + ".json"
	defer os.Remove(jsonPath)

	args := []string{
		"-m", w.ModelPath,
		"-f", wavPath,
		"-l", lang,
		"-mc", "0", // no context carryover → avoids repetition-loop hallucinations
		"-sns",     // suppress non-speech tokens ([Música], (speaking...), etc.)
		"-oj",        // emit JSON with timestamps
		"-of", outBase, // output file base (whisper appends .json)
	}
	// VAD (when a model is available) skips non-speech audio, which kills the
	// hallucinations whisper otherwise produces on near-silent channels (e.g. the
	// mic when you're mostly listening) and improves language auto-detection.
	if w.VADModel != "" {
		if _, err := os.Stat(w.VADModel); err == nil {
			args = append(args, "--vad", "-vm", w.VADModel)
		}
	}

	cmd := exec.CommandContext(ctx, w.BinPath, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("whisper: %w: %s", err, strings.TrimSpace(string(out)))
	}

	data, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, fmt.Errorf("read whisper output: %w", err)
	}
	return parseWhisperJSON(data)
}

// whisperOutput mirrors the subset of whisper.cpp's JSON we consume.
type whisperOutput struct {
	Transcription []struct {
		Offsets struct {
			From int `json:"from"` // milliseconds from start
		} `json:"offsets"`
		Text string `json:"text"`
	} `json:"transcription"`
}

func parseWhisperJSON(data []byte) ([]model.Segment, error) {
	var out whisperOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse whisper json: %w", err)
	}
	segs := make([]model.Segment, 0, len(out.Transcription))
	for _, t := range out.Transcription {
		text := strings.TrimSpace(t.Text)
		if text == "" {
			continue
		}
		// Speaker is left unset here; the caller labels each channel
		// (system = participants, mic = you) when merging.
		segs = append(segs, model.Segment{
			Start: time.Duration(t.Offsets.From) * time.Millisecond,
			Text:  text,
		})
	}
	return segs, nil
}
