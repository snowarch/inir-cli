package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/snowarch/inir-cli/internal/session"
	"github.com/snowarch/inir-cli/internal/shell"
)

// runSession is `inir run --session [-c DIR]`, what inir.service starts: the shell's environment, then the shell
// itself in this process. Other run flags stay with the launcher.
func runSession(args []string) (int, bool) {
	dir, isSession := "", false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--session":
			isSession = true
		case "-c", "--config":
			if i+1 >= len(args) {
				return 0, false
			}
			i++
			dir = args[i]
		default:
			return 0, false
		}
	}
	if !isSession {
		return 0, false
	}
	e := session.NewEnv(os.Environ())
	users := shell.ConfigDirs()
	userRuntime := filepath.Join(e.Get("HOME"), ".config", "quickshell", "inir")
	if cfg := e.Get("XDG_CONFIG_HOME"); cfg != "" {
		userRuntime = filepath.Join(cfg, "quickshell", "inir")
	}
	launcher := e.Get("INIR_BASH_LAUNCHER")
	launcherRoot := ""
	if launcher != "" {
		launcherRoot = filepath.Dir(filepath.Dir(launcher))
	}
	if err := session.SyncPackage(e, launcherRoot, userRuntime); err != nil {
		fmt.Fprintln(os.Stderr, "iNiR:", err)
		return 1, true
	}
	data := e.Get("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(e.Get("HOME"), ".local", "share")
	}
	qs := shell.Runtime(data, func(name string) string { p, _ := exec.LookPath(name); return p })
	if qs == "" {
		fmt.Fprintln(os.Stderr, "qs not found")
		return 1, true
	}
	dir, err := sessionDir(dir, e.Get("INIR_RUNTIME_DIR"), users)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1, true
	}
	run := e.Get("XDG_RUNTIME_DIR")
	if run == "" {
		run = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	sd := session.NewSystemd(run)
	// Helpers orphaned by the previous supervised shell.
	orphans(run, sd, users).Cleanup()
	session.HotReload(e, dir)
	cache := e.Get("XDG_CACHE_HOME")
	if cache == "" {
		cache = filepath.Join(e.Get("HOME"), ".cache")
	}
	session.CheckABI(qs, cache, os.Stderr)
	cli, err := os.Executable()
	if err != nil {
		cli = os.Args[0]
	}
	session.Prepare(e, qs, dir, cli, launcher, sd)
	if err := waitWayland(e, run); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1, true
	}
	// Under Umbriel: iNiR's config on the first login, the person's Niri keys on every one; under Rin: iNiR's keys,
	// rules and colours. Beside the shell, quiet.
	for _, c := range [][2]string{{"UMBRIEL_SOCKET", "umbriel"}, {"RIN_SOCKET", "rin"}} {
		if e.Get(c[0]) == "" {
			continue
		}
		cmd := exec.Command("bash", "-c", `source "$0" && compositor_`+c[1]+`_session_start "$1"`,
			filepath.Join(dir, "scripts", "lib", "compositor.sh"), filepath.Join(dir, "defaults", c[1]))
		cmd.Env = e.List()
		_ = cmd.Start()
	}
	bin := shellExecBin(qs, run)
	if err := syscall.Exec(bin, []string{bin, "-n", "-p", dir}, e.List()); err != nil {
		fmt.Fprintln(os.Stderr, "iNiR:", err)
		return 1, true
	}
	return 0, true
}

// runCleanupOrphans is `inir cleanup-orphans`, inir.service's ExecStopPost.
func runCleanupOrphans() int {
	run := os.Getenv("XDG_RUNTIME_DIR")
	if run == "" {
		run = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	orphans(run, session.NewSystemd(run), shell.ConfigDirs()).Cleanup()
	return 0
}

func orphans(run string, sd *session.Systemd, roots []string) session.Orphans {
	proc := os.Getenv("INIR_PROC_ROOT")
	if proc == "" {
		proc = "/proc"
	}
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return session.Orphans{RuntimeDir: run, ProcRoot: proc, Roots: roots, Systemd: sd,
		ServiceDir: filepath.Join(cfg, "service", "inir"), Kill: syscall.Kill}
}

// sessionDir is the shell tree to run: the one named (-c, INIR_RUNTIME_DIR), else the first installed.
func sessionDir(named, runtimeDir string, dirs []string) (string, error) {
	if named == "" {
		named = runtimeDir
	}
	if named != "" {
		if real, ok := shell.Installed(named); ok {
			return real, nil
		}
		return "", fmt.Errorf("Invalid iNiR config path: %s", named)
	}
	for _, d := range dirs {
		if real, ok := shell.Installed(d); ok {
			return real, nil
		}
	}
	return "", errors.New("Could not find an iNiR shell payload")
}

// waitWayland waits up to 10 s for the compositor's socket. The compositor is the authority for WAYLAND_DISPLAY:
// another socket under the runtime dir is never guessed (nested compositors and gamescope coexist).
func waitWayland(e session.Env, run string) error {
	display := e.Get("WAYLAND_DISPLAY")
	if display == "" {
		return errors.New("WAYLAND_DISPLAY is missing from the compositor's session environment")
	}
	socket := display
	if !filepath.IsAbs(socket) {
		socket = filepath.Join(run, socket)
	}
	for i := 0; i <= 50; i++ {
		if st, err := os.Stat(socket); err == nil && st.Mode()&os.ModeSocket != 0 {
			return nil
		}
		if i < 50 {
			time.Sleep(200 * time.Millisecond)
		}
	}
	return fmt.Errorf("the compositor's WAYLAND_DISPLAY socket did not appear: %s", socket)
}

// shellExecBin runs the shell through a link named inir-shell: CachyOS's ananicy rules run any process named qs as
// a background service (nice 10, ionice 6), and the kernel names a process after the file it ran.
func shellExecBin(qs, run string) string {
	real, err := filepath.EvalSymlinks(qs)
	if err != nil {
		real = qs
	}
	dir := filepath.Join(run, "inir", "bin")
	link := filepath.Join(dir, "inir-shell")
	tmp := fmt.Sprintf("%s.%d", link, os.Getpid())
	if os.MkdirAll(dir, 0o755) != nil || os.Symlink(real, tmp) != nil {
		return qs
	}
	if os.Rename(tmp, link) != nil {
		os.Remove(tmp)
		return qs
	}
	return link
}
