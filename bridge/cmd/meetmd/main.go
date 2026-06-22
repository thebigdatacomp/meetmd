// Command meetmd is the MeetMD bridge and its CLI client.
//
//	meetmd serve            run the bridge daemon (default)
//	meetmd start [título]   start a recording on the running bridge
//	meetmd stop             stop the active recording and write the .md
//	meetmd status           show the bridge state
//	meetmd cancel           abort the active recording
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/thebigdatacomp/meetmd/internal/audio"
	"github.com/thebigdatacomp/meetmd/internal/config"
	"github.com/thebigdatacomp/meetmd/internal/detect"
	"github.com/thebigdatacomp/meetmd/internal/server"
	"github.com/thebigdatacomp/meetmd/internal/session"
	"github.com/thebigdatacomp/meetmd/internal/transcribe"
)

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		runServe()
	case "start":
		runStart(os.Args[2:])
	case "stop":
		runStop()
	case "pause":
		runPause()
	case "resume":
		runResume()
	case "status":
		runStatus()
	case "cancel":
		runCancel()
	case "install":
		runInstall()
	case "uninstall":
		runUninstall()
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "comando desconhecido: %s\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

const usage = `MeetMD — captura reuniões em Markdown estruturado.

Uso:
  meetmd serve                     inicia o bridge (daemon)
  meetmd start [-p projeto] [título]  inicia uma gravação
  meetmd pause                     pausa a gravação atual
  meetmd resume                    retoma a gravação pausada
  meetmd stop                      para a gravação e grava os .md
  meetmd status                    mostra o estado do bridge
  meetmd cancel                    aborta a gravação atual
  meetmd install                   instala o serviço (inicia no login, macOS)
  meetmd uninstall                 remove o serviço

Exemplos:
  meetmd start -p bora "Daily"
  meetmd start -p bonavia "Reunião de deploy"
`

// runServe starts the bridge daemon.
// openLogFile opens (appending) ~/.meetmd/logs/bridge.log for the bridge log tee.
func openLogFile() (*os.File, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(home, ".meetmd", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(filepath.Join(dir, "bridge.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

func runServe() {
	// Tee logs to ~/.meetmd/logs/bridge.log so capture diagnostics (helper stream
	// death/restart, mic errors) are inspectable — the GUI-launched bridge's
	// stderr is otherwise hidden in the unified log.
	if lf, err := openLogFile(); err == nil {
		log.SetOutput(io.MultiWriter(os.Stderr, lf))
	} else {
		log.Printf("log file unavailable: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := os.MkdirAll(cfg.RecordingsRoot, 0o755); err != nil {
		log.Fatalf("recordings root %q: %v", cfg.RecordingsRoot, err)
	}

	// Hot-reloadable config: the store is read live; the watcher reloads it when
	// ~/.meetmd/config.yaml changes (transcription/output settings apply to the
	// next recording without a restart). Port and audio capture are bound here.
	store := config.NewStore(cfg)
	go config.Watch(context.Background(), store, func(config.Config) {
		log.Printf("config recarregado")
	})

	// Real OS audio capture (macOS via ScreenCaptureKit; Stub elsewhere). Reads
	// helper path + mic setting live from the store at each Start.
	capturer := audio.NewCapturer(store)

	// Transcriber is built per recording from the live config, so model/language/
	// VAD changes take effect without a restart. Falls back to an empty
	// transcript when the CLI or model is missing.
	newTranscriber := func(c config.Config, voice bool) transcribe.Transcriber {
		t, _ := transcribe.New(transcribe.Options{
			Engine:    c.Whisper.Engine,
			BinPath:   c.Whisper.BinPath,
			ModelPath: c.Whisper.ModelPath,
			Language:  c.Language,
			VADModel:  c.Whisper.VADModel,
			Voice:     voice,
		})
		return t
	}
	if _, note := transcribe.New(transcribe.Options{
		Engine: cfg.Whisper.Engine, BinPath: cfg.Whisper.BinPath,
		ModelPath: cfg.Whisper.ModelPath, Language: cfg.Language, VADModel: cfg.Whisper.VADModel,
	}); note != "" {
		log.Printf("transcrição: %s", note)
	}

	mgr := session.New(store, capturer, newTranscriber)

	// Auto-detect meetings (macOS/Safari). Always runs; reads auto_detect from
	// the live store each tick, so enabled/mode/project are hot-reloadable.
	detect.Start(context.Background(), mgr, store)

	srv := server.New(mgr, store)

	log.Printf("MeetMD bridge listening on 127.0.0.1:%d", cfg.Port)
	log.Printf("recordings root: %s", cfg.RecordingsRoot)
	if err := srv.ListenAndServe(cfg.Port); err != nil {
		log.Fatalf("server: %v", err)
	}
}
