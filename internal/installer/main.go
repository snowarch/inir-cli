package installer

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/snowarch/inir-cli/internal/doctor"
	"github.com/snowarch/inir-cli/internal/ui"
)

// Interactive reports whether `inir install args` is for the wizard: a person
// at a terminal and no -y. Everything else stays setup's, unchanged.
func Interactive(args []string) bool {
	for _, a := range args {
		if a == "-y" || a == "--yes" || a == "-h" || a == "--help" {
			return false
		}
	}
	return isTTY(os.Stdin) && isTTY(os.Stdout) && os.Getenv("TERM") != "dumb"
}

// Main is the wizard: welcome, choices, password, the install, the summary.
// args are setup install's own flags: --no-* start those groups unticked,
// --set-default-shell starts fish ticked, the rest go to setup as they are.
func Main(args []string) int {
	t := ui.Current()
	env := doctor.NewEnv()
	setupDir := SetupDir(env)
	if setupDir == "" {
		ui.Log("inir install").Error("run it from an iNiR checkout (./setup install)")
		return 1
	}
	accessible := os.Getenv("ACCESSIBLE") != ""
	sys := Detect(env.ConfDir)
	pk := LoadPackages(setupDir, sys.OS)

	Welcome(os.Stdout, t, sys)
	backupDir := os.Getenv("BACKUP_DIR")
	if backupDir == "" {
		backupDir = "~/inir-backup"
	}
	c, err := Ask(t, sys, pk, backupDir, accessible, args)
	if errors.Is(err, ErrCancelled) {
		ui.Card(os.Stdout, t.Muted, "Nothing changed. Run inir install whenever you are ready.")
		return 1
	}
	if err != nil {
		ui.Log("inir install").Error(err)
		return 1
	}
	if err := Authenticate(t, accessible); err != nil {
		if errors.Is(err, ErrCancelled) {
			ui.Card(os.Stdout, t.Muted, "Nothing changed. Run inir install whenever you are ready.")
			return 1
		}
		ui.Log("inir install").Error(err)
		return 1
	}
	stopSudo := make(chan struct{})
	go KeepSudo(stopSudo)
	defer close(stopSudo)

	flags, answers := c.Flags(pk.Optional())
	for _, a := range args {
		if !strings.HasPrefix(a, "--no-") && a != "--set-default-shell" && !slices.Contains(flags, a) {
			flags = append(flags, a)
		}
	}
	var p *tea.Program
	var run *Run
	model := NewProgress(t, "install", InstallStages(), func() { run.Stop() })
	p = tea.NewProgram(model)
	run, err = Start(setupDir, "install", flags, answers, false, func(m any) { p.Send(m) })
	if err != nil {
		ui.Log("inir install").Error(err)
		return 1
	}
	if _, err := p.Run(); err != nil {
		run.Stop()
		ui.Log("inir install").Error(err)
		return 1
	}
	return Summary(t, model, run.Log)
}

// SetupDir is the checkout to install from: the one that handed over
// (INIR_SETUP_DIR), the one we are standing in, or the installed one.
func SetupDir(env *doctor.Env) string {
	if d := os.Getenv("INIR_SETUP_DIR"); d != "" && isSetup(d) {
		return d
	}
	if wd, err := os.Getwd(); err == nil && isSetup(wd) {
		return wd
	}
	if isSetup(env.SetupDir) {
		return env.SetupDir
	}
	return ""
}

func isSetup(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "setup"))
	if err != nil || st.Mode()&0o111 == 0 {
		return false
	}
	_, err = os.Stat(filepath.Join(dir, "sdata", "subcmd-install"))
	return err == nil
}

// summary holds what every closing card shares: its styles and width.
type summary struct {
	t                  ui.Theme
	muted, faint, bold lipgloss.Style
	width              int
	warnings           []string
	failed             *Stage
	b                  strings.Builder
}

