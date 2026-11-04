package hub

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Check is what a pull request to a hub must pass, the same in CI and on the author's machine before it is sent
// (`inir hub check`; most items are written by an AI assistant, which loops on this until it prints nothing):
//   - the branch changes one item, plus that item's page under docs/<id>/, and nothing else of the hub;
//   - the item validates and, if it was already in the hub, raises its version;
//   - it keeps the shell's contracts: every IpcHandler and each of its functions says what it does in a `///`
//     comment (the IPC reference is built from them), and every language it ships holds every string;
//   - the lines it adds draw with the family's tokens: no hex or numeric colours, no fixed font sizes, no
//     Qt5Compat effects, and in a file that draws with IrisStyle no alpha of its own;
//   - outside a family, it stays in its lane (reach.go): no file over 600 lines, no copy of another file of the hub,
//     imports only what a family offers items, never starts the shell's binary, and from outside at most 2500 lines
//     of code per pull request.
//
// Rules about lines look only at what the branch adds, so code already in the hub never fails someone else's pull
// request. Every failure says what to do instead.
// maintainer: the pull request comes from someone who keeps the hub (CI passes it for the repo's owner, members and
// collaborators). Families are the desktops every install gets; only they change them.
func (r *Repo) Check(base string, maintainer bool) []*entry {
	since := r.git("merge-base", base, "HEAD")
	if since == "" {
		r.errorf(r.Root, "%s is not here: run `git fetch origin main` (or pass --base REF)", base)
		return nil
	}
	changed := r.changedSince(since)
	if len(changed) == 0 {
		r.errorf(r.Root, "nothing changed since %s", base)
		return nil
	}

	items, docs := map[string]bool{}, map[string]bool{}
	for _, p := range changed {
		parts := strings.Split(p, "/")
		switch {
		case parts[0] == "dist":
			r.errorf(p, "dist/ is built by CI: leave it out of the pull request")
		case parts[0] == "docs" && len(parts) >= 3:
			docs[parts[1]] = true
		case len(parts) >= 3 && slices.ContainsFunc(Kinds, func(k Kind) bool { return k.Dir == parts[0] }):
			items[parts[0]+"/"+parts[1]] = true
		default:
			r.errorf(p, "outside any item: a pull request changes one item's folder (and docs/<id>/) only; leave this file as it is on main")
		}
	}
	names := sortedKeys(items)
	if len(names) > 1 {
		r.errorf(r.Root, "one item per pull request, this branch changes %s: send each on its own branch", strings.Join(names, ", "))
		return nil
	}
	if len(names) == 0 {
		for _, id := range sortedKeys(docs) {
			if !slices.ContainsFunc(r.itemDirs(), func(d string) bool { return filepath.Base(d) == id }) {
				r.errorf(filepath.Join(r.Root, "docs", id), "docs/%s/ belongs to no item: a page lives beside the item it documents", id)
			}
		}
		return nil
	}
	item := names[0]
	id := filepath.Base(item)
	for _, other := range sortedKeys(docs) {
		if other != id {
			r.errorf(filepath.Join(r.Root, "docs", other), "this branch changes %s: docs/%s/ is another item's page, send it on its own branch", item, other)
		}
	}
	folder := filepath.Join(r.Root, item)
	if !isDir(folder) {
		return nil // removed: a maintainer's call, nothing of it left to check
	}

	kindDir := strings.Split(item, "/")[0]
	if kindDir == "families" && !maintainer {
		r.errorf(folder, "%s is the desktop every iNiR install gets, kept by its maintainers: a pull request from outside does not change it. Build what you want to add as its own component (components/<id>, CONTRIBUTING.md › Adding to a family); a fix to the family goes in an issue", item)
	}
	if r.git("ls-tree", "--name-only", since, "--", item) == "" {
		r.newID(item, id)
	}

	entries := r.Validate([]string{folder})
	r.bumped(base, item)
	r.contracts(folder)
	r.itemRules(since, item, maintainer)
	return entries
}

// itemRules: what the item's own files and the lines the branch adds keep (Check and Submit alike).
func (r *Repo) itemRules(since, item string, maintainer bool) {
	kindDir := strings.Split(item, "/")[0]
	added := r.addedByFile(since, item)
	if kindDir != "families" {
		r.ownLane(filepath.Join(r.Root, item), filepath.Base(item))
		r.size(added, item, maintainer)
		r.copies(added, item)
		if kindDir == "components" || kindDir == "widgets" || kindDir == "designs" {
			r.reach(added, kindDir)
		}
	}
	r.addedLines(added, item)
}

