package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture is a fake install under a temp HOME: a runtime copy with the
// critical files, a config dir with the installed marker.
func fixture(t *testing.T) (*Env, *checker) {
	t.Helper()
	home := t.TempDir()
	e := &Env{Getenv: func(string) string { return "" }, Home: home, OS: "arch"}
	e.ConfigHome = filepath.Join(home, ".config")
	e.StateHome = filepath.Join(home, ".local", "state")
	e.CacheHome = filepath.Join(home, ".cache")
	e.DataHome = filepath.Join(home, ".local", "share")
	e.BinHome = filepath.Join(home, ".local", "bin")
	e.ConfDir = filepath.Join(e.ConfigHome, "inir")
	e.RuntimeDir = filepath.Join(e.ConfigHome, "quickshell", "inir")
	e.RepoRoot = filepath.Join(home, "inir")
	for _, f := range []string{"shell.qml", "GlobalStates.qml", "modules/common/Config.qml", "services/NiriService.qml", "modules/ii/plugin.json"} {
		write(t, filepath.Join(e.RuntimeDir, f), "", 0o644)
	}
	write(t, filepath.Join(e.ConfDir, "installed_true"), "", 0o644)
	no := false
	return e, &checker{e: e, jq: true, usableUserSystemd: &no}
}

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func checkStep(c *checker, n int) *step {
	s := &step{}
	Steps[n-1].check(c, s)
	return s
}

func want(t *testing.T, name string, s *step, status Status) {
	t.Helper()
	if got := s.status(); got != status {
		t.Errorf("%s: %s, want %s (%v)", name, got, status, s.lines)
	}
}

func TestStepsAreNumberedInOrder(t *testing.T) {
	for i, s := range Steps {
		if s.N != i+1 {
			t.Fatalf("step %d at %d", s.N, i+1)
		}
	}
}

func TestRuntimeMissingFailsOnce(t *testing.T) {
	_, c := fixture(t)
	c.e.RuntimeDir = ""
	a, b := checkStep(c, 4), checkStep(c, 5)
	want(t, "first", a, StatusFail)
	want(t, "second", b, StatusPass) // only "skipped" info, like bash
}

func TestScriptPermissions(t *testing.T) {
	e, c := fixture(t)
	write(t, filepath.Join(e.RuntimeDir, "scripts", "ok.sh"), "", 0o755)
	write(t, filepath.Join(e.RuntimeDir, "scripts", "lib", "test-skip.py"), "", 0o644)
	want(t, "all executable", checkStep(c, 5), StatusPass)
	write(t, filepath.Join(e.RuntimeDir, "scripts", "lib", "bad.py"), "", 0o644)
	want(t, "one not", checkStep(c, 5), StatusFixable)
}

func TestUserConfig(t *testing.T) {
	e, c := fixture(t)
	c.jq = true
	want(t, "absent", checkStep(c, 7), StatusPass)
	write(t, filepath.Join(e.ConfDir, "config.json"), `{"a": 1}`, 0o644)
	want(t, "valid", checkStep(c, 7), StatusPass)
	write(t, filepath.Join(e.ConfDir, "config.json"), `{"a": `, 0o644)
	want(t, "broken", checkStep(c, 7), StatusFail)
	c.jq = false // bash skips the parse without jq
	want(t, "broken without jq", checkStep(c, 7), StatusPass)
}

func TestStateDirectoriesAndManifest(t *testing.T) {
	e, c := fixture(t)
	want(t, "dirs missing", checkStep(c, 8), StatusFixable)
	os.MkdirAll(filepath.Join(e.StateHome, "quickshell", "user"), 0o755)
	os.MkdirAll(filepath.Join(e.CacheHome, "quickshell"), 0o755)
	want(t, "dirs there", checkStep(c, 8), StatusPass)
	want(t, "manifest missing", checkStep(c, 10), StatusFixable)
	write(t, filepath.Join(e.RuntimeDir, ".inir-manifest"), "", 0o644)
	want(t, "manifest there", checkStep(c, 10), StatusPass)
}

func TestVersionTracking(t *testing.T) {
	e, c := fixture(t)
	want(t, "missing", checkStep(c, 9), StatusFixable)
	write(t, filepath.Join(e.ConfDir, "version.json"), `{"version": "2.34.0"}`, 0o644)
	s := checkStep(c, 9)
	want(t, "incomplete", s, StatusFixable)
	if s.lines[0].Fix != "Repaired incomplete version tracking" {
		t.Errorf("fix text %q", s.lines[0].Fix)
	}
	write(t, filepath.Join(e.ConfDir, "version.json"), `{"version": "2.34.0", "commit": "abc"}`, 0o644)
	want(t, "complete", checkStep(c, 9), StatusPass)
}

