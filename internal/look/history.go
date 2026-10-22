package look

import (
	"os"
	"path/filepath"
	"sort"
	"time"
)

// keep is how many snapshots history holds.
const keep = 30

// Snapshot is a look saved before an import or a restore changed it.
type Snapshot struct {
	Path string
	*File
}

// HistoryDir is where snapshots live.
func HistoryDir() string {
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(os.Getenv("HOME"), ".local", "state")
	}
	return filepath.Join(state, "inir", "looks")
}

// Save keeps f as the newest snapshot and drops the oldest past keep.
func Save(f *File) (string, error) {
	dir := HistoryDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var p string
	for {
		p = filepath.Join(dir, time.Now().Format("20060102-150405.000000000")+".look.json")
		if _, err := os.Stat(p); err != nil {
			break
		}
	}
	if err := os.WriteFile(p, f.Bytes(), 0o644); err != nil {
		return "", err
	}
	if all, _ := names(); len(all) > keep {
		for _, old := range all[keep:] {
			os.Remove(old)
		}
	}
	return p, nil
}

// History is every snapshot, newest first: number n is History()[n-1].
func History() ([]Snapshot, error) {
	all, err := names()
	if err != nil {
		return nil, err
	}
	var out []Snapshot
	for _, p := range all {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if f, err := Read(data); err == nil {
			out = append(out, Snapshot{Path: p, File: f})
		}
	}
	return out, nil
}

func names() ([]string, error) {
	all, err := filepath.Glob(filepath.Join(HistoryDir(), "*.look.json"))
	sort.Sort(sort.Reverse(sort.StringSlice(all)))
	return all, err
}
