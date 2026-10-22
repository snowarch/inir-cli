// Package look turns the part of an iNiR config that makes how the desktop
// looks into one small file, and back.
package look

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Roots are the config sections a look is made of.
var Roots = []string{"panelFamily", "appearance", "background", "bar", "dock", "iris", "waffles", "lock"}

// Inside the roots, what is someone's own rather than their look.
var mine = map[string]bool{
	"appearance.recentThemes": true, "appearance.favoriteThemes": true,
	"background.wallpapersByMonitor": true, "background.autoWallpaper": true,
	"background.multiMonitor": true, "background.hideUpscaleNotification": true,
	"iris.anime": true, "iris.wallpaper": true, "iris.palette": true,
	"lock.security": true, "lock.launchOnStartup": true, "lock.useHyprlock": true,
	"waffles.behavior": true,
}

var mineKey = regexp.MustCompile(`(?i)(path|^pinnedApps|^pinnedItems|^screenList|^city|^screenCastOutput|^ignoredAppRegexes)$`)

func personal(path []string) bool {
	if mineKey.MatchString(path[len(path)-1]) {
		return true
	}
	for i := 1; i <= len(path); i++ {
		if mine[strings.Join(path[:i], ".")] {
			return true
		}
	}
	return false
}

// File is a look: what it was made from and the config keys that make it.
type File struct {
	Name      string
	Made      time.Time
	Family    string
	Design    string
	Wallpaper Wallpaper
	Config    *Object
}

// Wallpaper is where the look's wallpaper is on the machine it came from, and
// a link to it when there is one.
type Wallpaper struct{ Path, URL string }

// Export takes the look out of cfg. With the shell's defaults it keeps only
// what differs from them (a file small enough to paste); without, every look
// key (a snapshot that does not depend on a shell version).
func Export(cfg, defaults *Object) *File {
	f := &File{Made: time.Now().Truncate(time.Second), Config: delta(roots(cfg), defaults, nil)}
	f.Family = text(cfg, defaults, "panelFamily")
	if f.Family == "iris" {
		f.Design = text(cfg, defaults, "iris", "appearance", "design")
	}
	if p := text(cfg, nil, "background", "wallpaperPath"); p != "" {
		f.Wallpaper = Wallpaper{Path: p, URL: Link(p)}
	}
	return f
}

func roots(cfg *Object) *Object {
	out := NewObject()
	for _, k := range cfg.Keys() {
		if slices.Contains(Roots, k) {
			v, _ := cfg.Get(k)
			out.Set(k, v)
		}
	}
	return out
}

func delta(cur, def *Object, path []string) *Object {
	out := NewObject()
	for _, k := range cur.Keys() {
		p := append(slices.Clone(path), k)
		if personal(p) {
			continue
		}
		cv, _ := cur.Get(k)
		dv, hasD := def.Get(k)
		if co, ok := cv.(*Object); ok {
			do, _ := dv.(*Object)
			if d := delta(co, do, p); d.Len() > 0 {
				out.Set(k, d)
			}
			continue
		}
		if hasD && Equal(cv, dv) {
			continue
		}
		out.Set(k, cv)
	}
	return out
}

func text(cfg, defaults *Object, path ...string) string {
	for _, o := range []*Object{cfg, defaults} {
		if v, ok := o.At(path...); ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
	}
	return ""
}

var wallhaven = regexp.MustCompile(`^wallhaven-([a-z0-9]{6})\.(jpg|jpeg|png)$`)

// Link is a public address for a wallpaper inir downloaded, "" for a file
// only this machine has.
func Link(path string) string {
	if m := wallhaven.FindStringSubmatch(filepath.Base(path)); m != nil {
		return "https://w.wallhaven.cc/full/" + m[1][:2] + "/wallhaven-" + m[1] + "." + m[2]
	}
	return ""
}

// Change is one key an import sets.
type Change struct {
	Path     string
	Old, New any
	Added    bool // the config did not have it
}

// Skip is a key of the file an import left alone, and why.
type Skip struct{ Path, Why string }

// Apply writes the look into cfg: every look key takes the file's value, or
// the default when the file leaves it out, so the desktop ends up as the one
// the file came from. Someone's own keys stay theirs.
func Apply(cfg, defaults *Object, f *File) ([]Change, []Skip) {
	var a applier
	def := NewObject()
	for _, k := range Roots {
		if v, ok := defaults.Get(k); ok {
			def.Set(k, v)
		}
	}
	a.walk(cfg, def, f.Config, nil)
	return a.changes, a.skips
}

type applier struct {
	changes []Change
	skips   []Skip
}

