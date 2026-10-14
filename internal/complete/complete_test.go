package complete

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowarch/inir-cli/internal/ipc"
)

const sample = `{
 "schema": 1,
 "targets": [
  {"name": "altSwitcher", "family": "shared", "description": "Alt+Tab. Second sentence.", "example": "",
   "functions": [{"name": "opens", "args": ["which"], "description": "Which switcher: ` + "`inir`" + ` or ` + "`niri`" + `", "values": ["inir", "niri"]},
                 {"name": "next", "args": [], "description": "", "values": []}]},
  {"name": "iris", "family": "shared", "description": "", "example": "",
   "functions": [{"name": "theme", "args": ["action"], "description": "", "values": ["list", "apply:"]},
                 {"name": "move", "args": ["x", "y"], "description": "", "values": []}]},
  {"name": "settings", "family": "shared", "description": "Settings target", "example": "",
   "functions": [{"name": "open", "args": [], "description": "", "values": []}]},
  {"name": "hub", "family": "shared", "description": "", "example": "",
   "functions": [{"name": "openItem", "args": ["id"], "description": "", "values": []},
                 {"name": "list", "args": [], "description": "", "values": []}]}
 ],
 "aliases": {"alt-switcher": "altSwitcher"}
}`

func registry(t *testing.T) *ipc.Registry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ipc.json")
	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := ipc.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func values(r Result) []string {
	var out []string
	for _, c := range r.Candidates {
		out = append(out, c.Value)
	}
	return out
}

func has(list []string, want ...string) bool {
	set := map[string]bool{}
	for _, v := range list {
		set[v] = true
	}
	for _, w := range want {
		if !set[w] {
			return false
		}
	}
	return true
}

func TestComplete(t *testing.T) {
	reg := registry(t)
	cases := []struct {
		line    string
		want    []string
		without []string
		dirs    bool
		hint    string
	}{
		{line: "", want: []string{"update", "hub", "altSwitcher", "alt-switcher"}, without: []string{"settings-target", "cleanup-orphans"}},
		{line: "alt-switcher ", want: []string{"opens", "next"}},
		{line: "altSwitcher opens ", want: []string{"inir", "niri"}},
		{line: "altSwitcher opens inir ", want: nil},
		{line: "iris move 1 ", hint: "y"},
		{line: "iris move 1 2 ", want: nil},
		{line: "ipc ", want: []string{"settings", "altSwitcher"}},
		{line: "ipc -c /x settings ", want: []string{"open"}},
		{line: "hub ", want: []string{"sync", "list", "openItem"}},
		{line: "hub list -", want: []string{"--kind", "--installed", "--json"}},
		{line: "hub list --kind ", want: []string{"widget", "webapp"}},
		{line: "update -", want: []string{"--dry-run", "--local", "--yes"}},
		{line: "run -c ", dirs: true},
		{line: "logs -n ", hint: "n"},
		{line: "service ", want: []string{"install", "logs"}},
		{line: "service logs -", want: []string{"--full"}},
		{line: "service logs ", want: nil},
		{line: "completions install ", want: []string{"bash", "zsh", "fish"}},
		{line: "bind ", hint: "keys, e.g. Mod+O"},
		{line: "bind -", want: []string{"--add", "--examples"}},
		{line: "bind --examples ", want: []string{"iris", "all"}},
		{line: "bind --add Mod+O ", want: []string{"altSwitcher", "restart"}},
		{line: "bind Mod+O iris theme ", want: []string{"apply:"}},
		{line: "nosuch ", want: nil},
		{line: "doctor -", want: []string{"--check", "--json", "--only"}},
		{line: "deps ", want: []string{"plan"}},
		{line: "deps plan --distro ", want: []string{"arch", "void"}},
	}
	for _, c := range cases {
		words := strings.Split(c.line, " ")
		r := Complete(words, reg)
		got := values(r)
		if c.want == nil && c.hint == "" && !c.dirs && len(got) != 0 {
			t.Errorf("%q: want nothing, got %v", c.line, got)
		}
		if !has(got, c.want...) {
			t.Errorf("%q: want %v in %v", c.line, c.want, got)
		}
		for _, w := range c.without {
			if has(got, w) {
				t.Errorf("%q: %q should not be offered", c.line, w)
			}
		}
		if r.Dirs != c.dirs || r.Hint != c.hint {
			t.Errorf("%q: dirs=%v hint=%q, want dirs=%v hint=%q", c.line, r.Dirs, r.Hint, c.dirs, c.hint)
		}
	}
}

