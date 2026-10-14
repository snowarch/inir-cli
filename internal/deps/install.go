package deps

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// System runs commands for an install; tests replace it.
type System struct {
	// Query runs a read-only command and returns its stdout and exit code.
	Query func(name string, args ...string) (string, int)
	// Run runs a command that changes the system, with the terminal attached.
	Run func(name string, args ...string) error
}

// RealSystem talks to this machine.
func RealSystem() System {
	return System{
		Query: func(name string, args ...string) (string, int) {
			out, err := exec.Command(name, args...).Output()
			if ee, ok := err.(*exec.ExitError); ok {
				return string(out), ee.ExitCode()
			} else if err != nil {
				return "", 127
			}
			return string(out), 0
		},
		Run: func(name string, args ...string) error {
			cmd := exec.Command(name, args...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
			cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")
			return cmd.Run()
		},
	}
}

// Manager is one distro's package manager: what is installed, what the repos have, how to install.
type Manager struct {
	sys       System
	installed func(pkg string) bool
	available func(pkg string) bool
	install   func(pkgs []string, yes bool) error
}

// ManagerFor returns the package manager of a distro from deps.Distros.
func ManagerFor(distro string, sys System) (*Manager, error) {
	ok := func(name string, args ...string) bool { _, code := sys.Query(name, args...); return code == 0 }
	m := &Manager{sys: sys}
	switch distro {
	case "arch":
		m.installed = func(p string) bool { return ok("pacman", "-T", p) }
		m.available = func(p string) bool { return ok("pacman", "-Si", p) || ok("pacman", "-Sg", p) }
		m.install = func(pkgs []string, yes bool) error {
			args := []string{"pacman", "-S", "--needed"}
			if yes {
				args = append(args, "--noconfirm")
			}
			return sys.Run("sudo", append(args, pkgs...)...)
		}
	case "fedora":
		m.installed = func(p string) bool { return ok("rpm", "-q", "--whatprovides", p) }
		m.available = func(p string) bool {
			out, code := sys.Query("dnf", "-q", "repoquery", "--whatprovides", p)
			return code == 0 && strings.TrimSpace(out) != ""
		}
		m.install = func(pkgs []string, yes bool) error {
			args := []string{"dnf", "install"}
			if yes {
				args = append(args, "-y")
			}
			return sys.Run("sudo", append(args, pkgs...)...)
		}
	case "debian", "ubuntu":
		m.installed = func(p string) bool {
			out, code := sys.Query("dpkg-query", "-W", "-f=${db:Status-Abbrev}", p)
			return code == 0 && strings.HasPrefix(out, "ii")
		}
		m.available = func(p string) bool {
			out, _ := sys.Query("apt-cache", "policy", p)
			for _, line := range strings.Split(out, "\n") {
				if f := strings.Fields(line); len(f) == 2 && f[0] == "Candidate:" {
					return f[1] != "(none)"
				}
			}
			return false
		}
		m.install = func(pkgs []string, yes bool) error {
			args := []string{"apt-get", "install"}
			if yes {
				args = append(args, "-y")
			}
			return sys.Run("sudo", append(args, pkgs...)...)
		}
	case "void":
		m.installed = func(p string) bool { return ok("xbps-query", p) }
		m.available = func(p string) bool { return ok("xbps-query", "-R", p) }
		m.install = func(pkgs []string, yes bool) error {
			args := []string{"xbps-install"}
			if yes {
				args = append(args, "-y")
			}
			return sys.Run("sudo", append(args, pkgs...)...)
		}
	default:
		return nil, fmt.Errorf("no package manager for %q", distro)
	}
	return m, nil
}

// Steps is what an install of a plan does on this machine, worked out before anything runs.
type Steps struct {
	Install     []string // repository packages still missing
	Unavailable []string // asked for, not in any configured repository
	AUR         []string // Arch: still missing, through the AUR helper
	Providers   []string // recipes to run through `setup provider`
}

// Resolve turns a plan into steps: picks the first available alternative, drops what is installed and sets
// apart what the repositories do not have. Recipes not in providers (this distro's list) are skipped.
func (m *Manager) Resolve(p *Plan, providers map[string]bool) Steps {
	var s Steps
	want := append([]string{}, p.Packages...)
	for _, alts := range p.First {
		for _, alt := range alts {
			parts := strings.Split(alt, "+")
			all := true
			for _, part := range parts {
				all = all && (m.installed(part) || m.available(part))
			}
			if all {
				want = append(want, parts...)
				break
			}
		}
	}
	seen := map[string]bool{}
	for _, pkg := range want {
		if seen[pkg] {
			continue
		}
		seen[pkg] = true
		switch {
		case m.installed(pkg):
		case m.available(pkg):
			s.Install = append(s.Install, pkg)
		default:
			s.Unavailable = append(s.Unavailable, pkg)
		}
	}
	for _, pkg := range p.AUR {
		if !m.installed(pkg) {
			s.AUR = append(s.AUR, pkg)
		}
	}
	for _, recipe := range p.Builds {
		if providers[recipe] {
			s.Providers = append(s.Providers, recipe)
		}
	}
	return s
}

// InstallPackages installs the repository packages of s in one transaction.
func (m *Manager) InstallPackages(s Steps, yes bool) error {
	if len(s.Install) == 0 {
		return nil
	}
	return m.install(s.Install, yes)
}

// InstallAUR installs Arch AUR packages with paru or yay, whichever is there.
func (m *Manager) InstallAUR(s Steps, yes bool) error {
	if len(s.AUR) == 0 {
		return nil
	}
	for _, helper := range []string{"paru", "yay"} {
		if _, code := m.sys.Query("sh", "-c", "command -v "+helper); code == 0 {
			args := []string{"-S", "--needed"}
			if yes {
				args = append(args, "--noconfirm")
			}
			return m.sys.Run(helper, append(args, s.AUR...)...)
		}
	}
	return fmt.Errorf("AUR packages need paru or yay: %s", strings.Join(s.AUR, " "))
}
