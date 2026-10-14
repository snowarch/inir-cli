package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/snowarch/inir-cli/internal/deps"
	"github.com/snowarch/inir-cli/internal/doctor"
	"github.com/snowarch/inir-cli/internal/shell"
)

// runDeps is `inir deps plan [--distro D] [--no-GROUP...] [--table PATH] [--json]`:
// what an install would put on this machine, from the shell's sdata/deps.toml.
func runDeps(args []string) int {
	if len(args) == 0 || args[0] != "plan" {
		fmt.Fprintln(os.Stderr, "Usage: inir deps plan [--distro D] [--no-audio] [--no-toolkit] [--no-screencapture] [--no-fonts] [--table PATH] [--json]")
		return 2
	}
	distro, table, asJSON := doctor.DetectOS(), "", false
	on := map[string]bool{}
	for _, g := range deps.Groups {
		on[g] = true
	}
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--json":
			asJSON = true
		case a == "--distro" && i+1 < len(rest):
			i++
			distro = rest[i]
		case a == "--table" && i+1 < len(rest):
			i++
			table = rest[i]
		case strings.HasPrefix(a, "--no-") && on[strings.TrimPrefix(a, "--no-")]:
			on[strings.TrimPrefix(a, "--no-")] = false
		default:
			fmt.Fprintf(os.Stderr, "inir deps plan: unknown argument %q\n", a)
			return 2
		}
	}
	if table == "" {
		table = installedFile("sdata", "deps.toml")
	}
	if table == "" {
		fmt.Fprintln(os.Stderr, "inir deps: no installed shell with sdata/deps.toml")
		return 1
	}
	t, err := deps.Load(table)
	if err != nil {
		fmt.Fprintln(os.Stderr, "inir deps:", err)
		return 1
	}
	p, err := t.Plan(distro, on)
	if err != nil {
		fmt.Fprintln(os.Stderr, "inir deps:", err)
		return 1
	}
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(p)
		return 0
	}
	fmt.Printf("%s, groups: core %s\n", p.Distro, strings.Join(p.Groups, " "))
	fmt.Printf("packages (%d): %s\n", len(p.Packages), strings.Join(p.Packages, " "))
	if len(p.AUR) > 0 {
		fmt.Printf("AUR (%d): %s\n", len(p.AUR), strings.Join(p.AUR, " "))
	}
	for _, f := range p.First {
		fmt.Printf("first available of: %s\n", strings.Join(f, ", "))
	}
	if len(p.Builds) > 0 {
		fmt.Printf("providers, when still missing: %s\n", strings.Join(p.Builds, " "))
	}
	return 0
}

// installedFile is a file of the installed shell, "" when there is none.
func installedFile(parts ...string) string {
	for _, dir := range shell.ConfigDirs() {
		if real, ok := shell.Installed(dir); ok {
			p := filepath.Join(append([]string{real}, parts...)...)
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}
