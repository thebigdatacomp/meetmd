// Package writer renders a finished meeting into the structured Markdown
// layout consumed by Claude. See docs/specs/2026-06-08-output-format.md.
package writer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/thebigdatacomp/meetmd/internal/model"
)

// Output file names (one fact, one constant — never inline these literals).
const (
	FileMeeting    = "meeting.md"
	FileTranscript = "transcript.md"
	FileSummary    = "summary.md"
	FileActions    = "actions.md"
	FileIndex      = "INDEX.md"

	// indexState is the machine-readable source of truth for INDEX.md. Keeping
	// it separate avoids fragile re-parsing of the rendered Markdown.
	indexState = ".meetmd-index.json"

	source = "meetmd"

	statusRaw   = "raw"   // meeting.md: summary not generated yet
	statusEmpty = "empty" // summary.md / actions.md: template not filled yet
)

const dirPerm = 0o755

// Result reports what Write produced.
type Result struct {
	SessionDir string   // absolute path to the meeting directory
	Files      []string // file names written inside SessionDir
}

// Write renders a meeting and its transcript into outputRoot and refreshes
// INDEX.md. It is idempotent: writing the same meeting ID again overwrites its
// files and updates (not duplicates) its index row.
func Write(outputRoot string, m model.Meeting, segments []model.Segment, lang string) (Result, error) {
	dir := filepath.Join(outputRoot, m.DirName())
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return Result{}, fmt.Errorf("create session dir: %w", err)
	}

	t := textsFor(lang)
	files := map[string]string{
		FileMeeting:    renderMeeting(m, t),
		FileTranscript: renderTranscript(m, segments, t),
		FileSummary:    renderSummary(m, t),
		FileActions:    renderActions(m, t),
	}
	written := make([]string, 0, len(files))
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return Result{}, fmt.Errorf("write %s: %w", name, err)
		}
		written = append(written, name)
	}
	sort.Strings(written)

	if err := refreshIndex(outputRoot, m, t); err != nil {
		return Result{}, fmt.Errorf("refresh index: %w", err)
	}
	return Result{SessionDir: dir, Files: written}, nil
}

// --- per-file renderers -----------------------------------------------------

func renderMeeting(m model.Meeting, t texts) string {
	var b strings.Builder
	b.WriteString(frontmatter(m, "", statusRaw, t))
	fmt.Fprintf(&b, "# %s\n\n", titleOrFallback(m, t))
	fmt.Fprintf(&b, t.capturedBy, m.StartedAt.Format("2006-01-02 15:04"), m.DurationMin())
	b.WriteString(t.filesHeading)
	fmt.Fprintf(&b, t.linkFull, FileTranscript)
	fmt.Fprintf(&b, t.linkSummary, FileSummary)
	fmt.Fprintf(&b, t.linkActions, FileActions)
	b.WriteString(t.participants)
	if len(m.Participants) == 0 {
		b.WriteString(t.notCaptured)
	}
	for _, p := range m.Participants {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	return b.String()
}

func renderTranscript(m model.Meeting, segments []model.Segment, t texts) string {
	var b strings.Builder
	b.WriteString(frontmatter(m, "transcript", "", t))
	fmt.Fprintf(&b, t.transcriptTitle, titleOrFallback(m, t))
	if len(segments) == 0 {
		b.WriteString(t.noSpeech)
		return b.String()
	}
	for _, s := range segments {
		fmt.Fprintf(&b, "[%s] %s: %s\n", clock(s.Start), speakerLabel(s.Speaker, t), strings.TrimSpace(s.Text))
	}
	return b.String()
}

func renderSummary(m model.Meeting, t texts) string {
	var b strings.Builder
	b.WriteString(frontmatter(m, "summary", statusEmpty, t))
	fmt.Fprintf(&b, t.summaryTitle, titleOrFallback(m, t))
	b.WriteString(t.summaryComment)
	b.WriteString(t.tldr)
	b.WriteString(t.topics)
	b.WriteString(t.decisions)
	b.WriteString(t.openPoints)
	return b.String()
}

func renderActions(m model.Meeting, t texts) string {
	var b strings.Builder
	b.WriteString(frontmatter(m, "actions", statusEmpty, t))
	fmt.Fprintf(&b, t.actionsTitle, titleOrFallback(m, t))
	b.WriteString(t.actionsComment)
	b.WriteString(t.actionsTableHead)
	b.WriteString(t.actionsTableRow)
	return b.String()
}

// --- frontmatter ------------------------------------------------------------

// frontmatter builds the shared YAML block. kind/status are omitted when empty.
func frontmatter(m model.Meeting, kind, status string, t texts) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", m.ID)
	fmt.Fprintf(&b, "title: %s\n", titleOrFallback(m, t))
	fmt.Fprintf(&b, "date: %s\n", m.StartedAt.Format("2006-01-02"))
	if kind == "" { // full identity only on meeting.md
		if m.Project != "" {
			fmt.Fprintf(&b, "project: %s\n", m.Project)
		}
		fmt.Fprintf(&b, "start: %q\n", m.StartedAt.Format("15:04"))
		fmt.Fprintf(&b, "end: %q\n", m.EndedAt.Format("15:04"))
		fmt.Fprintf(&b, "duration_min: %d\n", m.DurationMin())
		fmt.Fprintf(&b, "platform: %s\n", m.Platform)
		b.WriteString("participants:\n")
		for _, p := range m.Participants {
			fmt.Fprintf(&b, "  - %s\n", p)
		}
	}
	fmt.Fprintf(&b, "source: %s\n", source)
	if kind != "" {
		fmt.Fprintf(&b, "kind: %s\n", kind)
	}
	if status != "" {
		fmt.Fprintf(&b, "status: %s\n", status)
	}
	b.WriteString("---\n\n")
	return b.String()
}

