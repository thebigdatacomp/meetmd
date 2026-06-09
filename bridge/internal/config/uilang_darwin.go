//go:build darwin

package config

import (
	"os/exec"
	"strings"
)

// osLanguage returns the macOS preferred language/locale (e.g. "pt_BR" or
// "en-US"), or "" if it cannot be read. Used to resolve UILanguage == "auto".
func osLanguage() string {
	// AppleLocale is a single value (e.g. "pt_BR"); preferred when available.
	if out, err := exec.Command("defaults", "read", "-g", "AppleLocale").Output(); err == nil {
		if s := strings.TrimSpace(string(out)); s != "" {
			return s
		}
	}
	// Fallback: first entry of the AppleLanguages array.
	out, err := exec.Command("defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if s := strings.Trim(strings.TrimSpace(line), "(),\""); s != "" {
			return s
		}
	}
	return ""
}
