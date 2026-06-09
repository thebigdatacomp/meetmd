//go:build !darwin

package main

import "fmt"

// install/uninstall are macOS-only (LaunchAgent) for now. Windows (Task
// Scheduler / service) and Linux (systemd user unit) are future work.
func runInstall()   { fmt.Println("`install` é suportado só no macOS por enquanto (LaunchAgent)") }
func runUninstall() { fmt.Println("`uninstall` é suportado só no macOS por enquanto") }
