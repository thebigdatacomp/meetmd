package server

import (
	"testing"

	"github.com/thebigdatacomp/meetmd/internal/config"
)

func TestToDTO(t *testing.T) {
	cfg := config.Config{
		OutputRoot: "/out",
		Language:   "pt",
		Audio:      config.Audio{CaptureMic: true, DeleteWavOnFinish: false},
		AutoDetect: config.AutoDetect{Enabled: true, Mode: "auto", Project: "bora"},
	}
	d := toDTO(cfg)
	if d.OutputRoot != "/out" || d.Language != "pt" || d.DefaultProject != "bora" {
		t.Errorf("basic fields wrong: %+v", d)
	}
	if d.AutoDetect != autoAuto || !d.CaptureMic || d.DeleteAudio {
		t.Errorf("derived fields wrong: %+v", d)
	}
}

func TestToDTOAutoDetectOff(t *testing.T) {
	cfg := config.Config{AutoDetect: config.AutoDetect{Enabled: false, Mode: "ask"}}
	if got := toDTO(cfg).AutoDetect; got != autoOff {
		t.Errorf("disabled auto-detect = %q, want %q", got, autoOff)
	}
}

func TestApplyDTOPreservesInternalFields(t *testing.T) {
	// internal fields (bin/model/port) must survive a settings update
	base := config.Config{
		Port:    8765,
		Whisper: config.Whisper{BinPath: "/whisper", ModelPath: "/model.bin"},
		Audio:   config.Audio{MacHelperPath: "/helper"},
	}
	dto := settingsDTO{
		OutputRoot:     "/new",
		Language:       "es",
		DefaultProject: "fevo",
		AutoDetect:     autoOff,
		CaptureMic:     false,
		DeleteAudio:    true,
	}
	out := applyDTO(base, dto)

	if out.OutputRoot != "/new" || out.Language != "es" || out.AutoDetect.Project != "fevo" {
		t.Errorf("user fields not applied: %+v", out)
	}
	if out.AutoDetect.Enabled || out.Audio.CaptureMic || !out.Audio.DeleteWavOnFinish {
		t.Errorf("toggles wrong: %+v", out)
	}
	if out.Port != 8765 || out.Whisper.BinPath != "/whisper" || out.Audio.MacHelperPath != "/helper" {
		t.Errorf("internal fields not preserved: %+v", out)
	}
}

func TestApplyDTOAutoModes(t *testing.T) {
	for label, wantEnabled := range map[string]bool{autoOff: false, autoAsk: true, autoAuto: true} {
		out := applyDTO(config.Config{}, settingsDTO{OutputRoot: "/x", AutoDetect: label})
		if out.AutoDetect.Enabled != wantEnabled {
			t.Errorf("%q -> enabled=%v, want %v", label, out.AutoDetect.Enabled, wantEnabled)
		}
	}
}
