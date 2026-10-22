package main

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/fang/v2"
	"charm.land/lipgloss/v2"
	"github.com/spf13/cobra"

	"github.com/snowarch/inir-cli/internal/cli"
	"github.com/snowarch/inir-cli/internal/ipc"
	"github.com/snowarch/inir-cli/internal/shell"
	"github.com/snowarch/inir-cli/internal/ui"
)

// helpGroups orders the launcher's words for `inir help`. Every command people
// see sits in exactly one group (help_test.go holds that).
var helpGroups = []struct {
	ID, Title string
	Names     []string
}{
	{"start", "Start here", []string{"run", "settings", "welcome", "doctor", "update", "logs", "status"}},
	{"shell", "The shell", []string{"start", "stop", "restart", "kill", "open", "repair", "settings-window", "waffle-settings-window"}},
	{"yours", "Make it yours", []string{"theme", "look", "hub", "bind", "config", "completions", "hook"}},
	{"apps", "Open things", []string{"terminal", "browser", "colorpicker", "close-window"}},
	{"keep", "Install and keep", []string{"install", "reinstall", "migrate", "rollback", "my-changes", "backup", "uninstall", "deps"}},
	{"hood", "Under the hood", []string{"ipc", "service", "info", "version", "setup", "path", "test-local", "dev", "cli"}},
}

// rootHidden are words people rarely type by hand: they keep their own help
// (inir path --help) but stay out of the first screen.
var rootHidden = []string{"open", "kill", "settings-window", "waffle-settings-window", "ipc", "path", "test-local", "setup", "dev", "cli"}

// favoriteActions are the IPC targets `inir help` shows by name; the rest wait
// in `inir help actions`.
var favoriteActions = []string{"overview", "clipboard", "wallpaperSelector", "region", "lock", "session", "audio", "brightness", "notifications"}

var familyTitles = map[string]string{"shared": "Every family", "ii": "Material", "waffle": "Waffle", "iris": "iRiS"}

const rootLong = `Your Niri shell, from the terminal.
Every shell action works too: inir <target> <function>. inir help actions lists them all.`

const rootExample = `# first time here? check that everything is in place
inir doctor
# open settings, or any shell action by its name
inir settings
inir overview toggle
# grab something the community made
inir hub install countdown`

// wantsHelp tells whether args ask for help that this binary draws, and the
// command path it is about.
func wantsHelp(args []string) ([]string, bool) {
	switch {
	case len(args) == 0:
		return nil, true
	case args[0] == "help" || args[0] == "--help" || args[0] == "-h":
		return args[1:], true
	}
	last := args[len(args)-1]
	if last != "--help" && last != "-h" {
		return nil, false
	}
	path := args[:len(args)-1]
	if slices.ContainsFunc(path, func(a string) bool { return strings.HasPrefix(a, "-") }) {
		return nil, false
	}
	return path, true
}

// runHelp draws help for path with Fang. handled is false for a path it does
// not know, which the launcher then answers in its own words.
func runHelp(path []string) (code int, handled bool) {
	reg := loadRegistry()
	root := helpTree(reg)
	if len(path) > 0 {
		if c, rest, err := root.Find(path); err != nil || len(rest) > 0 || c == root {
			return 0, false
		}
	}
	root.SetArgs(append(slices.Clone(path), "--help"))
	return execute(root), true
}

// runMan prints the manual page Fang builds from the same tree.
func runMan() int {
	root := helpTree(loadRegistry())
	root.SetArgs([]string{"man"})
	return execute(root)
}

func execute(root *cobra.Command) int {
	// Fang asks the terminal for its background before drawing, up to 2 s per
	// question when nobody answers. With the shell's palette that answer is
	// already known (the shell themes the terminals too), so it is not asked.
	if _, ok := ui.Generated(); ok && os.Getenv("INIR_CHARM_COLORS") == "" {
		if null, err := os.Open(os.DevNull); err == nil {
			stdin := os.Stdin
			os.Stdin = null
			defer func() { os.Stdin = stdin; null.Close() }()
		}
	}
	err := fang.Execute(context.Background(), root,
		fang.WithoutVersion(), fang.WithoutCompletions(), fang.WithColorSchemeFunc(colorScheme))
	if err != nil {
		return 1
	}
	return 0
}

// loadRegistry is the installed shell's IPC surface, nil without a shell.
func loadRegistry() *ipc.Registry {
	for _, dir := range shell.ConfigDirs() {
		if real, ok := shell.Installed(dir); ok {
			if reg, err := ipc.Load(filepath.Join(real, "scripts", "lib", "ipc.json")); err == nil {
				return reg
			}
		}
	}
	return nil
}

