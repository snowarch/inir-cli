package hub

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPulledItemsAreRefused(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "index.json"), []byte(`{"schema": 1, "name": "test", "items": [
		{"id": "bad-clock", "kind": "widget", "name": "Bad clock", "version": "1.0.1", "package": "packages/x.tar.gz"},
		{"id": "good-clock", "kind": "widget", "name": "Good clock", "version": "2.0.0", "package": "packages/y.tar.gz"}],
		"blocked": [{"id": "bad-clock", "reason": "sends the clipboard to a paste site"},
			{"id": "good-clock", "versions": ["1.0.0"], "reason": "1.0.0 leaked a token"}]}`), 0o644)
	e := &Env{ConfigHome: filepath.Join(dir, "config"), State: filepath.Join(dir, "state"), Cache: filepath.Join(dir, "cache"),
		Plugins: filepath.Join(dir, "plugins"), Version: "9.9.9", Sources: []string{src}}
	os.MkdirAll(filepath.Join(e.ConfigHome, "inir", "widgets", "good-clock"), 0o755)
	e.saveRecords(map[string]Record{"good-clock": {Kind: "widget", Version: "1.0.0", Source: src}})

	raw, _ := e.Catalogue(0)
	items := e.Annotate(raw)
	bad, _ := find("bad-clock", items)
	good, _ := find("good-clock", items)
	if bad["blocked"] != "sends the clipboard to a paste site" || good["blocked"] != "" {
		t.Fatalf("blocked marks wrong: bad %v, good %v", bad["blocked"], good["blocked"])
	}
	if good["installedBlocked"] != "1.0.0 leaked a token" {
		t.Errorf("the installed pulled version is not flagged: %v (installed %v)", good["installedBlocked"], good["installed"])
	}
	if _, err := e.install([]string{"bad-clock"}, items); Code(err) != ExitPulled {
		t.Errorf("a pulled item installed: %v", err)
	}
}
