// Package shell finds the installed iNiR shell and whether Quickshell runs it.
package shell

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// ConfigDirs lists where an iNiR shell can be installed, in the order the bash
// launcher resolves them (scripts/inir resolve_config_dir).
func ConfigDirs() []string {
	var dirs []string
	if d := os.Getenv("INIR_RUNTIME_DIR"); d != "" {
		dirs = append(dirs, d)
	}
	config := os.Getenv("XDG_CONFIG_HOME")
	if config == "" {
		config = filepath.Join(os.Getenv("HOME"), ".config")
	}
	dirs = append(dirs, filepath.Join(config, "quickshell", "inir"))
	dirs = append(dirs, envOr("INIR_SYSTEM_RUNTIME_DIR", "/usr/local/share/quickshell/inir"))
	dirs = append(dirs, envOr("INIR_FALLBACK_SYSTEM_RUNTIME_DIR", "/usr/share/quickshell/inir"))
	return dirs
}

// Installed returns the real path of a directory that holds shell.qml.
// Quickshell keys instances by the resolved path, so symlinks are followed.
func Installed(dir string) (string, bool) {
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(real, "shell.qml")); err != nil {
		return "", false
	}
	return real, true
}

// Running reports whether a Quickshell instance of the shell at realDir answers
// on its IPC socket. It reads Quickshell's runtime index instead of spawning
// `qs list`, which costs as much as the IPC call itself.
func Running(realDir string) bool {
	sum := md5.Sum([]byte(filepath.Join(realDir, "shell.qml")))
	byPath := filepath.Join(runtimeDir(), "quickshell", "by-path", hex.EncodeToString(sum[:]))
	entries, err := os.ReadDir(byPath)
	if err != nil {
		return false
	}
	for _, e := range entries {
		// Dial the resolved by-id path: socket addresses are capped at 108 bytes
		// and the by-path route through two links is the longest spelling.
		instance, err := filepath.EvalSymlinks(filepath.Join(byPath, e.Name()))
		if err != nil {
			continue
		}
		conn, err := net.DialTimeout("unix", filepath.Join(instance, "ipc.sock"), 200*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
	}
	return false
}

func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return fmt.Sprintf("/run/user/%d", os.Getuid())
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
