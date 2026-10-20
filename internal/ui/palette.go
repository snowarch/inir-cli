// Package ui is how the inir binary looks in a terminal: the shell's own
// Material colors when it has generated them, Charm's defaults when not.
package ui

import (
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"

	"charm.land/lipgloss/v2"
)

// Palette is the subset of the shell's Material scheme the terminal uses.
type Palette struct {
	Primary          color.Color
	Secondary        color.Color
	Tertiary         color.Color
	Error            color.Color
	OnError          color.Color
	Success          color.Color
	Warning          color.Color
	Text             color.Color
	TextVariant      color.Color
	Outline          color.Color
	OutlineVariant   color.Color
	Surface          color.Color
	SurfaceContainer color.Color
	// ANSI is the 16-color terminal scheme the shell harmonized with the same
	// wallpaper (terminal.json): the other hues, for when one accent is not enough.
	ANSI [16]color.Color
	Dark bool // the scheme was made for a dark background
}

// Generated is the palette the shell wrote for the current wallpaper, from
// $XDG_STATE_HOME/quickshell/user/generated/colors.json (the same file setup's
// tui.sh reads). ok is false on a machine where the shell never themed itself.
func Generated() (p Palette, ok bool) {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return p, false
		}
		state = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(state, "quickshell", "user", "generated")
	data, err := os.ReadFile(filepath.Join(dir, "colors.json"))
	if err != nil {
		return p, false
	}
	var m map[string]any
	if json.Unmarshal(data, &m) != nil {
		return p, false
	}
	missing := false
	get := func(keys ...string) color.Color {
		for _, k := range keys {
			if s, ok := m[k].(string); ok && len(s) == 7 && s[0] == '#' {
				return lipgloss.Color(s)
			}
		}
		missing = true
		return nil
	}
	p = Palette{
		Primary:          get("primary"),
		Secondary:        get("secondary"),
		Tertiary:         get("tertiary"),
		Error:            get("error"),
		OnError:          get("on_error"),
		Success:          get("success", "term10"),
		Text:             get("on_surface"),
		TextVariant:      get("on_surface_variant"),
		Outline:          get("outline"),
		OutlineVariant:   get("outline_variant"),
		Surface:          get("surface", "background"),
		SurfaceContainer: get("surface_container"),
	}
	if data, err := os.ReadFile(filepath.Join(dir, "terminal.json")); err == nil && json.Unmarshal(data, &m) == nil {
		for i := range p.ANSI {
			p.ANSI[i] = get(fmt.Sprintf("term%d", i))
		}
		p.Warning = p.ANSI[11]
	} else {
		missing = true
	}
	if missing {
		return Palette{}, false
	}
	p.Dark = luminance(p.Surface) < 0.5
	return p, true
}

func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 0xffff
}
