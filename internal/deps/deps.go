// Package deps reads the shell's sdata/deps.toml, the table of what iNiR
// installs per distro, and turns it into a plan for one machine: the
// repository packages, AUR packages, first-available choices and providers to
// run, for the install groups that are on.
package deps

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// SchemaVersion is the deps.toml schema this binary understands.
const SchemaVersion = 1

// Groups other than core, in the order setup's --no-* flags name them.
var Groups = []string{"audio", "toolkit", "screencapture", "fonts"}

// Distros the table has keys for; ubuntu falls back to debian.
var Distros = []string{"arch", "fedora", "debian", "ubuntu", "void"}

type Table struct {
	Schema int   `toml:"schema"`
	Deps   []Dep `toml:"dep"`
}

type Dep struct {
	Name   string   `toml:"name"`
	Group  string   `toml:"group"`
	Groups []string `toml:"groups"`
	Arch   any      `toml:"arch"`
	Fedora any      `toml:"fedora"`
	Debian any      `toml:"debian"`
	Ubuntu any      `toml:"ubuntu"`
	Void   any      `toml:"void"`
}

// Spec is one distro's way to provide a dep.
type Spec struct {
	Packages []string // from the distro's repositories
	AUR      []string
	First    []string // the first of these the repositories have ("a+b" installs both)
	Build    string   // provider recipe, run when the thing is still missing
	Groups   []string // this distro's groups, when they differ from the dep's
}

// Load reads and checks a deps.toml.
func Load(path string) (*Table, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t Table
	md, err := toml.Decode(string(data), &t)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Inside a distro's table the keys are read by Spec; anything else unknown is a typo.
	for _, key := range md.Undecoded() {
		if len(key) >= 3 && key[0] == "dep" && isDistro(key[1]) {
			continue
		}
		return nil, fmt.Errorf("%s: unknown key %s", path, key)
	}
	if t.Schema != SchemaVersion {
		return nil, fmt.Errorf("%s: schema %d, this inir reads %d", path, t.Schema, SchemaVersion)
	}
	seen := map[string]bool{}
	for i := range t.Deps {
		d := &t.Deps[i]
		if d.Name == "" || seen[d.Name] {
			return nil, fmt.Errorf("%s: dep %d has no name or repeats %q", path, i+1, d.Name)
		}
		seen[d.Name] = true
		for _, g := range d.groups() {
			if !validGroup(g) {
				return nil, fmt.Errorf("%s: %s: no group %q", path, d.Name, g)
			}
		}
		for _, distro := range Distros {
			if _, err := d.Spec(distro); err != nil {
				return nil, fmt.Errorf("%s: %s: %s: %w", path, d.Name, distro, err)
			}
		}
	}
	return &t, nil
}

func isDistro(name string) bool {
	for _, d := range Distros {
		if d == name {
			return true
		}
	}
	return false
}

func validGroup(g string) bool {
	if g == "core" {
		return true
	}
	for _, x := range Groups {
		if g == x {
			return true
		}
	}
	return false
}

func (d *Dep) groups() []string {
	if len(d.Groups) > 0 {
		return d.Groups
	}
	return []string{d.Group}
}

func (d *Dep) raw(distro string) any {
	switch distro {
	case "arch":
		return d.Arch
	case "fedora":
		return d.Fedora
	case "debian":
		return d.Debian
	case "ubuntu":
		if d.Ubuntu != nil {
			return d.Ubuntu
		}
		return d.Debian
	case "void":
		return d.Void
	}
	return nil
}

// Spec is how distro provides the dep, nil when it does not install it.
func (d *Dep) Spec(distro string) (*Spec, error) {
	raw := d.raw(distro)
	if raw == nil {
		return nil, nil
	}
	s := &Spec{}
	switch v := raw.(type) {
	case string, []any:
		pkgs, err := words(v)
		if err != nil {
			return nil, err
		}
		s.Packages = pkgs
		return s, nil
	case map[string]any:
		for key, val := range v {
			var err error
			switch key {
			case "pkg":
				s.Packages, err = words(val)
			case "aur":
				s.AUR, err = words(val)
			case "first":
				s.First, err = words(val)
			case "build":
				var ok bool
				if s.Build, ok = val.(string); !ok || s.Build == "" {
					err = fmt.Errorf("build is a recipe name")
				}
			case "group":
				g, ok := val.(string)
				if !ok {
					err = fmt.Errorf("group is a name")
				}
				s.Groups = []string{g}
			case "groups":
				s.Groups, err = words(val)
			default:
				err = fmt.Errorf("unknown key %q", key)
			}
			if err != nil {
				return nil, err
			}
		}
		if len(s.Packages)+len(s.AUR)+len(s.First) == 0 && s.Build == "" {
			return nil, fmt.Errorf("says neither pkg, aur, first nor build")
		}
		for _, g := range s.Groups {
			if !validGroup(g) {
				return nil, fmt.Errorf("no group %q", g)
			}
		}
		return s, nil
	}
	return nil, fmt.Errorf("a package, a list or a table, not %T", raw)
}

func words(v any) ([]string, error) {
	switch x := v.(type) {
	case string:
		return []string{x}, nil
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("lists hold names, not %T", e)
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, fmt.Errorf("a name or a list of names, not %T", v)
}

// Plan is what to install on one distro.
type Plan struct {
	Distro   string     `json:"distro"`
	Groups   []string   `json:"groups"`
	Packages []string   `json:"packages"`
	AUR      []string   `json:"aur,omitempty"`
	First    [][]string `json:"first,omitempty"`
	Builds   []string   `json:"builds,omitempty"` // in table order
}

// Plan selects the deps of distro whose groups are on (core always is).
func (t *Table) Plan(distro string, on map[string]bool) (*Plan, error) {
	if !isDistro(distro) {
		return nil, fmt.Errorf("no distro %q in the table (%s)", distro, strings.Join(Distros, ", "))
	}
	p := &Plan{Distro: distro}
	for _, g := range Groups {
		if on[g] {
			p.Groups = append(p.Groups, g)
		}
	}
	pkgs, aur, builds := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for i := range t.Deps {
		d := &t.Deps[i]
		s, _ := d.Spec(distro)
		if s == nil {
			continue
		}
		groups := d.groups()
		if len(s.Groups) > 0 {
			groups = s.Groups
		}
		wanted := false
		for _, g := range groups {
			wanted = wanted || g == "core" || on[g]
		}
		if !wanted {
			continue
		}
		for _, x := range s.Packages {
			pkgs[x] = true
		}
		for _, x := range s.AUR {
			aur[x] = true
		}
		if len(s.First) > 0 {
			p.First = append(p.First, s.First)
		}
		if s.Build != "" && !builds[s.Build] {
			builds[s.Build] = true
			p.Builds = append(p.Builds, s.Build)
		}
	}
	p.Packages, p.AUR = sorted(pkgs), sorted(aur)
	return p, nil
}

func sorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
