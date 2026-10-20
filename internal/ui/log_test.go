package ui

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestLogLine(t *testing.T) {
	r, w, _ := os.Pipe()
	stderr := os.Stderr
	os.Stderr = w
	Log("inir deps plan").Error("unknown argument", "arg", "--bogus")
	Log("inir deps").Warn("package index was not refreshed", "err", "exit status 1")
	os.Stderr = stderr
	w.Close()
	out, _ := io.ReadAll(r)
	got := strings.Split(strings.TrimSpace(ansi.Strip(string(out))), "\n")
	want := []string{
		`ERROR inir deps plan: unknown argument arg=--bogus`,
		`WARN inir deps: package index was not refreshed err="exit status 1"`,
	}
	for i := range got { // the badges' padding is spacing, not text
		got[i] = strings.Join(strings.Fields(got[i]), " ")
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
