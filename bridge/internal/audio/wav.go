package audio

import (
	"encoding/binary"
	"fmt"
	"os"
)

// finalizeWAV repairs a WAV whose header was never finalized. The capture helper
// writes a placeholder header and only records the real data/RIFF sizes when
// AVAudioFile flushes on close; if the helper is terminated — or its capture
// stream dies — before that flush, the header reports 0 frames and whisper
// cannot read the file, losing the entire recording even though the audio bytes
// are on disk. This recomputes the data and RIFF sizes from the actual file
// length and writes them, so the audio is always readable however the helper
// exited. It is a no-op when the header is already correct.
func finalizeWAV(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	size := info.Size()
	if size < 44 {
		return nil // too small to hold real audio
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()

	// Read enough of the header to walk past any JUNK/fmt/FLLR padding chunks
	// AVAudioFile writes before the data chunk.
	scan := size
	if scan > 1<<16 {
		scan = 1 << 16
	}
	head := make([]byte, scan)
	if _, err := f.ReadAt(head, 0); err != nil {
		return err
	}
	if string(head[0:4]) != "RIFF" || string(head[8:12]) != "WAVE" {
		return nil // not a WAV we wrote
	}

	for pos := 12; pos+8 <= len(head); {
		id := string(head[pos : pos+4])
		chunkSize := int(binary.LittleEndian.Uint32(head[pos+4 : pos+8]))
		if id == "data" {
			realData := size - (int64(pos) + 8)
			if int64(chunkSize) == realData {
				return nil // already finalized
			}
			var buf [4]byte
			binary.LittleEndian.PutUint32(buf[:], uint32(realData))
			if _, err := f.WriteAt(buf[:], int64(pos)+4); err != nil { // data chunk size
				return err
			}
			binary.LittleEndian.PutUint32(buf[:], uint32(size-8))
			if _, err := f.WriteAt(buf[:], 4); err != nil { // RIFF size
				return err
			}
			return nil
		}
		// Chunks before data have valid sizes; walk past them (word-aligned).
		pos += 8 + chunkSize
		if chunkSize%2 == 1 {
			pos++
		}
	}
	return fmt.Errorf("no data chunk found in %s", path)
}
