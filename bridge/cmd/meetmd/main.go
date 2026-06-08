// Command meetmd runs the MeetMD bridge: a local daemon that captures meeting
// audio, transcribes it, and writes structured Markdown for Claude to process.
package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/thebigdatacomp/meetmd/internal/audio"
	"github.com/thebigdatacomp/meetmd/internal/config"
	"github.com/thebigdatacomp/meetmd/internal/server"
	"github.com/thebigdatacomp/meetmd/internal/session"
	"github.com/thebigdatacomp/meetmd/internal/transcribe"
)

func main() {
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
	srv := server.New(mgr)

	log.Printf("MeetMD bridge listening on 127.0.0.1:%d", cfg.Port)
	log.Printf("output root: %s", cfg.OutputRoot)
	if err := srv.ListenAndServe(cfg.Port); err != nil {
		log.Fatalf("server: %v", err)
	}
}
