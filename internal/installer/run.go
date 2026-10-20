package installer

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Stage is one part of setup's install pipeline, as setup announces it on the
// events descriptor ("stage<TAB>deps").
type Stage struct {
	Key, Title string
	Expected   time.Duration // how long it usually takes, so the bar keeps its pace
	Start, End time.Time
	Status     Status
	Last       string   // the latest thing setup said in this stage
	Warnings   []string // what it warned about, for the summary
}

type Status int

const (
	Waiting Status = iota
	Running
	Done
	Failed
	Skipped
)

// InstallStages in the order setup's run_install goes through them, with the
// time each took on a fresh Arch install (measured in a clean container).
func InstallStages() []*Stage {
	return []*Stage{
		{Key: "conflicts", Title: "Checking what is here", Expected: 2 * time.Second},
		{Key: "deps", Title: "Packages", Expected: 9 * time.Minute},
		{Key: "system", Title: "System setup", Expected: 10 * time.Second},
		{Key: "files", Title: "Shell and configs", Expected: 90 * time.Second},
		{Key: "finish", Title: "Finishing up", Expected: 3 * time.Second},
	}
}

// UpdateStages are run_update's seven steps, keyed by their number, with
// times from a real update of two commits (the restart waits on the shell).
func UpdateStages() []*Stage {
	return []*Stage{
		{Key: "1", Title: "Pulling changes", Expected: 2 * time.Second},
		{Key: "2", Title: "Syncing files", Expected: 3 * time.Second},
		{Key: "3", Title: "Running migrations", Expected: time.Second},
		{Key: "4", Title: "Finalizing", Expected: time.Second},
		{Key: "5", Title: "Checking dependencies", Expected: 2 * time.Second},
		{Key: "6", Title: "Python packages", Expected: 2 * time.Second},
		{Key: "7", Title: "Restarting the shell", Expected: 6 * time.Second},
	}
}

// Messages from a running setup.
type (
	EventMsg  struct{ Kind, Text string } // a line from the events descriptor
	OutputMsg string                      // a line of setup's own output
	DoneMsg   struct{ Err error }         // setup exited
)

// Run is a setup install in progress.
type Run struct {
	cmd     *exec.Cmd
	Log     string // everything setup printed, for when something goes wrong
	waitErr error
	done    chan struct{}
}

// Start runs `setup <sub> -y <args>` from setupDir with env added, quietly:
// no terminal on stdin (setup asks nothing), its output to the log and to send,
// and its progress events on descriptor 3. detach also takes the terminal away
// from it, so a sudo with no ticket fails at once instead of stopping the whole
// group on a read from a terminal the progress view owns.
func Start(setupDir, sub string, args, env []string, detach bool, send func(any)) (*Run, error) {
	logDir := filepath.Join(stateHome(), "inir", sub)
	os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, time.Now().Format("2006-01-02-150405")+".log")
	logf, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	evR, evW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(filepath.Join(setupDir, "setup"), append([]string{sub, "-y"}, args...)...)
	cmd.Dir = setupDir
	cmd.Stdout, cmd.Stderr = outW, outW
	cmd.ExtraFiles = []*os.File{evW} // descriptor 3
	cmd.Env = append(os.Environ(), append([]string{"INIR_CLI_DELEGATED=1", "INIR_EVENTS_FD=3"}, env...)...)
	// Its own process group, so stopping the install stops everything it started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: !detach, Setsid: detach}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	evW.Close()
	outW.Close()
	r := &Run{cmd: cmd, Log: logPath, done: make(chan struct{})}
	var wg sync.WaitGroup
	wg.Add(2)
	var logMu sync.Mutex
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(evR)
		for sc.Scan() {
			kind, text, _ := strings.Cut(sc.Text(), "\t")
			logMu.Lock()
			io.WriteString(logf, "[inir] "+kind+": "+text+"\n")
			logMu.Unlock()
			send(EventMsg{kind, text})
		}
	}()
	go func() {
		defer wg.Done()
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 64*1024), 1024*1024)
		for sc.Scan() {
			line := sc.Text()
			logMu.Lock()
			io.WriteString(logf, line+"\n")
			logMu.Unlock()
			send(OutputMsg(line))
		}
	}()
	go func() {
		r.waitErr = cmd.Wait()
		// Something setup started in the background (the shell it restarts) can
		// hold the pipes open: what is left after a moment is not setup's.
		read := make(chan struct{})
		go func() { wg.Wait(); close(read) }()
		select {
		case <-read:
		case <-time.After(2 * time.Second):
			evR.Close()
			outR.Close()
			<-read
		}
		logf.Close()
		close(r.done)
		send(DoneMsg{r.waitErr})
	}()
	return r, nil
}

// Stop interrupts setup and everything under it.
func (r *Run) Stop() {
	syscall.Kill(-r.cmd.Process.Pid, syscall.SIGINT)
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		syscall.Kill(-r.cmd.Process.Pid, syscall.SIGKILL)
	}
}

func stateHome() string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return d
	}
	return filepath.Join(os.Getenv("HOME"), ".local", "state")
}
