package transcribe

import (
	"os"
	"os/exec"
	"path/filepath"
)

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// siblingExe returns the first of names found next to the running executable —
// used to find binaries bundled in MeetMD.app/Contents/MacOS.
func siblingExe(names ...string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	dir := filepath.Dir(exe)
	for _, n := range names {
		if p := filepath.Join(dir, n); exists(p) {
			return p
		}
	}
	return ""
}

// bundleResource returns Contents/Resources/<rel> when running inside a .app and
// the file exists (exe = .../Contents/MacOS/<bin> → ../Resources/<rel>).
func bundleResource(rel string) string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	p := filepath.Join(filepath.Dir(filepath.Dir(exe)), "Resources", rel)
	if exists(p) {
		return p
	}
	return ""
}

// resolveBin locates the whisper CLI: the configured path, then a sibling in the
// .app bundle, then PATH.
func resolveBin(configured string) string {
	if configured != "" && exists(configured) {
		return configured
	}
	if s := siblingExe(whisperBinaries...); s != "" {
		return s
	}
	for _, n := range whisperBinaries {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// resolveModel returns a usable model: the configured path, or the bundled model
// with the same filename. An empty input stays empty (the VAD model is optional).
func resolveModel(configured string) string {
	if configured == "" || exists(configured) {
		return configured
	}
	return bundleResource("models/" + filepath.Base(configured))
}
