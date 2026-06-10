package transcribe

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/thebigdatacomp/meetmd/internal/model"
)

// LanguageAuto lets whisper.cpp detect the spoken language per recording.
const LanguageAuto = "auto"

// Silence-filter thresholds for the mic channel (16-bit PCM RMS). A segment is
// kept when its loudness is at least relFactor of the speech level (a high
// percentile, not the max, so a lone loud transient doesn't drop quiet speech)
// AND above a small absolute floor (which catches the case where the user never
// speaks, so there is no speech level to anchor the relative test). These drop
// whisper's hallucinations over near-silence (~10x below real speech) without a
// VAD pass — see the issue on mic timestamp drift. Tuned on a narrow sample;
// adjust if real recordings lose quiet speech or keep noisy hallucinations.
const (
	silenceRelFactor = 0.15
	silenceAbsFloor  = 350.0
	// minRMSWindow is the fallback span when whisper emits a zero/negative-length
	// segment (to <= from, which it does for very short or final words): measuring
	// a real window keeps the segment from being dropped as "silent" by accident.
	minRMSWindow = 300 * time.Millisecond
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

// dropSilent removes segments whose audio is near-silent — whisper's
// hallucinations over a mic that's mostly quiet. This is a loudness heuristic,
// not a speech detector: it reliably drops silence-driven hallucinations but
// cannot tell quiet speech from loud non-speech, so background music or a noisy
// room can still let a hallucination through (the VAD on the system channel
// would not). It fails open: if the WAV can't be read, every segment is kept
// rather than risk discarding real speech.
func dropSilent(wavPath string, segs []seg) []seg {
	if len(segs) == 0 {
		return segs
	}
	samples, rate, err := loadPCM16(wavPath)
	if err != nil {
		log.Printf("transcribe: loudness filter skipped (%v) — keeping all mic segments", err)
		return segs
	}
	if len(samples) == 0 {
		return segs
	}
	rms := make([]float64, len(segs))
	for i, s := range segs {
		end := s.end
		if end <= s.start {
			end = s.start + minRMSWindow
		}
		rms[i] = windowRMS(samples, rate, s.start, end)
	}
	threshold := silenceAbsFloor
	if rel := loudLevel(rms) * silenceRelFactor; rel > threshold {
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

// loudLevel returns the 90th-percentile RMS as the "speech loudness" reference,
// so a single loud transient (cough, mic bump) doesn't inflate the threshold and
// drop genuine quiet speech the way the raw maximum would.
func loudLevel(rms []float64) float64 {
	if len(rms) == 0 {
		return 0
	}
	sorted := append([]float64(nil), rms...)
	sort.Float64s(sorted)
	return sorted[(len(sorted)-1)*9/10]
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
