package hub

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// What the store adds to the catalogue beyond each item's own listing: what was pulled and why, and what people
// did with it (downloads and votes, gathered daily by the hub repo's stats workflow into stats.json).

// Pull is one line of blocked.json: an item, or some of its versions, people must not install.
type Pull struct {
	ID       string   `json:"id"`
	Versions []string `json:"versions,omitempty"` // none: every version
	Reason   string   `json:"reason"`
}

func (p Pull) covers(version string) bool {
	return len(p.Versions) == 0 || slices.Contains(p.Versions, version)
}

// pulls reads blocked.json at the hub's root.
func (r *Repo) pulls() []Pull {
	p := filepath.Join(r.Root, "blocked.json")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	var f struct{ Items []Pull }
	if err := json.Unmarshal(b, &f); err != nil {
		r.errorf(p, "not JSON: %v", err)
		return nil
	}
	var out []Pull
	for _, pull := range f.Items {
		ok := pull.ID != "" && !strings.ContainsAny(pull.ID, "/\\ ") && strings.TrimSpace(pull.Reason) != ""
		for _, v := range pull.Versions {
			ok = ok && semverRE.MatchString(v)
		}
		if !ok {
			r.errorf(p, `each pull is {"id", "versions" (none: all), "reason" people read}`)
			continue
		}
		out = append(out, pull)
	}
	return out
}

// Stats is one item's line in stats.json.
type Stats struct {
	Downloads int            `json:"downloads"`
	Installs  int            `json:"installs"`
	Up        int            `json:"up"`
	Down      int            `json:"down"`
	Reactions map[string]int `json:"reactions,omitempty"`
}

// stats reads stats.json at the root, else the one the stats workflow keeps on the "stats" branch.
func (r *Repo) stats() map[string]Stats {
	b, err := os.ReadFile(filepath.Join(r.Root, "stats.json"))
	if err != nil {
		for _, ref := range []string{"origin/stats", "stats"} {
			if out := r.git("show", ref+":stats.json"); out != "" {
				b = []byte(out)
				break
			}
		}
	}
	var f struct{ Items map[string]Stats }
	if len(b) == 0 || json.Unmarshal(b, &f) != nil || f.Items == nil {
		return map[string]Stats{}
	}
	return f.Items
}

// store fills an index item with what the store knows about it.
func (r *Repo) store(item map[string]any, id, folder string, stats map[string]Stats, pulls []Pull) {
	if added := strings.SplitN(r.git("log", "--format=%cs", "--reverse", "--", folder), "\n", 2)[0]; added != "" {
		item["added"] = added
	}
	if s, ok := stats[id]; ok {
		item["downloads"], item["installs"], item["votesUp"], item["votesDown"] = s.Downloads, s.Installs, s.Up, s.Down
		if len(s.Reactions) > 0 {
			item["reactions"] = s.Reactions
		}
	}
	for _, p := range pulls {
		if p.ID == id && p.covers(text(item, "version")) {
			item["blocked"] = p.Reason
		}
	}
}

// pulledFor is the reason a source pulled this version of an item, or "".
func (e *Env) pulledFor(source, id, version string) string {
	for _, p := range e.pulled[source] {
		if p.ID == id && p.covers(version) {
			return p.Reason
		}
	}
	return ""
}

func parsePulls(raw any) []Pull {
	var out []Pull
	b, _ := json.Marshal(raw)
	json.Unmarshal(b, &out)
	return slices.DeleteFunc(out, func(p Pull) bool { return p.ID == "" || p.Reason == "" })
}
