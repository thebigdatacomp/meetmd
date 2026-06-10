package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/thebigdatacomp/meetmd/internal/audio"
	"github.com/thebigdatacomp/meetmd/internal/config"
	"github.com/thebigdatacomp/meetmd/internal/transcribe"
)

func newTestManager(root string) *Manager {
	return New(config.NewStore(config.Config{OutputRoot: root}), audio.Stub{},
		func(config.Config) transcribe.Transcriber { return transcribe.Stub{} })
}

func TestStopRoutesOutputByProject(t *testing.T) {
	root := t.TempDir()
	mgr := newTestManager(root)
	ctx := context.Background()

	if _, err := mgr.Start(ctx, StartRequest{Title: "Daily", Project: "Bora"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	res, err := mgr.Stop(ctx, "")
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// "Bora" is sanitized to "bora" and becomes a subfolder of the root.
	wantDir := filepath.Join(root, "bora")
	if filepath.Dir(res.SessionDir) != wantDir {
		t.Errorf("session dir = %s, want under %s", res.SessionDir, wantDir)
	}
	if _, err := os.Stat(filepath.Join(wantDir, "INDEX.md")); err != nil {
		t.Errorf("missing per-project INDEX.md: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(res.SessionDir, "meeting.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "project: bora") {
		t.Errorf("meeting.md missing project frontmatter:\n%s", body)
	}
}

func TestStopNoProjectUsesRoot(t *testing.T) {
	root := t.TempDir()
	mgr := newTestManager(root)
	ctx := context.Background()

	if _, err := mgr.Start(ctx, StartRequest{Title: "Solo"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	res, err := mgr.Stop(ctx, "")
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if filepath.Dir(res.SessionDir) != root {
		t.Errorf("session dir parent = %s, want %s", filepath.Dir(res.SessionDir), root)
	}
}

func TestStartNoteWritesToNotes(t *testing.T) {
	root := t.TempDir()
	notes := filepath.Join(root, "notes")
	mgr := New(config.NewStore(config.Config{OutputRoot: root, NotesRoot: notes}),
		audio.Stub{}, func(config.Config) transcribe.Transcriber { return transcribe.Stub{} })
	ctx := context.Background()

	note, err := mgr.StartNote(ctx)
	if err != nil {
		t.Fatalf("StartNote: %v", err)
	}
	if mgr.Status().Kind != KindNote {
		t.Errorf("status kind = %q, want note", mgr.Status().Kind)
	}
	res, err := mgr.Stop(ctx, note.ID)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	// The note lands in the notes folder (not meetings/), as a single .md file.
	if res.SessionDir != notes {
		t.Errorf("note dir = %s, want %s", res.SessionDir, notes)
	}
	if len(res.Files) != 1 || !strings.HasSuffix(res.Files[0], "-note.md") {
		t.Fatalf("unexpected note files: %v", res.Files)
	}
	body, err := os.ReadFile(filepath.Join(notes, res.Files[0]))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "kind: note") {
		t.Errorf("note missing kind frontmatter:\n%s", body)
	}
}

func TestCommonDir(t *testing.T) {
	cases := []struct{ a, b, want string }{
		{"/Users/rob/.meetmd/recordings/meetings", "/Users/rob/.meetmd/recordings/notes", "/Users/rob/.meetmd/recordings"},
		{"/Users/rob/.meetmd/recordings/meetings", "/Users/rob/.meetmd/recordings/meetings", "/Users/rob/.meetmd/recordings/meetings"},
		{"/var/data", "", "/var/data"},
		{"", "/var/data", "/var/data"},
		{"/a/b", "/c/d", "/"},
	}
	for _, c := range cases {
		if got := commonDir(c.a, c.b); got != c.want {
			t.Errorf("commonDir(%q, %q) = %q, want %q", c.a, c.b, got, c.want)
		}
	}
}

func TestSanitizeProject(t *testing.T) {
	cases := map[string]string{
		"Bora":        "bora",
		"Meu Projeto": "meu-projeto",
		"../etc":      "etc",
		"a/b/c":       "a-b-c",
		"  Bonavia  ": "bonavia",
	}
	for in, want := range cases {
		if got := sanitizeProject(in); got != want {
			t.Errorf("sanitizeProject(%q) = %q, want %q", in, got, want)
		}
	}
}
