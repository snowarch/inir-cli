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
				if len(args) > 3 {
					out, code := "", 0
					for _, q := range args[2:] {
						if installed[q] {
							out += q + "\tii \n"
						} else {
							code = 1
						}
					}
					return out, code
				}
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

func TestArchInstalledInOneQuery(t *testing.T) {
	queries := 0
	sys := System{
		Query: func(name string, args ...string) (string, int) {
			queries++
			if name == "pacman" && args[0] == "-T" && len(args) > 2 {
				return "fish\nfoot\nnope\nfonts\n", 127
			}
			if name == "pacman" && args[0] == "-Sl" {
				return "extra fish 4.0-1\nextra foot 1.2-1 [installed: 1.1-1]\ncore jq 1.7-1\n", 0
			}
			if name == "pacman" && args[0] == "-Sg" {
				return "fonts noto-fonts\nfonts ttf-dejavu\n", 0
			}
			return "", 1
		},
		Run: func(string, ...string) error { return nil },
	}
	m, _ := ManagerFor("arch", sys)
	s := m.Resolve(&Plan{Distro: "arch", Packages: []string{"jq", "fish", "foot", "nope", "fonts"}, AUR: []string{"darkly-bin"}}, nil)
	if got := strings.Join(s.Install, " "); got != "fish foot fonts" || len(s.AUR) != 0 {
		t.Errorf("install = %q, aur = %q", got, s.AUR)
	}
	if got := strings.Join(s.Unavailable, " "); got != "nope" {
		t.Errorf("unavailable = %q", got)
	}
	if queries != 3 {
		t.Errorf("%d queries, want one for what is installed, one for the repos and one for groups", queries)
	}
	queries = 0
	sys.Query = func(name string, args ...string) (string, int) { queries++; return "", 0 }
	m, _ = ManagerFor("arch", sys)
	m.Resolve(&Plan{Distro: "arch", Packages: []string{"jq"}}, nil)
	if queries != 1 {
		t.Errorf("all installed: %d queries, want just the one for what is installed", queries)
	}
}
