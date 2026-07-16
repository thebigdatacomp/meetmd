package transcribe

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"
)

// dropMuted removes mic segments that start inside a muted range. The helper
// writes these ranges (one "startMs endMs" per line) next to the mic WAV whenever
// the system microphone was muted during the recording (e.g. the keyboard's mic
// key), so audio captured while the user was muted is not transcribed. The ranges
// are in mic-WAV time, the same base as the segment timestamps. A missing or
// empty file means nothing was muted.
func dropMuted(path string, segs []seg) []seg {
	data, err := os.ReadFile(path)
	if err != nil {
		return segs
	}
	type rng struct{ start, end time.Duration }
	var ranges []rng
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		s, err1 := strconv.Atoi(f[0])
		e, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil || e <= s {
			continue
		}
		ranges = append(ranges, rng{time.Duration(s) * time.Millisecond, time.Duration(e) * time.Millisecond})
	}
	if len(ranges) == 0 {
		return segs
	}
	kept := segs[:0]
	for _, sg := range segs {
		muted := false
		for _, r := range ranges {
			if sg.start >= r.start && sg.start < r.end {
				muted = true
				break
			}
		}
		if !muted {
			kept = append(kept, sg)
		}
	}
	return kept
}

// loadPCM16 reads a mono 16-bit PCM WAV (the format the macOS capture helper
// writes) into samples plus its sample rate. Multi-channel input is downmixed.
func loadPCM16(path string) (samples []int16, rate int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("not a WAV file: %s", path)
	}
	var channels, bits int
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(data[pos+4 : pos+8]))
		body := pos + 8
		if body+size > len(data) {
			size = len(data) - body
		}
		switch id {
		case "fmt ":
			if size >= 16 {
				if format := binary.LittleEndian.Uint16(data[body : body+2]); format != 1 {
					return nil, 0, fmt.Errorf("unsupported WAV format %d (want PCM)", format)
				}
				channels = int(binary.LittleEndian.Uint16(data[body+2 : body+4]))
				rate = int(binary.LittleEndian.Uint32(data[body+4 : body+8]))
				bits = int(binary.LittleEndian.Uint16(data[body+14 : body+16]))
			}
		case "data":
			if bits != 16 {
				return nil, 0, fmt.Errorf("unsupported bit depth %d (want 16)", bits)
			}
			n := size / 2
			samples = make([]int16, n)
			for i := 0; i < n; i++ {
				samples[i] = int16(binary.LittleEndian.Uint16(data[body+i*2 : body+i*2+2]))
			}
		}
		pos = body + size + (size & 1) // chunks are word-aligned
	}
	if rate == 0 || samples == nil {
		return nil, 0, fmt.Errorf("missing fmt/data chunk in %s", path)
	}
	if channels > 1 {
		mono := make([]int16, len(samples)/channels)
		for i := range mono {
			sum := 0
			for c := 0; c < channels; c++ {
				sum += int(samples[i*channels+c])
			}
			mono[i] = int16(sum / channels)
		}
		samples = mono
	}
	return samples, rate, nil
}

// windowRMS returns the root-mean-square amplitude of the samples spanning
// [from, to). It is the loudness proxy used to tell speech from near-silence.
func windowRMS(samples []int16, rate int, from, to time.Duration) float64 {
	i0 := int(from.Seconds() * float64(rate))
	i1 := int(to.Seconds() * float64(rate))
	if i0 < 0 {
		i0 = 0
	}
	if i1 > len(samples) {
		i1 = len(samples)
	}
	if i1 <= i0 {
		return 0
	}
	var sum float64
	for _, v := range samples[i0:i1] {
		f := float64(v)
		sum += f * f
	}
	return math.Sqrt(sum / float64(i1-i0))
}

// HasAudio reports whether a WAV holds any non-silent sample.
//
// It exists to tell "the mic captured nothing" from "the user simply didn't
// speak" — a distinction a transcription error cannot make. A mic that broke but
// still emitted buffers writes digital silence (exact zeroes), and whisper
// transcribes that happily into zero segments; a working mic always carries a
// noise floor. Unreadable or empty files count as no audio.
func HasAudio(path string) bool {
	samples, _, err := loadPCM16(path)
	if err != nil {
		return false
	}
	for _, s := range samples {
		if s != 0 {
			return true
		}
	}
	return false
}
