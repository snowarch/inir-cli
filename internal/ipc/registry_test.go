package ipc

import (
	"os"
	"path/filepath"
	"testing"
)

const sample = `{
 "schema": 1,
 "targets": [
  {"name": "wallpaperSelector", "family": "shared", "description": "", "example": "",
   "functions": [{"name": "toggle", "args": [], "description": "", "values": []}]},
  {"name": "iris", "family": "iris", "description": "", "example": "",
   "functions": [{"name": "status", "args": [], "description": "", "values": []},
                 {"name": "open", "args": ["surface"], "description": "", "values": ["dock"]}]}
 ],
 "aliases": {"wallpaper-selector": "wallpaperSelector"}
}`

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ipc.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestTargetsAndAliases(t *testing.T) {
	r, err := Load(write(t, sample))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"wallpaperSelector", "wallpaper-selector"} {
		target, ok := r.Target(name)
		if !ok || target.Name != "wallpaperSelector" {
			t.Fatalf("Target(%q) = %v, %v", name, target, ok)
		}
	}
	iris, _ := r.Target("iris")
	if fn, ok := iris.Function("open"); !ok || fn.Args[0] != "surface" {
		t.Fatalf("iris.open = %v, %v", fn, ok)
	}
	if _, ok := iris.Function("nope"); ok {
		t.Fatal("unknown function resolved")
	}
	if _, ok := r.Target("nope"); ok {
		t.Fatal("unknown target resolved")
	}
}

func TestRejectsOtherSchema(t *testing.T) {
	if _, err := Load(write(t, `{"schema": 2, "targets": [], "aliases": {}}`)); err == nil {
		t.Fatal("schema 2 accepted")
	}
}
