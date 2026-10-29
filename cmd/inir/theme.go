package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/snowarch/inir-cli/internal/doctor"
	"github.com/snowarch/inir-cli/internal/look"
	"github.com/snowarch/inir-cli/internal/theme"
)

const themeUsage = `usage: inir theme list-targets [--json]
       inir theme inspect <id> [--json]
       inir theme doctor [id] [--json]
       inir theme scaffold <id> [--label TEXT] [--description TEXT] [--category NAME] [--inputs FILE...]
                          [--module FILE] [--index N] [--config-key KEY]
   any of them: [--targets-dir DIR] [--modules-dir DIR] [--config FILE]`

var themeValueFlags = []string{"--targets-dir", "--config", "--modules-dir", "--module", "--label", "--description",
	"--category", "--config-key", "--index", "--inputs", "--json"}

// themeFlag is argparse's long option matching: the whole name or a prefix only one flag has.
func themeFlag(arg string) (string, error) {
	var hits []string
	for _, f := range themeValueFlags {
		if f == arg {
			return f, nil
		}
		if strings.HasPrefix(f, arg) {
			hits = append(hits, f)
		}
	}
	if len(hits) == 1 {
		return hits[0], nil
	}
	if len(hits) > 1 {
		return "", fmt.Errorf("ambiguous option: %s could match %s", arg, strings.Join(hits, ", "))
	}
	return "", fmt.Errorf("unrecognized arguments: %s", arg)
}

func isOption(a string) bool {
	if !strings.HasPrefix(a, "-") || a == "-" {
		return false
	}
	_, err := strconv.ParseFloat(a, 64)
	return err != nil
}

// themeRoot is the shell `inir theme` looks at, as scripts/inir's resolve_setup_dir picks it
// (the launcher's own checkout first).
func themeRoot() string {
	if l, err := bashLauncher(); err == nil {
		root := filepath.Dir(filepath.Dir(l))
		setup, err := os.Stat(filepath.Join(root, "setup"))
		if _, qml := os.Stat(filepath.Join(root, "shell.qml")); err == nil && setup.Mode()&0o111 != 0 && qml == nil {
			return root
		}
	}
	return doctor.NewEnv().SetupDir
}

