// Command inir is the iNiR command line. IPC calls to a running shell are made
// here directly; everything else is handed to the shell's bash launcher
// (scripts/inir) until it moves into this binary.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/snowarch/inir-cli/internal/ipc"
	"github.com/snowarch/inir-cli/internal/shell"
)

// Commands the bash launcher handles itself before it treats a word as an IPC
// target (the explicit cases of its dispatch).
var launcherCommands = map[string]bool{
	"install": true, "start": true, "stop": true, "run": true, "restart": true, "kill": true,
	"logs": true, "terminal": true, "browser": true, "close-window": true, "colorpicker": true,
	"ipc": true, "dev": true, "settings": true, "open": true, "settings-window": true,
	"waffle-settings-window": true, "welcome": true, "repair": true, "path": true,
	"test-local": true, "setup": true, "service": true, "cleanup-orphans": true, "doctor": true,
	"migrate": true, "status": true, "update": true, "reinstall": true, "rollback": true,
	"my-changes": true, "uninstall": true, "config": true, "info": true, "backup": true,
	"version": true, "theme": true, "hub": true, "bind": true, "completions": true, "help": true,
}

func main() {
	args := os.Args[1:]
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
	if launcherCommands[args[0]] || (args[0] == "lock" && args[1] == "prepareSleep") {
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

func quickshell() (string, error) {
	if _, err := os.Stat("/usr/bin/qs"); err == nil {
		return "/usr/bin/qs", nil
	}
	return exec.LookPath("qs")
}

// delegate replaces this process with the shell's bash launcher.
func delegate(args []string) {
	launcher, err := bashLauncher()
	if err != nil {
		fmt.Fprintln(os.Stderr, "inir:", err)
		os.Exit(1)
	}
	argv := append([]string{launcher}, args...)
	if err := syscall.Exec(launcher, argv, os.Environ()); err != nil {
		fmt.Fprintln(os.Stderr, "inir:", err)
		os.Exit(1)
	}
}

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
