package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mkRecovery creates recovery/<name> with a WAV of the given size and mod time.
func mkRecovery(t *testing.T, root, name string, size int64, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(root, "recovery", name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	wav := filepath.Join(dir, name+".wav")
	if err := os.WriteFile(wav, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	for _, p := range []string{wav, dir} {
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestPruneRecoveryDropsAgedOut(t *testing.T) {
	root := t.TempDir()
	old := mkRecovery(t, root, "old", 1<<20, 10*24*time.Hour)
	fresh := mkRecovery(t, root, "fresh", 1<<20, 1*time.Hour)

	pruneRecovery(root, 7*24*time.Hour, 0) // age bound only

	if exists(old) {
		t.Error("a 10-day-old recording should be pruned under a 7-day limit")
	}
	if !exists(fresh) {
		t.Error("a 1-hour-old recording must be kept")
	}
}

func TestPruneRecoveryEnforcesSizeBudgetOldestFirst(t *testing.T) {
	root := t.TempDir()
	// Three 1 GB recordings, all within the age limit, under a 2 GB budget.
	oldest := mkRecovery(t, root, "d1-oldest", 1<<30, 72*time.Hour)
	mid := mkRecovery(t, root, "d2-mid", 1<<30, 48*time.Hour)
	newest := mkRecovery(t, root, "d3-newest", 1<<30, 24*time.Hour)

	pruneRecovery(root, 30*24*time.Hour, 2<<30)

	if exists(oldest) {
		t.Error("oldest recording should be dropped first when over budget")
	}
	if !exists(mid) || !exists(newest) {
		t.Error("the two newest recordings fit the 2 GB budget and must be kept")
	}
}

// The failure mode to avoid: disabling retention wholesale on the machine that
// most needs it. Age or size == 0 disables only that one axis.
func TestPruneRecoveryZeroBoundDisablesOnlyThatAxis(t *testing.T) {
	root := t.TempDir()
	ancient := mkRecovery(t, root, "ancient", 1<<20, 400*24*time.Hour)

	pruneRecovery(root, 0, 10<<30) // no age limit, generous size limit
	if !exists(ancient) {
		t.Error("a zero age bound must not delete by age")
	}

	pruneRecovery(root, 0, 0) // both disabled
	if !exists(ancient) {
		t.Error("both bounds disabled must keep everything")
	}
}

func TestPruneRecoveryNoFolderIsNoop(t *testing.T) {
	// Must not panic or create anything when nothing has ever been preserved.
	pruneRecovery(t.TempDir(), 7*24*time.Hour, 2<<30)
}
