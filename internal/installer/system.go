// Package installer is `inir install`: a wizard in front of setup's install
// pipeline. It asks everything up front, authenticates once, then runs setup
// quietly and shows its stages as they happen.
package installer

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/snowarch/inir-cli/internal/doctor"
)

// System is what the wizard knows about the machine before asking anything.
type System struct {
	Name     string // PRETTY_NAME from os-release
	OS       string // setup's OS_GROUP_ID: arch, fedora, debian...
	Release  string // UBUNTU_CODENAME, else VERSION_CODENAME: resolute, trixie...
	FirstRun bool   // no iNiR install has finished here yet
	Configs  bool   // there are configs an install would overwrite (niri, fish, foot...)
	SDDM     bool   // the login screen can take the pixel theme
	Sudo     bool   // sudo works without asking right now
	Umbriel  bool   // Umbriel, the other compositor iNiR runs on, is installed
	// Others are other shells installed here: they stay unless the person asks
	// to remove them; Replace are Quickshell builds setup swaps for the stable one.
	Others, Replace []string
}

// Other Quickshell shells setup finds on Arch (sdata/lib/conflicts.sh).
var otherShells = []string{
	"cachyos-niri-noctalia", "noctalia-shell", "noctalia-qs", "noctalia-qs-git",
	"dms-shell", "dms-shell-git", "caelestia-shell", "caelestia-shell-git", "bms-shell-bin",
}

// Detect reads the machine the way setup install will see it.
func Detect(confDir string) System {
	rel := osRelease()
	s := System{OS: doctor.DetectOS(), Name: rel["PRETTY_NAME"], Release: rel["UBUNTU_CODENAME"]}
	if s.Name == "" {
		s.Name = "Linux"
	}
	if s.Release == "" {
		s.Release = rel["VERSION_CODENAME"]
	}
	_, err := os.Stat(filepath.Join(confDir, "installed_true"))
	s.FirstRun = err != nil
	cfg := os.Getenv("XDG_CONFIG_HOME")
	if cfg == "" {
		cfg = filepath.Join(os.Getenv("HOME"), ".config")
	}
	for _, d := range []string{"niri", "fish", "foot", "kitty", "matugen", "fuzzel"} {
		if _, err := os.Stat(filepath.Join(cfg, d)); err == nil {
			s.Configs = true
			break
		}
	}
	_, err = exec.LookPath("sddm")
	s.SDDM = err == nil
	s.Sudo = exec.Command("sudo", "-n", "true").Run() == nil
	_, err = exec.LookPath("umbriel")
	s.Umbriel = err == nil
	if s.OS == "arch" {
		s.Others = pacmanInstalled(otherShells...)
		s.Replace = pacmanInstalled("quickshell-git", "quickshell-bin")
	}
	return s
}

// umbrielInstallable is whether setup can install Umbriel itself here (compositor_install_umbriel_package: pacman
// with the AUR, dnf with Terra, Noctalia's APT repository for the releases it builds, AerynOS and Void by building it).
func umbrielInstallable(osID, release string) bool {
	switch osID {
	case "arch", "fedora", "aerynos", "void":
		return true
	case "debian", "ubuntu":
		return release == "trixie" || release == "resolute" || release == "stonking"
	}
	return false
}

// umbrielBuilt is where no repository packages Umbriel, so setup builds it (build_umbriel_source).
func umbrielBuilt(osID string) bool { return osID == "aerynos" || osID == "void" }

// osRelease is os-release's keys, unquoted.
func osRelease() map[string]string {
	keys := map[string]string{}
	f, err := os.Open("/etc/os-release")
	if err != nil {
		// AerynOS keeps /etc stateless: its os-release may live only in /usr/lib
		if f, err = os.Open("/usr/lib/os-release"); err != nil {
			return keys
		}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			keys[k] = strings.Trim(v, `"'`)
		}
	}
	return keys
}

// pacmanInstalled is the subset of names pacman has installed.
func pacmanInstalled(names ...string) []string {
	out, _ := exec.Command("pacman", append([]string{"-Qq"}, names...)...).Output()
	return strings.Fields(string(out))
}