// runTheme does list-targets, inspect, doctor and scaffold; apply and the help go
// back to the bash launcher.
func runTheme(args []string) (int, bool) {
	if len(args) == 0 {
		return 0, false
	}
	action := args[0]
	switch action {
	case "list-targets":
		action = "list"
	case "inspect", "doctor", "scaffold":
		if len(args) == 1 && action != "doctor" {
			fmt.Fprintf(os.Stderr, "Usage: inir theme %s <target-id>\n", action)
			return 1, true
		}
	default:
		return 0, false
	}
	opts := map[string]string{"--category": "custom", "--config-key": "appearance.wallpaperTheming.enableAppsAndShell",
		"--index": "70", "--config": theme.DefaultConfig()}
	var pos, inputs []string
	asJSON := false
	fail := func(err error) (int, bool) {
		fmt.Fprintf(os.Stderr, "%s\ninir theme: %v\n", themeUsage, err)
		return 2, true
	}
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if a == "-h" || a == "--help" {
			fmt.Println(themeUsage)
			return 0, true
		}
		if a == "--" {
			pos = append(pos, rest[i+1:]...)
			break
		}
		if !isOption(a) {
			pos = append(pos, a)
			continue
		}
		name, value, hasValue := strings.Cut(a, "=")
		flag, err := themeFlag(name)
		if err != nil {
			return fail(err)
		}
		switch flag {
		case "--json":
			if hasValue {
				return fail(fmt.Errorf("argument --json: ignored explicit argument '%s'", value))
			}
			asJSON = true
		case "--inputs":
			inputs = []string{}
			if hasValue {
				inputs = append(inputs, value)
			}
			for i+1 < len(rest) && !isOption(rest[i+1]) {
				i++
				inputs = append(inputs, rest[i])
			}
		default:
			if !hasValue {
				if i+1 >= len(rest) || isOption(rest[i+1]) {
					return fail(fmt.Errorf("argument %s: expected one argument", flag))
				}
				i++
				value = rest[i]
			}
			opts[flag] = value
		}
	}
	if len(pos) > 1 {
		return fail(fmt.Errorf("unrecognized arguments: %s", strings.Join(pos[1:], " ")))
	}
	index, err := strconv.Atoi(strings.TrimSpace(opts["--index"]))
	if err != nil {
		return fail(fmt.Errorf("argument --index: invalid int value: '%s'", opts["--index"]))
	}
	target := ""
	if len(pos) == 1 {
		target = pos[0]
	}

	colors := filepath.Join(themeRoot(), "scripts", "colors")
	if real, err := filepath.EvalSymlinks(colors); err == nil {
		colors = real
	}
	d := theme.Dirs{Colors: colors, Targets: filepath.Join(colors, "targets"), Modules: filepath.Join(colors, "modules"),
		Config: opts["--config"]}
	if v := opts["--targets-dir"]; v != "" {
		d.Targets = filepath.Clean(v)
	}
	if v := opts["--modules-dir"]; v != "" {
		d.Modules = filepath.Clean(v)
	}

	switch action {
	case "scaffold":
		return theme.Make(theme.Scaffold{ID: target, Module: opts["--module"], Label: opts["--label"],
			Description: opts["--description"], Category: opts["--category"], ConfigKey: opts["--config-key"],
			Index: index, Inputs: inputs}, d), true
	case "inspect":
		if target == "" {
			fmt.Println("inspect requires a target id")
			return 1, true
		}
		t := theme.Find(d.Targets, target)
		if t == nil {
			fmt.Println("Unknown target: " + target)
			return 1, true
		}
		fmt.Println(theme.Dump(theme.Inspect(t, d)))
		return 0, true
	case "doctor":
		config := theme.LoadConfig(d.Config)
		if target != "" {
			t := theme.Find(d.Targets, target)
			if t == nil {
				fmt.Println("Unknown target: " + target)
				return 1, true
			}
			r := theme.Doctor(t, d, config)
			fmt.Println(theme.Dump(r))
			if s, _ := r.Get("status"); s == "error" {
				return 1, true
			}
			return 0, true
		}
		var results []any
		code := 0
		for _, t := range theme.Load(d.Targets) {
			r := theme.Doctor(t, d, config)
			results = append(results, r)
			if s, _ := r.Get("status"); s == "error" {
				code = 1
			}
		}
		if asJSON {
			fmt.Println(theme.Dump(orEmpty(results)))
			return code, true
		}
		for _, r := range results {
			o := r.(*look.Object)
			s, _ := o.Get("status")
			id, _ := o.Get("id")
			lbl, ok := o.Get("label")
			if !ok {
				lbl = ""
			}
			fmt.Printf("%s\t%s\t%s\n", theme.Text(s), theme.Text(id), theme.Text(lbl))
			for _, kind := range []string{"problem", "warning"} {
				list, _ := o.Get(kind + "s")
				for _, m := range list.([]any) {
					fmt.Printf("  %s: %s\n", kind, m)
				}
			}
		}
		return code, true
	}
	targets := theme.Load(d.Targets)
	if asJSON {
		all := []any{}
		for _, t := range targets {
			all = append(all, t)
		}
		fmt.Println(theme.Dump(all))
		return 0, true
	}
	if len(targets) == 0 {
		fmt.Println("No theming targets found")
		return 0, true
	}
	for _, t := range targets {
		get := func(k, def string) string {
			if v, ok := t.Get(k); ok {
				return theme.Text(v)
			}
			return def
		}
		var ins []string
		for _, in := range theme.Inputs(t) {
			ins = append(ins, theme.Text(in))
		}
		fmt.Printf("%s\t%s\t%s\t%s\t%s\n", get("id", "<missing>"), get("label", ""), get("category", ""), get("module", ""),
			strings.Join(ins, ", "))
	}
	return 0, true
}

func orEmpty(v []any) []any {
	if v == nil {
		return []any{}
	}
	return v
}
