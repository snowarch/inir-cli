// Command inir is the iNiR command line. IPC calls to a running shell are made
// here directly; everything else is handed to the shell's bash launcher
// (scripts/inir) until it moves into this binary.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/snowarch/inir-cli/internal/buildinfo"
	"github.com/snowarch/inir-cli/internal/ui"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/snowarch/inir-cli/internal/activity"
	"github.com/snowarch/inir-cli/internal/cli"
	"github.com/snowarch/inir-cli/internal/installer"
	"github.com/snowarch/inir-cli/internal/ipc"
	"github.com/snowarch/inir-cli/internal/shell"
)

func main() {
	args := os.Args[1:]
	// Whatever goes wrong in here, the bash launcher still answers.
	defer func() {
		if r := recover(); r != nil {
			ui.Log("inir").Error("crashed, the bash launcher takes over", "err", r)
			delegate(args)
		}
	}()
	if path, ok := wantsHelp(args); ok {
		if code, handled := runHelp(path); handled {
			os.Exit(code)
		}
	}
	if len(args) > 0 {
		switch args[0] {
		case "man":
			if len(args) == 1 {
				os.Exit(runMan())
			}
		case "__complete":
			os.Exit(runComplete(args[1:]))
		case "__commands":
			os.Exit(runCommands())
		case "completions":
			os.Exit(runCompletions(args[1:]))
		case "doctor":
			if code, handled := runDoctor(args[1:]); handled {
				os.Exit(code)
			}
		case "deps":
			os.Exit(runDeps(args[1:]))
		case "hub":
			if code, handled := runHub(args[1:]); handled {
				os.Exit(code)
			}
		case "run":
			if argv, title, ok := activityArgs(args[1:]); ok {
				os.Exit(runActivity(argv, title))
			}
		case "hook":
			os.Exit(runHook(args[1:]))
		case "__hook":
			os.Exit(activity.Hook(args[1:]))
		case "look":
			os.Exit(runLook(args[1:]))
		case "install":
			if installer.Interactive(args[1:]) {
				os.Exit(installer.Main(args[1:]))
			}
		case "update":
			if installer.Interactive(args[1:]) {
				os.Exit(installer.Update(args[1:]))
			}
		case "setup":
			if len(args) > 1 && args[1] == "install" && installer.Interactive(args[2:]) {
				os.Exit(installer.Main(args[2:]))
			}
			if len(args) > 1 && args[1] == "update" && installer.Interactive(args[2:]) {
				os.Exit(installer.Update(args[2:]))
			}
		case "cli":
			if len(args) == 2 && args[1] == "version" {
				fmt.Println("inir-cli", buildinfo.String())
				os.Exit(0)
			}
			fmt.Fprintln(os.Stderr, "usage: inir cli version")
			os.Exit(2)
		}
	}
	if code, handled := callIPC(args); handled {
		os.Exit(code)
	}
	delegate(args)
}

// callIPC runs `inir <target> <function> [args]` against a live shell. It only
// takes calls whose target and function the shell declares and whose shell is
// already running; the launcher keeps everything that needs a decision
// (starting the shell, help, errors, flags, special cases).
func callIPC(args []string) (int, bool) {
	if len(args) < 2 || strings.HasPrefix(args[0], "-") || strings.HasPrefix(args[1], "-") {
		return 0, false
	}
	if _, ok := cli.Lookup(args[0]); ok || (args[0] == "lock" && args[1] == "prepareSleep") {
		return 0, false
	}
	dir, ok := runningShell()
	if !ok {
		return 0, false
	}
	reg, err := ipc.Load(filepath.Join(dir, "scripts", "lib", "ipc.json"))
	if err != nil {
		return 0, false
	}
	target, ok := reg.Target(args[0])
	if !ok {
		return 0, false
	}
	if _, ok := target.Function(args[1]); !ok {
		return 0, false
	}
	qs, err := quickshell()
	if err != nil {
		return 0, false
	}
	cmd := exec.Command(qs, append([]string{"-p", dir, "ipc", "call", target.Name}, args[1:]...)...)
	var stderr bytes.Buffer
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, &stderr
	if err := cmd.Run(); err != nil {
		// The instance went away or refused the call: the launcher retries,
		// starts the shell if needed and reports the error in its own words.
		return 0, false
	}
	os.Stderr.Write(stderr.Bytes())
	return 0, true
}

func runningShell() (string, bool) {
	for _, dir := range shell.ConfigDirs() {
		if real, ok := shell.Installed(dir); ok && shell.Running(real) {
			return real, true
		}
	}
	return "", false
}

// quickshell is the executable of the running inir shell (Quickshell or qs-rs), so calls
// reach it with its own runtime; with no shell running, the one the launcher starts
// (scripts/inir find_qs): INIR_QS_BIN, qs-rs where setup puts it, then the installed qs.
func quickshell() (string, error) {
	if exe, ok := runningShellExe(); ok {
		return exe, nil
	}
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(os.Getenv("HOME"), ".local", "share")
	}
	for _, p := range []string{os.Getenv("INIR_QS_BIN"), filepath.Join(data, "inir", "runtime", "current", "bin", "qs"), "/usr/bin/qs"} {
		if st, err := os.Stat(p); p != "" && err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return exec.LookPath("qs")
}

// runningShellExe finds a live instance of the inir shell in Quickshell's runtime dir
// (by-pid links whose instance is under by-shell/inir) and returns its executable.
func runningShellExe() (string, bool) {
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

// shellCall makes one IPC call on the running shell; false when none runs or
// the call fails.
func shellCall(target string, args ...string) bool {
	dir, ok := runningShell()
	if !ok {
		return false
	}
	qs, err := quickshell()
	if err != nil {
		return false
	}
	return exec.Command(qs, append([]string{"-p", dir, "ipc", "call", target}, args...)...).Run() == nil
}

// delegate replaces this process with the shell's bash launcher.
func delegate(args []string) {
	launcher, err := bashLauncher()
	if err != nil {
		ui.Log("inir").Error(err)
		os.Exit(1)
	}
	argv := append([]string{launcher}, args...)
	if err := syscall.Exec(launcher, argv, delegatedEnv()); err != nil {
		ui.Log("inir").Error(err)
		os.Exit(1)
	}
}

// delegatedEnv tells scripts/inir (and setup) that the binary already had its
// turn, so they do not hand the command back.
func delegatedEnv() []string {
	return append(os.Environ(), "INIR_CLI_DELEGATED=1")
}

// bashLauncher is the scripts/inir that execed this binary (INIR_BASH_LAUNCHER),
// or the installed shell's.
func bashLauncher() (string, error) {
	if l := os.Getenv("INIR_BASH_LAUNCHER"); l != "" {
		return l, nil
	}
	for _, dir := range shell.ConfigDirs() {
		if real, ok := shell.Installed(dir); ok {
			l := filepath.Join(real, "scripts", "inir")
			if st, err := os.Stat(l); err == nil && st.Mode()&0o111 != 0 {
				return l, nil
			}
		}
	}
	return "", errors.New("no iNiR shell installed (looked for scripts/inir next to shell.qml)")
}