func helpTree(reg *ipc.Registry) *cobra.Command {
	cobra.EnableCommandSorting = false
	root := &cobra.Command{Use: "inir", Short: "Your Niri shell, from the terminal", Long: rootLong, Example: rootExample,
		Run: func(*cobra.Command, []string) {}}
	root.SetHelpCommand(&cobra.Command{Use: "help", Hidden: true})
	for _, g := range helpGroups {
		root.AddGroup(&cobra.Group{ID: g.ID, Title: g.Title})
		for _, n := range g.Names {
			c, _ := cli.LookupAny(n)
			cmd := launcherCommand(*c, reg)
			cmd.GroupID = g.ID
			cmd.Hidden = slices.Contains(rootHidden, n)
			root.AddCommand(cmd)
		}
	}
	if reg == nil {
		return root
	}
	root.AddGroup(&cobra.Group{ID: "actions", Title: "Shell actions"})
	actions := &cobra.Command{Use: "actions", Short: "Every shell action, by family",
		Long: "What inir <target> <function> can call on the running shell.", Hidden: true,
		Run: func(*cobra.Command, []string) {}}
	families := map[string]bool{}
	for _, t := range reg.Targets {
		if _, taken := cli.LookupAny(t.Name); taken {
			continue // hub and dev already have a word of their own
		}
		if !families[t.Family] {
			families[t.Family] = true
			actions.AddGroup(&cobra.Group{ID: t.Family, Title: familyTitle(t.Family)})
		}
		in := targetCommand(t, reg)
		in.GroupID = t.Family
		actions.AddCommand(in)
		if slices.Contains(favoriteActions, t.Name) {
			top := targetCommand(t, reg)
			top.GroupID = "actions"
			root.AddCommand(top)
		} else {
			hidden := targetCommand(t, reg)
			hidden.Hidden = true
			root.AddCommand(hidden)
		}
	}
	root.AddCommand(actions)
	return root
}

func familyTitle(f string) string {
	if t, ok := familyTitles[f]; ok {
		return t
	}
	return f
}

func launcherCommand(c cli.Command, reg *ipc.Registry) *cobra.Command {
	cmd := &cobra.Command{Use: c.Name, Short: c.Desc, Run: func(*cobra.Command, []string) {}}
	addFlags(cmd, c.Flags)
	for _, s := range c.Subs {
		sub := &cobra.Command{Use: s.Name, Short: s.Desc, Run: func(*cobra.Command, []string) {}}
		addFlags(sub, s.Flags)
		cmd.AddCommand(sub)
	}
	if c.Name == "bind" {
		addFlags(cmd, cli.BindFlags)
	}
	// hub and dev also take their IPC target's functions after the word.
	if t, ok := targetOf(reg, c.Target); ok {
		for _, f := range t.Functions {
			if _, taken := c.Sub(f.Name); !taken {
				cmd.AddCommand(functionCommand(f))
			}
		}
	}
	return cmd
}

func targetOf(reg *ipc.Registry, name string) (*ipc.Target, bool) {
	if reg == nil || name == "" {
		return nil, false
	}
	return reg.Target(name)
}

func targetCommand(t ipc.Target, reg *ipc.Registry) *cobra.Command {
	name := t.Name
	var aliases []string
	for alias, canonical := range reg.Aliases {
		if canonical == t.Name {
			name, aliases = alias, []string{t.Name}
		}
	}
	short, _, _ := strings.Cut(t.Description, ". ")
	cmd := &cobra.Command{Use: name, Aliases: aliases, Short: strings.TrimSuffix(short, "."),
		Long: t.Description, Run: func(*cobra.Command, []string) {}}
	if t.Example != "" {
		cmd.Example = t.Example
	} else if len(t.Functions) > 0 {
		cmd.Example = "inir " + name + " " + t.Functions[0].Name
	}
	for _, f := range t.Functions {
		cmd.AddCommand(functionCommand(f))
	}
	return cmd
}

func functionCommand(f ipc.Function) *cobra.Command {
	use := f.Name
	for _, a := range f.Args {
		use += " <" + a + ">"
	}
	short := f.Description
	if len(f.Values) > 0 {
		short += " (" + strings.Join(f.Values, ", ") + ")"
	}
	return &cobra.Command{Use: use, Short: short, Run: func(*cobra.Command, []string) {}}
}

// addFlags turns the table's flags into pflags: a short flag rides on the long
// one with the same meaning, and a value list goes in the description.
func addFlags(cmd *cobra.Command, flags []cli.Flag) {
	fs := cmd.Flags()
	fs.SortFlags = false // the table's order is the one worth reading
	for _, f := range flags {
		if f.Name == "--help" || !strings.HasPrefix(f.Name, "--") {
			continue
		}
		name := strings.TrimPrefix(f.Name, "--")
		short := ""
		for _, s := range flags {
			if len(s.Name) == 2 && s.Name[0] == '-' && s.Desc == f.Desc {
				short = s.Name[1:]
			}
		}
		usage := f.Desc
		if len(f.Values) > 0 {
			usage += " (" + strings.Join(f.Values, ", ") + ")"
		}
		if fs.Lookup(name) != nil {
			continue
		}
		if f.Value != "" {
			fs.StringP(name, short, "", usage)
		} else {
			fs.BoolP(name, short, false, usage)
		}
	}
}

// colorScheme dresses Fang in the shell's colors for the current wallpaper,
// and keeps Charm's own on a machine the shell has not themed yet
// (INIR_CHARM_COLORS=1 asks for those anyway).
func colorScheme(c lipgloss.LightDarkFunc) fang.ColorScheme {
	p, ok := ui.Generated()
	if !ok || os.Getenv("INIR_CHARM_COLORS") != "" {
		return fang.DefaultColorScheme(c)
	}
	return fang.ColorScheme{
		Base:           p.Text,
		Title:          p.Primary,
		Description:    p.TextVariant,
		Codeblock:      p.SurfaceContainer,
		Program:        p.Primary,
		DimmedArgument: p.Outline,
		Comment:        p.Outline,
		Flag:           p.ANSI[14],
		FlagDefault:    p.Outline,
		Command:        p.ANSI[13],
		QuotedString:   p.ANSI[10],
		Argument:       p.Text,
		Help:           p.TextVariant,
		Dash:           p.OutlineVariant,
		ErrorHeader:    [2]color.Color{p.OnError, p.Error},
		ErrorDetails:   p.Error,
	}
}
