// Package model defines MeetMD's core domain types shared across the bridge.
package model

import (
	"regexp"
	"strings"
	"time"
)

// Platform identifies where a meeting happened.
type Platform string

const (
	PlatformGoogleMeet Platform = "google-meet"
	PlatformManual     Platform = "manual"
)

// Label returns a human-friendly platform name for display in the .md output.
func (p Platform) Label() string {
	switch p {
	case PlatformGoogleMeet:
		return "Google Meet"
	case PlatformManual:
		return "Manual"
	default:
		return string(p)
	}
}

// Speaker is the minimal diarization label produced from the two capture
// channels (system loopback vs. user mic). Real per-person diarization is a
// post-MVP upgrade — see docs/specs/2026-06-08-architecture.md §3.2.
type Speaker string

const (
	SpeakerYou    Speaker = "Você"
	SpeakerOthers Speaker = "Participantes"
)

// Segment is one contiguous chunk of transcribed speech.
type Segment struct {
	Start   time.Duration // offset from the start of the meeting
	Speaker Speaker
	Text    string
}

// Meeting holds the metadata captured for a single recording session.
type Meeting struct {
	ID           string
	Title        string
	Project      string // optional; routes output to a per-project folder
	Platform     Platform
	Participants []string
	StartedAt    time.Time
	EndedAt      time.Time
	// MicMissing marks a meeting whose mic channel captured nothing. The meeting is
	// still saved (the participants' audio is the meeting), but the user must be
	// told in the output — a silently missing voice is only discovered days later.
	MicMissing bool
	// TranscriptSuspect marks a meeting whose transcript accounts for only a
	// fraction of what was recorded, so audio was lost somewhere in the pipeline.
	// The partial transcript is still saved and the raw audio kept, but a
	// half-empty transcript reads exactly like a quiet meeting unless we say so.
	TranscriptSuspect bool
}

// DurationMin returns the meeting length in whole minutes (0 if not ended).
func (m Meeting) DurationMin() int {
	if m.EndedAt.IsZero() || m.EndedAt.Before(m.StartedAt) {
		return 0
	}
	return int(m.EndedAt.Sub(m.StartedAt).Minutes())
}

var (
	nonAlnum    = regexp.MustCompile(`[^a-z0-9]+`)
	accentPairs = strings.NewReplacer(
		"á", "a", "à", "a", "ã", "a", "â", "a", "ä", "a",
		"é", "e", "è", "e", "ê", "e", "ë", "e",
		"í", "i", "ì", "i", "î", "i", "ï", "i",
		"ó", "o", "ò", "o", "õ", "o", "ô", "o", "ö", "o",
		"ú", "u", "ù", "u", "û", "u", "ü", "u",
		"ç", "c", "ñ", "n",
	)
)

const defaultSlug = "reuniao"

// Slug returns a filesystem-safe, accent-free version of the title.
func (m Meeting) Slug() string {
	s := accentPairs.Replace(strings.ToLower(strings.TrimSpace(m.Title)))
	s = nonAlnum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		return defaultSlug
	}
	return s
}

// DirName returns the per-meeting directory name: YYYY-MM-DD-hhmm-slug.
func (m Meeting) DirName() string {
	return m.StartedAt.Format("2006-01-02-1504") + "-" + m.Slug()
}
