package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/snowarch/inir-cli/internal/shell"
)

// windows are the shell's own windows that run beside it as qs processes of their own, by the word that opens each.
var windows = map[string]string{
	"settings-window":        "settings.qml",
	"waffle-settings-window": "settings.qml", // its old name
	"welcome":                "welcome.qml",
	"kill-dialog":            "killDialog.qml", // services/ConflictKiller.qml
}

// serviceEnv describes inir.service's own process; a window of the shell is not the service.
var serviceEnv = []string{"NOTIFY_SOCKET", "INVOCATION_ID", "JOURNAL_STREAM", "MANAGERPID", "SYSTEMD_EXEC_PID",
	"WATCHDOG_PID", "WATCHDOG_USEC"}

// runWindow opens one of the shell's windows (settings-window --toggle closes it when open) on the running shell's
// own runtime and environment: the launcher set that environment up when it started the shell (GPU policy, Qt
// paths, allocator, logging rules), so the window gets it with no work here. A window starts alone at most once at
// a time (the launcher's lock file, shared with it). Help, a flag it does not take, or no shell running go to the
// launcher, which starts the window from scratch.
func runWindow(word string, args []string) (int, bool) {
	entry := windows[word]
	toggle, config := false, ""
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--toggle" && entry == "settings.qml":
			toggle = true
		case (a == "-c" || a == "--config") && word != "kill-dialog" && i+1 < len(args):
			config = args[i+1]
			i++
		default:
			return 0, false
		}
	}
	dir, pid, ok := windowShell(config)
	if !ok {
		return 0, false
	}
	exe, ok := shell.Exe(fmt.Sprint(pid))
	if !ok {
		return 0, false
	}
	env, err := shell.Environ(pid)
	if err != nil {
		return 0, false
	}
	qml := filepath.Join(dir, entry)
	if _, err := os.Stat(qml); err != nil {
		fmt.Fprintf(os.Stderr, "Could not find %s in %s\n", entry, dir)
		return 1, true
	}

	// Held from the check to the exec (the file closes on exec): presses that come quicker than a window starts
	// open one.
	lock, err := os.OpenFile(filepath.Join(os.TempDir(), "inir-launch-"+strings.TrimSuffix(entry, ".qml")+".lock"),
		os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, false
	}
	if syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		fmt.Fprintf(os.Stderr, "%s launch already in progress\n", entry)
		return 0, true
	}
	if open := shell.Instances(qml); len(open) > 0 {
		if !toggle {
			return 0, true
		}
		for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGKILL} {
			for _, p := range open {
				syscall.Kill(p, sig)
			}
			for poll := 0; poll < 60 && len(shell.Instances(qml)) > 0; poll++ {
				time.Sleep(100 * time.Millisecond)
			}
			if len(shell.Instances(qml)) == 0 {
				return 0, true
			}
		}
		fmt.Fprintf(os.Stderr, "Timed out waiting for %s to stop\n", entry)
		return 1, true
	}

	keep := env[:0]
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.Contains(serviceEnv, name) && !strings.HasPrefix(name, "QS_SETTINGS_") {
			keep = append(keep, kv)
		}
	}
	// The page and section a caller asks Settings to open on.
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "QS_SETTINGS_") {
			keep = append(keep, kv)
		}
	}
	bin := shellLink(exe)
	if err := syscall.Exec(bin, []string{bin, "-n", "-p", qml}, keep); err != nil {
		fmt.Fprintf(os.Stderr, "inir %s: %v\n", word, err)
		return 1, true
	}
	return 0, true
}

// windowShell is the shell a window belongs to and the pid of its running instance: the one -c or INIR_RUNTIME_DIR
// names, else the launcher's own shell (a shell's buttons call its scripts/inir), else the first installed one that
// runs.
func windowShell(config string) (string, int, bool) {
	var dirs []string
	if config == "" {
		config = os.Getenv("INIR_RUNTIME_DIR")
	}
	if config != "" {
		abs, err := filepath.Abs(config)
		if err != nil {
			return "", 0, false
		}
		dirs = []string{abs}
	} else {
		if l := os.Getenv("INIR_BASH_LAUNCHER"); l != "" {
			dirs = append(dirs, filepath.Dir(filepath.Dir(l)))
		}
		dirs = append(dirs, shell.ConfigDirs()...)
	}
	for _, d := range dirs {
		real, ok := shell.Installed(d)
		if !ok {
			continue
		}
		if pids := shell.Instances(filepath.Join(real, "shell.qml")); len(pids) > 0 {
			return real, pids[0], true
		}
	}
	return "", 0, false
}

// shellLink is the inir-shell link to exe the launcher runs the shell through (CachyOS's ananicy rules nice any
// process named qs), or exe itself when the link cannot be made.
func shellLink(exe string) string {
	dir := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "inir", "bin")
	if os.Getenv("XDG_RUNTIME_DIR") == "" {
		dir = filepath.Join(os.TempDir(), "inir", "bin")
	}
	link := filepath.Join(dir, "inir-shell")
	if target, err := os.Readlink(link); err == nil && target == exe {
		return link
	}
	tmp := fmt.Sprintf("%s.%d", link, os.Getpid())
	if os.MkdirAll(dir, 0o755) != nil || os.Symlink(exe, tmp) != nil {
		return exe
	}
	if os.Rename(tmp, link) != nil {
		os.Remove(tmp)
		return exe
	}
	return link
}
