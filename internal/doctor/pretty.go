package doctor

import (
	"fmt"
	"image/color"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/tree"

	"github.com/snowarch/inir-cli/internal/ui"
)

// printer draws a doctor run with Lip Gloss: one line per step, what a step
// found as a tree under it, and a card with the verdict. On a terminal the
// step being checked spins until its line replaces it.
type printer struct {
	w     io.Writer
	total int
	live  bool

	num, title, text, muted, faint, hint lipgloss.Style
	mark                                 map[Status]string
	lineMark                             map[Level]string
	theme                                ui.Theme

	stopSpin chan struct{}
	spinDone sync.WaitGroup
}

// dots is the spinner Charm's Bubbles calls Dot.
var dots = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

func newPrinter(w io.Writer, total int) *printer {
	t := ui.Current()
	paint := func(c color.Color, s string) string { return lipgloss.NewStyle().Foreground(c).Render(s) }
	ok, warn, bad, info := paint(t.Success, "✓"), paint(t.Warning, "!"), paint(t.Error, "✗"), paint(t.Muted, "→")
	f, isFile := w.(*os.File)
	return &printer{
		w: w, total: total, theme: t,
		live:  isFile && f == os.Stdout && isTerminal(f) && os.Getenv("TERM") != "dumb",
		num:   lipgloss.NewStyle().Foreground(t.Faint).Width(len(fmt.Sprint(total))).Align(lipgloss.Right),
		title: lipgloss.NewStyle().Foreground(t.Text),
		text:  lipgloss.NewStyle().Foreground(t.Muted),
		muted: lipgloss.NewStyle().Foreground(t.Muted),
		faint: lipgloss.NewStyle().Foreground(t.Faint),
		hint:  lipgloss.NewStyle().Foreground(t.Faint).Italic(true),
		mark:  map[Status]string{StatusPass: ok, StatusFail: bad, StatusFixable: warn, StatusFixed: ok, StatusSkip: info},
		lineMark: map[Level]string{LevelPass: ok, LevelFail: bad, LevelFixable: warn, LevelFixed: ok,
			LevelWarn: warn, LevelInfo: info},
	}
}

func (p *printer) header(fix bool) {
	what := "checking your install"
	if fix {
		what = "checking your install, fixing what it can"
	}
	ui.Header(p.w, p.theme, "inir doctor", what)
	lipgloss.Fprintln(p.w)
}

// spin shows the step being checked until stop.
func (p *printer) spin(st Step) {
	if !p.live {
		return
	}
	p.stopSpin = make(chan struct{})
	p.spinDone.Add(1)
	label := p.faint.Render(strings.TrimSuffix(st.Title, "...") + "…")
	go func() {
		defer p.spinDone.Done()
		tick := time.NewTicker(80 * time.Millisecond)
		defer tick.Stop()
		for i := 0; ; i++ {
			lipgloss.Fprint(p.w, "\r  "+p.num.Render(fmt.Sprint(st.N))+"  "+
				lipgloss.NewStyle().Foreground(p.theme.Accent).Render(dots[i%len(dots)])+"  "+label)
			select {
			case <-p.stopSpin:
				fmt.Fprint(p.w, "\r\x1b[2K")
				return
			case <-tick.C:
			}
		}
	}()
}

func (p *printer) stop() {
	if p.stopSpin == nil {
		return
	}
	close(p.stopSpin)
	p.spinDone.Wait()
	p.stopSpin = nil
}

func (p *printer) step(s StepResult) {
	results := 0
	for _, l := range s.Lines {
		if l.Level != LevelHint {
			results++
		}
	}
	lead := "  " + p.num.Render(fmt.Sprint(s.N)) + "  " + p.mark[s.Status] + "  "
	if results == 1 && len(s.Lines) == 1 {
		lipgloss.Fprintln(p.w, lead+p.title.Render(lineText(s.Lines[0])))
		return
	}
	lipgloss.Fprintln(p.w, lead+p.title.Render(heading(s.Title)))
	t := tree.New().Enumerator(tree.RoundedEnumerator).EnumeratorStyle(p.faint)
	for _, l := range s.Lines {
		if l.Level == LevelHint {
			t.Child(" " + p.hint.Render(l.Text))
			continue
		}
		t.Child(" " + p.lineMark[l.Level] + " " + p.text.Render(lineText(l)))
	}
	pad := strings.Repeat(" ", 2+lipgloss.Width(p.num.Render("0"))+2)
	for _, line := range strings.Split(t.String(), "\n") {
		lipgloss.Fprintln(p.w, pad+line)
	}
}

func (p *printer) summary(r *Report) {
	count := func(n int, label string, c color.Color) string {
		st := p.faint
		if n > 0 {
			st = lipgloss.NewStyle().Foreground(c).Bold(true)
		}
		return st.Render(fmt.Sprintf("%d %s", n, label))
	}
	sep := p.faint.Render("  ·  ")
	counts := count(r.Summary.Passed, "passed", p.theme.Success) + sep
	if r.Mode == "check" {
		counts += count(r.Summary.Fixable, "fixable", p.theme.Warning)
	} else {
		counts += count(r.Summary.Fixed, "fixed", p.theme.Success)
	}
	counts += sep + count(r.Summary.Failed, "failed", p.theme.Error)

	verdict, tone := "Everything looks good.", p.theme.Success
	switch {
	case r.Summary.Failed > 0:
		verdict, tone = "Some issues need a hand: the lines marked ✗ say what.", p.theme.Error
	case r.Summary.Fixable > 0:
		verdict, tone = "", p.theme.Warning
	case r.Summary.Fixed > 0:
		verdict = "Fixed it all. Restart the shell to apply: inir restart"
	}
	if r.Summary.Fixable > 0 {
		verdict = strings.TrimPrefix(verdict+"\nRun inir doctor without --check and it fixes what is marked !.", "\n")
	}
	lipgloss.Fprintln(p.w)
	ui.Card(p.w, tone, counts+"\n"+lipgloss.NewStyle().Foreground(tone).Render(verdict))
}

// heading is a step's title as a heading: "Checking theme colors..." reads
// "Theme colors".
func heading(title string) string {
	t := strings.TrimSuffix(strings.TrimPrefix(title, "Checking "), "...")
	if t == "" {
		return title
	}
	return strings.ToUpper(t[:1]) + t[1:]
}
