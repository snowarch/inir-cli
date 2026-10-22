package look

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func obj(t *testing.T, s string) *Object {
	t.Helper()
	v, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return v.(*Object)
}

func TestFormatKeepsOrderAndNumbers(t *testing.T) {
	in := "{\n    \"z\": 1.0,\n    \"a\": {\n        \"<b>\": [\n        ],\n        \"c\": [\n            1,\n            \"x\"\n        ]\n    },\n    \"e\": {\n    }\n}\n"
	v, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(Format(v)); got != in {
		t.Fatalf("got\n%s", got)
	}
	if !Equal(obj(t, `{"a":1.0}`), obj(t, `{"a":1}`)) || Equal(obj(t, `{"a":"1"}`), obj(t, `{"a":1}`)) {
		t.Error("Equal")
	}
}

const defaults = `{"panelFamily":"ii","ai":{"key":""},
 "appearance":{"globalStyle":"material","recentThemes":[],"palette":{"type":"auto","accentColor":""}},
 "background":{"wallpaperPath":"","effects":{"blur":0}},
 "dock":{"pinnedApps":["kitty"],"iconSize":40},
 "iris":{"appearance":{"design":"iris","texture":"solid"},"anime":{"following":[]}}}`

func TestExportImportRoundTrip(t *testing.T) {
	def := obj(t, defaults)
	mine := obj(t, `{"panelFamily":"iris","ai":{"key":"secret"},
	 "appearance":{"globalStyle":"material","recentThemes":["a"],"palette":{"type":"scheme-tonal-spot","accentColor":"#ff0066"}},
	 "background":{"wallpaperPath":"/home/x/Pictures/wallhaven-abc123.jpg","effects":{"blur":30}},
	 "dock":{"pinnedApps":["firefox"],"iconSize":48},
	 "iris":{"appearance":{"design":"airisu","texture":"solid"},"anime":{"following":["frieren"]}}}`)
	f := Export(mine, def)
	if f.Family != "iris" || f.Design != "airisu" || f.Wallpaper.URL != "https://w.wallhaven.cc/full/ab/wallhaven-abc123.jpg" {
		t.Fatalf("file %+v", f)
	}
	got := Compact(f.Config)
	want := `{"panelFamily":"iris","appearance":{"palette":{"type":"scheme-tonal-spot","accentColor":"#ff0066"}},"background":{"effects":{"blur":30}},"dock":{"iconSize":48},"iris":{"appearance":{"design":"airisu"}}}`
	if got != want {
		t.Fatalf("delta\n%s\nwant\n%s", got, want)
	}
	back, err := Read(f.Bytes())
	if err != nil {
		t.Fatal(err)
	}

	theirs := obj(t, `{"panelFamily":"waffle","ai":{"key":"theirs"},
	 "appearance":{"globalStyle":"angel","recentThemes":["b"],"palette":{"type":"auto","accentColor":""}},
	 "background":{"wallpaperPath":"/theirs.png","effects":{"blur":5}},
	 "dock":{"pinnedApps":["code"],"iconSize":40},
	 "iris":{"appearance":{"design":"iris","texture":"paper"},"anime":{"following":["one piece"]}}}`)
	changes, skips := Apply(theirs, def, back)
	if len(skips) != 0 {
		t.Errorf("skips %+v", skips)
	}
	// Their look is now mine; what is theirs stays.
	if Compact(Export(theirs, def).Config) != want {
		t.Errorf("after import\n%s", Compact(Export(theirs, def).Config))
	}
	for path, want := range map[string]string{"ai.key": `"theirs"`, "appearance.recentThemes": `["b"]`,
		"background.wallpaperPath": `"/theirs.png"`, "dock.pinnedApps": `["code"]`, "iris.anime.following": `["one piece"]`,
		"appearance.globalStyle": `"material"`, "iris.appearance.texture": `"solid"`} {
		v, _ := theirs.At(strings.Split(path, ".")...)
		if Compact(v) != want {
			t.Errorf("%s = %s, want %s", path, Compact(v), want)
		}
	}
	if len(changes) != 8 {
		t.Errorf("%d changes: %+v", len(changes), changes)
	}
}

func TestImportRefusesWhatIsNotALook(t *testing.T) {
	def := obj(t, defaults)
	f, err := Read([]byte(`{"inirLook":1,"family":"ii","config":{"apps":{"terminal":"rm -rf ~"},
	 "dock":{"iconSize":"huge","pinnedApps":["evil"]},"appearance":{"globalStyle":"angel"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	cfg := obj(t, `{"apps":{"terminal":"kitty"},"dock":{"iconSize":40,"pinnedApps":["kitty"]}}`)
	_, skips := Apply(cfg, def, f)
	if Compact(cfg) != `{"apps":{"terminal":"kitty"},"dock":{"iconSize":40,"pinnedApps":["kitty"]},"panelFamily":"ii","appearance":{"globalStyle":"angel","palette":{"type":"auto","accentColor":""}},"background":{"effects":{"blur":0}},"iris":{"appearance":{"design":"iris","texture":"solid"}}}` {
		t.Errorf("cfg %s", Compact(cfg))
	}
	if len(skips) != 3 {
		t.Errorf("skips %+v", skips)
	}
	if _, err := Read([]byte(`{"inirLook":2,"config":{}}`)); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("v2: %v", err)
	}
	if _, err := Read([]byte(`{"ai":{}}`)); err == nil {
		t.Error("a config is not a look")
	}
}

func TestEditAndHistory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	p := filepath.Join(dir, "config.json")
	os.WriteFile(p, []byte("{\n    \"b\": 1.0,\n    \"a\": true\n}\n"), 0o600)
	if err := Edit(p, func(c *Object) (bool, error) { Set(c, "x", "panelFamily"); return true, nil }); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	st, _ := os.Stat(p)
	if string(data) != "{\n    \"b\": 1.0,\n    \"a\": true,\n    \"panelFamily\": \"x\"\n}\n" || st.Mode().Perm() != 0o600 {
		t.Fatalf("%o %s", st.Mode().Perm(), data)
	}
	for i := 0; i < keep+2; i++ {
		if _, err := Save(&File{Name: string(rune('a' + i%26)), Family: "ii", Config: NewObject()}); err != nil {
			t.Fatal(err)
		}
	}
	h, _ := History()
	if len(h) != keep || h[0].Name != string(rune('a'+(keep+1)%26)) {
		t.Fatalf("%d snapshots, newest %q", len(h), h[0].Name)
	}
}

// The shell's real defaults: an export of them is empty, and nothing personal
// is exported from a config that has it all.
func TestRealDefaults(t *testing.T) {
	repo := os.Getenv("INIR_SHELL_REPO")
	if repo == "" {
		t.Skip("set INIR_SHELL_REPO")
	}
	data, err := os.ReadFile(filepath.Join(repo, "defaults", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	def := obj(t, string(data))
	if n := Export(def, def).Config.Len(); n != 0 {
		t.Errorf("defaults differ from themselves in %d roots", n)
	}
	full := string(Format(Export(def, nil).Config))
	for _, bad := range []string{"wallpaperPath", "pinnedApps", "\"following\"", "\"security\"", "requirePasswordToPower", "screenList", "\"city\""} {
		if strings.Contains(full, bad) {
			t.Errorf("snapshot carries %s", bad)
		}
	}
}