// newID: a new item's id is its own in the whole hub (a component and a widget both called "clock" would both be
// asked for by name), and a plugin's is not a folder of the base, where it would land at modules/<id>.
func (r *Repo) newID(item, id string) {
	for _, k := range Kinds {
		if other := k.Dir + "/" + id; other != item && isDir(filepath.Join(r.Root, other)) {
			r.errorf(filepath.Join(r.Root, item), "the id %q is taken by %s: pick another name for the folder", id, other)
		}
	}
}

// ownLane: an item that is not a family lives beside the shell, never in it. It keeps its options to itself (a
// widget's in widget.json, a component's in its own files) instead of writing the person's settings, and its IPC
// targets carry its id, so it can't answer for the base or another plugin.
func (r *Repo) ownLane(folder, id string) {
	want := laneKey(id)
	for _, p := range filesOf(folder) {
		ext := filepath.Ext(p)
		if ext != ".qml" && ext != ".js" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			code := line
			if j := strings.Index(line, "//"); j >= 0 && !strings.Contains(line[:j], `"`) {
				code = line[:j]
			}
			where := fmt.Sprintf("%s:%d", p, i+1)
			if configWriteRE.MatchString(code) {
				r.errorf(where, "writes the person's settings: only families do. A widget's options are its widget.json \"options\"; anything else keeps them in its own file (a FileView on Directories.pluginFile(\"%s\")), so removing it leaves their settings as they were", id)
			}
			if m := targetRE.FindStringSubmatch(code); m != nil && !strings.HasPrefix(laneKey(m[1]), want) {
				r.errorf(where, "IPC target %q: start it with your id (%q), so it never answers for the shell or another plugin", m[1], lowerCamel(id))
			}
		}
	}
}

func laneKey(s string) string {
	return strings.Map(func(c rune) rune {
		if c >= 'A' && c <= 'Z' {
			return c + 32
		}
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			return c
		}
		return -1
	}, s)
}

func lowerCamel(id string) string {
	parts := strings.FieldsFunc(id, func(c rune) bool { return c == '-' || c == '_' })
	for i := 1; i < len(parts); i++ {
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, "")
}

