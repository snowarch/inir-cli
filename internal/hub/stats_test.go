package hub

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// A GitHub that knows one packages release.
func fakeGitHub(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/hub/releases/tags/packages":
			w.Write([]byte(`{"id": 7}`))
		case r.URL.Path == "/repos/o/hub/releases/7/assets":
			w.Write([]byte(`[{"name": "clock-1.0.0.tar.gz", "download_count": 40},
				{"name": "clock-1.1.0.tar.gz", "download_count": 2},
				{"name": "web-app-2.0.0-beta.1.tar.gz", "download_count": 5},
				{"name": "notes.txt", "download_count": 99}]`))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestStatsFromGitHub(t *testing.T) {
	srv := fakeGitHub(t)
	defer srv.Close()
	g := &GitHub{API: srv.URL}

	counts, err := g.downloads("o/hub")
	if err != nil {
		t.Fatal(err)
	}
	if counts["clock"] != 42 || counts["web-app"] != 5 || len(counts) != 2 {
		t.Errorf("downloads per item, every version summed: %v", counts)
	}

	none, err := (&GitHub{API: srv.URL}).downloads("o/other")
	if err != nil || len(none) != 0 {
		t.Errorf("no packages release yet should be zero downloads, got %v %v", none, err)
	}
}

func TestStatsFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "stats.json")
	want := map[string]Stats{"clock": {Downloads: 42, Installs: 3, Up: 9, Down: 1, Reactions: map[string]int{"🔥": 2}}}
	if err := WriteStats(p, want); err != nil {
		t.Fatal(err)
	}
	got := (&Repo{Root: dir}).stats()
	b1, _ := json.Marshal(want)
	b2, _ := json.Marshal(got)
	if string(b1) != string(b2) {
		t.Errorf("build reads back %s, wrote %s", b2, b1)
	}
}

func TestDownloadPrefersTheCountedCopy(t *testing.T) {
	data := []byte("package bytes")
	s := sha256.Sum256(data)
	sum := hex.EncodeToString(s[:])
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path == "/clock-1.0.0.tar.gz" {
			w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	dir := t.TempDir()
	local := filepath.Join(dir, "clock-1.0.0.tar.gz")
	os.WriteFile(local, data, 0o644)

	e := &Env{Cache: filepath.Join(dir, "cache"), Version: "9.9.9"}
	item := Item{"id": "clock", "version": "1.0.0", "sha256": sum, "packageUrl": "file://" + local, "downloadUrl": srv.URL + "/clock-1.0.0.tar.gz"}
	if got, err := e.download(item, Kinds[0]); err != nil || string(got) != string(data) || hits != 1 {
		t.Errorf("the release copy should be fetched: %q %v, hits %d", got, err, hits)
	}

	e.Cache = filepath.Join(dir, "cache2")
	item["downloadUrl"] = srv.URL + "/not-yet-uploaded.tar.gz"
	if got, err := e.download(item, Kinds[0]); err != nil || string(got) != string(data) {
		t.Errorf("a missing release copy should fall back to the index's: %q %v", got, err)
	}
}
