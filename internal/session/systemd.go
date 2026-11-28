package session

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Systemd is the user manager as the session start sees it, asked once.
type Systemd struct {
	runtimeDir string
	probed     bool
	usable     bool
	env        map[string]string
}

// NewSystemd asks the manager whose socket is under runtimeDir; it answers nothing when there is none (runit).
func NewSystemd(runtimeDir string) *Systemd { return &Systemd{runtimeDir: runtimeDir} }

func (s *Systemd) probe() {
	if s.probed {
		return
	}
	s.probed = true
	// The socket exists only while the manager runs. A probe that times out is a busy manager, still systemd.
	if _, err := os.Stat(filepath.Join(s.runtimeDir, "systemd", "private")); err != nil {
		return
	}
	out, err := systemctl(3*time.Second, "--user", "show-environment")
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return
	}
	s.usable = true
	if err != nil {
		return
	}
	s.env = map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			if _, seen := s.env[k]; !seen {
				s.env[k] = v
			}
		}
	}
}

// Usable reports whether a user manager runs (else the supervisor is runit).
func (s *Systemd) Usable() bool { s.probe(); return s.usable }

// Environment is the manager's environment, empty when it did not answer.
func (s *Systemd) Environment() map[string]string { s.probe(); return s.env }

// Show reads properties of a unit.
func (s *Systemd) Show(unit string, props ...string) map[string]string {
	if !s.Usable() {
		return nil
	}
	args := []string{"--user", "show", unit}
	for _, p := range props {
		args = append(args, "-p", p)
	}
	out, err := systemctl(3*time.Second, args...)
	if err != nil {
		return nil
	}
	m := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			m[k] = v
		}
	}
	return m
}

// MainPID of a unit, 0 when it has none.
func (s *Systemd) MainPID(unit string) int {
	pid, _ := strconv.Atoi(s.Show(unit, "MainPID")["MainPID"])
	return pid
}

// NiriSockets finds the one Niri socket and Wayland display that niri.service's own process serves
// (niri.<display>.<pid>.sock beside <display>). Two or none: nothing, the session's values stand.
func (s *Systemd) NiriSockets(e Env) (socket, display string, ok bool) {
	if s.Environment() == nil {
		return "", "", false
	}
	u := s.Show("niri.service", "ActiveState", "MainPID")
	pid, _ := strconv.Atoi(u["MainPID"])
	if u["ActiveState"] != "active" || pid <= 0 {
		return "", "", false
	}
	dir := runtimeDirOf(e)
	suffix := "." + strconv.Itoa(pid) + ".sock"
	matches, _ := filepath.Glob(filepath.Join(dir, "niri.*"+suffix))
	found := 0
	for _, m := range matches {
		name := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), "niri."), suffix)
		if !isSocket(m) || !strings.HasPrefix(name, "wayland-") || !isSocket(filepath.Join(dir, name)) {
			continue
		}
		socket, display = m, name
		found++
	}
	return socket, display, found == 1
}

func systemctl(timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	cmd.Stdout = &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, context.DeadlineExceeded
	}
	return out.Bytes(), err
}

func isSocket(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode()&os.ModeSocket != 0
}

func itoa(n int) string { return strconv.Itoa(n) }
