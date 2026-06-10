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

// Silence-filter thresholds for the mic channel (16-bit PCM RMS). A segment is
// kept when its loudness is at least relFactor of the loudest segment, and above
// a small absolute floor. This drops whisper's hallucinations over near-silence
// (which sit ~10x below real speech) without a VAD pass — see issue on mic
// timestamp drift. Tunable if real recordings need it.
const (
	silenceRelFactor = 0.15
	silenceAbsFloor  = 200.0
)

// Whisper transcribes audio with the local whisper.cpp CLI (whisper-cli).
type Whisper struct {
	BinPath   string
	ModelPath string
	Language  string // ISO code (e.g. "pt", "en") or "auto" to detect
	VADModel  string // optional ggml VAD model; enables silence skipping when present
	// Voice transcribes a sparse, single-speaker channel (the user's mic): VAD is
	// disabled so timestamps stay on the real timeline, and near-silent segments
	// are dropped afterwards by loudness. VAD would otherwise glue speech across
	// long silences into one mis-timestamped segment.
	Voice bool
}

// seg is one whisper output segment with its real start/end before we discard
// the end (model.Segment keeps only Start).
type seg struct {
	start, end time.Duration
	text       string
}

// Transcribe runs whisper.cpp over wavPath and parses its JSON output into
// timestamped segments.
func (w Whisper) Transcribe(ctx context.Context, wavPath string) ([]model.Segment, error) {
	raw, err := w.run(ctx, wavPath)
	if err != nil {
		return nil, err
	}
	if w.Voice {
		raw = dropSilent(wavPath, raw)
	}
	segs := make([]model.Segment, 0, len(raw))
	for _, r := range raw {
		segs = append(segs, model.Segment{Start: r.start, Text: r.text})
	}
	return segs, nil
}

func (w Whisper) run(ctx context.Context, wavPath string) ([]seg, error) {
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
		"-sns",         // suppress non-speech tokens ([Música], (speaking...), etc.)
		"-oj",          // emit JSON with timestamps
		"-of", outBase, // output file base (whisper appends .json)
	}
	// VAD skips non-speech audio, which kills hallucinations on near-silent
	// channels. We use it on the system channel (continuous speech) but NOT on the
	// mic (Voice): there, VAD compresses the long silences and whisper fuses far
	// apart utterances into one segment stamped at the earlier time. The mic relies
	// on the loudness filter instead.
	if !w.Voice && w.VADModel != "" {
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

// dropSilent removes segments whose audio is near-silent (whisper hallucinations
// over a mic that's mostly quiet). It fails open: if the WAV can't be read, every
// segment is kept rather than risk discarding real speech.
func dropSilent(wavPath string, segs []seg) []seg {
	if len(segs) == 0 {
		return segs
	}
	samples, rate, err := loadPCM16(wavPath)
	if err != nil || len(samples) == 0 {
		return segs
	}
	rms := make([]float64, len(segs))
	var peak float64
	for i, s := range segs {
		rms[i] = windowRMS(samples, rate, s.start, s.end)
		if rms[i] > peak {
			peak = rms[i]
		}
	}
	threshold := silenceAbsFloor
	if rel := peak * silenceRelFactor; rel > threshold {
		threshold = rel
	}
	kept := make([]seg, 0, len(segs))
	for i, s := range segs {
		if rms[i] >= threshold {
			kept = append(kept, s)
		}
	}
	return kept
}

// whisperOutput mirrors the subset of whisper.cpp's JSON we consume.
type whisperOutput struct {
	Transcription []struct {
		Offsets struct {
			From int `json:"from"` // milliseconds from start
			To   int `json:"to"`
		} `json:"offsets"`
		Text string `json:"text"`
	} `json:"transcription"`
}

func parseWhisperJSON(data []byte) ([]seg, error) {
	var out whisperOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse whisper json: %w", err)
	}
	segs := make([]seg, 0, len(out.Transcription))
	for _, t := range out.Transcription {
		text := strings.TrimSpace(t.Text)
		if text == "" {
			continue
		}
		// Speaker is left unset here; the caller labels each channel
		// (system = participants, mic = you) when merging.
		segs = append(segs, seg{
			start: time.Duration(t.Offsets.From) * time.Millisecond,
			end:   time.Duration(t.Offsets.To) * time.Millisecond,
			text:  text,
		})
	}
	return segs, nil
}