func TestWallpapers(t *testing.T) {
	e, c := fixture(t)
	t.Setenv("PATH", t.TempDir()) // no xdg-user-dir: Pictures under the fake HOME
	want(t, "no dir", checkStep(c, 22), StatusPass)
	dir := filepath.Join(e.Home, "Pictures", "Wallpapers")
	write(t, filepath.Join(dir, "a.png"), "png", 0o644)
	want(t, "healthy", checkStep(c, 22), StatusPass)
	write(t, filepath.Join(dir, "b.JPG"), "", 0o644)
	want(t, "empty one", checkStep(c, 22), StatusFixable)
}

func TestEnvironmentProfiles(t *testing.T) {
	e, c := fixture(t)
	write(t, filepath.Join(e.Home, ".bashrc"), "# nothing\n", 0o644)
	s := checkStep(c, 23)
	want(t, "bashrc without INIR_VENV", s, StatusFixable)
	write(t, filepath.Join(e.Home, ".bashrc"), "export INIR_VENV=x\n", 0o644)
	write(t, filepath.Join(e.ConfigHome, "fish", "conf.d", "inir-env.fish"), "", 0o644)
	want(t, "set", checkStep(c, 23), StatusPass)
	write(t, filepath.Join(e.ConfigHome, "environment.d", "quickshell-mem.conf"), "MALLOC_ARENA_MAX=2\n", 0o644)
	want(t, "legacy allocator", checkStep(c, 23), StatusFixable)
}

func TestLegacyNiriStartup(t *testing.T) {
	e, c := fixture(t)
	write(t, filepath.Join(e.ConfigHome, "niri", "config.kdl"), "spawn-at-startup \"qs\" \"-c\" \"ii\"\n", 0o644)
	if !c.legacyNiriStartup() {
		t.Error("qs -c ii not seen")
	}
	write(t, filepath.Join(e.ConfigHome, "niri", "config.kdl"), "spawn-at-startup \"inir\" \"run\"\n", 0o644)
	if c.legacyNiriStartup() {
		t.Error("inir run is not the legacy line")
	}
}

func TestDenoVersion(t *testing.T) {
	dir := t.TempDir()
	for v, ok := range map[string]bool{"2.3.0": true, "2.10.1": true, "2.2.9": false, "1.46.3": false, "3.0.0": true} {
		bin := filepath.Join(dir, "deno-"+v)
		write(t, bin, "#!/bin/sh\necho 'deno "+v+" (stable, release)'\n", 0o755)
		if got := denoCompatible(bin); got != ok {
			t.Errorf("deno %s: %v", v, got)
		}
	}
}

func TestCriticalFilesFamilies(t *testing.T) {
	e, c := fixture(t)
	want(t, "default family", checkStep(c, 4), StatusPass)
	write(t, filepath.Join(e.ConfDir, "config.json"), `{"panelFamily": "iris"}`, 0o644)
	want(t, "family missing", checkStep(c, 4), StatusFail)
	c.jq = false // bash can't read the family without jq
	want(t, "family unknown", checkStep(c, 4), StatusPass)
	c.jq = true
	write(t, filepath.Join(e.RuntimeDir, "modules", "iris", "plugin.json"), "{}", 0o644)
	want(t, "family installed", checkStep(c, 4), StatusPass)
	os.Symlink(filepath.Join(e.DataHome, "inir", "plugins", "gone"), filepath.Join(e.RuntimeDir, "modules", "gone"))
	want(t, "link broken", checkStep(c, 4), StatusFail)
}

