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

func TestSanitizeProject(t *testing.T) {
	cases := map[string]string{
		"Bora":          "bora",
		"Meu Projeto":   "meu-projeto",
		"../etc":        "etc",
		"a/b/c":         "a-b-c",
		"  Bonavia  ":   "bonavia",
	}
	for in, want := range cases {
		if got := sanitizeProject(in); got != want {
			t.Errorf("sanitizeProject(%q) = %q, want %q", in, got, want)
		}
	}
}
