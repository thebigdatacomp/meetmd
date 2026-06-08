package server

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/thebigdatacomp/meetmd/internal/config"
	"github.com/thebigdatacomp/meetmd/internal/detect"
)

// Auto-detect choices exposed to the UI (combine enabled + mode).
const (
	autoOff  = "off"
	autoAsk  = "ask"
	autoAuto = "auto"
)

// settingsDTO is the user-facing subset of the config (no internal paths).
type settingsDTO struct {
	OutputRoot     string `json:"outputRoot"`
	Language       string `json:"language"`       // auto | pt | es | en | ...
	DefaultProject string `json:"defaultProject"` // project for auto-detected meetings
	AutoDetect     string `json:"autoDetect"`     // off | ask | auto
	CaptureMic     bool   `json:"captureMic"`     // include the user's mic
	DeleteAudio    bool   `json:"deleteAudio"`    // delete the raw WAV after transcribing
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, toDTO(s.store.Get()))
	case http.MethodPut, http.MethodPost:
		s.updateSettings(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	var dto settingsDTO
	if err := json.NewDecoder(r.Body).Decode(&dto); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(dto.OutputRoot) == "" {
		writeError(w, http.StatusBadRequest, "outputRoot é obrigatório")
		return
	}

	cfg := applyDTO(s.store.Get(), dto)
	if err := os.MkdirAll(cfg.OutputRoot, 0o755); err != nil {
		writeError(w, http.StatusBadRequest, "não foi possível criar a pasta de saída: "+err.Error())
		return
	}
	if err := config.Save(cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "falha ao salvar config: "+err.Error())
		return
	}
	s.store.Set(cfg) // apply immediately (the watcher would also pick it up)
	writeJSON(w, http.StatusOK, toDTO(cfg))
}

func toDTO(cfg config.Config) settingsDTO {
	auto := autoOff
	if cfg.AutoDetect.Enabled {
		auto = cfg.AutoDetect.Mode
		if auto == "" {
			auto = autoAsk
		}
	}
	return settingsDTO{
		OutputRoot:     cfg.OutputRoot,
		Language:       cfg.Language,
		DefaultProject: cfg.AutoDetect.Project,
		AutoDetect:     auto,
		CaptureMic:     cfg.Audio.CaptureMic,
		DeleteAudio:    cfg.Audio.DeleteWavOnFinish,
	}
}

// applyDTO maps the user-facing settings onto a full config, preserving the
// internal fields (model/bin/vad/helper paths, port) untouched.
func applyDTO(cfg config.Config, dto settingsDTO) config.Config {
	cfg.OutputRoot = strings.TrimSpace(dto.OutputRoot)
	cfg.Language = strings.TrimSpace(dto.Language)
	cfg.AutoDetect.Project = strings.TrimSpace(dto.DefaultProject)
	cfg.Audio.CaptureMic = dto.CaptureMic
	cfg.Audio.DeleteWavOnFinish = dto.DeleteAudio

	switch dto.AutoDetect {
	case autoOff:
		cfg.AutoDetect.Enabled = false
	case autoAuto:
		cfg.AutoDetect.Enabled = true
		cfg.AutoDetect.Mode = detect.ModeAuto
	default:
		cfg.AutoDetect.Enabled = true
		cfg.AutoDetect.Mode = detect.ModeAsk
	}
	return cfg
}
