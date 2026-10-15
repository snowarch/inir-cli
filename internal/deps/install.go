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
	// installedAll answers installed for many packages in one query; nil when the distro asks one by one.
	// ok is false when the query itself failed.
	installedAll func(pkgs []string) (set map[string]bool, ok bool)
	available    func(pkg string) bool
	install      func(pkgs []string, yes bool) error
	// refresh brings the package index up to date before anything is resolved. On Arch that is the full
	// upgrade setup install does too: a refresh without it is a partial upgrade.
	refresh func(yes bool) error
}

// ManagerFor returns the package manager of a distro from deps.Distros.
func ManagerFor(distro string, sys System) (*Manager, error) {
	ok := func(name string, args ...string) bool { _, code := sys.Query(name, args...); return code == 0 }
	m := &Manager{sys: sys}
	switch distro {
	case "arch":
		m.installed = func(p string) bool { return ok("pacman", "-T", p) }
		// pacman -T prints the arguments that are not satisfied and exits 127 when there are any.
		m.installedAll = func(pkgs []string) (map[string]bool, bool) {
			out, code := sys.Query("pacman", append([]string{"-T"}, pkgs...)...)
			if code != 0 && code != 127 {
				return nil, false
			}
			set := map[string]bool{}
			for _, p := range pkgs {
				set[p] = true
			}
			for _, p := range strings.Fields(out) {
				set[p] = false
			}
			return set, true
		}
		m.available = func(p string) bool { return ok("pacman", "-Si", p) || ok("pacman", "-Sg", p) }
		m.refresh = func(yes bool) error {
			return sys.Run("sudo", yesFlag([]string{"pacman", "-Syu"}, yes, "--noconfirm")...)
		}
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
		m.refresh = func(bool) error { return sys.Run("sudo", "dnf", "makecache") }
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
		// dpkg-query prints a line for each known package and exits 1 when some are unknown.
		m.installedAll = func(pkgs []string) (map[string]bool, bool) {
			out, code := sys.Query("dpkg-query", append([]string{"-W", "-f=${Package}\t${db:Status-Abbrev}\n"}, pkgs...)...)
			if code != 0 && code != 1 {
				return nil, false
			}
			set := map[string]bool{}
			for _, line := range strings.Split(out, "\n") {
				if name, status, found := strings.Cut(line, "\t"); found && strings.HasPrefix(status, "ii") {
					set[name] = true
				}
			}
			return set, true
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
		m.refresh = func(bool) error { return sys.Run("sudo", "apt-get", "update") }
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
		m.refresh = func(yes bool) error { return sys.Run("sudo", yesFlag([]string{"xbps-install", "-S"}, yes, "-y")...) }
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

// Refresh updates the package index (Arch: a full upgrade).
func (m *Manager) Refresh(yes bool) error { return m.refresh(yes) }

func yesFlag(args []string, yes bool, flag string) []string {
	if yes {
		return append(args, flag)
	}
	return args
}

// HasAURHelper says whether paru or yay is there.
func (m *Manager) HasAURHelper() bool {
	for _, helper := range []string{"paru", "yay"} {
		if _, code := m.sys.Query("sh", "-c", "command -v "+helper); code == 0 {
			return true
		}
	}
	return false
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
	installed := m.installed
	if m.installedAll != nil {
		all := append(append([]string{}, p.Packages...), p.AUR...)
		for _, alts := range p.First {
			for _, alt := range alts {
				all = append(all, strings.Split(alt, "+")...)
			}
		}
		if set, ok := m.installedAll(all); ok {
			installed = func(pkg string) bool { return set[pkg] }
		}
	}
	want := append([]string{}, p.Packages...)
	for _, alts := range p.First {
		for _, alt := range alts {
			parts := strings.Split(alt, "+")
			all := true
			for _, part := range parts {
				all = all && (installed(part) || m.available(part))
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
		case installed(pkg):
		case m.available(pkg):
			s.Install = append(s.Install, pkg)
		default:
			s.Unavailable = append(s.Unavailable, pkg)
		}
	}
	for _, pkg := range p.AUR {
		if !installed(pkg) {
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
