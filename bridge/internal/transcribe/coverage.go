package transcribe

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"time"
)

// Coverage is how MeetMD checks its own work: the share of a recording that came
// back as speech. It exists because every way this pipeline has failed so far
// looked the same from the inside — a stage quietly produced almost nothing and
// the meeting was written as if that were the truth (a VAD that rejected quiet
// audio, a mic that emitted digital silence, a run that stopped early). None of
// those announce themselves, but all of them show up as a long recording with an
// implausibly short transcript, which is a question we can always ask.
//
// It is deliberately a property of the output, not of any one failure mode, so
// it also catches the next failure we have not seen yet.

const (
	// MinPlausibleCoverage is the speech share below which a transcript cannot be
	// trusted to represent its recording. It gates keeping the raw audio and
	// warning the user, so it is set low: a real meeting sits far above it, and
	// erring towards "keep the audio" costs disk instead of a lost meeting.
	MinPlausibleCoverage = 0.05

	// minVADCoverage is the speech share below which a VAD-assisted pass is redone
	// without VAD. It is higher than MinPlausibleCoverage because retrying is
	// cheap and reversible — the better of the two runs wins.
	minVADCoverage = 0.10

	// minAuditedLength is the shortest recording for which coverage means
	// anything: on a short clip a few seconds of speech is a plausible whole.
	minAuditedLength = 2 * time.Minute
)

// speechSeconds is the total time the segments claim to be speech.
func speechSeconds(segs []seg) time.Duration {
	var total time.Duration
	for _, s := range segs {
		if s.end > s.start {
			total += s.end - s.start
		}
	}
	return total
}

// speechCoverage is the share of a recording of length total that segs cover.
// It returns 0 for an unknown length so callers treat it as "cannot judge".
func speechCoverage(segs []seg, total time.Duration) float64 {
	if total <= 0 {
		return 0
	}
	return float64(speechSeconds(segs)) / float64(total)
}

// audioDuration returns a WAV's length by reading only its header chunks, so a
// 45-minute recording costs a few bytes of I/O rather than loading 85MB.
func audioDuration(path string) (time.Duration, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	head := make([]byte, 4096)
	n, err := io.ReadFull(f, head)
	if n == 0 && err != nil {
		return 0, err
	}
	head = head[:n]
	if len(head) < 12 || string(head[0:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		return 0, fmt.Errorf("not a WAV file: %s", path)
	}

	var byteRate uint32
	for pos := 12; pos+8 <= len(head); {
		id := string(head[pos : pos+4])
		size := binary.LittleEndian.Uint32(head[pos+4 : pos+8])
		body := pos + 8
		switch id {
		case "fmt ":
			if body+12 <= len(head) {
				byteRate = binary.LittleEndian.Uint32(head[body+8 : body+12])
			}
		case "data":
			if byteRate == 0 {
				return 0, fmt.Errorf("data chunk before fmt chunk in %s", path)
			}
			// Trust whichever source says the recording is longer. A header is
			// only written correctly when the file is closed cleanly, so a run
			// that was killed mid-write leaves a size of zero or a stale, too
			// small one — and understating the length overstates coverage, which
			// would wave through exactly the broken transcripts this exists to
			// catch. Overstating it only risks keeping audio we could have
			// deleted.
			bytes := int64(size)
			if st, err := f.Stat(); err == nil {
				if onDisk := st.Size() - int64(body); onDisk > bytes {
					bytes = onDisk
				}
			}
			if bytes <= 0 {
				return 0, nil
			}
			return time.Duration(float64(bytes) / float64(byteRate) * float64(time.Second)), nil
		}
		pos = body + int(size) + int(size&1) // chunks are word-aligned
	}
	return 0, fmt.Errorf("missing data chunk in %s", path)
}
