package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/snowarch/inir-cli/internal/complete"
	"github.com/snowarch/inir-cli/internal/ipc"
	"github.com/snowarch/inir-cli/internal/shell"
)

// runComplete answers the completion scripts: `inir __complete SHELL -- WORD...`,
// the last word being the one under the cursor.
func runComplete(args []string) int {
	if len(args) < 2 || args[1] != "--" {
		fmt.Fprintln(os.Stderr, "usage: inir __complete bash|zsh|fish -- WORD...")
		return 2
	}
	words := args[2:]
	cur := ""
	if len(words) > 0 {
		cur = words[len(words)-1]
	}
	fmt.Print(complete.Format(args[0], complete.Complete(words, installedRegistry()), cur))
	return 0
}

// installedRegistry reads ipc.json from the installed shell, running or not.
func installedRegistry() *ipc.Registry {
	for _, dir := range shell.ConfigDirs() {
		if real, ok := shell.Installed(dir); ok {
			if reg, err := ipc.Load(filepath.Join(real, "scripts", "lib", "ipc.json")); err == nil {
				return reg
			}
		}
	}
	return nil
}

// runCompletions is `inir completions <bash|zsh|fish|install [shell]>`, with
// the launcher's words.
func runCompletions(args []string) int {
	what := ""
	if len(args) > 0 {
		what = args[0]
	}
	switch what {
	case "install":
		sh := ""
		if len(args) > 1 {
			sh = args[1]
		}
		if sh == "" {
			sh = filepath.Base(os.Getenv("SHELL"))
			if sh == "." || sh == "/" {
				sh = "bash"
			}
		}
		if _, err := complete.Dest(sh); err != nil {
			fmt.Fprintf(os.Stderr, "Completions exist for bash, zsh and fish, not %s.\n", sh)
			return 1
		}
		if err := complete.Install(sh, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "inir:", err)
			return 1
		}
		return 0
	case "bash", "zsh", "fish":
		script, _ := complete.Script(what)
		fmt.Print(script)
		return 0
	case "":
		fmt.Print(`Usage: inir completions <bash|zsh|fish>

Print shell completion script to stdout.

Examples:
  eval "$(inir completions bash)"     # Add to .bashrc
  inir completions zsh > ~/.zsh/completions/_inir
  inir completions fish | source        # Add to config.fish

Or let inir put them where your shell looks:
  inir completions install [bash|zsh|fish]
`)
		return 0
	}
	fmt.Fprintf(os.Stderr, "Unknown shell: '%s'. Supported: bash, zsh, fish\n", what)
	return 1
}
