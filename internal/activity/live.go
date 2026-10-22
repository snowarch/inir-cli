package activity

import (
	"fmt"
	"strings"
	"time"

	"github.com/snowarch/inir-cli/internal/notify"
)

// live is the one notification of a running command, replaced in place.
type live struct {
	bus       *notify.Conn
	id        uint32
	title     string
	dismissed bool
	body      string
	percent   int
	at        time.Time
}

// newLive connects to the session bus; without one every call is a no-op and
// the command just runs.
func newLive(title string) *live {
	l := &live{title: title, percent: -1}
	l.bus, _ = notify.Dial()
	return l
}

func (l *live) events() <-chan notify.Event {
	if l.bus == nil {
		return nil
	}
	return l.bus.Events
}

// update shows the time so far and the percentage, when there is one, as the
// standard value hint. Text-only changes go out once a second at most.
func (l *live) update(elapsed time.Duration, pct int, line string) {
	if l.bus == nil || l.dismissed || elapsed < time.Second {
		return
	}
	body := clock(elapsed)
	if pct >= 0 {
		body = fmt.Sprintf("%d%% · %s", pct, body)
	}
	if line != "" {
		body += "\n" + trim(line, 90)
	}
	if body == l.body || (pct == l.percent && time.Since(l.at) < time.Second) {
		return
	}
	hints := map[string]any{"urgency": notify.Low, "transient": true}
	if pct >= 0 {
		hints["value"] = int32(pct)
	}
	l.show(notify.Note{Icon: "utilities-terminal", Body: body, Actions: []string{"cancel", "Stop"},
		Hints: hints, Timeout: 0})
	l.body, l.percent, l.at = body, pct, time.Now()
}

// finish replaces the live notification with how the command ended.
func (l *live) finish(elapsed time.Duration, code int, cancelled bool, line string) {
	if l.bus == nil {
		return
	}
	icon, body := "utilities-terminal", "Done in "+clock(elapsed)
	switch {
	case cancelled:
		body = "Stopped after " + clock(elapsed)
	case code > 128 && code < 160:
		body = fmt.Sprintf("Killed by signal %d after %s", code-128, clock(elapsed))
		icon = "dialog-error"
	case code != 0:
		body = fmt.Sprintf("Failed with exit %d after %s", code, clock(elapsed))
		icon = "dialog-error"
	}
	if code != 0 && !cancelled && line != "" {
		body += "\n" + trim(line, 90)
	}
	l.dismissed = false
	l.show(notify.Note{Icon: icon, Body: body, Hints: map[string]any{"urgency": notify.Normal}, Timeout: -1})
	l.bus.Close()
}

// dismiss takes the notification away (the terminal it belonged to closed).
func (l *live) dismiss() {
	if l.bus != nil && l.id != 0 {
		l.bus.Dismiss(l.id)
	}
}

// handle reads a server signal; true when the person pressed Stop.
func (l *live) handle(ev notify.Event) bool {
	if l.id == 0 || ev.ID != l.id {
		return false
	}
	if ev.Closed {
		l.dismissed = true
		return false
	}
	return ev.Action == "cancel"
}

func (l *live) show(n notify.Note) {
	if l.dismissed {
		return
	}
	n.App, n.Summary, n.Replaces = "inir", l.title, l.id
	n.Body = markup.Replace(n.Body)
	id, err := l.bus.Notify(n)
	if err != nil {
		l.bus.Close()
		l.bus = nil
		return
	}
	l.id = id
}

var markup = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func clock(d time.Duration) string {
	s := int(d.Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func trim(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// Title names a command line in a notification: its words, the program
// without its directory, short enough for one line.
func Title(line string) string {
	words := strings.Fields(line)
	if len(words) == 0 {
		return "command"
	}
	if i := strings.LastIndexByte(words[0], '/'); i >= 0 && i < len(words[0])-1 {
		words[0] = words[0][i+1:]
	}
	return trim(strings.Join(words, " "), 48)
}