func TestOwnRuntime(t *testing.T) {
	e, c := fixture(t)
	runtime := filepath.Join(e.DataHome, "inir", "runtime")
	write(t, filepath.Join(runtime, "0.1.0", "bin", "qs"), "#!/bin/sh\nexit 0\n", 0o755)
	if err := os.Symlink("0.1.0", filepath.Join(runtime, "current")); err != nil {
		t.Fatal(err)
	}
	want(t, "starts", checkStep(c, 15), StatusPass)
	want(t, "polkit", checkStep(c, 16), StatusPass)
	// the Web Apps tab turned on: Qt WebEngine is wanted over qs-rs 0.1.0
	write(t, filepath.Join(e.RepoRoot, "shell.qml"), "", 0o644)
	write(t, filepath.Join(e.RepoRoot, "sdata", "prebuilt.json"), `{"parts":{"qs-rs-webengine":{"dest":"runtime","with":"qs-rs","when":"sidebar.plugins.enable","title":"Qt WebEngine","version":"0.1.0","sha256":"ab"}}}`, 0o644)
	write(t, filepath.Join(e.ConfDir, "config.json"), `{"sidebar":{"plugins":{"enable":true}}}`, 0o644)
	write(t, filepath.Join(runtime, "0.1.0", ".sha256"), "cd\n", 0o644)
	want(t, "add-on missing", checkStep(c, 15), StatusFixable)
	write(t, filepath.Join(runtime, "0.1.0", ".qs-rs-webengine.sha256"), "ab\n", 0o644)
	want(t, "add-on in", checkStep(c, 15), StatusPass)
	write(t, filepath.Join(runtime, "0.1.0", "bin", "qs"), "#!/bin/sh\nexit 1\n", 0o755)
	want(t, "won't start", checkStep(c, 15), StatusFixable)
	os.RemoveAll(filepath.Join(runtime, "0.1.0"))
	want(t, "gone", checkStep(c, 15), StatusFixable)
}

func TestAccelHooks(t *testing.T) {
	e, c := fixture(t)
	if !c.accelHooksOK() {
		t.Fatal("nothing installed")
	}
	accel := filepath.Join(e.DataHome, "inir", "accel")
	bin := filepath.Join(accel, "current", "inir-accel")
	write(t, filepath.Join(accel, "0.1.0", "inir-accel"), "", 0o755)
	if err := os.Symlink("0.1.0", filepath.Join(accel, "current")); err != nil {
		t.Fatal(err)
	}
	venv := filepath.Join(e.StateHome, "quickshell", ".venv", "bin")
	gen := filepath.Join(e.StateHome, "quickshell", "user", "generated", "bin")
	os.MkdirAll(venv, 0o755)
	os.MkdirAll(gen, 0o755)
	os.Symlink("/usr/bin/python3", filepath.Join(venv, "python3"))
	if c.accelHooksOK() {
		t.Fatal("accel there, no hooks")
	}
	os.Remove(filepath.Join(venv, "python3"))
	for _, h := range append([]string{filepath.Join(venv, "python3")}, filepath.Join(gen, accelGo[0]), filepath.Join(gen, accelGo[1]), filepath.Join(gen, accelGo[2])) {
		os.Symlink(bin, h)
	}
	if !c.accelHooksOK() {
		t.Fatal("hooks set")
	}
	os.Remove(filepath.Join(accel, "current"))
	if c.accelHooksOK() {
		t.Fatal("accel gone, hooks left")
	}
}

func TestConfiguredFontsFromFamilies(t *testing.T) {
	e, _ := fixture(t)
	write(t, filepath.Join(e.RuntimeDir, "modules", "iris", "plugin.json"), `{"settings":{"font":{"key":"iris.appearance.fontFamily","fallback":"Geist"}}}`, 0o644)
	write(t, filepath.Join(e.RuntimeDir, "modules", "waffle", "plugin.json"), `{"settings":{"font":{"key":"waffles.theming.font.family","fallback":"Noto Sans"}}}`, 0o644)
	cfg := filepath.Join(e.ConfDir, "config.json")
	write(t, cfg, `{"appearance":{"typography":{"mainFont":"Roboto Flex"}},"iris":{"appearance":{"fontFamily":""}},"waffles":{"theming":{"font":{"family":"Segoe"}}}}`, 0o644)
	// iRiS has no font set: the one it falls back to is the one it shows
	if got := strings.Join(configuredFonts(cfg, e.RuntimeDir), ","); got != "Geist,Roboto Flex,Segoe" {
		t.Fatalf("got %q", got)
	}
	// a key whose path hits a string or a false, and a broken manifest, cost only their own family
	write(t, filepath.Join(e.RuntimeDir, "modules", "broken", "plugin.json"), `{`, 0o644)
	write(t, cfg, `{"appearance":{"typography":{"mainFont":"Roboto Flex"}},"iris":"x","waffles":{"theming":{"font":{"family":false}}}}`, 0o644)
	if got := strings.Join(configuredFonts(cfg, e.RuntimeDir), ","); got != "Geist,Noto Sans,Roboto Flex" {
		t.Fatalf("got %q", got)
	}
}
