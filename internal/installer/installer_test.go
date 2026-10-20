package installer

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

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
	m := NewProgress(ui.Current(), func() {})
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
	m := NewProgress(ui.Current(), func() {})
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