func newSummary(t ui.Theme, m *Progress) *summary {
	s := &summary{
		t:     t,
		muted: lipgloss.NewStyle().Foreground(t.Muted),
		faint: lipgloss.NewStyle().Foreground(t.Faint),
		bold:  lipgloss.NewStyle().Bold(true).Foreground(t.Text),
		width: min(ui.Width(), 96) - 8,
	}
	for _, st := range m.Stages() {
		s.warnings = append(s.warnings, st.Warnings...)
		if st.Status == Failed {
			s.failed = st
		}
	}
	return s
}

// bullet wraps its lines under the text, not under the dot.
func (s *summary) bullet(text string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, s.muted.Render("• "), s.muted.Width(s.width-2).Render(text)) + "\n"
}

// Summary is the last thing on screen: how it went, what to look at, what next.
func Summary(t ui.Theme, m *Progress, logPath string) int {
	s := newSummary(t, m)
	muted, faint, bold, failed, bullet := s.muted, s.faint, s.bold, s.failed, s.bullet
	b := &s.b
	switch {
	case m.Stopped:
		b.WriteString(bold.Render("Install stopped.") + "\n")
		b.WriteString(muted.Render("What already ran stays. Running inir install again is safe.") + "\n")
	case failed != nil || m.Err != nil:
		where := "before it started"
		if failed != nil {
			where = "at " + strings.ToLower(failed.Title)
		}
		b.WriteString(bold.Render("The install stopped "+where+".") + "\n")
		for _, l := range lastLines(logPath, 6) {
			b.WriteString(faint.Render("│ "+l) + "\n")
		}
		b.WriteString(muted.Render("Running inir install again is safe once that is sorted.") + "\n")
	case len(m.Reboot) > 0:
		b.WriteString(bold.Render("iNiR is installed. Restart before using it.") + " " + muted.Render("Took "+Duration(m.Elapsed)+".") + "\n")
		for _, r := range m.Reboot {
			b.WriteString(bullet(r))
		}
		b.WriteString(muted.Render("Then pick Niri at the login screen.") + "\n")
	default:
		b.WriteString(bold.Render("iNiR is installed.") + " " + muted.Render("Took "+Duration(m.Elapsed)+".") + "\n")
		if os.Getenv("NIRI_SOCKET") != "" {
			b.WriteString(muted.Render("The shell is already running on the new files.") + "\n")
		} else {
			b.WriteString(muted.Render("Log out and pick Niri at the login screen.") + "\n")
		}
	}
	return s.print(m, logPath)
}

// print closes the card with the warnings and where to look, framed in the
// tone of how it went, and returns the exit code that goes with it.
func (s *summary) print(m *Progress, logPath string) int {
	t, b := s.t, &s.b
	if n := len(s.warnings); n > 0 {
		b.WriteString("\n" + lipgloss.NewStyle().Foreground(t.Warning).Render(fmt.Sprintf("Worth a look (%d)", n)) + "\n")
		for i, w := range s.warnings {
			if i == 5 {
				b.WriteString(s.faint.Render(fmt.Sprintf("… and %d more in the log", n-5)) + "\n")
				break
			}
			b.WriteString(s.bullet(w))
		}
	}
	if home, _ := os.UserHomeDir(); home != "" && strings.HasPrefix(logPath, home+"/") {
		logPath = "~" + logPath[len(home):]
	}
	b.WriteString("\n" + s.faint.Render("Full log  ") + s.muted.Render(logPath) + "\n")
	b.WriteString(s.faint.Render("Checkup   ") + s.muted.Render("inir doctor"))
	tone := t.Success
	switch {
	case m.Stopped:
		tone = t.Warning
	case s.failed != nil || m.Err != nil:
		tone = t.Error
	}
	lipgloss.Println()
	ui.Card(os.Stdout, tone, lipgloss.NewStyle().Width(s.width).Render(b.String()))
	lipgloss.Println()
	if tone == t.Success {
		return 0
	}
	return 1
}

func lastLines(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		if l := strings.TrimSpace(ansi.Strip(sc.Text())); l != "" && !strings.HasPrefix(l, "[inir] ") {
			lines = append(lines, l)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
