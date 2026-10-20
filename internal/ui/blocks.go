package ui

import (
	"image/color"
	"io"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
)

// Header is the first line of a command's report: its name on an accent
// badge and what it is doing, after a blank line.
func Header(w io.Writer, t Theme, name, doing string) {
	badge := lipgloss.NewStyle().Bold(true).Foreground(t.OnAccent).Background(t.Accent).
		Padding(0, 1).Render(name)
	lipgloss.Fprintln(w)
	lipgloss.Fprintln(w, "  "+badge+"  "+lipgloss.NewStyle().Foreground(t.Muted).Render(doing))
}

// Card frames a verdict in a rounded border of its tone.
func Card(w io.Writer, tone color.Color, body string) {
	lipgloss.Fprintln(w, lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(tone).
		Padding(0, 2).MarginLeft(2).Render(body))
}

// Step announces one stage of a long job ("Package index") on its own line.
func Step(w io.Writer, t Theme, text string) {
	lipgloss.Fprintln(w, "\n  "+lipgloss.NewStyle().Foreground(t.Accent).Render("▸")+" "+
		lipgloss.NewStyle().Bold(true).Foreground(t.Text).Render(text))
}

// Width is the terminal's width, 100 when stdout is not one.
func Width() int {
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w
	}
	return 100
}