// Right after `inir`, `settings` is the launcher command, so the target of the
// same name is not offered there (it is under `inir ipc`).
func TestCommandShadowsTarget(t *testing.T) {
	r := Complete([]string{""}, registry(t))
	n := 0
	for _, c := range r.Candidates {
		if c.Value == "settings" {
			n++
			if c.Kind != KindCommand {
				t.Errorf("settings offered as %s", c.Kind)
			}
		}
	}
	if n != 1 {
		t.Errorf("settings offered %d times", n)
	}
}

func TestNoRegistry(t *testing.T) {
	if got := values(Complete([]string{""}, nil)); !has(got, "install", "update") {
		t.Errorf("without a shell the commands still complete: %v", got)
	}
	if got := Complete([]string{"altSwitcher", ""}, nil); len(got.Candidates) != 0 {
		t.Errorf("without a shell there are no functions: %v", got)
	}
}

func TestFormat(t *testing.T) {
	r := Result{Candidates: []Candidate{{"apply:", "", KindValue}, {"list", "All themes", KindValue}}}
	if got := Format("bash", r, "ap"); got != "apply:\n" {
		t.Errorf("bash filters by the typed word: %q", got)
	}
	if got := Format("zsh", r, "ap"); got != "value\t1\tapply:\t\nvalue\t0\tlist\tAll themes\n" {
		t.Errorf("zsh: %q", got)
	}
	if got := Format("fish", r, "ap"); got != "apply:\nlist\tAll themes\n" {
		t.Errorf("fish: %q", got)
	}
	if got := Format("bash", Result{Dirs: true}, ""); got != ":dirs\n" {
		t.Errorf("dirs: %q", got)
	}
	if got := Format("zsh", Result{Hint: "keys"}, ""); got != ":hint keys\n" {
		t.Errorf("hint: %q", got)
	}
}

func TestSummary(t *testing.T) {
	cases := map[string]string{
		"Alt+Tab. Second sentence.":        "Alt+Tab",
		"Which switcher: `inir` or `niri`": "Which switcher: inir or niri",
		"Open iRiS Settings on a section: `general`, `appearance`, `motion`, `bar`, `bubbles`": "Open iRiS Settings on a section: general, appearance…",
	}
	for in, want := range cases {
		if got := Summary(in); got != want {
			t.Errorf("Summary(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestInstall(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	dest, _ := Dest("fish")
	var out strings.Builder

	// A link the bash launcher made is replaced by the script.
	os.MkdirAll(filepath.Dir(dest), 0o755)
	os.Symlink("/nonexistent/inir.fish", dest)
	if err := Install("fish", &out); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dest); !strings.Contains(string(data), Marker) {
		t.Fatalf("not written: %q", data)
	}
	// Ours again: rewritten.
	if err := Install("fish", &out); err != nil {
		t.Fatal(err)
	}
	// A file of the user's stays.
	os.WriteFile(dest, []byte("complete -c inir -a mine\n"), 0o644)
	out.Reset()
	if err := Install("fish", &out); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(dest); string(data) != "complete -c inir -a mine\n" {
		t.Errorf("user file overwritten")
	}
	if !strings.Contains(out.String(), "file of your own") {
		t.Errorf("no word about the user file: %q", out.String())
	}
}
