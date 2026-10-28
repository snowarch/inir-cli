package doctor

import (
	"os"
	"path/filepath"
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
