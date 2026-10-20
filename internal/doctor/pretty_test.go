package doctor

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrinterPlainAndComplete(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var out bytes.Buffer
	p := newPrinter(&out, 25)
	p.header(false)
	p.spin(Steps[0]) // not a terminal: no spinner, nothing written
	p.stop()
	p.step(StepResult{N: 1, Title: "Checking dependencies...", Status: StatusPass,
		Lines: []Line{{Level: LevelPass, Text: "All dependencies available"}}})
	p.step(StepResult{N: 3, Title: "Checking repo checkout...", Status: StatusFail, Lines: []Line{
		{Level: LevelFail, Text: "Repo checkout is missing git metadata"},
		{Level: LevelHint, Text: "Run setup from a real checkout"}}})
	r := &Report{Mode: "check", Summary: Summary{Passed: 1, Fixable: 1, Failed: 1}}
	p.summary(r)
	got := out.String()
	for _, want := range []string{
		"inir doctor", " 1  ✓  All dependencies available", " 3  ✗  Repo checkout\n",
		"✗ Repo checkout is missing git metadata", "Run setup from a real checkout",
		"1 passed  ·  1 fixable  ·  1 failed", "Some issues need a hand", "without --check",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\r") {
		t.Errorf("report to a pipe carries escapes:\n%q", got)
	}
}
