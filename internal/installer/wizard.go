package installer

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"

	"github.com/snowarch/inir-cli/internal/ui"
)

// Choices is everything the person decides before anything changes.
type Choices struct {
	Groups []string // optional dependency groups to install
	Extras []string // walls, yamis, sddm, fish
	Backup bool     // copy the configs iNiR replaces to BackupDir first
}

const (
	extraWalls = "walls"
	extraYAMIS = "yamis"
	extraSDDM  = "sddm"
	extraFish  = "fish"
)

// ErrCancelled is the person saying "not now" (or Esc, or Ctrl+C) in the form.
var ErrCancelled = errors.New("cancelled")

// Welcome is the first thing on screen: what this is and what it found.
func Welcome(w io.Writer, t ui.Theme, s System) {
	ui.Header(w, t, "inir install", "set up iNiR on "+s.Name)
	label := lipgloss.NewStyle().Foreground(t.Faint).Width(12)
	value := lipgloss.NewStyle().Foreground(t.Text)
	row := func(k, v string) { lipgloss.Fprintln(w, "    "+label.Render(k)+value.Render(v)) }
	lipgloss.Fprintln(w)
	what := "a fresh install"
	if !s.FirstRun {
		what = "over the iNiR already here (your settings stay)"
	}
	row("Install", what)
	if len(s.Remove) > 0 {
		row("Found", strings.Join(s.Remove, ", ")+" (another shell)")
	}
	lipgloss.Fprintln(w, "\n    "+lipgloss.NewStyle().Foreground(t.Muted).Render(
		"Pick what you want first. Nothing changes until you say Install."))
	lipgloss.Fprintln(w)
}

// Ask runs the form: the picks, then a review that can go back to them.
// Defaults match what `./setup install` does when it asks.
func Ask(t ui.Theme, s System, pk Packages, backupDir string, accessible bool, args []string) (Choices, error) {
	c := Choices{Backup: true}
	for _, g := range pk.Optional() {
		if !slices.Contains(args, "--no-"+g.Key) {
			c.Groups = append(c.Groups, g.Key)
		}
	}
	if s.FirstRun {
		c.Extras = append(c.Extras, extraYAMIS)
		if s.SDDM {
			c.Extras = append(c.Extras, extraSDDM)
		}
	}
	if slices.Contains(args, "--set-default-shell") {
		c.Extras = append(c.Extras, extraFish)
	}
	run := func(groups ...*huh.Group) error {
		err := huh.NewForm(groups...).
			WithTheme(ui.FormTheme(t)).WithKeyMap(ui.FormKeys()).
			WithWidth(min(ui.Width()-4, 88)).
			WithAccessible(accessible).
			Run()
		if errors.Is(err, huh.ErrUserAborted) {
			return ErrCancelled
		}
		return err
	}
	for {
		if err := run(picks(t, s, pk, backupDir, &c)...); err != nil {
			return c, err
		}
		next := "install"
		err := run(huh.NewGroup(
			huh.NewSelect[string]().
				Title("Ready").
				Description(Review(s, pk, c)).
				Options(
					huh.NewOption("Install iNiR", "install"),
					huh.NewOption("Change something", "back"),
					huh.NewOption("Not now", "quit")).
				Value(&next),
		))
		switch {
		case err != nil:
			return c, err
		case next == "quit":
			return c, ErrCancelled
		case next == "install":
			return c, nil
		}
	}
}

