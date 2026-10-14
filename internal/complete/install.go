package complete

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Marker is in every script this binary writes, so it can replace its own
// files (and the links scripts/inir made) but never a file of the user's.
const Marker = "answered by the inir binary on every Tab"

// Dest is where a shell loads completions by itself, the same places
// scripts/inir's install_completions links to.
func Dest(shell string) (string, error) {
	data := envOr("XDG_DATA_HOME", filepath.Join(os.Getenv("HOME"), ".local", "share"))
	config := envOr("XDG_CONFIG_HOME", filepath.Join(os.Getenv("HOME"), ".config"))
	switch shell {
	case "bash":
		return filepath.Join(data, "bash-completion", "completions", "inir"), nil
	case "zsh":
		return filepath.Join(data, "zsh", "site-functions", "_inir"), nil
	case "fish":
		return filepath.Join(config, "fish", "completions", "inir.fish"), nil
	}
	return "", fmt.Errorf("completions exist for bash, zsh and fish, not %s", shell)
}

// Install writes the completion script for a shell where it loads it, and
// tells w what else the shell needs.
func Install(shell string, w io.Writer) error {
	dest, err := Dest(shell)
	if err != nil {
		return err
	}
	script, err := Script(shell)
	if err != nil {
		return err
	}
	if st, err := os.Lstat(dest); err == nil && st.Mode().IsRegular() {
		data, err := os.ReadFile(dest)
		if err != nil {
			return err
		}
		if !bytes.Contains(data, []byte(Marker)) {
			fmt.Fprintf(w, "Left %s as it is: it is a file of your own, not one inir made.\n", dest)
			return nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".inir-tmp"
	if err := os.WriteFile(tmp, []byte(script), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	fmt.Fprintf(w, "Installed %s completions: %s\n", shell, dest)
	switch shell {
	case "bash":
		if !exists("/usr/share/bash-completion/bash_completion") && !exists("/etc/bash_completion") {
			fmt.Fprintln(w, "bash-completion is not installed; install it (package bash-completion) so bash loads them.")
		}
		fmt.Fprintln(w, "Open a new terminal to use them.")
	case "zsh":
		dir := filepath.Dir(dest)
		if _, err := exec.LookPath("zsh"); err == nil && !inZshFpath(dir) {
			fmt.Fprintf(w, "Add this line to ~/.zshrc before compinit, then open a new terminal:\n  fpath=(%s $fpath)\n", dir)
		} else {
			fmt.Fprintln(w, "Open a new terminal to use them.")
		}
	case "fish":
		fmt.Fprintln(w, "fish loads them in new terminals.")
	}
	return nil
}

func inZshFpath(dir string) bool {
	out, err := exec.Command("zsh", "-ic", "print -l -- $fpath").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if line == dir {
			return true
		}
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
