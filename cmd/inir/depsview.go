package main

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/snowarch/inir-cli/internal/deps"
	"github.com/snowarch/inir-cli/internal/ui"
)

// printPlan draws `inir deps plan`: the packages of each install group in
// columns, the ones still missing lit up when steps knows what this machine
// has (nil for another distro's plan), and a card with the count.
func printPlan(w io.Writer, p *deps.Plan, steps *deps.Steps) {
	t := ui.Current()
	ui.Header(w, t, "inir deps", p.Distro+" · core "+strings.Join(p.Groups, " "))
	lipgloss.Fprintln(w)

	missing := map[string]bool{} // installable and not installed yet
	var unavailable []string
	if steps != nil {
		for _, list := range [][]string{steps.Install, steps.AUR} {
			for _, x := range list {
				missing[x] = true
			}
		}
		unavailable = steps.Unavailable
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(t.Accent)
	note := lipgloss.NewStyle().Foreground(t.Muted)
	have := lipgloss.NewStyle().Foreground(t.Faint)
	need := lipgloss.NewStyle().Foreground(t.Warning).Bold(true)
	gone := lipgloss.NewStyle().Foreground(t.Error).Bold(true)

	section := func(name string, pkgs []string) {
		if len(pkgs) == 0 {
			return
		}
		n, lost := 0, 0
		for _, x := range pkgs {
			switch {
			case missing[x]:
				n++
			case slices.Contains(unavailable, x):
				lost++
			}
		}
		status := fmt.Sprintf("%d packages", len(pkgs))
		switch {
		case steps == nil:
		case n+lost == 0:
			status += " · " + lipgloss.NewStyle().Foreground(t.Success).Render("all installed")
		default:
			if n > 0 {
				status += " · " + need.UnsetBold().Render(fmt.Sprintf("%d to install", n))
			}
			if lost > 0 {
				status += " · " + gone.UnsetBold().Render(fmt.Sprintf("%d not found", lost))
			}
		}
		lipgloss.Fprintln(w, "  "+title.Render(strings.ToUpper(name))+"  "+note.Render(status))
		cells := make([]string, len(pkgs))
		for i, x := range pkgs {
			switch {
			case steps == nil:
				cells[i] = note.Render(x)
			case slices.Contains(unavailable, x):
				cells[i] = gone.Render(x)
			case missing[x]:
				cells[i] = need.Render(x)
			default:
				cells[i] = have.Render(x)
			}
		}
		lipgloss.Fprintln(w, columns(cells, pkgs, ui.Width()-4))
		lipgloss.Fprintln(w)
	}

	byGroup := map[string][]string{}
	for _, x := range p.Packages {
		byGroup[p.Group[x]] = append(byGroup[p.Group[x]], x)
	}
	for _, g := range append([]string{"core"}, deps.Groups...) {
		section(g, byGroup[g])
	}
	section("aur", p.AUR)
	for _, f := range p.First {
		lipgloss.Fprintln(w, "  "+note.Render("first available of ")+strings.Join(f, note.Render(", ")))
	}
	if len(p.Builds) > 0 {
		lipgloss.Fprintln(w, "  "+note.Render("built from source when still missing: ")+strings.Join(p.Builds, " "))
	}

	lipgloss.Fprintln(w)
	total := len(p.Packages) + len(p.AUR)
	switch {
	case steps == nil:
		ui.Card(w, t.Accent, fmt.Sprintf("%d packages for %s", total, p.Distro))
	case len(missing)+len(unavailable) == 0:
		ui.Card(w, t.Success, fmt.Sprintf("%d packages, all installed.", total))
	default:
		body := fmt.Sprintf("%d packages, %d to install.", total, len(missing))
		if len(unavailable) > 0 {
			body += "\n" + lipgloss.NewStyle().Foreground(t.Error).Render(
				fmt.Sprintf("%d not in your repositories: %s", len(unavailable), strings.Join(unavailable, " ")))
		}
		tone := t.Warning
		if len(missing) > 0 {
			body += "\n" + note.Render("inir deps install takes care of that.")
		} else {
			tone = t.Error
		}
		ui.Card(w, tone, body)
	}
}

// columns flows cells into as many columns as fit in width, read top to
// bottom like ls. plain holds the cells without styling, for measuring.
func columns(cells, plain []string, width int) string {
	colw := 0
	for _, x := range plain {
		colw = max(colw, lipgloss.Width(x))
	}
	colw += 3
	cols := max(1, width/colw)
	rows := (len(cells) + cols - 1) / cols
	var b strings.Builder
	for r := 0; r < rows; r++ {
		b.WriteString("  ")
		for c := 0; c < cols; c++ {
			i := c*rows + r
			if i >= len(cells) {
				break
			}
			b.WriteString(cells[i])
			if c < cols-1 && (c+1)*rows+r < len(cells) {
				b.WriteString(strings.Repeat(" ", colw-lipgloss.Width(plain[i])))
			}
		}
		if r < rows-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}
