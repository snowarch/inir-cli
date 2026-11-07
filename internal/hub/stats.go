package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// What people did with the hub's items, gathered with no server of ours: downloads are the download counts of
// the packages published as assets of the hub repo's "packages" release; installs, votes and reactions are what
// people published from their own config.json (social.go). The hub's stats workflow runs `inir hub stats` daily and keeps stats.json on
// its "stats" branch, where `inir hub build` reads it.

// PackagesTag is the release whose assets are the packages people download.
const PackagesTag = "packages"

var assetRE = regexp.MustCompile(`^(.+)-(\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.-]+)?)\.tar\.gz$`)

// GitHub is the little of GitHub's API the hub needs.
type GitHub struct {
	Token string
	API   string // https://api.github.com unless a test points it elsewhere
}

// GitHubFromEnv takes the token a workflow gives (GITHUB_TOKEN, GH_TOKEN) or the one gh is logged in with.
func GitHubFromEnv() *GitHub {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		if out, err := exec.Command("gh", "auth", "token").Output(); err == nil {
			token = strings.TrimSpace(string(out))
		}
	}
	return &GitHub{Token: token, API: "https://api.github.com"}
}

func (g *GitHub) do(method, path string, body, out any) (int, error) {
	var in io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		in = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, strings.TrimSuffix(g.API, "/")+path, in)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "inir-hub")
	if g.Token != "" {
		req.Header.Set("Authorization", "Bearer "+g.Token)
	}
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, fail(ExitNetwork, "github: %v", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fail(ExitNetwork, "github %s %s: HTTP %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out != nil {
		return resp.StatusCode, json.Unmarshal(b, out)
	}
	return resp.StatusCode, nil
}

// downloads sums each item's packages, every version, from the packages release.
func (g *GitHub) downloads(repo string) (map[string]int, error) {
	var release struct{ ID int }
	if code, err := g.do(http.MethodGet, "/repos/"+repo+"/releases/tags/"+PackagesTag, nil, &release); err != nil {
		if code == http.StatusNotFound {
			return map[string]int{}, nil
		}
		return nil, err
	}
	out := map[string]int{}
	for page := 1; ; page++ {
		var assets []struct {
			Name     string
			Download int `json:"download_count"`
		}
		if _, err := g.do(http.MethodGet, fmt.Sprintf("/repos/%s/releases/%d/assets?per_page=100&page=%d", repo, release.ID, page), nil, &assets); err != nil {
			return nil, err
		}
		for _, a := range assets {
			if m := assetRE.FindStringSubmatch(a.Name); m != nil {
				out[m[1]] += a.Download
			}
		}
		if len(assets) < 100 {
			return out, nil
		}
	}
}

// Gather reads the hub repo on GitHub (owner/name) for downloads and the relays for what people did, and returns
// every item's stats.
func (r *Repo) Gather(g *GitHub, repo string, relays []string) (map[string]Stats, error) {
	entries := r.Validate(nil)
	counts, err := g.downloads(repo)
	if err != nil {
		return nil, err
	}
	out := map[string]Stats{}
	for _, e := range entries {
		out[e.id] = Stats{Downloads: counts[e.id]}
	}
	if len(relays) > 0 {
		events, answered := fetch(context.Background(), relays)
		if answered == 0 {
			return nil, fail(ExitNetwork, "no relay answered: votes and installs were not counted")
		}
		fold(events, out)
	}
	return out, nil
}

// WriteStats writes stats.json the way build reads it.
func WriteStats(path string, stats map[string]Stats) error {
	b, err := json.MarshalIndent(map[string]any{"generated": time.Now().UTC().Format(time.RFC3339), "items": stats}, "", " ")
	if err != nil {
		return err
	}
	return writeAtomic(path, append(b, '\n'))
}
