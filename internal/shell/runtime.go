package shell

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RunningExe finds a live instance of the inir shell in Quickshell's runtime dir
// (by-pid links whose instance is under by-shell/inir) and returns its executable.
func RunningExe() (string, bool) {
	run := os.Getenv("XDG_RUNTIME_DIR")
	if run == "" {
		run = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	run = filepath.Join(run, "quickshell")
	links, _ := filepath.Glob(filepath.Join(run, "by-pid", "*"))
	for _, link := range links {
		target, err := os.Readlink(link)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(run, "by-shell", "inir", filepath.Base(target))); err != nil {
			continue
		}
		pid := filepath.Base(link)
		comm, err := os.ReadFile(filepath.Join("/proc", pid, "comm"))
		// inir's launcher runs it through an inir-shell link (ananicy's `qs` rule nices it).
		if name := strings.TrimSpace(string(comm)); err != nil || (name != "qs" && name != "inir-shell") {
			continue
		}
		exe, err := os.Readlink(filepath.Join("/proc", pid, "exe"))
		if err != nil {
			continue
		}
		// A binary replaced on disk while running: the new one at that path.
		exe = strings.TrimSuffix(exe, " (deleted)")
		if st, err := os.Stat(exe); err == nil && st.Mode()&0o111 != 0 {
			return exe, true
		}
	}
	return "", false
}

// Runtime is the qs the shell runs on, or the one the launcher starts when none runs (scripts/inir find_qs): the
// running shell's own binary, INIR_QS_BIN, qs-rs as setup installs it (runtime/current), then Quickshell on PATH.
func Runtime(dataHome string, lookPath func(string) string) string {
	if exe, ok := RunningExe(); ok {
		return exe
	}
	for _, p := range []string{os.Getenv("INIR_QS_BIN"), filepath.Join(dataHome, "inir", "runtime", "current", "bin", "qs")} {
		if st, err := os.Stat(p); p != "" && err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p
		}
	}
	if p := lookPath("qs"); p != "" {
		return p
	}
	return lookPath("quickshell")
}
