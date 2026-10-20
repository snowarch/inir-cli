package installer

import (
	"errors"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	gansi "charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/snowarch/inir-cli/internal/doctor"
	"github.com/snowarch/inir-cli/internal/ui"
)

// Update is `inir update` for a person at a terminal: what is coming, one
// question, setup's seven steps live, how it went and what is new.
func Update(args []string) int {
	t := ui.Current()
	setupDir := SetupDir(doctor.NewEnv())
	if setupDir == "" {
		ui.Log("inir update").Error("no iNiR checkout found", "looked", "INIR_SETUP_DIR, here, the installed one")
		return 1
	}
	simulate := slices.Contains(args, "--simulate") || slices.Contains(args, "--dry-run")
	local := slices.Contains(args, "--local")
	before := Version(setupDir)
	branch := git(setupDir, "rev-parse", "--abbrev-ref", "HEAD")
	ui.Header(os.Stdout, t, "inir update", strings.TrimSpace("iNiR "+before+" · "+branch))
	lipgloss.Println()
	faint := lipgloss.NewStyle().Foreground(t.Faint)
	muted := lipgloss.NewStyle().Foreground(t.Muted)
	oldHead := git(setupDir, "rev-parse", "HEAD")

	if !simulate && !local {
		fmt.Print("    " + faint.Render("Checking for updates…"))
		commits, upstream := Pending(setupDir)
		fmt.Print("\r\033[K")
		switch {
		case upstream == "":
			// No upstream to compare with: setup works out what to track.
		case len(commits) == 0:
			lipgloss.Println("    " + muted.Render("Nothing new on "+upstream+". Checking the install is all in place."))
		default:
			lipgloss.Println("    " + lipgloss.NewStyle().Foreground(t.Text).Bold(true).Render(
				fmt.Sprintf("%d new on %s", len(commits), upstream)))
			for i, c := range commits {
				if i == 8 {
					lipgloss.Println("    " + faint.Render(fmt.Sprintf("… and %d more", len(commits)-8)))
					break
				}
				hash, subject, _ := strings.Cut(c, " ")
				lipgloss.Println("    " + faint.Render(hash) + " " + muted.Render(subject))
			}
			lipgloss.Println()
			ok := true
			err := huh.NewForm(huh.NewGroup(huh.NewConfirm().
				Title("Pull and apply them?").
				Description("A snapshot is taken first: ./setup rollback undoes it.").
				Affirmative("Update").Negative("Not now").Value(&ok),
			)).WithTheme(ui.FormTheme(t)).WithKeyMap(ui.FormKeys()).WithWidth(min(ui.Width()-4, 88)).
				WithAccessible(os.Getenv("ACCESSIBLE") != "").Run()
			if errors.Is(err, huh.ErrUserAborted) || (err == nil && !ok) {
				ui.Card(os.Stdout, t.Muted, "Nothing changed. Run inir update whenever you are ready.")
				return 1
			}
			if err != nil {
				ui.Log("inir update").Error(err)
				return 1
			}
		}
	}

	var p *tea.Program
	var run *Run
	model := NewProgress(t, "update", UpdateStages(), func() { run.Stop() })
	model.HideSkipped = true
	p = tea.NewProgram(model)
	// Most updates never need root; when one does and there is no sudo ticket,
	// it fails and says so in the summary instead of waiting on a hidden prompt.
	detach := exec.Command("sudo", "-n", "true").Run() != nil
	run, err := Start(setupDir, "update", args, nil, detach, func(m any) { p.Send(m) })
	if err != nil {
		ui.Log("inir update").Error(err)
		return 1
	}
	if _, err := p.Run(); err != nil {
		run.Stop()
		ui.Log("inir update").Error(err)
		return 1
	}

	after, newHead := Version(setupDir), git(setupDir, "rev-parse", "HEAD")
	s := newSummary(t, model)
	switch {
	case model.Stopped:
		s.b.WriteString(s.bold.Render("Update stopped.") + "\n")
		s.b.WriteString(s.muted.Render("Undo what already ran with ./setup rollback, or run inir update again.") + "\n")
	case s.failed != nil || model.Err != nil:
		where := ""
		if s.failed != nil {
			where = " at " + strings.ToLower(s.failed.Title)
		}
		s.b.WriteString(s.bold.Render("The update stopped"+where+".") + "\n")
		for _, l := range lastLines(run.Log, 6) {
			s.b.WriteString(s.faint.Render("│ "+l) + "\n")
		}
		s.b.WriteString(s.muted.Render("./setup rollback goes back to how it was before.") + "\n")
	case simulate:
		s.warnings = slices.DeleteFunc(s.warnings, func(w string) bool { return strings.HasPrefix(w, "Simulation mode") })
		s.b.WriteString(s.bold.Render("Simulated update done.") + " " + s.muted.Render("Took "+Duration(model.Elapsed)+". Nothing changed.") + "\n")
	case newHead == oldHead:
		s.b.WriteString(s.bold.Render("iNiR "+after+" is up to date.") + " " + s.muted.Render("Took "+Duration(model.Elapsed)+".") + "\n")
	default:
		what := "iNiR " + after
		if after != before {
			what = "iNiR " + before + " → " + after
		}
		s.b.WriteString(s.bold.Render(what+" is in place.") + " " + s.muted.Render("Took "+Duration(model.Elapsed)+".") + "\n")
		s.b.WriteString(s.muted.Render("Something off? ./setup rollback goes back.") + "\n")
	}
	// What is new goes above the card, so the verdict is the last thing on screen.
	if s.failed == nil && model.Err == nil && !model.Stopped {
		changelog, _ := os.ReadFile(filepath.Join(setupDir, "CHANGELOG.md"))
		var notes string
		switch {
		case simulate: // shows what the newest release would say
			notes = NewSections(string(changelog), previousRelease(string(changelog)))
		case newHead != oldHead:
			notes = WhatsNew(string(changelog), before, after,
				lines(git(setupDir, "log", "--no-merges", "--format=%s", oldHead+".."+newHead)))
		}
		if notes != "" {
			fmt.Print("\n" + RenderNotes(t, notes, min(ui.Width(), 100)))
		}
	}
	return s.print(model, run.Log)
}

