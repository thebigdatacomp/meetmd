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
func Write(outputRoot string, m model.Meeting, segments []model.Segment) (Result, error) {
	dir := filepath.Join(outputRoot, m.DirName())
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return Result{}, fmt.Errorf("create session dir: %w", err)
	}

	files := map[string]string{
		FileMeeting:    renderMeeting(m),
		FileTranscript: renderTranscript(m, segments),
		FileSummary:    renderSummary(m),
		FileActions:    renderActions(m),
	}
	written := make([]string, 0, len(files))
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return Result{}, fmt.Errorf("write %s: %w", name, err)
		}
		written = append(written, name)
	}
	sort.Strings(written)

	if err := refreshIndex(outputRoot, m); err != nil {
		return Result{}, fmt.Errorf("refresh index: %w", err)
	}
	return Result{SessionDir: dir, Files: written}, nil
}

// --- per-file renderers -----------------------------------------------------

func renderMeeting(m model.Meeting) string {
	var b strings.Builder
	b.WriteString(frontmatter(m, "", statusRaw))
	fmt.Fprintf(&b, "# %s\n\n", titleOrFallback(m))
	fmt.Fprintf(&b, "> Reunião capturada por MeetMD em %s (%d min).\n\n",
		m.StartedAt.Format("2006-01-02 15:04"), m.DurationMin())
	b.WriteString("## Arquivos\n")
	fmt.Fprintf(&b, "- [Transcrição completa](%s)\n", FileTranscript)
	fmt.Fprintf(&b, "- [Resumo](%s) — _a preencher_\n", FileSummary)
	fmt.Fprintf(&b, "- [Ações](%s) — _a preencher_\n\n", FileActions)
	b.WriteString("## Participantes\n")
	if len(m.Participants) == 0 {
		b.WriteString("- _(não capturados)_\n")
	}
	for _, p := range m.Participants {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	return b.String()
}

func renderTranscript(m model.Meeting, segments []model.Segment) string {
	var b strings.Builder
	b.WriteString(frontmatter(m, "transcript", ""))
	fmt.Fprintf(&b, "# Transcrição — %s\n\n", titleOrFallback(m))
	if len(segments) == 0 {
		b.WriteString("_(transcrição vazia — captura/transcrição ainda não implementada)_\n")
		return b.String()
	}
	for _, s := range segments {
		fmt.Fprintf(&b, "[%s] %s: %s\n", clock(s.Start), s.Speaker, strings.TrimSpace(s.Text))
	}
	return b.String()
}

func renderSummary(m model.Meeting) string {
	var b strings.Builder
	b.WriteString(frontmatter(m, "summary", statusEmpty))
	fmt.Fprintf(&b, "# Resumo — %s\n\n", titleOrFallback(m))
	b.WriteString("<!-- MeetMD: preencha a partir de transcript.md. Remova este comentário ao concluir. -->\n\n")
	b.WriteString("## TL;DR\n_(2-3 frases)_\n\n")
	b.WriteString("## Tópicos discutidos\n- \n\n")
	b.WriteString("## Decisões\n- \n\n")
	b.WriteString("## Pontos em aberto\n- \n")
	return b.String()
}

func renderActions(m model.Meeting) string {
	var b strings.Builder
	b.WriteString(frontmatter(m, "actions", statusEmpty))
	fmt.Fprintf(&b, "# Ações — %s\n\n", titleOrFallback(m))
	b.WriteString("<!-- MeetMD: extraia itens de ação de transcript.md. Um por linha. -->\n\n")
	b.WriteString("| # | Ação | Responsável | Prazo | Status |\n")
	b.WriteString("|---|------|-------------|-------|--------|\n")
	b.WriteString("|   |      |             |       | aberto |\n")
	return b.String()
}

// --- frontmatter ------------------------------------------------------------

// frontmatter builds the shared YAML block. kind/status are omitted when empty.
func frontmatter(m model.Meeting, kind, status string) string {
	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "id: %s\n", m.ID)
	fmt.Fprintf(&b, "title: %s\n", titleOrFallback(m))
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
func refreshIndex(outputRoot string, m model.Meeting) error {
	entries, err := loadIndex(outputRoot)
	if err != nil {
		return err
	}
	entry := indexEntry{
		ID:          m.ID,
		Title:       titleOrFallback(m),
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
	return os.WriteFile(filepath.Join(outputRoot, FileIndex), []byte(renderIndex(entries)), 0o644)
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

func renderIndex(entries []indexEntry) string {
	var b strings.Builder
	b.WriteString("---\nsource: " + source + "\nkind: index\n")
	fmt.Fprintf(&b, "updated: %s\n---\n\n", time.Now().Format("2006-01-02"))
	b.WriteString("# Reuniões — MeetMD\n\n")
	b.WriteString("| Data | Reunião | Duração | Plataforma | Status |\n")
	b.WriteString("|------|---------|---------|------------|--------|\n")
	for _, e := range entries {
		link := fmt.Sprintf("[%s](%s/%s)", e.Title, e.Dir, FileMeeting)
		fmt.Fprintf(&b, "| %s %s | %s | %d min | %s | %s |\n",
			e.Date, e.Start, link, e.DurationMin, e.Platform, e.Status)
	}
	return b.String()
}

// --- helpers ----------------------------------------------------------------

const titleFallback = "Reunião sem título"

func titleOrFallback(m model.Meeting) string {
	if strings.TrimSpace(m.Title) == "" {
		return titleFallback
	}
	return m.Title
}

// clock formats a duration as hh:mm:ss.
func clock(d time.Duration) string {
	total := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, (total%3600)/60, total%60)
}
