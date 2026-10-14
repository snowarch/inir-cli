package deps

import (
	"strings"
	"testing"
)

// fake answers like a Debian box with a few packages installed and a few in the repos.
func fake(installed, available map[string]bool, ran *[]string) System {
	return System{
		Query: func(name string, args ...string) (string, int) {
			p := args[len(args)-1]
			switch name {
			case "dpkg-query":
				if installed[p] {
					return "ii ", 0
				}
				return "", 1
			case "apt-cache":
				if available[p] || installed[p] {
					return "  Candidate: 1.0\n", 0
				}
				return "  Candidate: (none)\n", 0
			}
			return "", 1
		},
		Run: func(name string, args ...string) error {
			*ran = append(*ran, name+" "+strings.Join(args, " "))
			return nil
		},
	}
}

func TestResolveAndInstall(t *testing.T) {
	var ran []string
	m, err := ManagerFor("debian", fake(map[string]bool{"jq": true}, map[string]bool{"fish": true, "b": true, "c": true}, &ran))
	if err != nil {
		t.Fatal(err)
	}
	p := &Plan{Distro: "debian", Packages: []string{"jq", "fish", "niri"}, First: [][]string{{"a", "b+c"}},
		Builds: []string{"niri", "awww"}}
	s := m.Resolve(p, map[string]bool{"niri": true})
	if got := strings.Join(s.Install, " "); got != "fish b c" {
		t.Errorf("install = %q, want missing available packages plus the first available alternative", got)
	}
	if got := strings.Join(s.Unavailable, " "); got != "niri" {
		t.Errorf("unavailable = %q", got)
	}
	if got := strings.Join(s.Providers, " "); got != "niri" {
		t.Errorf("providers = %q, want only recipes this distro provides", got)
	}
	if err := m.InstallPackages(s, true); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0] != "sudo apt-get install -y fish b c" {
		t.Errorf("ran %q", ran)
	}
}

func TestUnknownDistro(t *testing.T) {
	if _, err := ManagerFor("gentoo", System{}); err == nil {
		t.Error("gentoo has no manager yet; want an error")
	}
}
