package activity

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"golang.org/x/sys/unix"

	"github.com/snowarch/inir-cli/internal/ui"
)

// Run runs argv here, in this terminal, and keeps one notification about it
// up to date: the percentage it prints or the time so far, then how it ended.
// It returns the command's exit code.
func Run(argv []string, title string) int {
	if title == "" {
		title = Title(strings.Join(argv, " "))
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	meter := NewMeter()
	var master, slave *os.File
	if term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) {
		// Through a pty the command still sees a terminal and draws its own
		// progress, which is what there is to read.
		if m, s, err := openPTY(); err == nil {
			master, slave = m, s
			defer master.Close()
			fitPTY(master)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
		}
	}
	if master == nil {
		cmd.Stdin = os.Stdin
		cmd.Stdout = io.MultiWriter(os.Stdout, meter)
		cmd.Stderr = io.MultiWriter(os.Stderr, meter)
	}
	if err := cmd.Start(); err != nil {
		ui.Log("inir run").Error(err)
		if errors.Is(err, exec.ErrNotFound) {
			return 127
		}
		return 126
	}
	output := make(chan struct{})
	if master != nil {
		slave.Close()
		if st, err := term.MakeRaw(os.Stdin.Fd()); err == nil {
			defer term.Restore(os.Stdin.Fd(), st)
		}
		go io.Copy(master, os.Stdin)
		go func() { io.Copy(io.MultiWriter(os.Stdout, meter), master); close(output) }()
	} else {
		close(output)
	}

	l := newLive(title)
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGWINCH)
	defer signal.Stop(sigs)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	start := time.Now()
	events := l.events()
	var waitErr error
	var kill *time.Timer
	cancelled := false
	for running := true; running; {
		select {
		case waitErr = <-done:
			running = false
		case <-tick.C:
			pct, line := meter.State()
			l.update(time.Since(start), pct, line)
		case s := <-sigs:
			switch {
			case s == syscall.SIGWINCH:
				if master != nil {
					fitPTY(master)
				}
			case s == syscall.SIGINT && master == nil:
				// Ctrl-C reached the command from the terminal already.
			default:
				cmd.Process.Signal(s)
			}
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if l.handle(ev) && !cancelled {
				cancelled = true
				kill = stop(cmd.Process, master != nil)
			}
		}
	}
	if kill != nil {
		kill.Stop()
	}
	select {
	case <-output:
	case <-time.After(300 * time.Millisecond): // something it started still holds the pty
	}
	code := exitCode(waitErr)
	_, line := meter.State()
	l.finish(time.Since(start), code, cancelled, line)
	return code
}

// stop asks the command (its whole process group, when it has its own pty)
// to end, and kills it if it is still there three seconds later.
func stop(p *os.Process, group bool) *time.Timer {
	pid := p.Pid
	if group {
		pid = -pid
	}
	syscall.Kill(pid, syscall.SIGTERM)
	return time.AfterFunc(3*time.Second, func() { syscall.Kill(pid, syscall.SIGKILL) })
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 1
}

func openPTY() (master, slave *os.File, err error) {
	master, err = os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return nil, nil, err
	}
	fd := int(master.Fd())
	if err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err == nil {
		var n uint32
		if n, err = unix.IoctlGetUint32(fd, unix.TIOCGPTN); err == nil {
			slave, err = os.OpenFile("/dev/pts/"+strconv.Itoa(int(n)), os.O_RDWR|syscall.O_NOCTTY, 0)
		}
	}
	if err != nil {
		master.Close()
		return nil, nil, err
	}
	return master, slave, nil
}

// fitPTY gives the pty the size of the terminal we run in.
func fitPTY(master *os.File) {
	if ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ); err == nil {
		unix.IoctlSetWinsize(int(master.Fd()), unix.TIOCSWINSZ, ws)
	}
}
