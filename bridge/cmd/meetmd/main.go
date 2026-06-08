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
	// Transcription is still a stub until M2 wires up whisper.cpp.
	capturer := audio.NewCapturer(audio.Options{
		HelperPath: cfg.Audio.MacHelperPath,
		WorkDir:    filepath.Join(os.TempDir(), "meetmd"),
	})
	mgr := session.New(cfg, capturer, transcribe.Stub{})
	srv := server.New(mgr)

	log.Printf("MeetMD bridge listening on 127.0.0.1:%d", cfg.Port)
	log.Printf("output root: %s", cfg.OutputRoot)
	if err := srv.ListenAndServe(cfg.Port); err != nil {
		log.Fatalf("server: %v", err)
	}
}