// changedSince lists what differs from the merge base: commits, the working tree and new files not yet added.
func (r *Repo) changedSince(since string) []string {
	seen := map[string]bool{}
	for _, out := range []string{r.git("diff", "--name-only", since), r.git("ls-files", "--others", "--exclude-standard")} {
		for _, p := range strings.Split(out, "\n") {
			if p != "" && !r.ignored()[p] {
				seen[p] = true
			}
		}
	}
	return sortedKeys(seen)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// bumped fails an item that was in base and kept its version.
func (r *Repo) bumped(base, item string) {
	old := r.git("show", base+":"+item+"/hub.json")
	if old == "" {
		return // new item
	}
	var was, now struct{ Version string }
	json.Unmarshal([]byte(old), &was)
	b, _ := os.ReadFile(filepath.Join(r.Root, item, "hub.json"))
	json.Unmarshal(b, &now)
	if semverRE.MatchString(was.Version) && semverRE.MatchString(now.Version) && !newer(now.Version, was.Version) {
		r.errorf(filepath.Join(r.Root, item, "hub.json"), `files changed: raise "version" past %s (and the same in the native file) so installed copies update`, was.Version)
	}
}

var (
	ipcOpenRE = regexp.MustCompile(`^\s*IpcHandler\s*\{`)
	ipcFuncRE = regexp.MustCompile(`^\s*function\s+(\w+)\s*\(`)
	targetRE  = regexp.MustCompile(`^\s*target:\s*"([^"]+)"`)
)

// contracts are what the whole item keeps, wherever the lines came from: they are cheap to keep and the shell's
// reference pages and other languages break without them.
func (r *Repo) contracts(folder string) {
	for _, p := range filesOf(folder) {
		if filepath.Ext(p) == ".qml" {
			r.ipcDocumented(p)
		}
	}
	r.catalogsWhole(folder)
	r.partsWhole(folder)
}

// partsWhole: every file plugin.json names in "parts" is in the folder (the base loads it by that name, and a missing
// one is a surface that silently never appears), and "families" lists family ids.
func (r *Repo) partsWhole(folder string) {
	nativeFile := filepath.Join(folder, "plugin.json")
	if !exists(nativeFile) {
		return
	}
	native := r.loadJSON(nativeFile)
	if native == nil {
		return
	}
	if parts, isMap := native["parts"].(map[string]any); isMap {
		for _, name := range sortedKeysAny(parts) {
			file, _ := parts[name].(string)
			if file == "" || strings.Contains(file, "..") || !exists(filepath.Join(folder, file)) {
				r.errorf(nativeFile, `"parts" → "%s": %q is not a file in this folder`, name, file)
			}
		}
	}
	if fams, present := native["families"]; present {
		list, isList := fams.([]any)
		for _, f := range list {
			if s, _ := f.(string); !pluginIDRE.MatchString(s) {
				isList = false
			}
		}
		if !isList {
			r.errorf(nativeFile, `"families" lists the family ids it is worn with, e.g. ["iris"]; leave it out for every family`)
		}
	}
}

func sortedKeysAny(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// ipcDocumented: `///` right above every IpcHandler and above each function in it, as the base's IPC registry reads
// them (the first paragraph is what `inir help` and inir.space/docs show).
func (r *Repo) ipcDocumented(p string) {
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), "IpcHandler") {
		return
	}
	lines := strings.Split(string(b), "\n")
	documented := func(i int) bool {
		for j := i - 1; j >= 0; j-- {
			t := strings.TrimSpace(lines[j])
			if strings.HasPrefix(t, "///") {
				return true
			}
			if t != "" && !strings.HasPrefix(t, "@") {
				return false
			}
		}
		return false
	}
	for i := 0; i < len(lines); i++ {
		if !ipcOpenRE.MatchString(lines[i]) {
			continue
		}
		start, depth, target := i, 0, ""
		for ; i < len(lines); i++ {
			if m := targetRE.FindStringSubmatch(lines[i]); m != nil && target == "" {
				target = m[1]
			}
			if m := ipcFuncRE.FindStringSubmatch(lines[i]); m != nil && depth == 1 && !documented(i) {
				r.errorf(fmt.Sprintf("%s:%d", p, i+1), "IPC function %s has no `///` line above it: say in one line what `inir %s %s` does", m[1], orDefault(target, "TARGET"), m[1])
			}
			depth += strings.Count(lines[i], "{") - strings.Count(lines[i], "}")
			if depth <= 0 {
				break
			}
		}
		if !documented(start) {
			r.errorf(fmt.Sprintf("%s:%d", p, start+1), "IpcHandler %s has no `///` comment above it: one line on what the target is for; a ```kdl fence below it is the keybind example the docs show", orDefault(target, ""))
		}
	}
}

// catalogsWhole: a language the item ships holds every string its en_US.json holds. A language it doesn't ship
// shows English, which is fine; one that is half there mixes two languages on one screen.
func (r *Repo) catalogsWhole(folder string) {
	dir := filepath.Join(folder, "translations")
	files, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(files) == 0 {
		return
	}
	read := func(p string) map[string]any {
		var m map[string]any
		b, err := os.ReadFile(p)
		if err == nil {
			err = json.Unmarshal(b, &m)
		}
		if err != nil {
			r.errorf(p, "not a JSON object of \"English text\": \"translated text\": %v", err)
			return nil
		}
		return m
	}
	english := filepath.Join(dir, "en_US.json")
	if !exists(english) {
		r.errorf(dir, "en_US.json is missing: it lists every string the item shows, as \"text\": \"text\"")
		return
	}
	keys := read(english)
	if keys == nil {
		return
	}
	for _, p := range files {
		if p == english {
			continue
		}
		have := read(p)
		if have == nil {
			continue
		}
		var missing []string
		for k := range keys {
			if _, ok := have[k]; !ok {
				missing = append(missing, k)
			}
		}
		if len(missing) == 0 {
			continue
		}
		slices.Sort(missing)
		shown := missing
		if len(shown) > 5 {
			shown = shown[:5]
		}
		quoted := make([]string, len(shown))
		for i, s := range shown {
			quoted[i] = strconv.Quote(s)
		}
		more := ""
		if len(missing) > len(shown) {
			more = fmt.Sprintf(" and %d more", len(missing)-len(shown))
		}
		r.errorf(p, "%d of en_US.json's strings are missing (%s%s): translate each into this language, or remove the file so the language shows English", len(missing), strings.Join(quoted, ", "), more)
	}
}

