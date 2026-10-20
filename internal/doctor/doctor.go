package doctor

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Options for a doctor run.
type Options struct {
	Fix    bool      // repair through the bash doctor (`setup doctor --only=N`)
	Yes    bool      // no questions (setup -y)
	Only   []int     // steps to run; all when empty
	Out    io.Writer // where progress and the bash doctor's own output go
	Pretty bool      // print each step as it finishes
	// Offline skips `git fetch` in the repo step, which then reports the remote check skipped.
	Offline bool
}

func (o Options) wants(n int) bool {
	if len(o.Only) == 0 {
		return true
	}
	for _, x := range o.Only {
		if x == n {
			return true
		}
	}
	return false
}

// Run checks every step and, with Fix, hands the failing ones that the bash
// doctor repairs to it, one at a time and in order, since a repair can change
// what a later step finds (dependencies first).
func Run(e *Env, o Options) *Report {
	if o.Out == nil {
		o.Out = io.Discard
	}
	c := &checker{e: e, jq: lookPath("jq") != "", offline: o.Offline}
	r := &Report{Schema: 1, Mode: "check", OS: e.OS, Runtime: e.RuntimeDir, Repo: e.RepoRoot}
	if o.Fix {
		r.Mode = "fix"
	}
	if o.Fix && !c.bashDoctorTakesSteps() {
		// A shell from before `setup doctor --only`: its doctor runs whole, once, as
		// it always did, and the report is what Go sees afterwards.
		c.bashWholeDoctor(o)
		o.Fix, o.Pretty = false, false // it printed every step already
	}
	var pr *printer
	if o.Pretty {
		pr = newPrinter(o.Out, len(Steps))
		pr.header(o.Fix)
	}
	for _, st := range Steps {
		if !o.wants(st.N) {
			continue
		}
		if o.Fix && st.N == 15 && !o.Yes && isTerminal(os.Stdin) && isTerminal(os.Stdout) {
			c.rebuildForQt(o.Out)
		}
		s := &step{}
		if pr != nil {
			pr.spin(st)
		}
		st.check(c, s)
		if pr != nil {
			pr.stop()
		}
		res := StepResult{N: st.N, ID: st.ID, Title: st.Title, Status: s.status(), Lines: s.lines, Missing: s.missing}
		if o.Fix && st.Fixer && (res.Status == StatusFail || res.Status == StatusFixable || res.Status == StatusSkip) {
			res = c.repair(st, res, o)
		}
		if o.Pretty {
			pr.step(res)
		}
		r.Steps = append(r.Steps, res)
	}
	r.count()
	if o.Pretty {
		pr.summary(r)
	}
	return r
}

// repair runs the bash doctor on one step and takes its verdict; the lines
// are what Go sees afterwards, with the repairs it made marked fixed.
func (c *checker) repair(st Step, before StepResult, o Options) StepResult {
	status, ok := c.bashStep(st.N, o)
	s := &step{}
	st.check(c, s)
	after := StepResult{N: st.N, ID: st.ID, Title: st.Title, Status: s.status(), Lines: s.lines, Missing: s.missing}
	if !ok {
		after.Status = StatusFail
		after.Lines = append(before.Lines, Line{Level: LevelFail, Text: "The bash doctor could not run this step"})
		return after
	}
	after.Status = status
	if status == StatusFixed {
		// Like bash: the repairs, then whatever is still worth saying, no pass lines.
		var out []Line
		for _, l := range before.Lines {
			if l.Level == LevelFixable {
				out = append(out, Line{Level: LevelFixed, Text: l.Fix})
			}
		}
		for _, l := range after.Lines {
			if l.Level != LevelPass {
				out = append(out, l)
			}
		}
		after.Lines = out
	}
	return after
}

// bashDoctorTakesSteps is whether the installed setup knows --only and
// --report (sdata/lib/doctor.sh has _doctor_wants).
func (c *checker) bashDoctorTakesSteps() bool {
	data, err := os.ReadFile(filepath.Join(c.e.SetupDir, "sdata", "lib", "doctor.sh"))
	return err == nil && strings.Contains(string(data), "_doctor_wants()")
}

func (c *checker) setupCommand(o Options, args ...string) *exec.Cmd {
	if o.Yes {
		args = append([]string{"-y"}, args...)
	}
	cmd := exec.Command(filepath.Join(c.e.SetupDir, "setup"), args...)
	cmd.Dir = c.e.SetupDir
	wd, _ := os.Getwd()
	cmd.Env = append(os.Environ(), "INIR_INVOKED_FROM="+wd, "INIR_CLI_DELEGATED=1")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, o.Out, o.Out
	return cmd
}

func (c *checker) bashWholeDoctor(o Options) {
	if isExec(filepath.Join(c.e.SetupDir, "setup")) {
		c.setupCommand(o, "doctor").Run()
	}
}

// bashStep runs `setup doctor --only=N` from where scripts/inir runs setup and
// reads the status it reports for the step. Step 1 installs packages and may
// ask for a password, so it runs in the open; the others are shown only when
// the repair fails.
func (c *checker) bashStep(n int, o Options) (Status, bool) {
	if !isExec(filepath.Join(c.e.SetupDir, "setup")) {
		return "", false
	}
	report, err := os.CreateTemp("", "inir-doctor-report.*")
	if err != nil {
		return "", false
	}
	report.Close()
	defer os.Remove(report.Name())
	cmd := c.setupCommand(o, "doctor", "--only="+strconv.Itoa(n), "--report="+report.Name())
	var captured bytes.Buffer
	if n != 1 {
		cmd.Stdout, cmd.Stderr = &captured, &captured
	}
	cmd.Run() // its exit status is the step's, read from the report instead
	f, err := os.Open(report.Name())
	if err != nil {
		return "", false
	}
	defer f.Close()
	status := Status("")
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[0] == strconv.Itoa(n) {
			status = Status(fields[1])
		}
	}
	if status != StatusFixed && status != StatusPass {
		o.Out.Write(captured.Bytes())
	}
	return status, status != ""
}

// rebuildForQt is the step-15 pre-check of run_doctor_with_fixes: asked in a
// terminal, it rebuilds Quickshell for the installed Qt before checking.
func (c *checker) rebuildForQt(out io.Writer) {
	msg := ABIMismatch()
	if msg == "" {
		return
	}
	fmt.Fprintf(out, "\n  Quickshell was built for another Qt version: %s\n", msg)
	fmt.Fprintln(out, "  Quickshell uses Qt internals and asks to be rebuilt after every Qt update. Until then it can crash.")
	launcher := filepath.Join(c.repoRoot(), "scripts", "inir")
	if c.repoRoot() == "" || !isFile(launcher) {
		return
	}
	cmd := exec.Command("bash", launcher, "doctor", "--fix-abi")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, out, out
	cmd.Run()
}

func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func lineText(l Line) string {
	switch l.Level {
	case LevelFixable:
		return l.Text + " (inir doctor fixes it)"
	case LevelFixed:
		return "Fixed: " + l.Text
	}
	return l.Text
}
