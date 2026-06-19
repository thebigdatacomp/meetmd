//go:build darwin

package audio

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/thebigdatacomp/meetmd/internal/config"
)

// helperName is the ScreenCaptureKit recorder binary (built from
// spike/macos-audio/SystemAudioRecorder.swift).
const helperName = "system-audio-recorder"

// minUsableWav is the smallest WAV (bytes) we consider real audio rather than
// an empty/header-only file.
const minUsableWav = 1024

// macCapturer drives the Swift ScreenCaptureKit helper as a child process:
// Start launches it (recording until signalled), Stop sends SIGTERM so it
// finalizes the WAV, Cancel kills it and discards the file.
type macCapturer struct {
	store   *config.Store
	workDir string

	mu      sync.Mutex
	cmd     *exec.Cmd
	wavPath string
	micPath string
	micOnly bool // mic-only capture (voice note): wavPath holds the mic recording
}

func newMacCapturer(store *config.Store) *macCapturer {
	return &macCapturer{store: store, workDir: filepath.Join(os.TempDir(), "meetmd")}
}

func (c *macCapturer) Start(_ context.Context, sessionID string) error {
	return c.launch(sessionID, false)
}

// StartMicOnly records only the mic (voice notes): the helper skips
// ScreenCaptureKit, so no Screen Recording permission is needed.
func (c *macCapturer) StartMicOnly(_ context.Context, sessionID string) error {
	return c.launch(sessionID, true)
}

func (c *macCapturer) launch(sessionID string, micOnly bool) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != nil {
		return errors.New("capture already running")
	}
	audioCfg := c.store.Get().Audio // live config (helper path, mic) at each Start
	helper, err := resolveHelper(audioCfg.MacHelperPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.workDir, 0o755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}
	wav := filepath.Join(c.workDir, sessionID+".wav")
	args := []string{wav} // no duration arg → record until signalled

	mic := ""
	switch {
	case micOnly:
		// The single WAV is the mic recording; no system audio, no --mic channel.
		args = append(args, "--mic-only")
	case audioCfg.CaptureMic:
		mic = filepath.Join(c.workDir, sessionID+".mic.wav")
		args = append(args, "--mic", mic)
	}

	cmd := exec.Command(helper, args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start audio helper: %w", err)
	}
	c.cmd, c.wavPath, c.micPath, c.micOnly = cmd, wav, mic, micOnly
	return nil
}

func (c *macCapturer) Stop() (Recording, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd == nil {
		return Recording{}, errors.New("no capture running")
	}
	cmd, wav, mic, micOnly := c.cmd, c.wavPath, c.micPath, c.micOnly
	c.cmd, c.wavPath, c.micPath, c.micOnly = nil, "", "", false

	// A mic-only note makes a mic failure (e.g. denied permission) fatal, so the
	// helper may have already exited by the time we stop. That is not an error —
	// fall through to the "no usable audio" handling below for a graceful empty.
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return Recording{}, fmt.Errorf("signal audio helper: %w", err)
	}
	waitErr := cmd.Wait() // exit 2 = ran but captured nothing

	// Repair the WAV headers if the helper didn't finalize them (terminated or
	// capture stream died before AVAudioFile flushed). Without this the recording
	// is unreadable and the whole meeting is lost. Best-effort: a repair failure
	// just leaves the file as-is for usableWav to judge.
	for _, p := range []string{wav, mic} {
		if p == "" {
			continue
		}
		if err := finalizeWAV(p); err != nil {
			log.Printf("audio: não consegui finalizar o WAV %s: %v", p, err)
		}
	}

	// In mic-only mode the single WAV is the mic recording, not system audio.
	rec := Recording{SystemWav: usableWav(wav), MicWav: usableWav(mic)}
	if micOnly {
		rec = Recording{MicWav: usableWav(wav)}
	}
	// Treat "no usable audio" as no audio rather than a hard failure, so the
	// session still completes with an empty transcript.
	if rec.SystemWav == "" && rec.MicWav == "" {
		if waitErr != nil {
			return Recording{}, fmt.Errorf("audio helper produced no audio: %w", waitErr)
		}
		return Recording{}, nil
	}
	return rec, nil
}

// usableWav returns path when it points to a WAV with real audio, else "".
func usableWav(path string) string {
	if path == "" {
		return ""
	}
	if info, err := os.Stat(path); err == nil && info.Size() >= minUsableWav {
		return path
	}
	return ""
}

func (c *macCapturer) Pause() error  { return c.signal(syscall.SIGUSR1) }
func (c *macCapturer) Resume() error { return c.signal(syscall.SIGUSR2) }

func (c *macCapturer) signal(sig syscall.Signal) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd == nil {
		return errors.New("no capture running")
	}
	return c.cmd.Process.Signal(sig)
}

func (c *macCapturer) Cancel() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd == nil {
		return nil
	}
	cmd, wav, mic := c.cmd, c.wavPath, c.micPath
	c.cmd, c.wavPath, c.micPath, c.micOnly = nil, "", "", false
	_ = cmd.Process.Signal(syscall.SIGKILL)
	_ = cmd.Wait()
	for _, p := range []string{wav, mic} {
		if p != "" {
			_ = os.Remove(p)
		}
	}
	return nil
}

// resolveHelper locates the capture helper binary: a configured path, then a
// sibling of the bridge executable (so it works inside a .app bundle), then PATH.
func resolveHelper(configured string) (string, error) {
	if configured != "" {
		if _, err := os.Stat(configured); err != nil {
			return "", fmt.Errorf("audio helper not found at %s: %w", configured, err)
		}
		return configured, nil
	}
	if exe, err := os.Executable(); err == nil {
		sibling := filepath.Join(filepath.Dir(exe), helperName)
		if _, err := os.Stat(sibling); err == nil {
			return sibling, nil
		}
	}
	if p, err := exec.LookPath(helperName); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("audio helper %q not found; set audio.mac_helper_path in config or add it to PATH", helperName)
}