// --- INDEX.md ---------------------------------------------------------------

type indexEntry struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Date        string `json:"date"`
	Start       string `json:"start"`
	DurationMin int    `json:"duration_min"`
	Platform    string `json:"platform"`
	Dir         string `json:"dir"`
	Status      string `json:"status"`
}

// refreshIndex upserts the meeting into the JSON state and re-renders INDEX.md.
func refreshIndex(outputRoot string, m model.Meeting, t texts) error {
	entries, err := loadIndex(outputRoot)
	if err != nil {
		return err
	}
	entry := indexEntry{
		ID:          m.ID,
		Title:       titleOrFallback(m, t),
		Date:        m.StartedAt.Format("2006-01-02"),
		Start:       m.StartedAt.Format("15:04"),
		DurationMin: m.DurationMin(),
		Platform:    m.Platform.Label(),
		Dir:         m.DirName(),
		Status:      statusRaw,
	}
	replaced := false
	for i := range entries {
		if entries[i].ID == entry.ID {
			entries[i] = entry
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, entry)
	}
	// Most recent first.
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Date+entries[i].Start > entries[j].Date+entries[j].Start
	})

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(outputRoot, indexState), data, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outputRoot, FileIndex), []byte(renderIndex(entries, t)), 0o644)
}

func loadIndex(outputRoot string) ([]indexEntry, error) {
	data, err := os.ReadFile(filepath.Join(outputRoot, indexState))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []indexEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func renderIndex(entries []indexEntry, t texts) string {
	var b strings.Builder
	b.WriteString("---\nsource: " + source + "\nkind: index\n")
	fmt.Fprintf(&b, "updated: %s\n---\n\n", time.Now().Format("2006-01-02"))
	b.WriteString(t.indexTitle)
	b.WriteString(t.indexTableHead)
	for _, e := range entries {
		link := fmt.Sprintf("[%s](%s/%s)", e.Title, e.Dir, FileMeeting)
		fmt.Fprintf(&b, "| %s %s | %s | %d min | %s | %s |\n",
			e.Date, e.Start, link, e.DurationMin, e.Platform, e.Status)
	}
	return b.String()
}

// --- helpers ----------------------------------------------------------------

func titleOrFallback(m model.Meeting, t texts) string {
	if strings.TrimSpace(m.Title) == "" {
		return t.titleFallback
	}
	return m.Title
}

// speakerLabel renders the localized display name for a diarization speaker.
func speakerLabel(s model.Speaker, t texts) string {
	switch s {
	case model.SpeakerYou:
		return t.speakerYou
	case model.SpeakerOthers:
		return t.speakerOthers
	default:
		return string(s)
	}
}

// clock formats a duration as hh:mm:ss.
func clock(d time.Duration) string {
	total := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, (total%3600)/60, total%60)
}