var (
	hexColourRE  = regexp.MustCompile(`["']#[0-9a-fA-F]{3,8}["']`)
	numericRGBRE = regexp.MustCompile(`Qt\.(rgba|hsla|hsva|rgb)\(\s*[0-9.]`)
	fontSizeRE   = regexp.MustCompile(`\bfont\.(pixelSize|pointSize)\s*:\s*[0-9.]+\s*([;}]|$|//)`)
	compatRE     = regexp.MustCompile(`^\s*import\s+Qt5Compat\.GraphicalEffects`)
	alphaRE      = regexp.MustCompile(`\b(applyAlpha)\([^;]*,\s*(?:[^,;?]*\?\s*)?[0-9]*\.[0-9]+`)
	literalOKRE  = regexp.MustCompile(`//\s*([\w-]+-)?literal:\s*\S`)
	// Config.setNestedValue(...), Config.options.x = ..., Persistent.states.x = ...: a write to the person's settings.
	configWriteRE = regexp.MustCompile(`\bConfig\.(setNestedValues?)\s*\(|\b(Config\.options|Persistent\.states)(\.\w+|\[[^\]]+\])+\s*=[^=]`)
	irisStyleRE   = regexp.MustCompile(`import\s+qs\.modules\.iris\.style\b`)
)

// tokenHomes are where a family defines its tokens: literals are the point there.
var tokenHomes = []string{"style", "looks"}

// addedLines checks the lines this branch adds to the item's QML: the person's theme, scale and light or dark scheme
// reach only what reads the family's tokens.
func (r *Repo) addedLines(added map[string][]int, item string) {
	for _, rel := range sortedKeysInt(added) {
		if filepath.Ext(rel) != ".qml" && filepath.Ext(rel) != ".js" {
			continue
		}
		inside := strings.Split(strings.TrimPrefix(rel, item+"/"), "/")
		if slices.ContainsFunc(inside[:len(inside)-1], func(d string) bool { return slices.Contains(tokenHomes, d) }) {
			continue
		}
		p := filepath.Join(r.Root, rel)
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		lines := strings.Split(string(b), "\n")
		iris := irisStyleRE.Match(b)
		for _, n := range added[rel] {
			if n < 1 || n > len(lines) {
				continue
			}
			line := lines[n-1]
			code := line
			if i := strings.Index(line, "//"); i >= 0 && !strings.Contains(line[:i], `"`) && !strings.Contains(line[:i], "`") {
				code = line[:i]
			}
			where := fmt.Sprintf("%s:%d", p, n)
			if compatRE.MatchString(code) {
				r.errorf(where, "Qt5Compat.GraphicalEffects is not on every distro iNiR runs on: use QtQuick.Effects (MultiEffect)")
			}
			if literalOKRE.MatchString(line) {
				continue
			}
			switch {
			case hexColourRE.MatchString(code) || numericRGBRE.MatchString(code):
				r.errorf(where, "a colour of its own: take it from the family's tokens (%s) so the person's theme and dark or light scheme reach it, or end the line with `// literal: <why>`", tokensOf(rel, iris))
			case iris && alphaRE.MatchString(code):
				r.errorf(where, "an alpha of its own in iRiS: use IrisStyle's fills and inks (fillQuiet, fill, fillHover, textSecondary, tintFill(c), trackOf(c), readingPlate, mediaScrim…), or end the line with `// iris-literal: <why>`")
			}
			if fontSizeRE.MatchString(code) {
				r.errorf(where, "a fixed font size: use the family's type tokens (%s) so the person's scale reaches it", typeTokensOf(rel, iris))
			}
		}
	}
}

func sortedKeysInt(m map[string][]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func tokensOf(rel string, iris bool) string {
	switch {
	case iris:
		return "IrisStyle.*"
	case strings.HasPrefix(rel, "families/waffle/") || strings.Contains(rel, "Waffle"):
		return "Looks.colors.*"
	default:
		return "Appearance.colors.* or a desktop widget's widgetInk, widgetInkMuted, widgetAccent…"
	}
}

func typeTokensOf(rel string, iris bool) string {
	switch {
	case iris:
		return "IrisStyle.typeBody, typeLabel, typeTitle…"
	case strings.HasPrefix(rel, "families/waffle/") || strings.Contains(rel, "Waffle"):
		return "Looks.font.pixelSize.*"
	default:
		return "Appearance.font.pixelSize.*"
	}
}

// Rel is a path as the hub repo names it.
func (r *Repo) Rel(p string) string { return r.rel(p) }

// Folder is the item's folder.
func (e *entry) Folder() string { return e.folder }
