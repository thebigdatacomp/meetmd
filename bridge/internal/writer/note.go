package writer

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/thebigdatacomp/meetmd/internal/model"
)

// FileNoteSuffix is appended to a note's timestamp to form its file name.
const FileNoteSuffix = "-note.md"

// WriteNote renders a quick voice note as a single lean Markdown file in
// inboxRoot — no per-note folder, no diarization, no INDEX. The note carries
// just a timestamp and the transcribed text, ready for Claude to pick up.
func WriteNote(inboxRoot string, note model.Meeting, segments []model.Segment, lang string) (Result, error) {
	if err := os.MkdirAll(inboxRoot, dirPerm); err != nil {
		return Result{}, fmt.Errorf("create inbox dir: %w", err)
	}
	t := textsFor(lang)
	name := note.StartedAt.Format("2006-01-02-1504") + FileNoteSuffix
	path := filepath.Join(inboxRoot, name)
	if err := os.WriteFile(path, []byte(renderNote(note, segments, t)), 0o644); err != nil {
		return Result{}, fmt.Errorf("write %s: %w", name, err)
	}
	return Result{SessionDir: inboxRoot, Files: []string{name}}, nil
}

func renderNote(note model.Meeting, segments []model.Segment, t texts) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", note.ID)
	fmt.Fprintf(&b, "date: %s\n", note.StartedAt.Format("2006-01-02"))
	fmt.Fprintf(&b, "time: %q\n", note.StartedAt.Format("15:04"))
	fmt.Fprintf(&b, "source: %s\n", source)
	b.WriteString("kind: note\n")
	b.WriteString("---\n\n")

	fmt.Fprintf(&b, "# %s — %s\n\n", t.noteTitle, note.StartedAt.Format("2006-01-02 15:04"))
	if text := noteText(segments); text != "" {
		b.WriteString(text)
		b.WriteString("\n")
	} else {
		b.WriteString(t.noSpeech)
	}
	return b.String()
}

// noteText joins the transcribed segments into a single flowing paragraph,
// dropping the speaker labels (a note is just you).
func noteText(segments []model.Segment) string {
	parts := make([]string, 0, len(segments))
	for _, s := range segments {
		if txt := strings.TrimSpace(s.Text); txt != "" {
			parts = append(parts, txt)
		}
	}
	return strings.Join(parts, " ")
}