// picks are the form's questions, ticked as c has them.
func picks(t ui.Theme, s System, pk Packages, backupDir string, c *Choices) []*huh.Group {
	opt := func(title, note, key string, on []string) huh.Option[string] {
		if note != "" {
			title += "  " + faint(t, note)
		}
		return huh.NewOption(title, key).Selected(slices.Contains(on, key))
	}
	var groupOpts []huh.Option[string]
	for _, g := range pk.Optional() {
		groupOpts = append(groupOpts, opt(g.Title, g.Describe(), g.Key, c.Groups))
	}
	var extraOpts []huh.Option[string]
	if s.FirstRun {
		extraOpts = append(extraOpts,
			opt("YAMIS icons", "monochrome icon theme, 23 MiB; pick it in Settings", extraYAMIS, c.Extras),
			opt("iNiR-Walls", "a wallpaper collection, downloaded now", extraWalls, c.Extras))
		if s.SDDM {
			extraOpts = append(extraOpts, opt("Pixel login screen", "SDDM theme that matches the lock screen", extraSDDM, c.Extras))
		}
	}
	extraOpts = append(extraOpts, opt("Fish as your login shell", "changes it with chsh", extraFish, c.Extras))

	groups := []*huh.Group{
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("What to install").
				Description("The shell itself always goes in. These are the extras around it.").
				Options(groupOpts...).
				Filterable(false).
				Height(len(groupOpts) + 2). // Huh takes the title and description out of its own guess
				Value(&c.Groups),
		),
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				Title("Extras").
				Description("Small things you can also add later with ./setup extras.").
				Options(extraOpts...).
				Filterable(false).
				Height(len(extraOpts) + 2).
				Value(&c.Extras),
		),
	}
	if s.Configs {
		groups = append(groups, huh.NewGroup(
			huh.NewConfirm().
				Title("Back up your current configs first?").
				Description("niri, fish, foot and friends get iNiR's versions. Yours are copied to "+backupDir+" before that.").
				Affirmative("Back up").
				Negative("Skip").
				Value(&c.Backup),
		))
	}
	return groups
}

// Review is what will happen, in the words of someone using the computer.
func Review(s System, pk Packages, c Choices) string {
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, "• "+format+"\n", a...) }
	if n, known := pk.Missing(c.Groups); known && n == 0 {
		line("Every package is already installed")
	} else if known {
		line("Installs about %d packages", n)
	} else {
		line("Installs what the shell needs")
	}
	if s.OS == "arch" {
		line("Updates the system first (pacman -Syu), as Arch expects")
	}
	if len(s.Remove) > 0 {
		line("Removes %s: it takes Quickshell's place", strings.Join(s.Remove, ", "))
	}
	if len(s.Replace) > 0 {
		line("Swaps %s for the stable Quickshell", strings.Join(s.Replace, ", "))
	}
	if s.Configs && c.Backup {
		line("Backs up your configs, then writes iNiR's")
	} else {
		line("Writes the shell and its configs")
	}
	for _, e := range c.Extras {
		switch e {
		case extraWalls:
			line("Downloads iNiR-Walls")
		case extraYAMIS:
			line("Adds the YAMIS icons")
		case extraSDDM:
			line("Puts the pixel theme on the login screen")
		case extraFish:
			line("Makes fish your login shell")
		}
	}
	if !s.Sudo {
		line("Asks for your password once, right after this")
	}
	return strings.TrimRight(b.String(), "\n")
}

func faint(t ui.Theme, s string) string {
	return lipgloss.NewStyle().Foreground(t.Faint).Render(s)
}

// Flags is what setup install needs to hear about the choices: the groups
// left out as --no-* flags, the rest as answers to the questions setup would ask.
func (c Choices) Flags(optional []Group) (args, env []string) {
	for _, g := range optional {
		if !slices.Contains(c.Groups, g.Key) {
			args = append(args, "--no-"+g.Key)
		}
	}
	yn := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	env = append(env,
		"INIR_BACKUP_CONFIGS="+yn(c.Backup),
		"INIR_INSTALL_WALLS="+yn(slices.Contains(c.Extras, extraWalls)),
		"INIR_INSTALL_YAMIS="+yn(slices.Contains(c.Extras, extraYAMIS)),
		"INIR_INSTALL_SDDM_THEME="+yn(slices.Contains(c.Extras, extraSDDM)))
	if slices.Contains(c.Extras, extraFish) {
		env = append(env, "INIR_SET_DEFAULT_SHELL=true")
	}
	return args, env
}
