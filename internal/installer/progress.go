package installer

import (
	"fmt"
	"math"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/snowarch/inir-cli/internal/ui"
)

// How long each stage usually takes on a fresh Arch install (measured in a
// clean container), so the bar moves at the pace the work does.
var expected = map[string]time.Duration{
	"conflicts": 2 * time.Second,
	"deps":      9 * time.Minute,
	"system":    10 * time.Second,
	"files":     90 * time.Second,
	"finish":    3 * time.Second,
}

// Progress is the live view of a running install.
type Progress struct {
	t       ui.Theme
	stages  []*Stage
	cur     int // the running stage, -1 before the first
	tail    []string
	start   time.Time
	spin    spinner.Model
	bar     progress.Model
	width   int
	stop    func()
	stopAsk bool // Ctrl+C once: asked to confirm
	Err     error
	Stopped bool
	Elapsed time.Duration
	Reboot  []string // why the machine needs a restart before iNiR is all there
}

func NewProgress(t ui.Theme, stop func()) *Progress {
	width := min(ui.Width(), 92)
	return &Progress{
		t:      t,
		stages: Stages(),
		cur:    -1,
		start:  time.Now(),
		spin:   spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(lipgloss.NewStyle().Foreground(t.Accent))),
		bar: progress.New(progress.WithColors(t.Accent, t.Success), progress.WithoutPercentage(),
			progress.WithWidth(width-30), progress.WithFillCharacters('━', '─')),
		width: width,
		stop:  stop,
	}
}

func (m *Progress) Stages() []*Stage { return m.stages }

func (m *Progress) Init() tea.Cmd { return m.spin.Tick }

func (m *Progress) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			if m.stopAsk {
				m.Stopped = true
				go m.stop()
				return m, nil
			}
			m.stopAsk = true
		} else {
			m.stopAsk = false
		}
	case tea.WindowSizeMsg:
		m.width = min(msg.Width, 92)
		m.bar.SetWidth(m.width - 30)
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case EventMsg:
		m.event(msg)
	case OutputMsg:
		line := string(msg)
		if i := strings.LastIndexByte(strings.TrimRight(line, "\r"), '\r'); i >= 0 {
			line = line[i+1:] // a progress meter redrawing itself: what a terminal would show last
		}
		line = strings.TrimSpace(strings.Map(func(r rune) rune {
			if r < ' ' {
				return -1
			}
			return r
		}, ansi.Strip(line)))
		if line != "" {
			m.tail = append(m.tail, line)
			if len(m.tail) > 3 {
				m.tail = m.tail[len(m.tail)-3:]
			}
		}
	case DoneMsg:
		m.Err = msg.Err
		m.Elapsed = time.Since(m.start)
		now := time.Now()
		for i, s := range m.stages {
			switch {
			case i == m.cur && msg.Err != nil:
				s.Status, s.End = Failed, now
			case i == m.cur:
				s.Status, s.End = Done, now
			case s.Status == Waiting:
				s.Status = Skipped
			}
		}
		m.tail = nil
		return m, tea.Quit
	}
	return m, nil
}

func (m *Progress) event(e EventMsg) {
	now := time.Now()
	if e.Kind == "reboot" {
		m.Reboot = append(m.Reboot, e.Text)
		return
	}
	if e.Kind == "stage" {
		for i, s := range m.stages {
			if s.Key != e.Text {
				continue
			}
			if m.cur >= 0 && m.stages[m.cur].Status == Running {
				m.stages[m.cur].Status, m.stages[m.cur].End = Done, now
			}
			for _, before := range m.stages[:i] {
				if before.Status == Waiting {
					before.Status = Skipped
				}
			}
			m.cur = i
			s.Status, s.Start = Running, now
			m.tail = nil
			return
		}
		return
	}
	if m.cur < 0 {
		m.cur = 0
		m.stages[0].Status, m.stages[0].Start = Running, now
	}
	s := m.stages[m.cur]
	s.Last = e.Text
	if e.Kind == "warn" || e.Kind == "error" {
		s.Warnings = append(s.Warnings, e.Text)
	}
}

// fraction of the whole install done: finished stages by their usual weight,
// the running one easing toward its share as its usual time passes.
func (m *Progress) fraction() float64 {
	var total, done float64
	for _, s := range m.stages {
		w := expected[s.Key].Seconds()
		total += w
		switch s.Status {
		case Done, Failed, Skipped:
			done += w
		case Running:
			done += w * eased(time.Since(s.Start).Seconds()/w)
		}
	}
	return done / total
}

// eased is how much of a stage to show after x of its usual time: straight
// up to 90%, then creeping toward the end without reaching it.
func eased(x float64) float64 {
	if x < 0.9 {
		return x
	}
	return 0.9 + 0.1*(1-math.Exp(-(x-0.9)/0.3))
}

func (m *Progress) View() tea.View {
	t := m.t
	text := lipgloss.NewStyle().Foreground(t.Text)
	muted := lipgloss.NewStyle().Foreground(t.Muted)
	faint := lipgloss.NewStyle().Foreground(t.Faint)
	var b strings.Builder
	b.WriteString("\n")
	for _, s := range m.stages {
		var mark, title, clock string
		switch s.Status {
		case Waiting:
			mark, title = faint.Render("○"), faint.Render(s.Title)
		case Running:
			mark, title = m.spin.View(), text.Bold(true).Render(s.Title)
			clock = Duration(time.Since(s.Start))
		case Done:
			mark, title = lipgloss.NewStyle().Foreground(t.Success).Render("✓"), text.Render(s.Title)
			clock = Duration(s.End.Sub(s.Start))
			if len(s.Warnings) > 0 {
				mark = lipgloss.NewStyle().Foreground(t.Warning).Render("!")
			}
		case Failed:
			mark, title = lipgloss.NewStyle().Foreground(t.Error).Render("✗"), text.Render(s.Title)
			clock = Duration(s.End.Sub(s.Start))
		case Skipped:
			mark, title = faint.Render("–"), faint.Render(s.Title+" (skipped)")
		}
		pad := max(m.width-8-lipgloss.Width(title)-lipgloss.Width(clock), 1)
		b.WriteString("  " + mark + " " + title + strings.Repeat(" ", pad) + faint.Render(clock) + "\n")
		if s.Status == Running {
			if s.Last != "" {
				b.WriteString("      " + muted.Render(ansi.Truncate(s.Last, m.width-8, "…")) + "\n")
			}
			for _, l := range m.tail {
				b.WriteString("      " + faint.Render("│ "+ansi.Truncate(l, m.width-10, "…")) + "\n")
			}
		}
	}
	b.WriteString("\n  " + m.bar.ViewAs(m.fraction()) + "  " + faint.Render(Duration(time.Since(m.start))) + "\n")
	switch {
	case m.Stopped:
		b.WriteString("  " + lipgloss.NewStyle().Foreground(t.Warning).Render("Stopping…") + "\n")
	case m.stopAsk:
		b.WriteString("  " + lipgloss.NewStyle().Foreground(t.Warning).Render("Press Ctrl+C again to stop the install.") + "\n")
	case m.Err == nil && m.Elapsed == 0:
		b.WriteString("  " + faint.Render("ctrl+c stop") + "\n")
	}
	return tea.NewView(b.String())
}

// Duration is how long, the way a person says it: 4s, 2m 05s, 1h 02m.
func Duration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}
