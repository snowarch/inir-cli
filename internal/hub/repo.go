package hub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// What a hub repo checks and builds: every item validated against what its code does, then packed
// reproducibly (the same folder always packs to the same bytes, so a sha256 only changes with content).

var Permissions = map[string]string{
	"process": "Runs commands on your computer",
	"network": "Talks to the internet",
	"files":   "Reads or writes files outside its own folder",
	"inject":  "Injects scripts into the site it opens",
}

var permissionOrder = []string{"process", "network", "files", "inject"}

var (
	handleRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}$`)
	tagRE    = regexp.MustCompile(`^[a-z0-9-]{2,20}$`)
	hexRE    = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

const (
	previewMax = 700 * 1024
	itemMax    = 2 << 20
)

var leftOut = map[string]bool{"hub.json": true, "README.md": true, "CHANGELOG.md": true} // store listing, not what installs
var codeExt = []string{".qml", ".js", ".mjs", ".sh", ".py", ".fish"}

// A Material theme must say at least these; the rest of customTheme falls back to iNiR's own.
var themeRequired = []string{
	"m3background", "m3onBackground", "m3surface", "m3onSurface", "m3surfaceVariant", "m3onSurfaceVariant",
	"m3surfaceContainerLowest", "m3surfaceContainerLow", "m3surfaceContainer", "m3surfaceContainerHigh",
	"m3surfaceContainerHighest", "m3outline", "m3outlineVariant", "m3primary", "m3onPrimary",
	"m3primaryContainer", "m3onPrimaryContainer", "m3secondary", "m3onSecondary", "m3secondaryContainer",
	"m3onSecondaryContainer", "m3tertiary", "m3onTertiary", "m3tertiaryContainer", "m3onTertiaryContainer",
	"m3error", "m3onError", "m3errorContainer", "m3onErrorContainer",
}

// iRiS ships these themes itself; a hub theme with one of these ids would sit beside the built-in one
// under the same name. Keep in step with `inir iris theme list` on the newest iNiR.
var irisBuiltIn = []string{
	"iris", "liquid-glass", "frost", "obsidian", "aurora", "terminal", "neo-tokyo", "afterglow", "y2k",
	"faded-light", "film-burn", "grain-orb", "polaroid", "twilight", "daybreak", "horizon", "monolith", "sakura",
	"meadow", "unit-01", "magical-girl", "spirit-forest", "signal", "inir", "lume", "adaptive",
}

// What the code does, as a reviewer would grep for it. A match the listing does not declare fails.
var detect = map[string]*regexp.Regexp{
	"process": regexp.MustCompile(`\bProcess\s*\{|execDetached|ShellExec\.|\bbash\s+-c\b|\bsh\s+-c\b`),
	"network": regexp.MustCompile(`XMLHttpRequest|\bcurl\b|\bwget\b|\bWebSocket\b|\bfetch\s*\(`),
	"files":   regexp.MustCompile(`\bFileView\s*\{|\bFolderListModel\s*\{|\bwriteAdapter\b`),
}

// Never accepted: code that fetches more code, or hides what it does.
var forbidden = []struct {
	re  *regexp.Regexp
	why string
}{
	{regexp.MustCompile(`(curl|wget)[^\n|]*\|\s*(ba|z|fi)?sh\b`), "pipes a download into a shell"},
	{regexp.MustCompile(`\beval\s*\(`), "uses eval()"},
	{regexp.MustCompile(`Qt\.createQmlObject\s*\(`), "builds QML from a string"},
	{regexp.MustCompile(`source\s*:\s*["']https?://[^"']*\.qml`), "loads QML from the internet"},
	{regexp.MustCompile(`atob\s*\(|base64\s+(-d|--decode)`), "decodes hidden content"},
}

// Repo is a hub repo checkout (the folder holding schema/hub.schema.json).
type Repo struct {
	Root     string
	Errors   []string
	Warnings []string
	Fix      bool // validate --fix writes the qmldir files a plugin lacks
}

// FindRepo walks up from dir to the hub repo that holds it.
func FindRepo(dir string) (*Repo, error) {
	dir, _ = filepath.Abs(dir)
	for d := dir; ; d = filepath.Dir(d) {
		if exists(filepath.Join(d, "schema", "hub.schema.json")) {
			return &Repo{Root: d}, nil
		}
		if d == filepath.Dir(d) {
			return nil, fail(ExitUsage, "not inside a hub repo (no schema/hub.schema.json above %s)", dir)
		}
	}
}

func (r *Repo) rel(p string) string {
	if out, err := filepath.Rel(r.Root, p); err == nil && !strings.HasPrefix(out, "..") {
		return out
	}
	return p
}

func (r *Repo) errorf(where, format string, a ...any) {
	text := fmt.Sprintf(format, a...)
	r.Errors = append(r.Errors, r.rel(where)+": "+text)
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		fmt.Printf("::error file=%s::%s\n", r.rel(where), text)
	}
}

func (r *Repo) warnf(where, format string, a ...any) {
	text := fmt.Sprintf(format, a...)
	r.Warnings = append(r.Warnings, r.rel(where)+": "+text)
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		fmt.Printf("::warning file=%s::%s\n", r.rel(where), text)
	}
}

// Report prints warnings and errors to stderr.
func (r *Repo) Report() {
	for _, w := range r.Warnings {
		fmt.Fprintln(os.Stderr, "warning  "+w)
	}
	for _, e := range r.Errors {
		fmt.Fprintln(os.Stderr, "error    "+e)
	}
}

func (r *Repo) loadJSON(p string) map[string]any {
	b, err := os.ReadFile(p)
	if err != nil {
		r.errorf(p, "missing")
		return nil
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		r.errorf(p, "not valid JSON (%v)", err)
		return nil
	}
	m, ok := v.(map[string]any)
	if !ok {
		r.errorf(p, "must be a JSON object")
		return nil
	}
	return m
}

func (r *Repo) itemDirs() []string {
	var out []string
	for _, k := range Kinds {
		entries, _ := os.ReadDir(filepath.Join(r.Root, k.Dir))
		for _, e := range entries {
			if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
				out = append(out, filepath.Join(r.Root, k.Dir, e.Name()))
			}
		}
	}
	return out
}

func kindOfDir(folder string) (Kind, bool) {
	parent := filepath.Base(filepath.Dir(folder))
	for _, k := range Kinds {
		if k.Dir == parent {
			return k, true
		}
	}
	return Kind{}, false
}

// qmldirsWhole: every folder of a plugin that holds types carries a qmldir, listing all of them. Stock Quickshell
// reads them from disk and never scans the plugin (a scan costs everyone seconds at every start), and it trusts a
// qmldir whole: a type it leaves out is "not a type" for everyone on Quickshell (qs-rs reads the folder itself).
func (r *Repo) qmldirsWhole(folder, provides string) {
	filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if p != folder && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		qmls, _ := filepath.Glob(filepath.Join(p, "[A-Z]*.qml"))
		if len(qmls) == 0 {
			return nil
		}
		at := filepath.Join(p, "qmldir")
		b, err := os.ReadFile(at)
		if err != nil {
			sub, _ := filepath.Rel(folder, p)
			if !r.Fix {
				r.errorf(p, "has no qmldir, so Quickshell can't find its types (inir hub validate --fix writes it)")
			} else if werr := os.WriteFile(at, []byte(synthQmldir(filepath.ToSlash(filepath.Join(provides, sub)), qmls)), 0o644); werr != nil {
				r.errorf(p, "can't write its qmldir: %v", werr)
			} else {
				fmt.Println("wrote " + r.rel(at))
			}
			return nil
		}
		listed := map[string][2]bool{}
		for _, line := range strings.Split(string(b), "\n") {
			if f := strings.Fields(line); len(f) > 0 && strings.HasSuffix(f[len(f)-1], ".qml") {
				listed[f[len(f)-1]] = [2]bool{slices.Contains(f, "internal"), slices.Contains(f, "singleton")}
			}
		}
		for _, q := range qmls {
			name := filepath.Base(q)
			line, ok := listed[name]
			internal, single := qmlPragmas(q)
			switch {
			case !ok:
				r.errorf(at, "lists no %s: Quickshell won't know that type", name)
			case line[1] != single:
				r.errorf(at, "%s is %s in its file: its qmldir line says otherwise", name, map[bool]string{true: "a singleton", false: "not a singleton"}[single])
			case line[0] != internal:
				r.errorf(at, "%s is %s in its file: its qmldir line says otherwise", name, map[bool]string{true: "internal", false: "not internal"}[internal])
			}
		}
		return nil
	})
}

// synthQmldir: the qmldir Quickshell would write for a folder at that shell path (src/core/scan.cpp).
func synthQmldir(shellPath string, qmls []string) string {
	var b strings.Builder
	if qsImportPath.MatchString(shellPath) {
		b.WriteString("module qs." + strings.ReplaceAll(shellPath, "/", ".") + "\n")
	}
	slices.Sort(qmls)
	for _, q := range qmls {
		internal, single := qmlPragmas(q)
		name := strings.TrimSuffix(filepath.Base(q), ".qml")
		if internal {
			b.WriteString("internal ")
		}
		if single {
			b.WriteString("singleton ")
		}
		b.WriteString(name + " 1.0 " + name + ".qml\n")
	}
	return b.String()
}

// qmlPragmas reads a QML file's header as Quickshell does: `//@ pragma Internal`, `pragma Singleton`.
func qmlPragmas(file string) (internal, singleton bool) {
	data, _ := os.ReadFile(file)
	for _, line := range strings.Split(string(data), "\n") {
		switch line = strings.TrimSpace(line); {
		case line == "pragma Singleton":
			singleton = true
		case line == "//@ pragma Internal":
			internal = true
		case strings.HasPrefix(line, "import"):
		case strings.Contains(line, "{"):
			return
		}
	}
	return
}

func filesOf(folder string) []string {
	var out []string
	filepath.WalkDir(folder, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}
		if d.Type().IsRegular() {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func pascal(id string) string {
	var b strings.Builder
	for _, part := range strings.Split(id, "-") {
		if part != "" {
			b.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return b.String()
}

// entry is one checked item: what goes in the index, plus what the build needs.
type entry struct {
	item    map[string]any
	kind    Kind
	id      string
	folder  string
	preview string
	palette map[string]string
	values  map[string]any
}

func text(m map[string]any, key string) string {
	switch v := m[key].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

func list(m map[string]any, key string) ([]any, bool) {
	v, present := m[key]
	if !present {
		return nil, true
	}
	l, ok := v.([]any)
	return l, ok
}

// widgetEntries is surface -> file, as iNiR's CustomWidgets reads widget.json.
func widgetEntries(native map[string]any, id string) [][2]string {
	var out [][2]string
	file := pascal(id) + ".qml"
	if v, set := native["main"]; set && v != nil {
		file = text(native, "main")
	}
	if file != "" {
		out = append(out, [2]string{"desktop", file})
	}
	for _, face := range [][2]string{{"iris", "island"}, {"waffle", "waffle-widgets"}} {
		if m, ok := native[face[0]].(map[string]any); ok && strings.TrimSpace(text(m, "main")) != "" {
			out = append(out, [2]string{face[1], strings.TrimSpace(text(m, "main"))})
		}
	}
	return out
}

func (r *Repo) check(folder string) *entry {
	kind, ok := kindOfDir(folder)
	id := filepath.Base(folder)
	if !ok {
		var dirs []string
		for _, k := range Kinds {
			dirs = append(dirs, k.Dir+"/")
		}
		r.errorf(folder, "items live in one of: %s", strings.Join(dirs, ", "))
		return nil
	}
	if !validID(kind, id) {
		r.errorf(folder, "folder name is the id: 3-48 chars, lowercase letters, digits and dashes")
	}
	hubFile, nativeFile := filepath.Join(folder, "hub.json"), filepath.Join(folder, kind.Native)
	hub, native := r.loadJSON(hubFile), r.loadJSON(nativeFile)
	if hub == nil || native == nil {
		return nil
	}
	before := len(r.Errors)

	version := text(hub, "version")
	if !semverRE.MatchString(version) {
		r.errorf(hubFile, `"version" must be x.y.z, got "%s"`, version)
	}
	summary := strings.TrimSpace(text(hub, "summary"))
	if n := utf8.RuneCountInString(summary); n < 8 || n > 80 {
		r.errorf(hubFile, `"summary" is what the card says, 8-80 characters (two lines on the smallest card)`)
	}
	authors, ok := list(hub, "authors")
	valid := ok && len(authors) > 0
	for _, a := range authors {
		if s, isStr := a.(string); !isStr || !handleRE.MatchString(s) {
			valid = false
		}
	}
	if !valid {
		r.errorf(hubFile, `"authors" lists GitHub handles, e.g. ["octocat"]`)
		authors = []any{}
	}
	license := strings.TrimSpace(text(hub, "license"))
	if license == "" {
		r.errorf(hubFile, `"license" is an SPDX id such as "MIT" or "GPL-3.0-or-later"`)
	}
	tags, ok := list(hub, "tags")
	valid = ok && len(tags) <= 6
	for _, t := range tags {
		if !tagRE.MatchString(fmt.Sprint(t)) {
			valid = false
		}
	}
	if !valid {
		r.errorf(hubFile, `"tags": up to 6 lowercase words`)
		tags = []any{}
	}
	minInir := strings.TrimSpace(text(hub, "minInir"))
	if minInir != "" && !semverRE.MatchString(minInir) {
		r.errorf(hubFile, `"minInir" must be x.y.z`)
	}
	declaredList, ok := list(hub, "permissions")
	declared := map[string]bool{}
	for _, p := range declaredList {
		s, _ := p.(string)
		if _, known := Permissions[s]; !known {
			ok = false
		}
		declared[s] = true
	}
	if !ok {
		r.errorf(hubFile, `"permissions" may hold: %s`, strings.Join(permissionOrder, ", "))
		declared = map[string]bool{}
	}

	name := strings.TrimSpace(text(native, "name"))
	description := strings.TrimSpace(text(native, "description"))
	if description == "" {
		description = strings.TrimSpace(text(hub, "description"))
	}
	if name == "" {
		r.errorf(nativeFile, `"name" is required`)
	}
	if utf8.RuneCountInString(description) > 600 {
		r.errorf(folder, "description: keep it under 600 characters")
	}

	e := &entry{kind: kind, id: id, folder: folder, item: map[string]any{"surfaces": []string{}, "families": []string{}}}
	nativeVersion := text(native, "version")
	switch kind.Name {
	case "widget":
		points := widgetEntries(native, id)
		if len(points) == 0 {
			r.errorf(nativeFile, "declares no surface: give it main, iris.main or waffle.main")
		}
		var surfaces, families []string
		for _, p := range points {
			if !exists(filepath.Join(folder, p[1])) {
				r.errorf(nativeFile, "%s entry point %s does not exist", p[0], p[1])
			}
			surfaces = append(surfaces, p[0])
		}
		if slices.Contains(surfaces, "desktop") {
			file := filepath.Join(folder, points[0][1])
			if b, err := os.ReadFile(file); err == nil && !strings.Contains(string(b), `"custom.`+id+`"`) {
				r.errorf(file, `configEntryName must be "custom.%s" (the folder name)`, id)
			}
			families = append(families, "material", "iris")
		}
		if slices.Contains(surfaces, "island") && !slices.Contains(families, "iris") {
			families = append(families, "iris")
		}
		if slices.Contains(surfaces, "waffle-widgets") {
			families = append(families, "waffle")
		}
		e.item["surfaces"], e.item["families"] = surfaces, families
		e.item["icon"] = orDefault(text(native, "icon"), "widgets")
		if nativeVersion != version {
			r.errorf(nativeFile, `"version" (%s) must match hub.json (%s)`, nativeVersion, version)
		}
	case "theme":
		if _, isBool := native["darkmode"].(bool); !isBool {
			r.errorf(nativeFile, `"darkmode" is true or false`)
		}
		var missing []string
		for _, k := range themeRequired {
			if _, ok := native[k]; !ok {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			r.errorf(nativeFile, "missing colours: %s", strings.Join(missing, ", "))
		}
		e.palette = map[string]string{}
		var keys []string
		for k := range native {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if strings.HasPrefix(k, "m3") {
				if !hexRE.MatchString(text(native, k)) {
					r.errorf(nativeFile, `%s must be "#rrggbb", got "%s"`, k, text(native, k))
				}
				e.palette[k] = text(native, k)
			}
		}
		var swatch []string
		for _, k := range []string{"m3background", "m3surfaceContainer", "m3primary", "m3secondary", "m3tertiary", "m3onSurface"} {
			swatch = append(swatch, orDefault(e.palette[k], "#000000"))
		}
		dark, _ := native["darkmode"].(bool)
		e.item["surfaces"], e.item["families"], e.item["icon"] = []string{"colours"}, []string{"material", "waffle"}, "palette"
		e.item["swatch"], e.item["dark"] = swatch, dark
	case "iris-theme":
		if text(native, "iris") != "theme" || text(native, "version") != "1" {
			r.errorf(nativeFile, `an iRiS theme starts with "iris": "theme", "version": 1`)
		}
		if text(native, "id") != id {
			r.errorf(nativeFile, `"id" must be "%s" (the folder name)`, id)
		}
		values, isMap := native["values"].(map[string]any)
		valid := isMap && len(values) > 0
		for k := range values {
			if !strings.HasPrefix(k, "iris.") {
				valid = false
			}
		}
		if !valid {
			r.errorf(nativeFile, `"values" maps iRiS paths ("iris.…") to values; export one from iRiS Settings › Colour › Themes`)
		}
		if slices.Contains(irisBuiltIn, id) {
			r.errorf(folder, `"%s" is one of iRiS's own themes: pick another id`, id)
		}
		e.values = values
		e.item["surfaces"], e.item["families"], e.item["icon"] = []string{"look"}, []string{"iris"}, "style"
	case "webapp":
		if text(native, "id") != id {
			r.errorf(nativeFile, `"id" must be "%s" (the folder name)`, id)
		}
		u := text(native, "url")
		if !strings.HasPrefix(u, "https://") {
			r.errorf(nativeFile, `"url" must be https://`)
		}
		scripts, _ := list(native, "userscripts")
		for _, s := range scripts {
			if !exists(filepath.Join(folder, fmt.Sprint(s))) {
				r.errorf(nativeFile, "userscript %v does not exist", s)
			}
		}
		if len(scripts) > 0 && !declared["inject"] {
			r.errorf(hubFile, `userscripts inject code into the site: declare "inject"`)
		}
		if icon := text(native, "iconPath"); icon != "" && !exists(filepath.Join(folder, icon)) {
			r.errorf(nativeFile, "iconPath %s does not exist", icon)
		}
		e.item["surfaces"], e.item["families"], e.item["icon"], e.item["url"] = []string{"sidebar"}, []string{"material"}, orDefault(text(native, "icon"), "language"), u
		if nativeVersion != version {
			r.errorf(nativeFile, `"version" (%s) must match hub.json (%s)`, nativeVersion, version)
		}
	default: // family, component, design
		if text(native, "id") != id {
			r.errorf(nativeFile, `"id" must be "%s" (the folder name)`, id)
		}
		provides := text(hub, "provides")
		if !validProvides(provides) {
			r.errorf(hubFile, `"provides" is where it lives in the shell, e.g. "modules/%s"`, id)
		}
		requires, ok := list(hub, "requires")
		var needs []string
		for _, q := range requires {
			s, _ := q.(string)
			if !pluginIDRE.MatchString(s) {
				ok = false
			}
			needs = append(needs, s)
		}
		if !ok {
			r.errorf(hubFile, `"requires" lists the ids of the plugins it needs`)
		}
		e.item["surfaces"], e.item["icon"], e.item["provides"] = []string{"shell"}, orDefault(text(native, "icon"), "extension"), provides
		r.qmldirsWhole(folder, provides)
		if len(needs) > 0 {
			e.item["requires"] = needs
		}
	}

	// What the code does against what it says it does.
	found := map[string]bool{}
	for _, p := range filesOf(folder) {
		if !slices.Contains(codeExt, filepath.Ext(p)) {
			continue
		}
		b, _ := os.ReadFile(p)
		for perm, re := range detect {
			if re.Match(b) {
				found[perm] = true
			}
		}
		for _, f := range forbidden {
			if f.re.Match(b) {
				r.errorf(p, "not accepted: %s", f.why)
			}
		}
		if ext := filepath.Ext(p); (ext == ".sh" || ext == ".py" || ext == ".fish") && kind.Name != "webapp" {
			found["process"] = true
		}
	}
	for _, perm := range permissionOrder {
		if found[perm] && !declared[perm] {
			r.errorf(hubFile, `the code %s: declare "%s" in "permissions"`, strings.ToLower(Permissions[perm]), perm)
		}
		if declared[perm] && !found[perm] && perm != "inject" {
			r.warnf(hubFile, `declares "%s" but no code needs it`, perm)
		}
	}

	// Store listing.
	if preview := text(hub, "preview"); preview == "" {
		if kind.Name != "theme" && kind.Name != "iris-theme" {
			r.errorf(hubFile, `"preview" is required: a screenshot of it running (see CONTRIBUTING.md)`)
		}
	} else if ext := strings.ToLower(filepath.Ext(preview)); ext != ".webp" && ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
		r.errorf(hubFile, "preview %s must be a .webp, .png or .jpg", preview)
	} else if p := filepath.Join(folder, preview); !exists(p) {
		r.errorf(hubFile, "%s is missing: add a screenshot of it running (CONTRIBUTING.md › Previews)", preview)
	} else {
		e.preview = p
		st, _ := os.Stat(p)
		if st.Size() > previewMax {
			r.errorf(p, "%d KB; keep previews under %d KB (WebP helps)", st.Size()/1024, previewMax/1024)
		}
		if w, h, ok := ImageSize(p); !ok {
			r.errorf(p, "could not read its size; is it really an image?")
		} else {
			if w < 800 || h < 450 {
				r.errorf(p, "%dx%d is too small: at least 800x450", w, h)
			}
			if ratio := float64(w) / float64(h); ratio < 1.2 || ratio > 2.0 {
				r.errorf(p, "%dx%d: a landscape shot between 6:5 and 2:1 (16:10 is ideal)", w, h)
			}
		}
	}
	if !exists(filepath.Join(folder, "README.md")) {
		r.warnf(folder, "no README.md: say what it does and how to set it up")
	}
	var total int64
	for _, p := range filesOf(folder) {
		if !leftOut[filepath.Base(p)] && p != e.preview {
			st, _ := os.Stat(p)
			total += st.Size()
		}
	}
	limit := int64(itemMax)
	if kind.Plugin {
		_, limit, _ = kind.limits()
	}
	if total > limit {
		r.errorf(folder, "%d KB to install; keep items under %d MB", total/1024, limit>>20)
	}

	if len(r.Errors) > before {
		return nil
	}
	var perms []string
	for _, p := range permissionOrder {
		if declared[p] {
			perms = append(perms, p)
		}
	}
	for k, v := range map[string]any{"id": id, "kind": kind.Name, "name": name, "summary": summary,
		"description": description, "version": version, "authors": authors, "license": license, "tags": tags,
		"permissions": orEmpty(perms), "minInir": minInir} {
		e.item[k] = v
	}
	return e
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Validate checks the given item folders, or every item in the repo.
func (r *Repo) Validate(paths []string) []*entry {
	var folders []string
	for _, p := range paths {
		abs, _ := filepath.Abs(p)
		folders = append(folders, abs)
	}
	if len(paths) == 0 {
		folders = r.itemDirs()
	}
	var out []*entry
	seen := map[string]string{}
	for _, folder := range folders {
		id := filepath.Base(folder)
		if other, taken := seen[id]; taken {
			r.errorf(folder, `id "%s" is taken by %s`, id, r.rel(other))
			continue
		}
		seen[id] = folder
		if e := r.check(folder); e != nil {
			out = append(out, e)
		}
	}
	if len(paths) == 0 {
		for _, k := range Kinds {
			entries, _ := os.ReadDir(filepath.Join(r.Root, k.Dir))
			for _, e := range entries {
				if !e.IsDir() && e.Name() != ".gitkeep" {
					r.errorf(filepath.Join(r.Root, k.Dir, e.Name()), "only item folders belong here")
				}
			}
		}
	}
	return out
}

// pack is a reproducible .tar.gz of what installs: sorted paths, zero times and owners.
func pack(e *entry) ([]byte, error) {
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, p := range filesOf(e.folder) {
		if leftOut[filepath.Base(p)] || p == e.preview {
			continue
		}
		st, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(e.folder, p)
		mode := int64(0o644)
		if st.Mode()&0o111 != 0 {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: filepath.ToSlash(rel), Size: st.Size(), Mode: mode,
			ModTime: time.Unix(0, 0), Typeflag: tar.TypeReg}); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		tw.Write(b)
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	gz, _ := gzip.NewWriterLevel(&out, gzip.BestCompression)
	gz.Write(raw.Bytes())
	gz.Close()
	return out.Bytes(), nil
}

func (r *Repo) git(args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", r.Root}, args...)...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Build writes index.json, packages, previews and the gallery into out.
func (r *Repo) Build(out string) (int, error) {
	entries := r.Validate(nil)
	if len(r.Errors) > 0 {
		return 0, fail(ExitError, "build stopped: %d error(s)", len(r.Errors))
	}
	os.RemoveAll(out)
	for _, d := range []string{"packages", "previews"} {
		if err := os.MkdirAll(filepath.Join(out, d), 0o755); err != nil {
			return 0, err
		}
	}
	featured := map[string]bool{}
	if b, err := os.ReadFile(filepath.Join(r.Root, "featured.json")); err == nil {
		var f struct{ Items []string }
		json.Unmarshal(b, &f)
		for _, id := range f.Items {
			featured[id] = true
		}
	}
	slices.SortFunc(entries, func(a, b *entry) int {
		return strings.Compare(a.kind.Name+"\x00"+a.id, b.kind.Name+"\x00"+b.id)
	})
	var items []map[string]any
	for _, e := range entries {
		data, err := pack(e)
		if err != nil {
			return 0, err
		}
		version := e.item["version"].(string)
		pkg := "packages/" + e.id + "-" + version + ".tar.gz"
		if err := os.WriteFile(filepath.Join(out, pkg), data, 0o644); err != nil {
			return 0, err
		}
		preview := ""
		if e.preview != "" {
			preview = "previews/" + e.id + strings.ToLower(filepath.Ext(e.preview))
			b, _ := os.ReadFile(e.preview)
			os.WriteFile(filepath.Join(out, preview), b, 0o644)
		} else if e.kind.Name == "theme" || e.kind.Name == "iris-theme" {
			preview = "previews/" + e.id + ".svg"
			card := themeCard(e)
			if e.kind.Name == "iris-theme" {
				card = irisCard(e)
			}
			os.WriteFile(filepath.Join(out, preview), []byte(card), 0o644)
		}
		folder := filepath.ToSlash(r.rel(e.folder))
		updated := r.git("log", "-1", "--format=%cs", "--", folder)
		if updated == "" {
			updated = time.Now().Format("2006-01-02")
		}
		sum := sha256.Sum256(data)
		item := e.item
		item["preview"], item["package"], item["sha256"] = preview, pkg, hex.EncodeToString(sum[:])
		item["size"], item["updated"], item["path"] = len(data), updated, folder
		if featured[e.id] {
			item["featured"] = true
		}
		items = append(items, item)
	}
	index := map[string]any{
		"schema": 1, "name": "iNiR Hub", "home": "https://github.com/snowarch/inir-hub",
		"commit": r.git("rev-parse", "HEAD"), "generated": time.Now().UTC().Format("2006-01-02T15:04:05+00:00"),
		"permissions": Permissions, "items": orNone(items),
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(index); err != nil {
		return 0, err
	}
	if err := os.WriteFile(filepath.Join(out, "index.json"), buf.Bytes(), 0o644); err != nil {
		return 0, err
	}
	if b, err := os.ReadFile(filepath.Join(r.Root, "tools", "gallery.html")); err == nil {
		os.WriteFile(filepath.Join(out, "index.html"), b, 0o644)
	}
	os.WriteFile(filepath.Join(out, ".nojekyll"), nil, 0o644)
	return len(items), nil
}

func orNone(items []map[string]any) []map[string]any {
	if items == nil {
		return []map[string]any{}
	}
	return items
}

// Bumps: every item whose files changed since base must carry a higher version than it had there.
func (r *Repo) Bumps(base string) {
	touched := map[string]bool{}
	for _, p := range strings.Split(r.git("diff", "--name-only", base+"...HEAD"), "\n") {
		parts := strings.Split(p, "/")
		if parts[0] == "dist" {
			r.errorf(p, "dist/ is built by CI; leave it out of the pull request")
		}
		for _, k := range Kinds {
			if len(parts) >= 2 && parts[0] == k.Dir {
				touched[parts[0]+"/"+parts[1]] = true
			}
		}
	}
	var dirs []string
	for d := range touched {
		dirs = append(dirs, d)
	}
	slices.Sort(dirs)
	for _, d := range dirs {
		folder := filepath.Join(r.Root, d)
		if !isDir(folder) {
			continue // removed
		}
		old := r.git("show", base+":"+d+"/hub.json")
		if old == "" {
			continue // new item
		}
		var was, now struct{ Version string }
		json.Unmarshal([]byte(old), &was)
		b, _ := os.ReadFile(filepath.Join(folder, "hub.json"))
		json.Unmarshal(b, &now)
		if semverRE.MatchString(was.Version) && semverRE.MatchString(now.Version) && !newer(now.Version, was.Version) {
			r.errorf(filepath.Join(folder, "hub.json"), `files changed: raise "version" so installed copies update`)
		}
	}
}

// New starts an item from templates/<kind>.
func (r *Repo) New(kindName, id, author string) (string, error) {
	kind, ok := KindOf(kindName)
	src := filepath.Join(r.Root, "templates", kindName)
	if !ok || !isDir(src) {
		return "", fail(ExitUsage, "kind is one of the templates: %s", strings.Join(r.templates(), ", "))
	}
	if !validID(kind, id) {
		return "", fail(ExitUsage, "id: 3-48 chars, lowercase letters, digits and dashes")
	}
	for _, d := range r.itemDirs() {
		if filepath.Base(d) == id {
			return "", fail(ExitError, `"%s" is taken by %s`, id, r.rel(d))
		}
	}
	if author == "" {
		author = "your-github-handle"
	}
	dest := filepath.Join(r.Root, kind.Dir, id)
	var words []string
	for _, w := range strings.Split(id, "-") {
		words = append(words, pascal(w))
	}
	name := strings.Join(words, " ")
	replace := strings.NewReplacer("__ID__", id, "__NAME__", name, "__PASCAL__", pascal(id), "__AUTHOR__", author)
	for _, p := range filesOf(src) {
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dest, strings.ReplaceAll(rel, "__PASCAL__", pascal(id)))
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		if ext := filepath.Ext(p); ext == ".json" || ext == ".qml" || ext == ".md" || ext == ".js" {
			b = []byte(replace.Replace(string(b)))
		}
		st, _ := os.Stat(p)
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(to, b, st.Mode().Perm()); err != nil {
			return "", err
		}
	}
	return r.rel(dest), nil
}

func (r *Repo) templates() []string {
	var out []string
	for _, k := range Kinds {
		if isDir(filepath.Join(r.Root, "templates", k.Name)) {
			out = append(out, k.Name)
		}
	}
	return out
}

// ImageSize reads the width and height of a PNG, JPEG or WebP from its header.
func ImageSize(p string) (int, int, bool) {
	f, err := os.Open(p)
	if err != nil {
		return 0, 0, false
	}
	defer f.Close()
	data := make([]byte, 65536)
	n, _ := io.ReadFull(f, data)
	data = data[:n]
	le := binary.LittleEndian
	switch {
	case len(data) >= 24 && bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return int(binary.BigEndian.Uint32(data[16:20])), int(binary.BigEndian.Uint32(data[20:24])), true
	case len(data) >= 30 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		switch string(data[12:16]) {
		case "VP8X":
			u24 := func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }
			return 1 + u24(data[24:27]), 1 + u24(data[27:30]), true
		case "VP8 ":
			return int(le.Uint16(data[26:28]) & 0x3FFF), int(le.Uint16(data[28:30]) & 0x3FFF), true
		case "VP8L":
			b := le.Uint32(data[21:25])
			return int(b&0x3FFF) + 1, int((b>>14)&0x3FFF) + 1, true
		}
	case len(data) >= 2 && data[0] == 0xFF && data[1] == 0xD8:
		for i := 2; i+9 < len(data); {
			if data[i] != 0xFF {
				i++
				continue
			}
			marker := data[i+1]
			if marker >= 0xC0 && marker <= 0xCF && marker != 0xC4 && marker != 0xC8 && marker != 0xCC {
				return int(binary.BigEndian.Uint16(data[i+7 : i+9])), int(binary.BigEndian.Uint16(data[i+5 : i+7])), true
			}
			i += 2 + int(binary.BigEndian.Uint16(data[i+2:i+4]))
		}
	}
	return 0, 0, false
}

// Preview frames a widget the way every card in the Hub is framed: the wallpaper filling 1280x800, the
// widget at about half the width on one side. The shot is the widget as iNiR draws it
// (`inir background widgetSnapshot custom.<id> shot.png`), taken at 200 % so it stays sharp.
func (r *Repo) Preview(folder, wallpaper, shot, side string) (string, error) {
	const pw, ph = 1280, 800
	if _, err := exec.LookPath("magick"); err != nil {
		return "", fail(ExitUsage, "preview needs ImageMagick (the magick command)")
	}
	if !isDir(folder) || !exists(wallpaper) {
		return "", fail(ExitUsage, "give an item folder and a wallpaper image")
	}
	tmp, err := os.MkdirTemp("", "inir-hub-preview-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if shot == "" {
		shot = filepath.Join(tmp, "shot.png")
		said, runErr := exec.Command("inir", "background", "widgetSnapshot", "custom."+filepath.Base(folder), shot).CombinedOutput()
		// iNiR saves the shot a moment after it answers (the widget renders offscreen first).
		for i := 0; i < 50; i++ {
			if st, err := os.Stat(shot); err == nil && st.Size() > 0 {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if runErr != nil || !exists(shot) {
			msg := "could not take the shot: install the widget, put it on the desktop, show the desktop and try again"
			if s := strings.TrimSpace(string(said)); s != "" {
				msg += " (" + s + ")"
			}
			return "", errors.New(msg)
		}
	}
	w, h, ok := ImageSize(shot)
	if !ok {
		return "", fail(ExitUsage, "%s: not an image", shot)
	}
	scale := min(pw*0.46/float64(w), ph*0.62/float64(h), 1.0)
	if scale >= 1.0 && float64(w) < pw*0.36 {
		fmt.Fprintf(os.Stderr, "note: the shot is %dx%d; set the widget to 200 %% before shooting so it reads at card size\n", w, h)
	}
	tw, th := int(float64(w)*scale+0.5), int(float64(h)*scale+0.5)
	margin := 102 // 8 % of the width
	x := margin
	if side == "right" {
		x = pw - margin - tw
	}
	y := max(margin/2, int(float64(ph-th)/2+0.5)-24)
	out := filepath.Join(folder, "preview.webp")
	cmd := exec.Command("magick", wallpaper+"[0]", "-resize", fmt.Sprintf("%dx%d^", pw, ph), "-gravity", "center",
		"-extent", fmt.Sprintf("%dx%d", pw, ph), "(", shot, "-background", "black", "-alpha", "background",
		"-resize", fmt.Sprintf("%dx%d", tw, th), ")", "-gravity", "northwest", "-geometry", fmt.Sprintf("+%d+%d", x, y),
		"-composite", "-quality", "88", out)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	return out, nil
}
