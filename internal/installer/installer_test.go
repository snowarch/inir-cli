package installer

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/snowarch/inir-cli/internal/ui"
)

func TestFlagsCarryEveryChoice(t *testing.T) {
	optional := Packages{}.Optional()
	c := Choices{Groups: []string{"audio", "fonts"}, Extras: []string{extraYAMIS, extraFish}, Backup: false}
	args, env := c.Flags(optional)
	if want := []string{"--no-toolkit", "--no-screencapture"}; !slices.Equal(args, want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for _, kv := range []string{
		"INIR_BACKUP_CONFIGS=no", "INIR_INSTALL_WALLS=no", "INIR_INSTALL_YAMIS=yes",
		"INIR_INSTALL_SDDM_THEME=no", "INIR_SET_DEFAULT_SHELL=true",
	} {
		if !slices.Contains(env, kv) {
			t.Errorf("env lacks %s: %v", kv, env)
		}
	}
}

func run(m *Progress, msgs ...any) {
	for _, msg := range msgs {
		m.Update(msg)
	}
}

func TestStagesFollowSetup(t *testing.T) {
	m := NewProgress(ui.Current(), "install", InstallStages(), func() {})
	// --skip-deps: setup goes from conflicts straight to system.
	run(m,
		EventMsg{"info", "before any stage"},
		EventMsg{"stage", "conflicts"},
		EventMsg{"stage", "system"},
		EventMsg{"warn", "Could not wire inir.service"},
		EventMsg{"reboot", "Your user joined video"},
		EventMsg{"stage", "files"},
		EventMsg{"ok", "Fish shell config installed"},
	)
	got := map[string]Status{}
	for _, s := range m.Stages() {
		got[s.Key] = s.Status
	}
	want := map[string]Status{"conflicts": Done, "deps": Skipped, "system": Done, "files": Running, "finish": Waiting}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %v, want %v", k, got[k], v)
		}
	}
	if s := m.Stages()[2]; len(s.Warnings) != 1 {
		t.Errorf("system warnings = %v", s.Warnings)
	}
	if len(m.Reboot) != 1 || m.Stages()[3].Last != "Fish shell config installed" {
		t.Errorf("reboot %v, last %q", m.Reboot, m.Stages()[3].Last)
	}
	run(m, DoneMsg{errors.New("exit status 1")})
	if s := m.Stages(); s[3].Status != Failed || s[4].Status != Skipped {
		t.Errorf("after failure: files %v, finish %v", s[3].Status, s[4].Status)
	}
	if !strings.Contains(m.View().Content, "Shell and configs") {
		t.Error("the view lost the stage list")
	}
}

func TestTailShowsWhatATerminalWould(t *testing.T) {
	m := NewProgress(ui.Current(), "install", InstallStages(), func() {})
	run(m, EventMsg{"stage", "deps"}, OutputMsg("  0  0\r 37%  12M\r 50%  1M\x1b[K\r"), OutputMsg("\x1b[1m==> Done\x1b[0m"))
	if got := strings.Join(m.tail, "|"); got != "50%  1M|==> Done" {
		t.Errorf("tail = %q", got)
	}
}

func TestReviewSaysWhatHappens(t *testing.T) {
	s := System{OS: "arch", FirstRun: true, Configs: true, Remove: []string{"dms-shell"}}
	r := Review(s, Packages{}, Choices{Backup: true, Extras: []string{extraSDDM}})
	for _, want := range []string{"pacman -Syu", "Removes dms-shell", "Backs up your configs", "login screen", "password once"} {
		if !strings.Contains(r, want) {
			t.Errorf("review lacks %q:\n%s", want, r)
		}
	}
}

