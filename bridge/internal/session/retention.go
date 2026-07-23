package session

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// bytesPerGB converts the configured gigabyte budget to bytes.
const bytesPerGB = 1 << 30

// recoveryEntry is one preserved recording: a folder under recovery/ holding the
// raw WAVs of a meeting that failed or whose transcript could not be trusted.
type recoveryEntry struct {
	path    string
	modTime time.Time
	size    int64
}

// pruneRecovery keeps the preserved-audio folder within both bounds: nothing
// older than maxAge, and no more than maxBytes in total (oldest deleted first).
//
// Preserved audio exists so a bad transcript can be redone, which makes deleting
// it a real loss — but keeping it forever is the same class of bug this whole
// mechanism was built to catch: something growing quietly until it breaks. So it
// is bounded on both axes and every deletion is logged, never silent.
//
// A non-positive bound disables that axis.
func pruneRecovery(root string, maxAge time.Duration, maxBytes int64) {
	dir := filepath.Join(root, "recovery")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return // no recovery folder yet: nothing to bound
	}

	var kept []recoveryEntry
	var total int64
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		size := dirSize(path)
		if maxAge > 0 && time.Since(info.ModTime()) > maxAge {
			remove(path, size, "older than "+maxAge.String())
			continue
		}
		kept = append(kept, recoveryEntry{path: path, modTime: info.ModTime(), size: size})
		total += size
	}

	if maxBytes <= 0 || total <= maxBytes {
		return
	}
	// Over budget: drop the oldest first, since the newest preserved recording is
	// the one most likely still worth redoing.
	sort.Slice(kept, func(i, j int) bool { return kept[i].modTime.Before(kept[j].modTime) })
	for _, e := range kept {
		if total <= maxBytes {
			return
		}
		remove(e.path, e.size, "recovery folder over its size budget")
		total -= e.size
	}
}

func remove(path string, size int64, why string) {
	if err := os.RemoveAll(path); err != nil {
		log.Printf("recovery: could not remove %s: %v", path, err)
		return
	}
	log.Printf("recovery: removed %s (%d MB, %s)", filepath.Base(path), size/(1<<20), why)
}

// dirSize totals the regular files under path. Unreadable entries count as zero,
// which can only under-estimate — so a folder is never deleted on a bad reading.
func dirSize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // an unreadable entry must not abort the walk
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}
