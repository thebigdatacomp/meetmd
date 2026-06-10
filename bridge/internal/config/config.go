// Package config loads MeetMD bridge configuration from ~/.meetmd/config.yaml,
// applying sensible defaults. See docs/specs/2026-06-08-architecture.md §6.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	defaultPort     = 8765
	defaultLanguage = "auto"           // detect per recording; pin to "pt"/"en"/... to force
	defaultEngine   = "local"          // local | api
	defaultModel    = "ggml-small.bin" // alinhado ao modelo bundlado no .app
	defaultVADModel = "ggml-silero-v5.1.2.bin"
	defaultInterval = 3      // seconds, auto-detect poll
	defaultMode     = "ask"  // auto-detect: prompt before recording
	defaultUILang   = "auto" // UI + .md output language: follow the OS

	configDirName  = ".meetmd"
	configFileName = "config.yaml"

	// Recordings live under <home>/.meetmd/recordings/{meetings,notes}.
	recordingsDir = "recordings"
	meetingsDir   = "meetings"
	notesDir      = "notes"

	// Supported UI languages. Anything else (incl. "auto") resolves via the OS,
	// falling back to English.
	LangPT = "pt"
	LangEN = "en"
)

// Config is the bridge's runtime configuration.
type Config struct {
	// RecordingsRoot is the single base folder for all output; meetings and notes
	// live in fixed subfolders under it (see MeetingsRoot/NotesRoot), so they can
	// never diverge and the UI can open one folder showing both.
	RecordingsRoot string `yaml:"recordings_root"`
	// LegacyOutputRoot migrates pre-recordings configs (output_root pointed at the
	// meetings dir): RecordingsRoot becomes its parent. Never re-persisted.
	LegacyOutputRoot string     `yaml:"output_root,omitempty"`
	Port             int        `yaml:"port"`
	Language         string     `yaml:"language"`    // whisper transcription language (auto|pt|es|en|...)
	UILanguage       string     `yaml:"ui_language"` // UI + .md output language (auto|pt|en)
	Whisper          Whisper    `yaml:"whisper"`
	Audio            Audio      `yaml:"audio"`
	AutoDetect       AutoDetect `yaml:"auto_detect"`
}

// MeetingsRoot is where meeting recordings are written (recordings/meetings).
func (c Config) MeetingsRoot() string { return filepath.Join(c.RecordingsRoot, meetingsDir) }

// NotesRoot is where quick voice notes are written (recordings/notes).
func (c Config) NotesRoot() string { return filepath.Join(c.RecordingsRoot, notesDir) }

// AutoDetect configures browser meeting auto-detection (macOS/Safari).
type AutoDetect struct {
	Enabled         bool   `yaml:"enabled"`
	Mode            string `yaml:"mode"`             // "ask" (prompt) | "auto" (record automatically)
	Project         string `yaml:"project"`          // project tag for auto-detected recordings
	IntervalSeconds int    `yaml:"interval_seconds"` // poll interval
}

// Whisper configures the transcription engine.
type Whisper struct {
	Engine    string `yaml:"engine"`     // local | api
	BinPath   string `yaml:"bin_path"`   // whisper CLI path (empty = look up in PATH)
	ModelPath string `yaml:"model_path"` // path to ggml model for local engine
	VADModel  string `yaml:"vad_model"`  // optional ggml VAD model (enables silence skipping)
}

// Audio configures capture behaviour.
type Audio struct {
	CaptureMic        bool   `yaml:"capture_mic"`          // capture the user mic as a separate channel
	DeleteWavOnFinish bool   `yaml:"delete_wav_on_finish"` // remove temp WAV after transcription
	MacHelperPath     string `yaml:"mac_helper_path"`      // path to system-audio-recorder (empty = look up in PATH)
}

// Default returns a Config with all defaults applied (no config file).
func Default() Config {
	return Config{
		RecordingsRoot: defaultRecordingsRoot(),
		Port:           defaultPort,
		Language:       defaultLanguage,
		UILanguage:     defaultUILang,
		Whisper:        Whisper{Engine: defaultEngine, ModelPath: defaultModelPath(), VADModel: defaultVADModelPath()},
		Audio:          Audio{CaptureMic: true, DeleteWavOnFinish: true},
		AutoDetect:     AutoDetect{Enabled: true, Mode: defaultMode, IntervalSeconds: defaultInterval},
	}
}

// Load reads the config file if present and fills gaps with defaults. A missing
// file is not an error — defaults are used.
func Load() (Config, error) {
	cfg := Default()
	path := DefaultPath()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.applyDefaults()
	return cfg, nil
}

// applyDefaults backfills any field left empty by the config file.
func (c *Config) applyDefaults() {
	d := Default()
	if c.RecordingsRoot == "" {
		if c.LegacyOutputRoot != "" {
			c.RecordingsRoot = filepath.Dir(c.LegacyOutputRoot) // migrate pre-recordings layout
		} else {
			c.RecordingsRoot = d.RecordingsRoot
		}
	}
	c.LegacyOutputRoot = "" // never re-persist the deprecated key
	if c.Port == 0 {
		c.Port = d.Port
	}
	if c.Language == "" {
		c.Language = d.Language
	}
	if c.UILanguage == "" {
		c.UILanguage = d.UILanguage
	}
	if c.Whisper.Engine == "" {
		c.Whisper.Engine = d.Whisper.Engine
	}
	if c.Whisper.ModelPath == "" {
		c.Whisper.ModelPath = d.Whisper.ModelPath
	}
	if c.Whisper.VADModel == "" {
		c.Whisper.VADModel = d.Whisper.VADModel
	}
	if c.AutoDetect.IntervalSeconds == 0 {
		c.AutoDetect.IntervalSeconds = d.AutoDetect.IntervalSeconds
	}
	if c.AutoDetect.Mode == "" {
		c.AutoDetect.Mode = d.AutoDetect.Mode
	}
}

// Save writes the config to DefaultPath() as YAML, creating the directory if
// needed. Note: this replaces any hand-written comments in the file.
func Save(cfg Config) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	path := DefaultPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// ResolvedUILang returns the concrete UI/output language ("pt" or "en"),
// resolving "auto"/unknown against the OS language and falling back to English.
func (c Config) ResolvedUILang() string {
	switch strings.ToLower(strings.TrimSpace(c.UILanguage)) {
	case LangPT:
		return LangPT
	case LangEN:
		return LangEN
	default: // auto / empty / unknown
		if strings.HasPrefix(strings.ToLower(osLanguage()), LangPT) {
			return LangPT
		}
		return LangEN
	}
}

// DefaultPath returns the expected config file location.
func DefaultPath() string {
	return filepath.Join(homeDir(), configDirName, configFileName)
}

func defaultRecordingsRoot() string {
	return filepath.Join(homeDir(), configDirName, recordingsDir)
}

func defaultModelPath() string {
	return filepath.Join(homeDir(), configDirName, "models", defaultModel)
}

func defaultVADModelPath() string {
	return filepath.Join(homeDir(), configDirName, "models", defaultVADModel)
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return "."
}
