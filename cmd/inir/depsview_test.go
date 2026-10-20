package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/snowarch/inir-cli/internal/deps"
)

func TestPrintPlanCounts(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	p := &deps.Plan{Distro: "arch", Groups: []string{"audio"},
		Packages: []string{"git", "mpv", "nope", "sl"},
		Group:    map[string]string{"git": "core", "sl": "core", "mpv": "audio", "nope": "audio"}}
	var out bytes.Buffer
	printPlan(&out, p, &deps.Steps{Install: []string{"sl"}, Unavailable: []string{"nope"}})
	got := out.String()
	for _, want := range []string{
		"CORE  2 packages · 1 to install",
		"AUDIO  2 packages · 1 not found",
		"4 packages, 1 to install.",
		"1 not in your repositories: nope",
		"inir deps install takes care of that.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plan lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Error("plan written to a pipe carries color codes")
	}
}

func TestColumnsReadDownwards(t *testing.T) {
	cells := []string{"a", "b", "c", "d", "e"}
	got := columns(cells, cells, 8) // two columns of width 4
	want := "  a   d\n  b   e\n  c"
	if got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}
