package writer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/thebigdatacomp/meetmd/internal/model"
)

func sampleMeeting() model.Meeting {
	start := time.Date(2026, 6, 8, 14, 30, 0, 0, time.UTC)
	return model.Meeting{
		ID:           "2026-06-08-1430-planejamento-sprint-7",
		Title:        "Planejamento Sprint 7",
		Platform:     model.PlatformGoogleMeet,
		Participants: []string{"Robson Müller", "Alessandro", "Leonardo"},
		StartedAt:    start,
		EndedAt:      start.Add(42 * time.Minute),
	}
}

func TestWriteCreatesAllFiles(t *testing.T) {
	root := t.TempDir()
	m := sampleMeeting()
	segs := []model.Segment{
		{Start: 4 * time.Second, Speaker: model.SpeakerOthers, Text: "Vamos começar pelo board."},
		{Start: 11 * time.Second, Speaker: model.SpeakerYou, Text: "Beleza, a issue 70 fechou."},
	}

	res, err := Write(root, m, segs)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	wantFiles := []string{FileMeeting, FileTranscript, FileSummary, FileActions}
	if len(res.Files) != len(wantFiles) {
		t.Fatalf("got %d files, want %d", len(res.Files), len(wantFiles))
	}
	for _, f := range append(wantFiles, "") {
		if f == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(res.SessionDir, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	// INDEX.md at root.
	if _, err := os.Stat(filepath.Join(root, FileIndex)); err != nil {
		t.Errorf("missing INDEX.md: %v", err)
	}
}

func TestTranscriptContent(t *testing.T) {
	root := t.TempDir()
	m := sampleMeeting()
	segs := []model.Segment{
		{Start: 4 * time.Second, Speaker: model.SpeakerOthers, Text: "Vamos começar."},
	}
	res, err := Write(root, m, segs)
	if err != nil {
		t.Fatal(err)
	}
	body := readFile(t, filepath.Join(res.SessionDir, FileTranscript))
	if !strings.Contains(body, "[00:00:04] Participantes: Vamos começar.") {
		t.Errorf("transcript missing formatted segment, got:\n%s", body)
	}
	if !strings.Contains(body, "kind: transcript") {
		t.Errorf("transcript missing frontmatter kind")
	}
}

func TestIndexIsIdempotent(t *testing.T) {
	root := t.TempDir()
	m := sampleMeeting()
	if _, err := Write(root, m, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(root, m, nil); err != nil { // same meeting again
		t.Fatal(err)
	}
	index := readFile(t, filepath.Join(root, FileIndex))
	if n := strings.Count(index, m.Title); n != 1 {
		t.Errorf("expected meeting once in INDEX, found %d times", n)
	}
}

func TestSlugFallback(t *testing.T) {
	cases := map[string]string{
		"Planejamento Sprint 7": "planejamento-sprint-7",
		"Reunião / Cliente X!":  "reuniao-cliente-x",
		"   ":                   "reuniao",
		"Café com o time":       "cafe-com-o-time",
	}
	for title, want := range cases {
		m := model.Meeting{Title: title}
		if got := m.Slug(); got != want {
			t.Errorf("Slug(%q) = %q, want %q", title, got, want)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