func (a *applier) walk(cur, def, want *Object, path []string) {
	keys := slices.Clone(def.Keys())
	for _, k := range want.Keys() {
		if _, ok := def.Get(k); !ok {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		p := append(slices.Clone(path), k)
		dv, hasD := def.Get(k)
		wv, hasW := want.Get(k)
		switch {
		case len(path) == 0 && !slices.Contains(Roots, k):
			a.skips = append(a.skips, Skip{k, "not part of a look"})
			continue
		case personal(p):
			if hasW {
				a.skips = append(a.skips, Skip{strings.Join(p, "."), "yours, kept"})
			}
			continue
		case hasW && hasD && dv != nil && kind(wv) != kind(dv):
			a.skips = append(a.skips, Skip{strings.Join(p, "."), "should be " + kind(dv)})
			wv, hasW = nil, false
		}
		cv, hasC := cur.Get(k)
		dObj, _ := dv.(*Object)
		wObj, _ := wv.(*Object)
		if dObj != nil || wObj != nil {
			cObj, ok := cv.(*Object)
			if !ok {
				cObj = NewObject()
			}
			a.walk(cObj, dObj, wObj, p)
			if !ok && cObj.Len() > 0 {
				cur.Set(k, cObj)
			}
			continue
		}
		target := dv
		if hasW {
			target = wv
		} else if !hasD {
			continue
		}
		if hasC && Equal(cv, target) {
			continue
		}
		a.changes = append(a.changes, Change{Path: strings.Join(p, "."), Old: cv, New: target, Added: !hasC})
		cur.Set(k, target)
	}
}

// Version is the look format this binary writes and reads.
const Version = 1

// Bytes is the file as people share it.
func (f *File) Bytes() []byte {
	o := NewObject()
	o.Set("inirLook", json.Number(fmt.Sprint(Version)))
	if f.Name != "" {
		o.Set("name", f.Name)
	}
	o.Set("made", f.Made.Format(time.RFC3339))
	o.Set("family", f.Family)
	if f.Design != "" {
		o.Set("design", f.Design)
	}
	if f.Wallpaper != (Wallpaper{}) {
		w := NewObject()
		if f.Wallpaper.Path != "" {
			w.Set("path", f.Wallpaper.Path)
		}
		if f.Wallpaper.URL != "" {
			w.Set("url", f.Wallpaper.URL)
		}
		o.Set("wallpaper", w)
	}
	if f.Config == nil {
		f.Config = NewObject()
	}
	o.Set("config", f.Config)
	return Format(o)
}

// Read parses a look file.
func Read(data []byte) (*File, error) {
	v, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("not a look file: %w", err)
	}
	o, _ := v.(*Object)
	n, ok := field[json.Number](o, "inirLook")
	if !ok {
		return nil, errors.New("not a look file (no inirLook field)")
	}
	if n != json.Number(fmt.Sprint(Version)) {
		return nil, fmt.Errorf("look format %s is newer than this inir reads (%d): update inir", n, Version)
	}
	f := &File{}
	f.Name, _ = field[string](o, "name")
	f.Family, _ = field[string](o, "family")
	f.Design, _ = field[string](o, "design")
	if s, ok := field[string](o, "made"); ok {
		f.Made, _ = time.Parse(time.RFC3339, s)
	}
	if w, ok := field[*Object](o, "wallpaper"); ok {
		f.Wallpaper.Path, _ = field[string](w, "path")
		f.Wallpaper.URL, _ = field[string](w, "url")
	}
	if f.Config, ok = field[*Object](o, "config"); !ok {
		f.Config = NewObject()
	}
	return f, nil
}

func field[T any](o *Object, k string) (T, bool) {
	v, _ := o.Get(k)
	t, ok := v.(T)
	return t, ok
}

// ConfigPath is the shell's config.json, found the way
// scripts/lib/config-path.sh finds it.
func ConfigPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	cur, old := filepath.Join(base, "inir"), filepath.Join(base, "illogical-impulse")
	isDir := func(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }
	if st, err := os.Lstat(old); err == nil && st.Mode()&os.ModeSymlink != 0 && isDir(cur) {
		return filepath.Join(cur, "config.json")
	}
	if isDir(old) {
		return filepath.Join(old, "config.json")
	}
	return filepath.Join(cur, "config.json")
}

// Edit reads the config, lets change edit it and, when it says so, writes it
// back in one rename under the lock switchwall.sh takes for its own writes.
// A config that does not exist yet starts empty.
func Edit(path string, change func(cfg *Object) (bool, error)) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer lock.Close()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			break
		} else if time.Now().After(deadline) {
			return fmt.Errorf("%s is locked: %w", path, err)
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	cfg := NewObject()
	mode := os.FileMode(0o644)
	if data, err := os.ReadFile(path); err == nil {
		v, err := Parse(data)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		var ok bool
		if cfg, ok = v.(*Object); !ok {
			return fmt.Errorf("%s is not a JSON object", path)
		}
		if st, err := os.Stat(path); err == nil {
			mode = st.Mode().Perm()
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	write, err := change(cfg)
	if err != nil || !write {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, Format(cfg), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Set puts a value at a path of cfg, making the objects on the way.
func Set(cfg *Object, v any, path ...string) {
	o := cfg
	for _, k := range path[:len(path)-1] {
		next, ok := o.vals[k].(*Object)
		if !ok {
			next = NewObject()
			o.Set(k, next)
		}
		o = next
	}
	o.Set(path[len(path)-1], v)
}
