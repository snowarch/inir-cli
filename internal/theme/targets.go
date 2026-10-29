// Package theme is `inir theme`: the theming targets the shell declares in
// scripts/colors/targets/*.json, their modules and the generated inputs they read.
// Output is what list-targets.py printed, key order and all.
package theme

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/snowarch/inir-cli/internal/look"
)

// Dirs are where a run looks: the shell's scripts/colors and what it holds.
type Dirs struct {
	Colors, Targets, Modules string
	Config                   string // config.json, for the gates targets declare
}

func generated(name string) string {
	return join(filepath.Join(os.Getenv("HOME"), ".local/state/quickshell/user/generated"), name)
}

// join is pathlib's /: an absolute right side wins.
func join(dir, name string) string {
	if filepath.IsAbs(name) {
		return filepath.Clean(name)
	}
	return filepath.Join(dir, name)
}

// DefaultConfig is config.json where the shell reads it (the old illogical-impulse
// one while it is real, the new one once it is a link to it).
func DefaultConfig() string {
	home := os.Getenv("XDG_CONFIG_HOME")
	if home == "" {
		home = filepath.Join(os.Getenv("HOME"), ".config")
	}
	newFile := filepath.Join(home, "inir", "config.json")
	oldFile := filepath.Join(home, "illogical-impulse", "config.json")
	if st, err := os.Lstat(oldFile); err == nil && st.Mode()&os.ModeSymlink != 0 && exists(newFile) {
		return newFile
	}
	if exists(oldFile) {
		return oldFile
	}
	return newFile
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func str(o *look.Object, key string) string {
	v, _ := o.Get(key)
	s, _ := v.(string)
	return s
}

// Load reads every manifest, skipping the ones that are not a JSON object.
func Load(dir string) []*look.Object {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var out []*look.Object
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		v, err := look.Parse(data)
		obj, ok := v.(*look.Object)
		if err != nil || !ok {
			continue
		}
		if _, ok := obj.Get("manifest"); !ok {
			obj.Set("manifest", filepath.Base(p))
		}
		if _, ok := obj.Get("manifestPath"); !ok {
			obj.Set("manifestPath", p)
		}
		out = append(out, obj)
	}
	return out
}

func Find(dir, id string) *look.Object {
	for _, t := range Load(dir) {
		if v, _ := t.Get("id"); v == id {
			return t
		}
	}
	return nil
}

func clone(o *look.Object) *look.Object {
	c := look.NewObject()
	for _, k := range o.Keys() {
		v, _ := o.Get(k)
		c.Set(k, v)
	}
	return c
}

// Inspect is the manifest plus where its module and inputs are and whether they exist.
func Inspect(t *look.Object, d Dirs) *look.Object {
	info := clone(t)
	modulePath := ""
	if m := str(t, "module"); m != "" {
		modulePath = join(d.Modules, m)
	}
	st, err := os.Stat(modulePath)
	info.Set("modulePath", modulePath)
	info.Set("moduleExists", modulePath != "" && err == nil)
	info.Set("moduleExecutable", modulePath != "" && err == nil && st.Mode().Perm()&0o111 != 0)
	inputs := []any{}
	for _, n := range Inputs(t) {
		name, ok := n.(string)
		if !ok {
			continue
		}
		in := look.NewObject()
		in.Set("name", name)
		in.Set("path", generated(name))
		in.Set("exists", exists(generated(name)))
		inputs = append(inputs, in)
	}
	info.Set("resolvedInputs", inputs)
	return info
}

// Inputs are the generated files a target reads; a lone string counts as one.
func Inputs(t *look.Object) []any {
	v, _ := t.Get("inputs")
	if s, ok := v.(string); ok {
		return []any{s}
	}
	list, _ := v.([]any)
	return list
}

// LoadConfig is config.json, or nil when it is missing or broken.
func LoadConfig(path string) any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	v, _ := look.Parse(data)
	return v
}

// Doctor is Inspect with a status: error when the module is missing, warn for a
// module that cannot run, missing inputs or a config gate turned off.
func Doctor(t *look.Object, d Dirs, config any) *look.Object {
	info := Inspect(t, d)
	key := str(t, "configKey")
	var value any
	if key != "" {
		value = config
		for _, part := range strings.Split(key, ".") {
			obj, ok := value.(*look.Object)
			if !ok {
				value = nil
				break
			}
			if value, ok = obj.Get(part); !ok {
				value = nil
				break
			}
		}
	}
	problems, warnings := []any{}, []any{}
	if v, _ := info.Get("moduleExists"); v == false {
		problems = append(problems, "module file is missing")
	} else if v, _ := info.Get("moduleExecutable"); v == false {
		warnings = append(warnings, "module file is not executable")
	}
	var missing []string
	resolved, _ := info.Get("resolvedInputs")
	for _, in := range resolved.([]any) {
		if ok, _ := in.(*look.Object).Get("exists"); ok == false {
			name, _ := in.(*look.Object).Get("name")
			missing = append(missing, name.(string))
		}
	}
	if len(missing) > 0 {
		warnings = append(warnings, "missing generated inputs: "+strings.Join(missing, ", "))
	}
	if key != "" && value == false {
		warnings = append(warnings, "config gate disabled: "+key+"=false")
	}
	status := "ok"
	if len(problems) > 0 {
		status = "error"
	} else if len(warnings) > 0 {
		status = "warn"
	}
	info.Set("status", status)
	info.Set("problems", problems)
	info.Set("warnings", warnings)
	info.Set("configValue", value)
	return info
}

