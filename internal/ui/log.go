package ui

import (
	"image/color"
	"os"

	"charm.land/lipgloss/v2"
	"charm.land/log/v2"
)

// Log is how the binary says something went wrong, on stderr: a level badge in
// the theme's colors (the same look Fang gives a bad flag), the command, the
// message and any details as key=value.
//
//	ERROR inir deps: package index err="pacman: exit status 1"
func Log(command string) *log.Logger {
	t := Current()
	l := log.NewWithOptions(os.Stderr, log.Options{Prefix: command})
	s := log.DefaultStyles()
	badge := func(text string, bg, fg color.Color) lipgloss.Style {
		return lipgloss.NewStyle().SetString(text).Bold(true).Padding(0, 1).Background(bg).Foreground(fg)
	}
	s.Levels[log.ErrorLevel] = badge("ERROR", t.Error, t.OnError)
	s.Levels[log.FatalLevel] = badge("FATAL", t.Error, t.OnError)
	s.Levels[log.WarnLevel] = lipgloss.NewStyle().SetString("WARN").Bold(true).Padding(0, 1).Foreground(t.Warning)
	s.Levels[log.InfoLevel] = lipgloss.NewStyle().SetString("INFO").Bold(true).Padding(0, 1).Foreground(t.Accent)
	s.Prefix = lipgloss.NewStyle().Foreground(t.Muted)
	s.Message = lipgloss.NewStyle().Foreground(t.Text)
	s.Key = lipgloss.NewStyle().Foreground(t.Faint)
	s.Value = lipgloss.NewStyle().Foreground(t.Muted)
	s.Separator = lipgloss.NewStyle().Foreground(t.Faint)
	l.SetStyles(s)
	return l
}
