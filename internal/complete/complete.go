// Package complete answers what can follow a partly typed `inir` command line:
// launcher commands and their flags from package cli, IPC targets, functions
// and values from the shell's ipc.json. The shell scripts in this package ask
// `inir __complete` on every Tab, so completions follow the installed shell.
package complete

import (
	"sort"
	"strings"

	"github.com/snowarch/inir-cli/internal/cli"
	"github.com/snowarch/inir-cli/internal/ipc"
)

// Kind groups candidates the way zsh lists them.
type Kind string

const (
	KindCommand  Kind = "command"
	KindTarget   Kind = "target"
	KindAlias    Kind = "alias"
	KindFunction Kind = "function"
	KindValue    Kind = "value"
	KindFlag     Kind = "flag"
)

type Candidate struct {
	Value string
	Desc  string
	Kind  Kind
}

// NoSpace tells the shell to leave the cursor on the word: values like
// `apply:` or `bubbles/` are the start of something longer.
func (c Candidate) NoSpace() bool {
	return strings.HasSuffix(c.Value, ":") || strings.HasSuffix(c.Value, "/")
}

// Result is what to offer for the word under the cursor. When there are no
// candidates, Dirs asks for directory completion and Hint names what to type.
type Result struct {
	Candidates []Candidate
	Dirs       bool
	Hint       string
}

// Complete takes the words after `inir`, the last one being the word under the
// cursor (empty after a space). reg may be nil when no shell is installed.
func Complete(words []string, reg *ipc.Registry) Result {
	if len(words) == 0 {
		words = []string{""}
	}
	prev, cur := words[:len(words)-1], words[len(words)-1]
	if len(prev) == 0 {
		if strings.HasPrefix(cur, "-") {
			return flags([]cli.Flag{{Name: "--help", Desc: "Show help"}})
		}
		return Result{Candidates: append(commands(), targets(reg, true)...)}
	}
	switch prev[0] {
	case "ipc":
		return ipcCall(reg, positionals(prev[1:]), cur)
	case "bind":
		return bind(reg, prev[1:], cur)
	}
	cmd, ok := cli.LookupAny(prev[0])
	if !ok {
		return ipcCall(reg, prev, cur)
	}
	return command(cmd, reg, prev[1:], cur)
}

func command(cmd *cli.Command, reg *ipc.Registry, prev []string, cur string) Result {
	var sub *cli.Sub
	var pos []string
	for i := 0; i < len(prev); i++ {
		w := prev[i]
		if strings.HasPrefix(w, "-") {
			if f, ok := cmd.Flag(w, sub); ok && f.Value != "" {
				if i == len(prev)-1 {
					return flagValue(f)
				}
				i++
			}
			continue
		}
		if sub == nil && len(pos) == 0 {
			if s, ok := cmd.Sub(w); ok {
				sub = s
				continue
			}
		}
		pos = append(pos, w)
	}
	if strings.HasPrefix(cur, "-") {
		var all []cli.Flag
		if sub != nil {
			all = append(all, sub.Flags...)
		}
		return flags(append(all, cmd.Flags...))
	}
	if sub == nil && cmd.Target != "" && reg != nil {
		if t, ok := reg.Target(cmd.Target); ok {
			if len(pos) == 0 {
				r := Result{}
				for _, s := range cmd.Subs {
					r.Candidates = append(r.Candidates, Candidate{s.Name, s.Desc, KindCommand})
				}
				r.Candidates = append(r.Candidates, functions(t, subNames(cmd))...)
				return r
			}
			if _, ok := t.Function(pos[0]); ok {
				return ipcCall(reg, append([]string{t.Name}, pos...), cur)
			}
			return Result{}
		}
	}
	if sub == nil && len(pos) == 0 && len(cmd.Subs) > 0 {
		r := Result{}
		for _, s := range cmd.Subs {
			r.Candidates = append(r.Candidates, Candidate{s.Name, s.Desc, KindCommand})
		}
		return r
	}
	if cmd.Name == "completions" && sub != nil && sub.Name == "install" && len(pos) == 0 {
		return Result{Candidates: []Candidate{
			{"bash", "", KindValue}, {"zsh", "", KindValue}, {"fish", "", KindValue}}}
	}
	return Result{}
}

