package ui

import (
	"image/color"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/charmtone"
)

// Theme is the handful of roles the binary's own output paints with.
type Theme struct {
	Accent   color.Color // titles, the program's name
	OnAccent color.Color
	Success  color.Color
	Warning  color.Color
	Error    color.Color
	OnError  color.Color
	Text     color.Color
	Muted    color.Color // secondary text
	Faint    color.Color // numbers, tree lines, hints
}

// Current is the shell's palette for this wallpaper, or Charm's own colors on
// a machine the shell has not themed yet (INIR_CHARM_COLORS=1 forces those).
func Current() Theme {
	if p, ok := Generated(); ok && os.Getenv("INIR_CHARM_COLORS") == "" {
		return Theme{
			Accent:   p.Primary,
			OnAccent: p.OnPrimary,
			Success:  p.Success,
			Warning:  p.Warning,
			Error:    p.Error,
			OnError:  p.OnError,
			Text:     p.Text,
			Muted:    p.TextVariant,
			Faint:    p.Outline,
		}
	}
	c := lipgloss.LightDark(true)
	if isTerminal(os.Stdout) {
		c = lipgloss.LightDark(lipgloss.HasDarkBackground(os.Stdin, os.Stdout))
	}
	return Theme{
		Accent:   charmtone.Charple,
		OnAccent: charmtone.Salt,
		Success:  c(lipgloss.Color("#0CB37F"), charmtone.Guac),
		Warning:  c(charmtone.Tang, charmtone.Mustard),
		Error:    c(charmtone.Cherry, charmtone.Coral),
		OnError:  charmtone.Butter,
		Text:     c(charmtone.Charcoal, charmtone.Ash),
		Muted:    c(charmtone.Oyster, charmtone.Squid),
		Faint:    c(charmtone.Squid, charmtone.Oyster),
	}
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
