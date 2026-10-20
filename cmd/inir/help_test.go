package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/snowarch/inir-cli/internal/cli"
	"github.com/snowarch/inir-cli/internal/ipc"
)

// Every word people can type sits in exactly one help group, so a new command
// cannot go missing from `inir help`.
func TestHelpGroupsCoverEveryCommand(t *testing.T) {
	seen := map[string]int{}
	for _, g := range helpGroups {
		for _, n := range g.Names {
			if _, ok := cli.LookupAny(n); !ok {
				t.Errorf("group %q names %q, which is not a command", g.ID, n)
			}
			seen[n]++
		}
	}
	for _, list := range [][]cli.Command{cli.Commands, cli.BinaryCommands} {
		for _, c := range list {
			if c.Hidden || c.Name == "help" {
				continue
			}
			if seen[c.Name] != 1 {
				t.Errorf("%q is in %d help groups, want 1", c.Name, seen[c.Name])
			}
		}
	}
	for _, n := range rootHidden {
		if seen[n] != 1 {
			t.Errorf("rootHidden names %q, which no group has", n)
		}
	}
}

func TestWantsHelp(t *testing.T) {
	cases := []struct {
		args []string
		path []string
		ok   bool
	}{
		{nil, nil, true},
		{[]string{"help"}, []string{}, true},
		{[]string{"--help"}, []string{}, true},
		{[]string{"help", "hub", "list"}, []string{"hub", "list"}, true},
		{[]string{"doctor", "--help"}, []string{"doctor"}, true},
		{[]string{"overview", "-h"}, []string{"overview"}, true},
		{[]string{"doctor", "--json", "--help"}, nil, false},
		{[]string{"overview", "toggle"}, nil, false},
		{[]string{"doctor"}, nil, false},
	}
	for _, c := range cases {
		path, ok := wantsHelp(c.args)
		if ok != c.ok || (ok && !slices.Equal(path, c.path)) {
			t.Errorf("wantsHelp(%q) = %q, %v; want %q, %v", c.args, path, ok, c.path, c.ok)
		}
	}
}

const testIPC = `{"schema": 1, "aliases": {"wallpaper-selector": "wallpaperSelector"}, "targets": [
 {"name": "overview", "family": "shared", "description": "Toggle the overview. Tiny windows.",
  "functions": [{"name": "toggle", "description": "Open/Close overview"}]},
 {"name": "wallpaperSelector", "family": "shared", "description": "Wallpaper picker",
  "functions": [{"name": "open", "args": ["path"], "description": "Open it"}]},
 {"name": "wbar", "family": "waffle", "description": "Taskbar",
  "functions": [{"name": "toggle", "values": ["a", "b"], "description": "Show or hide"}]},
 {"name": "hub", "family": "shared", "description": "Hub", "functions": [{"name": "use", "args": ["id"]}]}]}`

func testRegistry(t *testing.T) *ipc.Registry {
	path := filepath.Join(t.TempDir(), "ipc.json")
	if err := os.WriteFile(path, []byte(testIPC), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := ipc.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestHelpTree(t *testing.T) {
	root := helpTree(testRegistry(t))
	for _, path := range [][]string{
		{"doctor"}, {"hub", "list"}, {"hub", "use"}, {"overview", "toggle"},
		{"wallpaper-selector", "open"}, {"wallpaperSelector"}, {"wbar"}, {"actions"}, {"deps", "install"},
	} {
		c, rest, err := root.Find(path)
		if err != nil || len(rest) > 0 || c == root {
			t.Errorf("help has no %q", path)
		}
	}
	if c, _, _ := root.Find([]string{"wbar"}); c != nil && !c.Hidden {
		t.Error("wbar is not a favorite action and should wait in `inir help actions`")
	}
	if c, _, _ := root.Find([]string{"logs"}); c.Flags().ShorthandLookup("n") == nil {
		t.Error("logs -n lost its long twin --lines")
	}
	if c, _, _ := root.Find([]string{"wbar", "toggle"}); !strings.Contains(c.Short, "(a, b)") {
		t.Errorf("value list missing from %q", c.Short)
	}
	if code, handled := runHelp([]string{"no-such-word"}); handled {
		t.Errorf("an unknown word was answered (%d) instead of going to the launcher", code)
	}
}
