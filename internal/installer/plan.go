package installer

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/snowarch/inir-cli/internal/deps"
)

// Group is one optional part of the install, as the wizard offers it.
type Group struct {
	Key, Title string
	Packages   []string // what it adds on this distro
	Missing    int      // how many of them are not installed yet
}

var groupTitles = map[string]string{
	"audio":         "Audio tools",
	"toolkit":       "Input and brightness tools",
	"screencapture": "Screenshots and recording",
	"fonts":         "Fonts and themes",
}

// Packages is what deps.toml says the install brings, per group, and how much
// of it this machine still lacks. Core is Groups[0] and always goes in.
type Packages struct {
	Groups []Group
	ok     bool
}

// LoadPackages reads the shell's deps.toml for this distro. It is a guide for
// the person deciding (setup's own distro scripts do the installing), so any
// error just leaves the counts out.
func LoadPackages(setupDir, distro string) Packages {
	t, err := deps.Load(filepath.Join(setupDir, "sdata", "deps.toml"))
	if err != nil {
		return Packages{}
	}
	on := map[string]bool{}
	for _, g := range deps.Groups {
		on[g] = true
	}
	p, err := t.Plan(distro, on)
	if err != nil {
		return Packages{}
	}
	missing := map[string]bool{}
	if m, err := deps.ManagerFor(distro, deps.RealSystem()); err == nil {
		s := m.Resolve(p, nil)
		for _, x := range append(append(s.Install, s.AUR...), s.Unavailable...) {
			missing[x] = true
		}
	}
	byKey := map[string]*Group{}
	out := Packages{ok: true}
	for _, k := range append([]string{"core"}, deps.Groups...) {
		out.Groups = append(out.Groups, Group{Key: k, Title: groupTitles[k]})
	}
	for i := range out.Groups {
		byKey[out.Groups[i].Key] = &out.Groups[i]
	}
	for _, x := range append(append([]string{}, p.Packages...), p.AUR...) {
		g := byKey[p.Group[x]]
		if g == nil {
			continue
		}
		g.Packages = append(g.Packages, x)
		if missing[x] {
			g.Missing++
		}
	}
	return out
}

// Optional is every group but core.
func (p Packages) Optional() []Group {
	if !p.ok {
		var out []Group
		for _, k := range deps.Groups {
			out = append(out, Group{Key: k, Title: groupTitles[k]})
		}
		return out
	}
	return p.Groups[1:]
}

// Missing counts what the picked groups (and core) still need.
func (p Packages) Missing(picked []string) (n int, known bool) {
	if !p.ok {
		return 0, false
	}
	n = p.Groups[0].Missing
	for _, g := range p.Groups[1:] {
		for _, k := range picked {
			if k == g.Key {
				n += g.Missing
			}
		}
	}
	return n, true
}

// Describe is a group's line under its name: how much it adds and a taste of what.
func (g Group) Describe() string {
	if len(g.Packages) == 0 {
		return ""
	}
	names := g.Packages
	if len(names) > 3 {
		names = names[:3]
	}
	more := ""
	if len(g.Packages) > 3 {
		more = ", …"
	}
	state := "all here"
	if g.Missing > 0 {
		state = fmt.Sprintf("%d to install", g.Missing)
	}
	return fmt.Sprintf("%s (%s%s)", state, strings.Join(names, ", "), more)
}