var validID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// Scaffold options; empty strings take the defaults.
type Scaffold struct {
	ID, Module, Label, Description, Category, ConfigKey string
	Index                                               int
	Inputs                                              []string
}

func label(slug string) string {
	var b strings.Builder
	prevCased := false
	for _, r := range strings.NewReplacer("-", " ", "_", " ").Replace(slug) {
		cased := unicode.IsUpper(r) || unicode.IsLower(r)
		if cased && !prevCased {
			r = unicode.ToUpper(r)
		} else if cased {
			r = unicode.ToLower(r)
		}
		prevCased = cased
		b.WriteRune(r)
	}
	return b.String()
}

const moduleTemplate = `#!/usr/bin/env bash
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/lib/module-runtime.sh"
COLOR_MODULE_ID="%[1]s"

main() {
  log_module "stub target for %[2]s"
  # TODO: implement %[3]s
  # Read generated inputs from ~/.local/state/quickshell/user/generated/
  # and write/apply the app-specific theme here.
}

main "$@"
`

// Make writes a new manifest and its module stub, never over existing ones.
func Make(s Scaffold, d Dirs) int {
	if s.ID == "" {
		fmt.Println("scaffold requires a target id")
		return 1
	}
	if !validID.MatchString(s.ID) {
		fmt.Println("target id must match [a-z0-9][a-z0-9-]*")
		return 1
	}
	for _, dir := range []string{d.Targets, d.Modules} {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	module := s.Module
	if module == "" {
		module = fmt.Sprintf("%02d-%s.sh", s.Index, s.ID)
	}
	manifestPath := filepath.Join(d.Targets, s.ID+".json")
	modulePath := join(d.Modules, module)
	if exists(manifestPath) || exists(modulePath) {
		fmt.Println("Refusing to overwrite existing scaffold target files")
		fmt.Printf("manifest=%s\nmodule=%s\n", manifestPath, modulePath)
		return 1
	}
	name := s.Label
	if name == "" {
		name = label(s.ID)
	}
	desc := s.Description
	if desc == "" {
		desc = "Apply generated iNiR palette to " + name + "."
	}
	inputs := []any{}
	for _, in := range s.Inputs {
		inputs = append(inputs, in)
	}
	if len(inputs) == 0 {
		inputs = []any{"app-palette.json", "palette.json"}
	}
	m := look.NewObject()
	for _, kv := range [][2]any{{"id", s.ID}, {"label", name}, {"module", module}, {"category", s.Category},
		{"inputs", inputs}, {"description", desc}, {"configKey", s.ConfigKey}} {
		m.Set(kv[0].(string), kv[1])
	}
	if err := os.WriteFile(manifestPath, []byte(Dump(m)+"\n"), 0o666); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.WriteFile(modulePath, []byte(fmt.Sprintf(moduleTemplate, s.ID, name, desc)), 0o666); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := os.Chmod(modulePath, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("created manifest: %s\ncreated module:   %s\n", manifestPath, modulePath)
	return 0
}

// Dump is Python's json.dumps(v, indent=2, ensure_ascii=False).
func Dump(v any) string {
	var b bytes.Buffer
	dump(&b, v, "")
	return b.String()
}

func dump(b *bytes.Buffer, v any, ind string) {
	switch v := v.(type) {
	case *look.Object:
		if v.Len() == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{")
		for i, k := range v.Keys() {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("\n" + ind + "  ")
			quote(b, k)
			b.WriteString(": ")
			val, _ := v.Get(k)
			dump(b, val, ind+"  ")
		}
		b.WriteString("\n" + ind + "}")
	case []any:
		if len(v) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[")
		for i, e := range v {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString("\n" + ind + "  ")
			dump(b, e, ind+"  ")
		}
		b.WriteString("\n" + ind + "]")
	case string:
		quote(b, v)
	case bool:
		fmt.Fprint(b, v)
	case nil:
		b.WriteString("null")
	case json.Number:
		b.WriteString(number(v))
	default:
		fmt.Fprint(b, v)
	}
}

// number is how Python prints what json.loads read: ints exact, floats by repr.
func number(n json.Number) string {
	t := string(n)
	if !strings.ContainsAny(t, ".eE") {
		if t == "-0" {
			return "0"
		}
		return t
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return t
	}
	e := strconv.FormatFloat(f, 'e', -1, 64)
	if exp, _ := strconv.Atoi(e[strings.IndexByte(e, 'e')+1:]); f != 0 && (exp < -4 || exp >= 16) {
		return e
	}
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}

func quote(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// Text is how Python's str() prints a manifest value in the tab-separated list.
func Text(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case bool:
		if v {
			return "True"
		}
		return "False"
	case nil:
		return "None"
	case json.Number:
		return number(v)
	case *look.Object, []any:
		return look.Compact(v)
	default:
		return fmt.Sprint(v)
	}
}
