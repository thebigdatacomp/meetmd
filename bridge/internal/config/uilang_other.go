//go:build !darwin

package config

// osLanguage has no portable source outside macOS; UILanguage == "auto" then
// resolves to English. Override explicitly via ui_language: pt|en.
func osLanguage() string { return "" }