// Version is the checkout's VERSION, as setup reads it.
func Version(dir string) string {
	b, _ := os.ReadFile(filepath.Join(dir, "VERSION"))
	v, _, _ := strings.Cut(string(b), "\n")
	return strings.TrimSpace(v)
}

// Pending fetches and lists the commits the checkout is behind its upstream,
// newest first as "hash subject", with the upstream's name ("" when it has none).
func Pending(dir string) (commits []string, upstream string) {
	upstream = git(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}")
	if upstream == "" {
		return nil, ""
	}
	remote, _, _ := strings.Cut(upstream, "/")
	exec.Command("git", "-C", dir, "fetch", "--quiet", remote).Run()
	return lines(git(dir, "log", "--no-merges", "--format=%h %s", "HEAD..@{u}")), upstream
}

func git(dir string, args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

var (
	releaseHeader = regexp.MustCompile(`^## \[([^\]]+)\]`)
	link          = regexp.MustCompile(`\[([^\]]+)\]\([^)]*\)`)
	releaseLine   = regexp.MustCompile(`^## \[([^\]]+)\](?: - (\S+))?`)
	commitType    = regexp.MustCompile(`^[a-z]+(\([^)]*\))?!?: `)
)

// forTerminal is release notes trimmed for a terminal: links down to their
// text (the numbers and names read fine, the URLs are a wall) and without the
// GitHub-only parts, the issue list and the credits, which live on the release page.
func forTerminal(md string) string {
	var out []string
	skip := false
	for _, l := range strings.Split(md, "\n") {
		if strings.HasPrefix(l, "#") {
			h := strings.ToLower(strings.TrimSpace(strings.TrimLeft(l, "#")))
			skip = strings.HasPrefix(l, "### ") && (strings.HasPrefix(h, "issues") || h == "contributors")
		}
		if skip {
			continue
		}
		if m := releaseLine.FindStringSubmatch(l); m != nil { // "## [2.33.0] - 2026-10-08"
			l = "## " + m[1]
			if m[2] != "" {
				l += " · " + m[2]
			}
		}
		out = append(out, link.ReplaceAllString(l, "$1"))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// NewSections is the part of a Keep a Changelog file above `## [from]`: every
// release (and Unreleased) the person did not have yet. Empty when from is the top.
func NewSections(changelog, from string) string {
	var out []string
	started := false
	for _, l := range strings.Split(changelog, "\n") {
		if m := releaseHeader.FindStringSubmatch(l); m != nil {
			if m[1] == from {
				break
			}
			started = true
		}
		if started {
			out = append(out, l)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// WhatsNew is what an update brought: the changelog's new releases, or, when
// the version did not move (a branch between releases), the commits it pulled.
func WhatsNew(changelog, before, after string, subjects []string) string {
	if before != after {
		if notes := NewSections(changelog, before); notes != "" {
			return notes
		}
	}
	if len(subjects) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## What changed\n\n")
	for i, s := range subjects {
		if i == 15 {
			fmt.Fprintf(&b, "\n*… and %d more: `git log` in the checkout has them all.*\n", len(subjects)-15)
			break
		}
		// "fix(dock): b" reads as "B": the type is for the log, not for people.
		if t := commitType.ReplaceAllString(s, ""); t != "" {
			s = strings.ToUpper(t[:1]) + t[1:]
		}
		b.WriteString("- " + s + "\n")
	}
	return b.String()
}

// previousRelease is the second release in the changelog, so a simulated
// update shows the newest one's notes.
func previousRelease(changelog string) string {
	seen := 0
	for _, l := range strings.Split(changelog, "\n") {
		if m := releaseHeader.FindStringSubmatch(l); m != nil && m[1] != "Unreleased" {
			if seen++; seen == 2 {
				return m[1]
			}
		}
	}
	return ""
}

// RenderNotes draws release notes with Glamour in the binary's own colors:
// release headers on the accent, sections muted, the rest in plain text.
func RenderNotes(t ui.Theme, md string, width int) string {
	cfg := styles.DarkStyleConfig
	if ui.Luminance(t.Text) < 0.5 {
		cfg = styles.LightStyleConfig
	}
	hex := func(c color.Color) *string {
		r, g, b, _ := c.RGBA()
		s := fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
		return &s
	}
	yes, no := true, false
	margin := uint(2)
	cfg.Document.Margin = &margin
	cfg.Document.Color = hex(t.Text)
	cfg.Document.BlockPrefix, cfg.Document.BlockSuffix = "", ""
	cfg.Heading.Color = hex(t.Accent)
	cfg.H1 = gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Bold: &yes, Color: hex(t.Accent)}}
	cfg.H2 = gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{
		Prefix: " ", Suffix: " ", Bold: &yes, Color: hex(t.OnAccent), BackgroundColor: hex(t.Accent)}}
	cfg.H3 = gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Bold: &yes, Color: hex(t.Muted)}}
	cfg.H4 = gansi.StyleBlock{StylePrimitive: gansi.StylePrimitive{Bold: &no, Italic: &yes, Color: hex(t.Muted)}}
	cfg.Strong = gansi.StylePrimitive{Bold: &yes, Color: hex(t.Text)}
	cfg.Emph = gansi.StylePrimitive{Italic: &yes, Color: hex(t.Muted)}
	cfg.Item.BlockPrefix = "• "
	cfg.Item.Color = hex(t.Text)
	cfg.Link = gansi.StylePrimitive{Color: hex(t.Faint), Underline: &yes}
	cfg.LinkText = gansi.StylePrimitive{Color: hex(t.Accent)}
	cfg.Code.Color = hex(t.Warning)
	cfg.Code.BackgroundColor = nil
	cfg.Code.Prefix, cfg.Code.Suffix = "", ""
	cfg.HorizontalRule.Color = hex(t.Faint)
	r, err := glamour.NewTermRenderer(glamour.WithStyles(cfg), glamour.WithWordWrap(width-6))
	if err != nil {
		return md + "\n"
	}
	out, err := r.Render("# What's new\n\n" + forTerminal(md))
	if err != nil {
		return md + "\n"
	}
	return tidy(out)
}

// tidy fixes the two things Glamour leaves behind: lines padded with spaces to
// the wrap width, and list items whose wrapped lines go back under the bullet.
func tidy(out string) string {
	var b strings.Builder
	item := false
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		text := strings.TrimRight(ansi.Strip(l), " ")
		if text == "" {
			item = false
			b.WriteString("\n")
			continue
		}
		l = ansi.Truncate(l, ansi.StringWidth(text), "")
		if strings.HasPrefix(strings.TrimLeft(text, " "), "• ") {
			item = true
		} else if item {
			l = "  " + l
		}
		b.WriteString(l + "\n")
	}
	return b.String()
}
