// Package activity keeps one notification up to date while a command runs:
// `inir run -- CMD` and the shell hook for long commands.
package activity

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Meter reads a command's output for the latest percentage and line.
type Meter struct {
	mu      sync.Mutex
	carry   string
	percent int
	line    string
}

func NewMeter() *Meter { return &Meter{percent: -1} }

var (
	escapes = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)?|\x1b[()][0-9A-Z]|\x1b[=>78]`)
	percent = regexp.MustCompile(`(\d{1,3})(?:[.,]\d+)?\s?%`)
)

func (m *Meter) Write(p []byte) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.carry + string(p)
	// The piece after the last break is read again with the next chunk: a
	// number or an escape can be split between reads, and \r progress lines
	// never end in \n.
	m.carry = s
	if i := strings.LastIndexAny(s, "\r\n"); i >= 0 {
		m.carry = s[i+1:]
	}
	if len(m.carry) > 1024 {
		m.carry = m.carry[len(m.carry)-1024:]
	}
	for _, seg := range strings.FieldsFunc(s, func(r rune) bool { return r == '\r' || r == '\n' }) {
		m.scan(seg)
	}
	return len(p), nil
}

func (m *Meter) scan(seg string) {
	clean := strings.TrimSpace(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, escapes.ReplaceAllString(seg, "")))
	if clean == "" {
		return
	}
	m.line = clean
	if all := percent.FindAllStringSubmatch(clean, -1); len(all) > 0 {
		if v, err := strconv.Atoi(all[len(all)-1][1]); err == nil && v <= 100 {
			m.percent = v
		}
	}
}

// State is the last percentage printed (-1 for none yet) and the last line.
func (m *Meter) State() (int, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.percent, m.line
}
