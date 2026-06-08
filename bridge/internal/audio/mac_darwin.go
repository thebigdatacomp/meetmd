//go:build darwin

package audio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
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
	opts Options

	mu      sync.Mutex
	cmd     *exec.Cmd
	wavPath string
}

func newMacCapturer(opts Options) *macCapturer {
	return &macCapturer{opts: opts}
}

func (c *macCapturer) Start(_ context.Context, sessionID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != nil {
		return errors.New("capture already running")
	}
	helper, err := c.resolveHelper()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(c.opts.WorkDir, 0o755); err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}
	wav := filepath.Join(c.opts.WorkDir, sessionID+".wav")

	cmd := exec.Command(helper, wav) // no duration arg → record until signalled
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start audio helper: %w", err)
	}
	c.cmd, c.wavPath = cmd, wav
	return nil
}

func (c *macCapturer) Stop() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd == nil {
		return "", errors.New("no capture running")
	}
	cmd, wav := c.cmd, c.wavPath
	c.cmd, c.wavPath = nil, ""

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		return "", fmt.Errorf("signal audio helper: %w", err)
	}
	waitErr := cmd.Wait() // exit 2 = ran but captured nothing

	// Treat "no usable audio" as no audio rather than a hard failure, so the
	// session still completes with an empty transcript.
	info, statErr := os.Stat(wav)
	if statErr != nil || info.Size() < minUsableWav {
		if waitErr != nil {
			return "", fmt.Errorf("audio helper produced no audio: %w", waitErr)
		}
		return "", nil
	}
	return wav, nil
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
	cmd, wav := c.cmd, c.wavPath
	c.cmd, c.wavPath = nil, ""
	_ = cmd.Process.Signal(syscall.SIGKILL)
	_ = cmd.Wait()
	if wav != "" {
		_ = os.Remove(wav)
	}
	return nil
}

// resolveHelper locates the capture helper binary.
func (c *macCapturer) resolveHelper() (string, error) {
	if c.opts.HelperPath != "" {
		if _, err := os.Stat(c.opts.HelperPath); err != nil {
			return "", fmt.Errorf("audio helper not found at %s: %w", c.opts.HelperPath, err)
		}
		return c.opts.HelperPath, nil
	}
	if p, err := exec.LookPath(helperName); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("audio helper %q not found; set audio.mac_helper_path in config or add it to PATH", helperName)
}
