package deps

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `
schema = 1

[[dep]]
name = "pipewire"
group = "audio"
arch = ["pipewire", "wireplumber"]
debian = "pipewire"

[[dep]]
name = "kvantum"
group = "fonts"
arch = { pkg = "kvantum", group = "core" }
debian = { first = ["qt6-style-kvantum", "kvantum"] }

[[dep]]
name = "ocr"
groups = ["toolkit", "screencapture"]
void = "tesseract-ocr"

[[dep]]
name = "cliphist"
group = "core"
debian = { pkg = "cliphist", build = "cliphist" }
arch = { aur = "cliphist-bin" }
`

func load(t *testing.T, body string) (*Table, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "deps.toml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func all(on bool) map[string]bool {
	m := map[string]bool{}
	for _, g := range Groups {
		m[g] = on
	}
	return m
}

func has(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func TestPlan(t *testing.T) {
	tab, err := load(t, sample)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := tab.Plan("arch", all(false))
	if has(p.Packages, "pipewire") || !has(p.Packages, "kvantum") || !has(p.AUR, "cliphist-bin") {
		t.Errorf("arch, groups off: %+v", p)
	}
	p, _ = tab.Plan("ubuntu", all(true))
	if !has(p.Packages, "pipewire") || len(p.First) != 1 || p.First[0][0] != "qt6-style-kvantum" {
		t.Errorf("ubuntu falls back to debian: %+v", p)
	}
	if !has(p.Packages, "cliphist") || len(p.Builds) != 1 || p.Builds[0] != "cliphist" {
		t.Errorf("package plus provider: %+v", p)
	}
	on := all(false)
	on["screencapture"] = true
	p, _ = tab.Plan("void", on)
	if !has(p.Packages, "tesseract-ocr") {
		t.Errorf("any of its groups: %+v", p)
	}
	if _, err := tab.Plan("gentoo", on); err == nil {
		t.Error("an unknown distro plans nothing")
	}
}

func TestLoadRejects(t *testing.T) {
	cases := map[string]string{
		"schema":    "schema = 2\n",
		"group":     "schema = 1\n[[dep]]\nname = \"x\"\ngroup = \"games\"\narch = \"x\"\n",
		"spec key":  "schema = 1\n[[dep]]\nname = \"x\"\ngroup = \"core\"\narch = { package = \"x\" }\n",
		"empty":     "schema = 1\n[[dep]]\nname = \"x\"\ngroup = \"core\"\narch = { group = \"fonts\" }\n",
		"duplicate": "schema = 1\n[[dep]]\nname = \"x\"\ngroup = \"core\"\n[[dep]]\nname = \"x\"\ngroup = \"core\"\n",
		"top key":   "schema = 1\ndeps = []\n",
	}
	for name, body := range cases {
		if _, err := load(t, body); err == nil {
			t.Errorf("%s: loaded", name)
		}
	}
}

// The shell's own table loads and plans for every distro.
func TestShellTable(t *testing.T) {
	path := os.Getenv("INIR_DEPS_TABLE")
	if path == "" {
		if repo := os.Getenv("INIR_SHELL_REPO"); repo != "" {
			path = filepath.Join(repo, "sdata", "deps.toml")
		}
	}
	if path == "" {
		t.Skip("set INIR_SHELL_REPO (or INIR_DEPS_TABLE)")
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("this shell has no sdata/deps.toml yet")
	}
	tab, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range Distros {
		p, err := tab.Plan(d, all(true))
		if err != nil || len(p.Packages) == 0 {
			t.Errorf("%s: %v %+v", d, err, p)
		}
		for _, pkg := range p.Packages {
			if strings.TrimSpace(pkg) != pkg || pkg == "" {
				t.Errorf("%s: package %q", d, pkg)
			}
		}
	}
}
