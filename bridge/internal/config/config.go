// Package config loads MeetMD bridge configuration from ~/.meetmd/config.yaml,
// applying sensible defaults. See docs/specs/2026-06-08-architecture.md §6.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const (
	defaultPort     = 8765
	defaultLanguage = "auto"  // detect per recording; pin to "pt"/"en"/... to force
	defaultEngine   = "local" // local | api
	defaultModel    = "ggml-base.bin"
	defaultVADModel = "ggml-silero-v5.1.2.bin"
	defaultInterval = 3     // seconds, auto-detect poll
	defaultMode     = "ask" // auto-detect: prompt before recording

	configDirName  = ".meetmd"
	configFileName = "config.yaml"
)

// Config is the bridge's runtime configuration.
type Config struct {
	OutputRoot string     `yaml:"output_root"`
	Port       int        `yaml:"port"`
	Language   string     `yaml:"language"`
	Whisper    Whisper    `yaml:"whisper"`
	Audio      Audio      `yaml:"audio"`
	AutoDetect AutoDetect `yaml:"auto_detect"`
}

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
		OutputRoot: defaultOutputRoot(),
		Port:       defaultPort,
		Language:   defaultLanguage,
		Whisper:    Whisper{Engine: defaultEngine, ModelPath: defaultModelPath(), VADModel: defaultVADModelPath()},
		Audio:      Audio{CaptureMic: true, DeleteWavOnFinish: true},
		AutoDetect: AutoDetect{Enabled: true, Mode: defaultMode, IntervalSeconds: defaultInterval},
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
	if c.OutputRoot == "" {
		c.OutputRoot = d.OutputRoot
	}
	if c.Port == 0 {
		c.Port = d.Port
	}
	if c.Language == "" {
		c.Language = d.Language
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

// DefaultPath returns the expected config file location.
func DefaultPath() string {
	return filepath.Join(homeDir(), configDirName, configFileName)
}

func defaultOutputRoot() string {
	return filepath.Join(homeDir(), configDirName, "meetings")
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
