package transcribe

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"time"
)

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
