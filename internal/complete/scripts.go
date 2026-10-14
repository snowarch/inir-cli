package complete

import (
	"embed"
	"fmt"
	"strings"
)

//go:embed scripts/inir.bash scripts/inir.zsh scripts/inir.fish
var scripts embed.FS

// Shells are the shells inir writes completions for.
var Shells = []string{"bash", "zsh", "fish"}

// Script returns the completion script for a shell.
func Script(shell string) (string, error) {
	data, err := scripts.ReadFile("scripts/inir." + shell)
	if err != nil {
		return "", fmt.Errorf("no completions for %q (bash, zsh or fish)", shell)
	}
	return string(data), nil
}

// Format writes a result the way the shell's script reads it. bash gets bare
// values already filtered by the typed word; zsh and fish filter themselves
// (fish also matches inside words), so they get every candidate with its text.
func Format(shell string, r Result, cur string) string {
	if r.Dirs {
		return ":dirs\n"
	}
	if len(r.Candidates) == 0 {
		if r.Hint != "" {
			return ":hint " + r.Hint + "\n"
		}
		return ""
	}
	var b strings.Builder
	for _, c := range r.Candidates {
		switch shell {
		case "bash":
			if strings.HasPrefix(c.Value, cur) {
				b.WriteString(c.Value + "\n")
			}
		case "zsh":
			ns := "0"
			if c.NoSpace() {
				ns = "1"
			}
			fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", c.Kind, ns, c.Value, c.Desc)
		default:
			if c.Desc != "" {
				fmt.Fprintf(&b, "%s\t%s\n", c.Value, c.Desc)
			} else {
				b.WriteString(c.Value + "\n")
			}
		}
	}
	return b.String()
}
