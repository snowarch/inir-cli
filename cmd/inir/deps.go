package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/snowarch/inir-cli/internal/deps"
	"github.com/snowarch/inir-cli/internal/doctor"
	"github.com/snowarch/inir-cli/internal/shell"
	"github.com/snowarch/inir-cli/internal/ui"
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
			ui.Log("inir deps "+args[0]).Error("unknown argument", "arg", a)
			return 2
		}
	}
	if table == "" {
		table = installedFile("sdata", "deps.toml")
	}
	if table == "" {
		ui.Log("inir deps").Error("no installed shell with sdata/deps.toml; from your iNiR clone run: ./setup install")
		return 1
	}
	t, err := deps.Load(table)
	if err != nil {
		ui.Log("inir deps").Error(err)
		return 1
	}
	p, err := t.Plan(distro, on)
	if err != nil {
		ui.Log("inir deps").Error(err)
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
	var steps *deps.Steps
	if distro == doctor.DetectOS() {
		if m, err := deps.ManagerFor(distro, deps.RealSystem()); err == nil {
			s := m.Resolve(p, nil)
			steps = &s
		}
	}
	printPlan(os.Stdout, p, steps)
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
		ui.Log("inir deps").Error(err)
		return 1
	}
	// What is installed is known without the network: when nothing is missing, sources and the index
	// stay as they are (on Arch the refresh is a full system upgrade).
	t := ui.Current()
	if s := m.Resolve(p, providers); len(s.Install)+len(s.Unavailable)+len(s.AUR)+len(s.Providers) == 0 {
		ui.Card(os.Stdout, t.Success, "Everything is installed.")
		return 0
	}
	ui.Header(os.Stdout, t, "inir deps", "installing what "+p.Distro+" is missing")
	if providers["repos"] {
		ui.Step(os.Stdout, t, "Package sources")
		if !dryRun {
			if err := setup("provider", "repos").Run(); err != nil {
				ui.Log("inir deps").Warn("package sources were not set up", "err", err)
			}
		}
	}
	if !dryRun {
		ui.Step(os.Stdout, t, "Package index")
		if err := m.Refresh(yes); err != nil {
			ui.Log("inir deps").Warn("package index was not refreshed", "err", err)
		}
	}
	s := m.Resolve(p, providers)
	ui.Step(os.Stdout, t, fmt.Sprintf("Packages: %d to install", len(s.Install)))
	if len(s.Unavailable) > 0 {
		lipgloss.Println("    " + lipgloss.NewStyle().Foreground(t.Error).Render("not in the repositories: "+strings.Join(s.Unavailable, " ")))
	}
	if len(s.AUR) > 0 {
		ui.Step(os.Stdout, t, "AUR: "+strings.Join(s.AUR, " "))
	}
	if len(s.Providers) > 0 {
		ui.Step(os.Stdout, t, "Built from source: "+strings.Join(s.Providers, " "))
	}
	if dryRun {
		lipgloss.Println("    " + lipgloss.NewStyle().Foreground(t.Muted).Render(strings.Join(s.Install, " ")))
		return 0
	}
	code := 0
	if err := m.InstallPackages(s, yes); err != nil {
		ui.Log("inir deps").Error("packages did not install", "err", err, "retry", "inir deps install")
		code = 1
	}
	if len(s.AUR) > 0 && !m.HasAURHelper() && providers["aur-helper"] {
		ui.Step(os.Stdout, t, "AUR helper")
		if err := setup("provider", "aur-helper").Run(); err != nil {
			ui.Log("inir deps").Warn("AUR helper was not built", "err", err)
		}
	}
	if err := m.InstallAUR(s, yes); err != nil {
		ui.Log("inir deps").Error("AUR packages did not install", "err", err, "retry", "inir deps install")
		code = 1
	}
	for _, recipe := range s.Providers {
		ui.Step(os.Stdout, t, "Building "+recipe)
		if err := setup("provider", recipe).Run(); err != nil {
			ui.Log("inir deps").Error("build failed", "provider", recipe, "err", err, "retry", "inir deps install")
			code = 1
		}
	}
	return code
}
