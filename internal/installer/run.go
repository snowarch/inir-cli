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

// Stages in the order setup's run_install goes through them.
func Stages() []*Stage {
	return []*Stage{
		{Key: "conflicts", Title: "Checking what is here"},
		{Key: "deps", Title: "Packages"},
		{Key: "system", Title: "System setup"},
		{Key: "files", Title: "Shell and configs"},
		{Key: "finish", Title: "Finishing up"},
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

// Start runs `setup install -y <args>` from setupDir with env added, quietly:
// no terminal on stdin (setup asks nothing), its output to the log and to send,
// and its progress events on descriptor 3.
func Start(setupDir string, args, env []string, send func(any)) (*Run, error) {
	logDir := filepath.Join(stateHome(), "inir", "install")
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
	cmd := exec.Command(filepath.Join(setupDir, "setup"), append([]string{"install", "-y"}, args...)...)
	cmd.Dir = setupDir
	cmd.Stdout, cmd.Stderr = outW, outW
	cmd.ExtraFiles = []*os.File{evW} // descriptor 3
	cmd.Env = append(os.Environ(), append([]string{"INIR_CLI_DELEGATED=1", "INIR_EVENTS_FD=3"}, env...)...)
	// Its own process group, so stopping the install stops everything it started.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
