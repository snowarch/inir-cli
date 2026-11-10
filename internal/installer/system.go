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
	s := System{OS: doctor.DetectOS(), Name: osName()}
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

func osName() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return "Linux"
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(sc.Text(), "PRETTY_NAME="); ok {
			return strings.Trim(v, `"'`)
		}
	}
	return "Linux"
}

// pacmanInstalled is the subset of names pacman has installed.
func pacmanInstalled(names ...string) []string {
	out, _ := exec.Command("pacman", append([]string{"-Qq"}, names...)...).Output()
	return strings.Fields(string(out))
}
