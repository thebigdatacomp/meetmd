package config

import (
	"path/filepath"
	"testing"
)

func TestApplyDefaultsBackfills(t *testing.T) {
	c := Config{} // everything empty
	c.applyDefaults()

	if c.Port != defaultPort {
		t.Errorf("Port = %d, want %d", c.Port, defaultPort)
	}
	if c.Language != defaultLanguage {
		t.Errorf("Language = %q, want %q", c.Language, defaultLanguage)
	}
	if c.Whisper.Engine != defaultEngine {
		t.Errorf("Engine = %q, want %q", c.Whisper.Engine, defaultEngine)
	}
	if c.Whisper.ModelPath == "" || c.Whisper.VADModel == "" {
		t.Errorf("model/vad paths should be backfilled, got %q / %q", c.Whisper.ModelPath, c.Whisper.VADModel)
	}
	if c.AutoDetect.Mode != defaultMode || c.AutoDetect.IntervalSeconds != defaultInterval {
		t.Errorf("auto-detect defaults not backfilled: %+v", c.AutoDetect)
	}
}

func TestApplyDefaultsKeepsExplicitValues(t *testing.T) {
	c := Config{Port: 9000, Language: "es"}
	c.applyDefaults()
	if c.Port != 9000 || c.Language != "es" {
		t.Errorf("explicit values overwritten: port=%d lang=%q", c.Port, c.Language)
	}
}

func TestStoreGetSet(t *testing.T) {
	s := NewStore(Config{Port: 1})
	if s.Get().Port != 1 {
		t.Fatalf("Get() = %d, want 1", s.Get().Port)
	}
	s.Set(Config{Port: 2})
	if s.Get().Port != 2 {
		t.Errorf("after Set, Get() = %d, want 2", s.Get().Port)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // redirect DefaultPath() to a temp home

	want := Default()
	want.Language = "pt"
	want.Audio.CaptureMic = false
	if err := Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Language != "pt" || got.Audio.CaptureMic != false {
		t.Errorf("round-trip lost values: lang=%q mic=%v", got.Language, got.Audio.CaptureMic)
	}
	if filepath.Base(DefaultPath()) != configFileName {
		t.Errorf("unexpected config path %s", DefaultPath())
	}
}