// bind: `inir bind [--add|--remove|--list|--examples [family]] <keys> <call…>`.
func bind(reg *ipc.Registry, prev []string, cur string) Result {
	i := 0
	for ; i < len(prev) && strings.HasPrefix(prev[i], "-"); i++ {
	}
	if i == len(prev) {
		if i > 0 && prev[i-1] == "--examples" {
			f := cli.BindFlags[3]
			return flagValue(&f)
		}
		if strings.HasPrefix(cur, "-") {
			return flags(cli.BindFlags)
		}
		return Result{Hint: "keys, e.g. Mod+O"}
	}
	// After the keys comes any call you would type after `inir`.
	return Complete(append(append([]string{}, prev[i+1:]...), cur), reg)
}

// positionals drops the launcher flags an IPC call can carry (-c PATH, --).
func positionals(words []string) []string {
	var out []string
	for i := 0; i < len(words); i++ {
		switch words[i] {
		case "-c", "--config":
			i++
		case "--":
		default:
			out = append(out, words[i])
		}
	}
	return out
}

// ipcCall completes `<target> <function> [args…]` with call holding the words
// already typed.
func ipcCall(reg *ipc.Registry, call []string, cur string) Result {
	if reg == nil {
		return Result{}
	}
	if len(call) == 0 {
		return Result{Candidates: targets(reg, false)}
	}
	t, ok := reg.Target(call[0])
	if !ok {
		return Result{}
	}
	if len(call) == 1 {
		return Result{Candidates: functions(t, nil)}
	}
	f, ok := t.Function(call[1])
	if !ok {
		return Result{}
	}
	arg := len(call) - 2
	if arg >= len(f.Args) {
		return Result{}
	}
	if len(f.Values) == 0 {
		return Result{Hint: f.Args[arg]}
	}
	r := Result{}
	for _, v := range f.Values {
		r.Candidates = append(r.Candidates, Candidate{v, "", KindValue})
	}
	return r
}

func commands() []Candidate {
	var out []Candidate
	for _, c := range append(append([]cli.Command{}, cli.Commands...), cli.BinaryCommands...) {
		if !c.Hidden {
			out = append(out, Candidate{c.Name, c.Desc, KindCommand})
		}
	}
	return out
}

// targets lists IPC targets and their kebab-case aliases. Right after `inir`,
// a launcher command takes its word first (`inir settings` toggles, it is not
// the target), so those are left to the command list.
func targets(reg *ipc.Registry, afterInir bool) []Candidate {
	if reg == nil {
		return nil
	}
	var out []Candidate
	for _, t := range reg.Targets {
		if _, taken := cli.Lookup(t.Name); taken && afterInir {
			continue
		}
		out = append(out, Candidate{t.Name, Summary(t.Description), KindTarget})
	}
	aliases := make([]string, 0, len(reg.Aliases))
	for a := range reg.Aliases {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)
	for _, a := range aliases {
		if _, taken := cli.Lookup(a); taken && afterInir {
			continue
		}
		if t, ok := reg.Target(a); ok {
			out = append(out, Candidate{a, Summary(t.Description), KindAlias})
		}
	}
	return out
}

func functions(t *ipc.Target, skip map[string]bool) []Candidate {
	var out []Candidate
	for _, f := range t.Functions {
		if !skip[f.Name] {
			out = append(out, Candidate{f.Name, Summary(f.Description), KindFunction})
		}
	}
	return out
}

func subNames(c *cli.Command) map[string]bool {
	m := map[string]bool{}
	for _, s := range c.Subs {
		m[s.Name] = true
	}
	return m
}

func flags(fs []cli.Flag) Result {
	r := Result{}
	seen := map[string]bool{}
	for _, f := range fs {
		if !seen[f.Name] {
			seen[f.Name] = true
			r.Candidates = append(r.Candidates, Candidate{f.Name, f.Desc, KindFlag})
		}
	}
	return r
}

func flagValue(f *cli.Flag) Result {
	switch {
	case len(f.Values) > 0:
		r := Result{}
		for _, v := range f.Values {
			r.Candidates = append(r.Candidates, Candidate{v, "", KindValue})
		}
		return r
	case f.Dir:
		return Result{Dirs: true}
	default:
		return Result{Hint: strings.ToLower(f.Value)}
	}
}

// Summary shortens an IPC description to a completion line: its first
// sentence, without markdown backticks, cut at a word near 60 characters.
func Summary(desc string) string {
	s := strings.ReplaceAll(desc, "`", "")
	if i := strings.Index(s, ". "); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSuffix(strings.TrimSpace(s), ".")
	const max = 60
	if len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)[:max]
	if i := strings.LastIndexByte(string(r), ' '); i > max/2 {
		return strings.TrimRight(string(r)[:i], ",;:") + "…"
	}
	return string(r) + "…"
}
