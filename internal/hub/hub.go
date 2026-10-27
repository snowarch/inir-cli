// Package hub is the iNiR Hub: the client that browses, installs, updates and removes what a hub
// source lists (every package checked against the sha256 its index gives, unpacked into a staging
// folder and swapped into place in one rename), and the tooling that validates a hub repo and builds
// its catalogue.
package hub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/snowarch/inir-cli/internal/shell"
)

const (
	Official      = "https://snowarch.github.io/inir-hub/index.json"
	DefaultMaxAge = 6 * time.Hour
	// Cached never fetches; any other max age fetches a source whose copy is older.
	Cached = time.Duration(-1)
)

// Exit codes the shell's Hub page and scripts read.
const (
	ExitError        = 1
	ExitUsage        = 2
	ExitConflict     = 3 // a local item of that name exists that the hub did not install
	ExitIncompatible = 4 // needs a newer iNiR
	ExitNetwork      = 5
	ExitIntegrity    = 6 // package failed its checksum or its limits
	ExitUnknown      = 7
)

type Error struct {
	Msg  string
	Code int
}

func (e *Error) Error() string { return e.Msg }

func fail(code int, format string, a ...any) error { return &Error{fmt.Sprintf(format, a...), code} }

// Code is the exit code an error stands for.
func Code(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ExitError
}

// Kind is one sort of item: its folder in a hub repo and the file iNiR itself reads. Plugins
// (families, components, Designs) install into the plugins dir and are linked into the shell at
// the path their listing `provides`.
type Kind struct {
	Name, Dir, Native, Label string
	Plugin                   bool
}

var Kinds = []Kind{
	{"widget", "widgets", "widget.json", "Widget", false},
	{"theme", "themes", "theme.json", "Colour theme", false},
	{"iris-theme", "iris-themes", "theme.json", "iRiS theme", false},
	{"webapp", "webapps", "manifest.json", "Web app", false},
	{"family", "families", "plugin.json", "Family", true},
	{"component", "components", "plugin.json", "Component", true},
	{"design", "designs", "plugin.json", "Design", true},
}

func KindOf(name string) (Kind, bool) {
	for _, k := range Kinds {
		if k.Name == name {
			return k, true
		}
	}
	return Kind{}, false
}

func KindNames() []string {
	var out []string
	for _, k := range Kinds {
		out = append(out, k.Name)
	}
	return out
}

// limits: package bytes, unpacked bytes, files. A family is ~500 files and ~7 MB of QML.
func (k Kind) limits() (int64, int64, int) {
	if k.Plugin {
		return 32 << 20, 64 << 20, 4000
	}
	return 4 << 20, 12 << 20, 400
}

