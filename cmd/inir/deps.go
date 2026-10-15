package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/snowarch/inir-cli/internal/deps"
	"github.com/snowarch/inir-cli/internal/doctor"
	"github.com/snowarch/inir-cli/internal/shell"
)

const depsUsage = "Usage: inir deps plan|install [--distro D] [--no-audio] [--no-toolkit] [--no-screencapture] [--no-fonts] [--table PATH] [--json] [-y] [--dry-run]"

// runDeps is `inir deps plan|install`: what an install puts on this machine, from the shell's
// sdata/deps.toml, and installing it. Packages go through the distro's package manager; whatever the
// repositories lack goes through `setup provider <recipe>`, the same bash steps setup install runs.
func runDeps(args []string) int {
	if len(args) == 0 || (args[0] != "plan" && args[0] != "install") {
		fmt.Fprintln(os.Stderr, depsUsage)
		return 2
	}
	install := args[0] == "install"
	distro, table, asJSON, yes, dryRun := doctor.DetectOS(), "", false, false, false
	on := map[string]bool{}
	for _, g := range deps.Groups {
		on[g] = true
	}
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "-h" || a == "--help":
			fmt.Println(depsUsage)
			return 0
		case a == "--json":
			asJSON = true
		case install && (a == "-y" || a == "--yes"):
			yes = true
		case install && a == "--dry-run":
			dryRun = true
		case a == "--distro" && i+1 < len(rest):
			i++
			distro = rest[i]
		case a == "--table" && i+1 < len(rest):
			i++
			table = rest[i]
		case strings.HasPrefix(a, "--no-") && on[strings.TrimPrefix(a, "--no-")]:
			on[strings.TrimPrefix(a, "--no-")] = false
		default:
			fmt.Fprintf(os.Stderr, "inir deps %s: unknown argument %q\n", args[0], a)
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
	if install {
		return installPlan(p, on, yes, dryRun)
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

// installPlan prepares package sources, installs what is missing and runs the providers.
func installPlan(p *deps.Plan, on map[string]bool, yes, dryRun bool) int {
	setupDir := doctor.NewEnv().SetupDir
	setup := func(args ...string) *exec.Cmd {
		if yes {
			args = append(args, "-y")
		}
		cmd := exec.Command(filepath.Join(setupDir, "setup"), args...)
		cmd.Dir = setupDir
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		cmd.Env = append(os.Environ(), "INIR_CLI_DELEGATED=1")
		for g, enabled := range on {
			if !enabled {
				cmd.Env = append(cmd.Env, "INSTALL_"+strings.ToUpper(g)+"=false")
			}
		}
		return cmd
	}
	providers := map[string]bool{}
	if out, err := exec.Command(filepath.Join(setupDir, "setup"), "provider").Output(); err == nil {
		for _, name := range strings.Fields(string(out)) {
			providers[name] = true
		}
	}
	m, err := deps.ManagerFor(p.Distro, deps.RealSystem())
	if err != nil {
		fmt.Fprintln(os.Stderr, "inir deps:", err)
		return 1
	}
	// What is installed is known without the network: when nothing is missing, sources and the index
	// stay as they are (on Arch the refresh is a full system upgrade).
	if s := m.Resolve(p, providers); len(s.Install)+len(s.Unavailable)+len(s.AUR)+len(s.Providers) == 0 {
		fmt.Println("==> everything is installed")
		return 0
	}
	if providers["repos"] {
		fmt.Println("==> package sources")
		if !dryRun {
			if err := setup("provider", "repos").Run(); err != nil {
				fmt.Fprintln(os.Stderr, "inir deps: package sources:", err)
			}
		}
	}
	if !dryRun {
		fmt.Println("==> package index")
		if err := m.Refresh(yes); err != nil {
			fmt.Fprintln(os.Stderr, "inir deps: package index:", err)
		}
	}
	s := m.Resolve(p, providers)
	fmt.Printf("==> packages: %d to install", len(s.Install))
	if len(s.Unavailable) > 0 {
		fmt.Printf(", not in the repositories: %s", strings.Join(s.Unavailable, " "))
	}
	fmt.Println()
	if len(s.AUR) > 0 {
		fmt.Printf("==> AUR: %s\n", strings.Join(s.AUR, " "))
	}
	if len(s.Providers) > 0 {
		fmt.Printf("==> providers: %s\n", strings.Join(s.Providers, " "))
	}
	if dryRun {
		fmt.Println(strings.Join(s.Install, " "))
		return 0
	}
	code := 0
	if err := m.InstallPackages(s, yes); err != nil {
		fmt.Fprintln(os.Stderr, "inir deps: packages:", err)
		code = 1
	}
	if len(s.AUR) > 0 && !m.HasAURHelper() && providers["aur-helper"] {
		fmt.Println("==> provider aur-helper")
		if err := setup("provider", "aur-helper").Run(); err != nil {
			fmt.Fprintln(os.Stderr, "inir deps: provider aur-helper:", err)
		}
	}
	if err := m.InstallAUR(s, yes); err != nil {
		fmt.Fprintln(os.Stderr, "inir deps:", err)
		code = 1
	}
	for _, recipe := range s.Providers {
		fmt.Println("==> provider", recipe)
		if err := setup("provider", recipe).Run(); err != nil {
			fmt.Fprintf(os.Stderr, "inir deps: provider %s: %v\n", recipe, err)
			code = 1
		}
	}
	return code
}