func TestDuration(t *testing.T) {
	for in, want := range map[string]string{"4s": "4s", "125s": "2m 05s", "3720s": "1h 02m"} {
		d, _ := time.ParseDuration(in)
		if got := Duration(d); got != want {
			t.Errorf("Duration(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestUpdateStagesFollowSetup(t *testing.T) {
	m := NewProgress(ui.Current(), "update", UpdateStages(), func() {})
	m.HideSkipped = true
	// Nothing to pull: setup goes straight to syncing files.
	run(m,
		EventMsg{"stage", "2"},
		OutputMsg("  \x1b[32m●\x1b[0m \x1b[35m◉\x1b[0m \x1b[2m○\x1b[0m [2/7] Syncing files"),
		OutputMsg("  ✓ modules/ii/bar (3 files updated)"),
	)
	if len(m.tail) != 1 || !strings.Contains(m.tail[0], "modules/ii/bar") {
		t.Errorf("tail = %q, want only the file line", m.tail)
	}
	run(m,
		EventMsg{"ok", "Synced 1247 files"},
		EventMsg{"stage", "3"},
		EventMsg{"warn", "Could not refresh the inir launcher"},
		EventMsg{"stage", "7"},
	)
	s := m.Stages()
	if s[0].Status != Skipped || s[1].Status != Done || s[1].Last != "Synced 1247 files" {
		t.Fatalf("pull %v, sync %v %q", s[0].Status, s[1].Status, s[1].Last)
	}
	if len(s[2].Warnings) != 1 || s[3].Status != Skipped || s[6].Status != Running {
		t.Fatalf("setup check %v, finalizing %v, restart %v", s[2].Warnings, s[3].Status, s[6].Status)
	}
	m.Update(DoneMsg{})
	view := ansi.Strip(m.View().Content)
	if strings.Contains(view, "Pulling changes") || strings.Contains(view, "skipped") {
		t.Errorf("skipped stages still on screen:\n%s", view)
	}
	if !strings.Contains(view, "Restarting the shell") {
		t.Errorf("the stages that ran are missing:\n%s", view)
	}
}

const changelog = `# Changelog

## [Unreleased]

- Something on the branch

## [2.34.0] - 2026-10-20

### Added
- Live window previews ([#301](https://github.com/snowarch/iNiR/issues/301)), with a line long enough to wrap twice in a narrow terminal so the hang shows

### Contributors
Thanks to [@someone](https://github.com/someone).

## [2.33.0] - 2026-10-08

### Fixed
- Old things
`

func TestWhatsNew(t *testing.T) {
	got := WhatsNew(changelog, "2.33.0", "2.34.0", []string{"feat: x"})
	if !strings.HasPrefix(got, "## [Unreleased]") || !strings.Contains(got, "Live window previews") || strings.Contains(got, "Old things") {
		t.Errorf("new sections:\n%s", got)
	}
	// The version did not move: the pulled commits are the news.
	got = WhatsNew(changelog, "2.34.0", "2.34.0", []string{"feat(bar): a", "fix(dock): b"})
	if got != "## What changed\n\n- A\n- B\n" {
		t.Errorf("commits:\n%q", got)
	}
	if WhatsNew(changelog, "2.34.0", "2.34.0", nil) != "" {
		t.Error("nothing pulled should say nothing")
	}
	if v := previousRelease(changelog); v != "2.33.0" {
		t.Errorf("previous release = %q", v)
	}
}

func TestRenderNotes(t *testing.T) {
	out := ansi.Strip(RenderNotes(ui.Current(), NewSections(changelog, "2.33.0"), 80))
	for _, want := range []string{"What's new", "2.34.0", "Added", "• Live window previews"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered notes lack %q:\n%s", want, out)
		}
	}
	for _, not := range []string{"##", "https://", "Contributors"} {
		if strings.Contains(out, not) {
			t.Errorf("rendered notes keep %q:\n%s", not, out)
		}
	}
	if !strings.Contains(out, "(#301)") || !strings.Contains(out, "2.34.0 · 2026-10-20") {
		t.Errorf("a link or a release header lost its text:\n%s", out)
	}
	// Wrapped lines of an item hang under its text, and nothing trails.
	for i, l := range strings.Split(out, "\n") {
		if strings.HasSuffix(l, " ") {
			t.Errorf("line %d trails spaces: %q", i, l)
		}
		if strings.Contains(l, "hang shows") && !strings.HasPrefix(l, "    ") {
			t.Errorf("a wrapped line does not hang: %q", l)
		}
	}
}
