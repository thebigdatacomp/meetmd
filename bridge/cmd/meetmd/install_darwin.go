//go:build darwin

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const launchLabel = "com.tbdc.meetmd"

// runInstall installs a LaunchAgent so the bridge starts at login and restarts
// if it crashes. It copies the running binary to a stable path in ~/.meetmd/bin
// so macOS TCC permissions (Screen Recording, Automation, Microphone) persist.
func runInstall() {
	exe, err := os.Executable()
	if err != nil {
		clientFail(err)
	}
	if strings.Contains(exe, "go-build") || strings.HasPrefix(exe, os.TempDir()) {
		clientFail(fmt.Errorf("rode a partir do binário estável: cd bridge && make build && ./bin/meetmd install"))
	}

	home := mustHome()
	binDir := filepath.Join(home, ".meetmd", "bin")
	logDir := filepath.Join(home, ".meetmd", "logs")
	for _, d := range []string{binDir, logDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			clientFail(err)
		}
	}

	dest := filepath.Join(binDir, "meetmd")
	if err := copyExecutable(exe, dest); err != nil {
		clientFail(fmt.Errorf("copiar binário: %w", err))
	}

	plistPath := launchAgentPath(home)
	if err := os.WriteFile(plistPath, []byte(plistContent(dest, logDir)), 0o644); err != nil {
		clientFail(fmt.Errorf("escrever plist: %w", err))
	}

	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain, plistPath).Run() // ignore if not loaded
	if out, err := exec.Command("launchctl", "bootstrap", domain, plistPath).CombinedOutput(); err != nil {
		clientFail(fmt.Errorf("launchctl bootstrap: %v: %s", err, strings.TrimSpace(string(out))))
	}

	fmt.Printf("✓ instalado — o bridge inicia no login e reinicia se cair.\n")
	fmt.Printf("  binário: %s\n  plist:   %s\n  logs:    %s/bridge.{out,err}.log\n", dest, plistPath, logDir)
	fmt.Printf("  (se você ainda tem um `make run` aberto, pode fechá-lo — agora o serviço cuida disso)\n")
}

// runUninstall stops and removes the LaunchAgent.
func runUninstall() {
	home := mustHome()
	plistPath := launchAgentPath(home)
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain, plistPath).Run()
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		clientFail(err)
	}
	fmt.Println("✓ removido — o bridge não inicia mais no login.")
}

func launchAgentPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", launchLabel+".plist")
}

func mustHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		clientFail(err)
	}
	return home
}

func copyExecutable(src, dst string) error {
	if filepath.Clean(src) == filepath.Clean(dst) {
		return nil // already the installed binary; nothing to copy
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func plistContent(binPath, logDir string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>serve</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s/bridge.out.log</string>
	<key>StandardErrorPath</key>
	<string>%s/bridge.err.log</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
	</dict>
</dict>
</plist>
`, launchLabel, binPath, logDir, logDir)
}
