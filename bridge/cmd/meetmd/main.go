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
	"log"
	"os"
	"path/filepath"
	"time"

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

Exemplos:
  meetmd start -p bora "Daily"
  meetmd start -p bonavia "Reunião de deploy"
`

// runServe starts the bridge daemon.
func runServe() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := os.MkdirAll(cfg.OutputRoot, 0o755); err != nil {
		log.Fatalf("output root %q: %v", cfg.OutputRoot, err)
	}

	// Real OS audio capture (macOS via ScreenCaptureKit; Stub elsewhere).
	capturer := audio.NewCapturer(audio.Options{
		HelperPath: cfg.Audio.MacHelperPath,
		WorkDir:    filepath.Join(os.TempDir(), "meetmd"),
	})

	// Local whisper.cpp transcription (falls back to an empty transcript if the
	// CLI or model is missing).
	transcriber, note := transcribe.New(transcribe.Options{
		Engine:    cfg.Whisper.Engine,
		BinPath:   cfg.Whisper.BinPath,
		ModelPath: cfg.Whisper.ModelPath,
		Language:  cfg.Language,
	})
	log.Printf("transcrição: %s", note)

	mgr := session.New(cfg, capturer, transcriber)

	// Auto-detect meetings in the browser (macOS/Safari) and drive start/stop.
	if cfg.AutoDetect.Enabled {
		detect.Start(context.Background(), mgr, detect.Options{
			Project:  cfg.AutoDetect.Project,
			Interval: time.Duration(cfg.AutoDetect.IntervalSeconds) * time.Second,
			Mode:     cfg.AutoDetect.Mode,
		})
	}

	srv := server.New(mgr)

	log.Printf("MeetMD bridge listening on 127.0.0.1:%d", cfg.Port)
	log.Printf("output root: %s", cfg.OutputRoot)
	if err := srv.ListenAndServe(cfg.Port); err != nil {
		log.Fatalf("server: %v", err)
	}
}
