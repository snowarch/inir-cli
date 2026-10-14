package doctor

// Level is what a line of a step says, as the bash doctor prints it:
// doctor_pass, doctor_fail, doctor_fix (here "fixable" until it is applied),
// tui_warn, tui_info and the faint hint lines.
type Level string

const (
	LevelPass    Level = "pass"
	LevelFail    Level = "fail"
	LevelFixable Level = "fixable"
	LevelFixed   Level = "fixed"
	LevelWarn    Level = "warn"
	LevelInfo    Level = "info"
	LevelHint    Level = "hint"
)

// Line is one result. A fixable line says what is wrong; Fix is what the
// bash doctor prints once it repaired it.
type Line struct {
	Level Level  `json:"level"`
	Text  string `json:"text"`
	Fix   string `json:"fix,omitempty"`
}

// Status of a step. Checking alone gives pass, fail, fixable (the bash doctor
// repairs it) or skip (finding out would change the system); a run that
// repairs turns fixable into fixed or fail.
type Status string

const (
	StatusPass    Status = "pass"
	StatusFail    Status = "fail"
	StatusFixable Status = "fixable"
	StatusFixed   Status = "fixed"
	StatusSkip    Status = "skip"
)

type StepResult struct {
	N       int      `json:"step"`
	ID      string   `json:"id"`
	Title   string   `json:"title"`
	Status  Status   `json:"status"`
	Lines   []Line   `json:"lines"`
	Missing []string `json:"missing,omitempty"` // step 1: the IDs install-deps.sh maps to packages
}

type Report struct {
	Schema  int          `json:"schema"`
	Mode    string       `json:"mode"` // "check" or "fix"
	OS      string       `json:"os"`
	Runtime string       `json:"runtime"`
	Repo    string       `json:"repo"`
	Steps   []StepResult `json:"steps"`
	Summary Summary      `json:"summary"`
}

// Summary counts lines, like the bash doctor's Passed/Fixed/Failed badges.
type Summary struct {
	Passed  int `json:"passed"`
	Fixable int `json:"fixable,omitempty"`
	Fixed   int `json:"fixed"`
	Failed  int `json:"failed"`
}

func (r *Report) count() {
	r.Summary = Summary{}
	for _, s := range r.Steps {
		for _, l := range s.Lines {
			switch l.Level {
			case LevelPass:
				r.Summary.Passed++
			case LevelFail:
				r.Summary.Failed++
			case LevelFixable:
				r.Summary.Fixable++
			case LevelFixed:
				r.Summary.Fixed++
			}
		}
	}
}

// OK is the exit status: the bash doctor fails when anything failed.
func (r *Report) OK() bool {
	for _, s := range r.Steps {
		if s.Status == StatusFail || (r.Mode == "check" && s.Status == StatusFixable) {
			return false
		}
	}
	return true
}

// step collects the lines of one check.
type step struct {
	lines   []Line
	missing []string
	skipped bool
}

func (s *step) add(l Level, t string) { s.lines = append(s.lines, Line{Level: l, Text: t}) }
func (s *step) pass(t string)         { s.add(LevelPass, t) }
func (s *step) fail(t string)         { s.add(LevelFail, t) }
func (s *step) warn(t string)         { s.add(LevelWarn, t) }
func (s *step) info(t string)         { s.add(LevelInfo, t) }
func (s *step) hint(t string)         { s.add(LevelHint, t) }
func (s *step) skip(t string)         { s.skipped = true; s.info(t) }
func (s *step) fixable(problem, fix string) {
	s.lines = append(s.lines, Line{Level: LevelFixable, Text: problem, Fix: fix})
}

func (s *step) status() Status {
	fixable := false
	for _, l := range s.lines {
		switch l.Level {
		case LevelFail:
			return StatusFail
		case LevelFixable:
			fixable = true
		}
	}
	switch {
	case fixable:
		return StatusFixable
	case s.skipped:
		return StatusSkip
	}
	return StatusPass
}