var (
	idRE       = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,46}[a-z0-9]$`)
	pluginIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,46}[a-z0-9]$`) // Material's id is "ii"
	semverRE   = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)
	segmentRE  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,47}$`)
)

func validID(k Kind, id string) bool {
	if k.Plugin {
		return pluginIDRE.MatchString(id)
	}
	return idRE.MatchString(id)
}

// validProvides: where a plugin links in the shell, modules/<a>[/<b>...], never a path the index aims elsewhere.
func validProvides(p string) bool {
	parts := strings.Split(p, "/")
	if len(parts) < 2 || parts[0] != "modules" {
		return false
	}
	for _, s := range parts[1:] {
		if !segmentRE.MatchString(s) {
			return false
		}
	}
	return true
}

// Env is where the hub reads and writes on this machine.
type Env struct {
	ConfigHome string // XDG_CONFIG_HOME
	State      string // installed.json and its lock
	Cache      string // fetched indexes and packages
	Plugins    string // installed families, components and Designs
	Shell      string // the shell's runtime dir (shell.qml), where plugins are linked; "" when none
	Version    string
	Sources    []string
}

func Default() *Env {
	home, _ := os.UserHomeDir()
	xdg := func(name, fallback string) string {
		if v := os.Getenv(name); strings.HasPrefix(v, "/") {
			return v
		}
		return filepath.Join(home, fallback)
	}
	e := &Env{
		ConfigHome: xdg("XDG_CONFIG_HOME", ".config"),
		State:      filepath.Join(xdg("XDG_STATE_HOME", ".local/state"), "inir", "hub"),
		Cache:      filepath.Join(xdg("XDG_CACHE_HOME", ".cache"), "inir", "hub"),
		Plugins:    filepath.Join(xdg("XDG_DATA_HOME", ".local/share"), "inir", "plugins"),
		Version:    "0.0.0",
	}
	for _, dir := range shell.ConfigDirs() {
		if real, ok := shell.Installed(dir); ok {
			e.Shell = real
			break
		}
	}
	if b, err := os.ReadFile(filepath.Join(e.Shell, "VERSION")); err == nil && e.Shell != "" {
		e.Version = strings.TrimSpace(string(b))
	}
	e.Sources = e.sources()
	return e
}

// userConfig is Directories.shellConfig as the shell resolves it: inir, or a real legacy illogical-impulse folder.
func (e *Env) userConfig() string {
	inir, legacy := filepath.Join(e.ConfigHome, "inir"), filepath.Join(e.ConfigHome, "illogical-impulse")
	if st, err := os.Lstat(legacy); err == nil && st.Mode()&os.ModeSymlink != 0 && isDir(inir) {
		return inir
	}
	if isDir(legacy) {
		return legacy
	}
	return inir
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

func (e *Env) sources() []string {
	if env := strings.Fields(strings.ReplaceAll(os.Getenv("INIR_HUB_SOURCES"), ",", " ")); len(env) > 0 {
		return env
	}
	out := []string{Official}
	var config struct {
		Hub struct {
			Sources []any `json:"sources"`
		} `json:"hub"`
	}
	if b, err := os.ReadFile(filepath.Join(e.userConfig(), "config.json")); err == nil && json.Unmarshal(b, &config) == nil {
		for _, s := range config.Hub.Sources {
			if s, ok := s.(string); ok && strings.TrimSpace(s) != "" && !slices.Contains(out, strings.TrimSpace(s)) {
				out = append(out, strings.TrimSpace(s))
			}
		}
	}
	return out
}

// target is where iNiR loads each kind from; never a path the index chooses.
func (e *Env) target(kind, id string) string {
	switch kind {
	case "widget":
		return filepath.Join(e.ConfigHome, "inir", "widgets", id) // services/CustomWidgets.qml
	case "webapp":
		return filepath.Join(e.userConfig(), "plugins", id) // scripts/scan-plugins.py
	case "theme":
		return filepath.Join(e.userConfig(), "themes", id+".json") // modules/ii/settings/ThemesConfig.qml
	case "iris-theme":
		return filepath.Join(e.userConfig(), "iris", "themes", id+".json") // modules/iris/settings/IrisThemes.qml
	}
	return filepath.Join(e.Plugins, id)
}

func (e *Env) present(kind, id string) bool { return exists(e.target(kind, id)) }

func versionKey(text string) []int {
	if text == "" {
		text = "0"
	}
	var out []int
	for _, piece := range strings.Split(strings.SplitN(text, "-", 2)[0], ".") {
		n, err := strconv.Atoi(piece)
		if err != nil || n < 0 {
			n = 0
		}
		out = append(out, n)
	}
	for len(out) < 3 {
		out = append(out, 0)
	}
	return out
}

// newer reports whether version a is above b.
func newer(a, b string) bool { return slices.Compare(versionKey(a), versionKey(b)) > 0 }

func toURL(source string) string {
	if strings.Contains(source, "://") {
		return source
	}
	if strings.HasPrefix(source, "~/") {
		home, _ := os.UserHomeDir()
		source = filepath.Join(home, source[2:])
	}
	p, err := filepath.Abs(source)
	if err == nil {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
	}
	if isDir(p) {
		p = filepath.Join(p, "index.json")
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

func (e *Env) fetch(raw string, limit int64) ([]byte, error) {
	var body io.ReadCloser
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fail(ExitNetwork, "%s: %v", raw, err)
	}
	if u.Scheme == "file" {
		f, err := os.Open(u.Path)
		if err != nil {
			return nil, fail(ExitNetwork, "%s: %v", raw, err)
		}
		body = f
	} else {
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			return nil, fail(ExitNetwork, "%s: %v", raw, err)
		}
		req.Header.Set("User-Agent", "inir-hub/"+e.Version)
		resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
		if err != nil {
			return nil, fail(ExitNetwork, "%s: %v", raw, err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fail(ExitNetwork, "%s: HTTP %d", raw, resp.StatusCode)
		}
		body = resp.Body
	}
	defer body.Close()
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, fail(ExitNetwork, "%s: %v", raw, err)
	}
	if int64(len(data)) > limit {
		return nil, fail(ExitIntegrity, "%s: larger than %d KB", raw, limit/1024)
	}
	return data, nil
}

// Source is one catalogue as status reports it.
type Source struct {
	Source  string `json:"source"`
	URL     string `json:"url"`
	Fetched int64  `json:"fetched"`
	Error   string `json:"error"`
	Count   int    `json:"count"`
	Name    string `json:"name"`
}

type index struct {
	Name  string
	Home  string
	Items []map[string]any
}

type cacheFile struct {
	Fetched int64           `json:"fetched"`
	URL     string          `json:"url"`
	Index   json.RawMessage `json:"index"`
}

func parseIndex(data []byte) (*index, bool) {
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return nil, false
	}
	items, ok := raw["items"].([]any)
	if schema, _ := raw["schema"].(float64); schema != 1 || !ok {
		return nil, false
	}
	out := &index{}
	out.Name, _ = raw["name"].(string)
	out.Home, _ = raw["home"].(string)
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out.Items = append(out.Items, m)
		}
	}
	return out, true
}

func (e *Env) cachePath(source string) string {
	sum := sha1.Sum([]byte(toURL(source)))
	return filepath.Join(e.Cache, "index-"+hex.EncodeToString(sum[:])[:12]+".json")
}

func (e *Env) readCache(source string) (*cacheFile, *index) {
	b, err := os.ReadFile(e.cachePath(source))
	if err != nil {
		return nil, nil
	}
	var c cacheFile
	if json.Unmarshal(b, &c) != nil {
		return nil, nil
	}
	idx, ok := parseIndex(c.Index)
	if !ok {
		return nil, nil
	}
	return &c, idx
}

func writeAtomic(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func (e *Env) syncSource(source string) Source {
	u := toURL(source)
	state := Source{Source: source, URL: u}
	data, err := e.fetch(u, 2<<20)
	if err == nil {
		idx, ok := parseIndex(data)
		if !ok {
			err = fmt.Errorf("%s: not an iNiR Hub index (schema 1)", u)
		} else {
			now := time.Now().Unix()
			payload, _ := json.Marshal(cacheFile{Fetched: now, URL: u, Index: data})
			if err = writeAtomic(e.cachePath(source), payload); err == nil {
				state.Fetched, state.Count, state.Name = now, len(idx.Items), idx.Name
				return state
			}
		}
	}
	state.Error = err.Error()
	if c, idx := e.readCache(source); c != nil {
		state.Fetched, state.Count, state.Name = c.Fetched, len(idx.Items), idx.Name
	}
	return state
}

// Item is one catalogue entry as the index lists it, plus where it came from and its state here.
type Item = map[string]any

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func strs(m map[string]any, key string) []string {
	var out []string
	list, _ := m[key].([]any)
	for _, v := range list {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func join(base, ref string) string {
	b, err := url.Parse(base)
	r, err2 := url.Parse(ref)
	if err != nil || err2 != nil {
		return ref
	}
	return b.ResolveReference(r).String()
}

// Catalogue is every source's items (the first source wins an id) and each source's state; a source
// whose copy is older than maxAge is fetched first.
func (e *Env) Catalogue(maxAge time.Duration) ([]Item, []Source) {
	var items []Item
	seen := map[string]bool{}
	var states []Source
	now := time.Now().Unix()
	for _, source := range e.Sources {
		c, idx := e.readCache(source)
		var state Source
		if maxAge != Cached && (c == nil || time.Duration(now-c.Fetched)*time.Second > maxAge) {
			state = e.syncSource(source)
			c, idx = e.readCache(source)
		} else {
			state = Source{Source: source, URL: toURL(source)}
			if c != nil {
				state.Fetched, state.Name, state.Count = c.Fetched, idx.Name, len(idx.Items)
			} else {
				state.Error = "not fetched yet"
			}
		}
		states = append(states, state)
		if c == nil {
			continue
		}
		for _, raw := range idx.Items {
			id, kindName := str(raw, "id"), str(raw, "kind")
			kind, ok := KindOf(kindName)
			// An id or version the index could aim outside the item's own folder is not listed at all.
			if !ok || !validID(kind, id) || !semverRE.MatchString(str(raw, "version")) || seen[id] {
				continue
			}
			if kind.Plugin && !validProvides(str(raw, "provides")) {
				continue
			}
			seen[id] = true
			item := Item{}
			for k, v := range raw {
				item[k] = v
			}
			item["source"] = source
			item["sourceName"] = state.Name
			item["packageUrl"] = join(c.URL, str(raw, "package"))
			// Only a local index may point at local files.
			if strings.HasPrefix(str(item, "packageUrl"), "file:") && !strings.HasPrefix(c.URL, "file:") {
				continue
			}
			item["previewUrl"] = ""
			if p := str(raw, "preview"); p != "" {
				item["previewUrl"] = join(c.URL, p)
			}
			item["page"] = idx.Home
			if strings.HasPrefix(idx.Home, "https://github.com/") && str(raw, "path") != "" {
				item["page"] = idx.Home + "/tree/main/" + str(raw, "path")
			}
			items = append(items, item)
		}
	}
	return items, states
}

// Record is what the hub installed, in installed.json.
type Record struct {
	Kind      string   `json:"kind"`
	Version   string   `json:"version"`
	SHA256    string   `json:"sha256"`
	Source    string   `json:"source"`
	Name      string   `json:"name"`
	Path      string   `json:"path"`
	Installed string   `json:"installed"`
	Provides  string   `json:"provides,omitempty"`
	Requires  []string `json:"requires,omitempty"`
}

func (e *Env) records() map[string]Record {
	out := map[string]Record{}
	b, err := os.ReadFile(filepath.Join(e.State, "installed.json"))
	if err != nil {
		return out
	}
	var file struct {
		Items map[string]json.RawMessage `json:"items"`
	}
	if json.Unmarshal(b, &file) != nil {
		return out
	}
	for id, raw := range file.Items {
		// A hand-edited record without a kind or version is dropped, not a crash later on.
		var r Record
		if json.Unmarshal(raw, &r) != nil || r.Version == "" {
			continue
		}
		if k, ok := KindOf(r.Kind); ok && validID(k, id) {
			out[id] = r
		}
	}
	return out
}

func (e *Env) saveRecords(records map[string]Record) error {
	data, err := json.MarshalIndent(map[string]any{"schema": 1, "items": records}, "", " ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(e.State, "installed.json"), append(data, '\n'))
}

func (e *Env) lock() (func(), error) {
	if err := os.MkdirAll(e.State, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(e.State, ".lock"), os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

// linkTaken: something at a plugin's shell path that is not this hub's link to it.
func (e *Env) linkTaken(id, provides string) bool {
	if e.Shell == "" || provides == "" {
		return false
	}
	at := filepath.Join(e.Shell, provides)
	if dest, err := os.Readlink(at); err == nil {
		return dest != e.target("family", id)
	}
	return exists(at)
}

// Annotate adds each item's state here: installed version, update, compatible, conflict; items the hub
// installed that no source lists any more come last as orphans.
func (e *Env) Annotate(items []Item) []Item {
	records := e.records()
	current := e.Version
	known := map[string]bool{}
	for _, item := range items {
		id, kind := str(item, "id"), str(item, "kind")
		known[id] = true
		r, ok := records[id]
		if ok && !e.present(r.Kind, id) {
			ok = false // deleted by hand
		}
		item["installed"] = ""
		if ok {
			item["installed"] = r.Version
		}
		item["update"] = ok && newer(str(item, "version"), r.Version)
		item["compatible"] = str(item, "minInir") == "" || !newer(str(item, "minInir"), current)
		k, _ := KindOf(kind)
		item["conflict"] = !ok && (e.present(kind, id) || (k.Plugin && e.linkTaken(id, str(item, "provides"))))
	}
	var ids []string
	for id := range records {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		r := records[id]
		if known[id] || !e.present(r.Kind, id) {
			continue
		}
		name := r.Name
		if name == "" {
			name = id
		}
		items = append(items, Item{"id": id, "kind": r.Kind, "name": name, "summary": "", "description": "",
			"version": r.Version, "installed": r.Version, "update": false, "compatible": true, "conflict": false,
			"orphan": true, "authors": []any{}, "tags": []any{}, "permissions": []any{}, "families": []any{},
			"surfaces": []any{}, "previewUrl": "", "source": r.Source})
	}
	return items
}

func find(id string, items []Item) (Item, error) {
	for _, item := range items {
		if str(item, "id") == id {
			return item, nil
		}
	}
	return nil, fail(ExitUnknown, `no item "%s" in the hub; try: inir hub search`, id)
}

// Updates counts what can be updated now.
func Updates(items []Item) int {
	n := 0
	for _, item := range items {
		if item["update"] == true && item["compatible"] == true {
			n++
		}
	}
	return n
}

func unpack(data []byte, dest string, kind Kind) error {
	_, maxUnpacked, maxFiles := kind.limits()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fail(ExitIntegrity, "package is not a .tar.gz")
	}
	tr := tar.NewReader(gz)
	var total int64
	for n := 0; ; n++ {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fail(ExitIntegrity, "package is damaged: %v", err)
		}
		if n >= maxFiles {
			return fail(ExitIntegrity, "package holds too many files")
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := path.Clean(h.Name)
		if path.IsAbs(h.Name) || slices.Contains(strings.Split(h.Name, "/"), "..") ||
			(h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir) {
			return fail(ExitIntegrity, "package entry %q is not allowed", h.Name)
		}
		if name == "." {
			continue
		}
		at := filepath.Join(dest, filepath.FromSlash(name))
		if h.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(at, 0o755); err != nil {
				return err
			}
			continue
		}
		if total += h.Size; total > maxUnpacked {
			return fail(ExitIntegrity, "package unpacks too large")
		}
		if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if h.Mode&0o111 != 0 {
			mode = 0o755
		}
		f, err := os.OpenFile(at, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, io.LimitReader(tr, h.Size))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
}

func (e *Env) download(item Item, kind Kind) ([]byte, error) {
	want := str(item, "sha256")
	sum := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	cached := filepath.Join(e.Cache, "packages", str(item, "id")+"-"+str(item, "version")+".tar.gz")
	if b, err := os.ReadFile(cached); err == nil && sum(b) == want {
		return b, nil
	}
	limit, _, _ := kind.limits()
	data, err := e.fetch(str(item, "packageUrl"), limit)
	if err != nil {
		return nil, err
	}
	if sum(data) != want {
		return nil, fail(ExitIntegrity, "%s: package does not match the checksum the hub lists; nothing was installed", str(item, "id"))
	}
	_ = writeAtomic(cached, data)
	return data, nil
}

func (e *Env) place(item Item, kind Kind, data []byte) (string, error) {
	id := str(item, "id")
	target := e.target(kind.Name, id)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(target), ".hub-"+id+"-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	staging := filepath.Join(tmp, "item")
	if err := os.Mkdir(staging, 0o755); err != nil {
		return "", err
	}
	if err := unpack(data, staging, kind); err != nil {
		return "", err
	}
	// Refuse a file iNiR could not read.
	native := filepath.Join(staging, kind.Native)
	if kind.Name == "theme" || kind.Name == "iris-theme" || kind.Plugin {
		b, err := os.ReadFile(native)
		var v map[string]any
		if err != nil || json.Unmarshal(b, &v) != nil {
			return "", fail(ExitIntegrity, "%s: package has no readable %s", id, kind.Native)
		}
	}
	if kind.Name == "theme" || kind.Name == "iris-theme" {
		return target, os.Rename(native, target)
	}
	if kind.Plugin {
		if err := e.canLink(id, str(item, "provides")); err != nil {
			return "", err
		}
	}
	old := filepath.Join(tmp, "old")
	if exists(target) {
		if err := os.Rename(target, old); err != nil {
			return "", err
		}
	}
	if err := os.Rename(staging, target); err != nil {
		if exists(old) {
			os.Rename(old, target)
		}
		return "", err
	}
	if kind.Plugin {
		if err := e.link(id, str(item, "provides")); err != nil {
			os.RemoveAll(target)
			if exists(old) {
				os.Rename(old, target)
			}
			return "", err
		}
	}
	return target, nil
}

// canLink: the shell has the folder a plugin goes in and nothing of someone else's sits at its path.
func (e *Env) canLink(id, provides string) error {
	if e.Shell == "" {
		return fail(ExitError, "no iNiR shell installed to link %s into", id)
	}
	at := filepath.Join(e.Shell, provides)
	if !isDir(filepath.Dir(at)) {
		return fail(ExitError, "%s goes in %s, which this shell does not have", id, path.Dir(provides))
	}
	if _, err := os.Readlink(at); err != nil && exists(at) {
		return fail(ExitConflict, "%s already exists and was not installed from the hub", at)
	}
	return nil
}

// link puts a plugin at its shell path (a family at modules/<id>), so the shell imports it as if it were in the tree.
func (e *Env) link(id, provides string) error {
	if err := e.canLink(id, provides); err != nil {
		return err
	}
	at, want := filepath.Join(e.Shell, provides), e.target("family", id)
	if dest, err := os.Readlink(at); err == nil && dest == want {
		return e.writeScanIndex()
	}
	tmp := at + ".hub-link"
	os.Remove(tmp)
	if err := os.Symlink(want, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, at); err != nil {
		return err
	}
	if err := e.writeScanIndex(); err != nil {
		os.Remove(at)
		return err
	}
	return nil
}

// ScanIndex is the shell-root file that imports every folder of every linked plugin: stock Quickshell only
// registers folders an import reaches from the root (src/core/scan.cpp), and plugins are loaded by path.
const ScanIndex = "HubScan.qml"

var qsImportPath = regexp.MustCompile(`^[A-Za-z0-9_/]+$`)

// writeScanIndex rewrites ScanIndex from the links under modules/ (qs-rs reads folders itself; harmless there).
func (e *Env) writeScanIndex() error {
	if e.Shell == "" {
		return nil
	}
	folders := map[string]bool{}
	// folders reached through a link, links inside linked plugins included (a component in a family)
	var walk func(dir, rel string, linked bool, depth int)
	walk = func(dir, rel string, linked bool, depth int) {
		entries, err := os.ReadDir(dir)
		if err != nil || depth > 16 {
			return
		}
		for _, d := range entries {
			name, sub := d.Name(), filepath.Join(dir, d.Name())
			switch {
			case d.Type()&os.ModeSymlink != 0:
				if real, err := filepath.EvalSymlinks(sub); err == nil && isDir(real) {
					walk(real, path.Join(rel, name), true, depth+1)
				}
			case d.IsDir():
				walk(sub, path.Join(rel, name), linked, depth+1)
			case linked && name[0] >= 'A' && name[0] <= 'Z' && strings.HasSuffix(name, ".qml") && qsImportPath.MatchString(rel):
				folders[rel] = true
			}
		}
	}
	walk(filepath.Join(e.Shell, "modules"), "modules", false, 0)
	var b strings.Builder
	b.WriteString("// Written by inir hub: imports every folder of every plugin it linked, so Quickshell registers their types.\n")
	b.WriteString("import QtQuick\n")
	for _, dir := range slices.Sorted(maps.Keys(folders)) {
		b.WriteString("import qs." + strings.ReplaceAll(dir, "/", ".") + "\n")
	}
	b.WriteString("\nQtObject {}\n")
	return writeAtomic(filepath.Join(e.Shell, ScanIndex), []byte(b.String()))
}

// builtIn: a plugin the shell carries in its own tree (the families, until they move to the hub).
func (e *Env) builtIn(id string) bool {
	manifests, _ := filepath.Glob(filepath.Join(e.Shell, "modules", "*", "plugin.json"))
	for _, m := range manifests {
		var v struct{ ID string }
		if b, err := os.ReadFile(m); err == nil && json.Unmarshal(b, &v) == nil && v.ID == id {
			return true
		}
	}
	return false
}

// Result is what one install, update or remove did.
type Result struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Version string `json:"version,omitempty"`
	Changed bool   `json:"changed,omitempty"`
	From    string `json:"from,omitempty"`
	Path    string `json:"path,omitempty"`
	Removed bool   `json:"removed,omitempty"`
}

// Install installs or updates each id from a fresh index (a stale one names an older package whose
// sha256 no longer matches what the source serves; the cached copy stays if the fetch fails).
func (e *Env) Install(ids []string) ([]Result, error) {
	unlock, err := e.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	items, _ := e.Catalogue(0)
	return e.install(ids, e.Annotate(items))
}

func (e *Env) install(ids []string, items []Item) ([]Result, error) {
	records := e.records()
	var results []Result
	for _, id := range ids {
		item, err := find(id, items)
		if err != nil {
			return results, err
		}
		kind, _ := KindOf(str(item, "kind"))
		switch {
		case item["orphan"] == true:
			return results, fail(ExitUnknown, "%s is no longer listed in any hub source", id)
		case item["conflict"] == true:
			at := e.target(kind.Name, id)
			if kind.Plugin && !exists(at) {
				at = filepath.Join(e.Shell, str(item, "provides"))
			}
			return results, fail(ExitConflict, "%s already exists and was not installed from the hub; rename or remove it first", at)
		case item["compatible"] != true:
			return results, fail(ExitIncompatible, "%s needs iNiR %s (this is %s); run: inir update", str(item, "name"), str(item, "minInir"), e.Version)
		}
		from := str(item, "installed")
		if from != "" && item["update"] != true {
			results = append(results, Result{ID: id, Kind: kind.Name, Version: from})
			continue
		}
		for _, need := range strs(item, "requires") {
			if r, ok := records[need]; !(ok && e.present(r.Kind, need)) && !e.builtIn(need) {
				return results, fail(ExitError, "%s needs %s: inir hub install %s", id, need, need)
			}
		}
		data, err := e.download(item, kind)
		if err != nil {
			return results, err
		}
		at, err := e.place(item, kind, data)
		if err != nil {
			return results, err
		}
		records[id] = Record{Kind: kind.Name, Version: str(item, "version"), SHA256: str(item, "sha256"),
			Source: str(item, "source"), Name: str(item, "name"), Path: at, Installed: time.Now().Format("2006-01-02T15:04:05"),
			Provides: str(item, "provides"), Requires: strs(item, "requires")}
		if err := e.saveRecords(records); err != nil {
			return results, err
		}
		results = append(results, Result{ID: id, Kind: kind.Name, Version: str(item, "version"), Changed: true, From: from, Path: at})
	}
	return results, nil
}

// Update updates the given ids, or everything that has a compatible update.
func (e *Env) Update(ids []string) ([]Result, error) {
	unlock, err := e.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	raw, _ := e.Catalogue(0)
	items := e.Annotate(raw)
	if len(ids) == 0 {
		for _, item := range items {
			if item["update"] == true && item["compatible"] == true {
				ids = append(ids, str(item, "id"))
			}
		}
	}
	return e.install(ids, items)
}

// Remove removes what the hub installed; your own items are never touched.
func (e *Env) Remove(ids []string) ([]Result, error) {
	unlock, err := e.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	records := e.records()
	var results []Result
	for _, id := range ids {
		r, ok := records[id]
		if !ok {
			return results, fail(ExitUnknown, "%s was not installed from the hub; iNiR leaves your own items alone", id)
		}
		for other, o := range records {
			if other != id && slices.Contains(o.Requires, id) {
				return results, fail(ExitError, "%s needs %s: remove %s first", other, id, other)
			}
		}
		if r.Provides != "" && e.Shell != "" {
			at := filepath.Join(e.Shell, r.Provides)
			if dest, err := os.Readlink(at); err == nil && dest == e.target(r.Kind, id) {
				os.Remove(at)
				if err := e.writeScanIndex(); err != nil {
					return results, err
				}
			}
		}
		if err := os.RemoveAll(e.target(r.Kind, id)); err != nil {
			return results, err
		}
		delete(records, id)
		if err := e.saveRecords(records); err != nil {
			return results, err
		}
		results = append(results, Result{ID: id, Kind: r.Kind, Removed: true})
	}
	return results, nil
}
