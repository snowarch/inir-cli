package hub

import (
	"bytes"
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

// What people did with the hub's items, gathered from GitHub with no server of ours: downloads are the download
// counts of the packages published as assets of the hub repo's "packages" release, votes are the thumbs up and
// down on each item's Discussion. The hub's stats workflow runs `inir hub stats` daily and keeps stats.json on
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

func (g *GitHub) graphql(query string, vars map[string]any, out any) error {
	var reply struct {
		Data   json.RawMessage
		Errors []struct{ Message string }
	}
	if _, err := g.do(http.MethodPost, "/graphql", map[string]any{"query": query, "variables": vars}, &reply); err != nil {
		return err
	}
	if len(reply.Errors) > 0 {
		return fail(ExitNetwork, "github graphql: %s", reply.Errors[0].Message)
	}
	return json.Unmarshal(reply.Data, out)
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

type discussion struct {
	Title, URL string
	Up, Down   int
}

const discussionsQuery = `query($owner: String!, $name: String!, $after: String) {
  repository(owner: $owner, name: $name) {
    id
    discussionCategories(first: 25) { nodes { id slug } }
    discussions(first: 100, after: $after, orderBy: {field: CREATED_AT, direction: ASC}) {
      pageInfo { hasNextPage endCursor }
      nodes { title url category { slug } reactionGroups { content reactors { totalCount } } }
    }
  }
}`

// discussions are the category's threads by title (an item's id), the repo's node id and the category's.
func (g *GitHub) discussions(repo, category string) (map[string]discussion, string, string, error) {
	owner, name, _ := strings.Cut(repo, "/")
	out := map[string]discussion{}
	var repoID, categoryID string
	var after any
	for {
		var data struct {
			Repository struct {
				ID                   string
				DiscussionCategories struct{ Nodes []struct{ ID, Slug string } }
				Discussions          struct {
					PageInfo struct {
						HasNextPage bool
						EndCursor   string
					}
					Nodes []struct {
						Title, URL     string
						Category       struct{ Slug string }
						ReactionGroups []struct {
							Content  string
							Reactors struct{ TotalCount int }
						}
					}
				}
			}
		}
		if err := g.graphql(discussionsQuery, map[string]any{"owner": owner, "name": name, "after": after}, &data); err != nil {
			return nil, "", "", err
		}
		repoID = data.Repository.ID
		for _, c := range data.Repository.DiscussionCategories.Nodes {
			if c.Slug == category {
				categoryID = c.ID
			}
		}
		for _, n := range data.Repository.Discussions.Nodes {
			if n.Category.Slug != category {
				continue
			}
			if _, seen := out[n.Title]; seen {
				continue // the first thread of a title keeps the votes
			}
			d := discussion{Title: n.Title, URL: n.URL}
			for _, r := range n.ReactionGroups {
				switch r.Content {
				case "THUMBS_UP":
					d.Up = r.Reactors.TotalCount
				case "THUMBS_DOWN":
					d.Down = r.Reactors.TotalCount
				}
			}
			out[n.Title] = d
		}
		if !data.Repository.Discussions.PageInfo.HasNextPage {
			return out, repoID, categoryID, nil
		}
		after = data.Repository.Discussions.PageInfo.EndCursor
	}
}

const openDiscussion = `mutation($repo: ID!, $category: ID!, $title: String!, $body: String!) {
  createDiscussion(input: {repositoryId: $repo, categoryId: $category, title: $title, body: $body}) { discussion { url } }
}`

// Gather reads the hub repo on GitHub (owner/name) and returns every item's stats. With a Discussion category
// (its slug) it counts votes there and, when open is set, opens a thread for each item that has none.
func (r *Repo) Gather(g *GitHub, repo, category string, open bool) (map[string]Stats, error) {
	entries := r.Validate(nil)
	counts, err := g.downloads(repo)
	if err != nil {
		return nil, err
	}
	threads := map[string]discussion{}
	if category != "" {
		var repoID, categoryID string
		threads, repoID, categoryID, err = g.discussions(repo, category)
		if err != nil {
			return nil, err
		}
		if categoryID == "" {
			r.warnf(repo, "no Discussion category %q: votes are not counted until it exists", category)
			open = false
		}
		for _, e := range entries {
			if _, ok := threads[e.id]; ok || !open {
				continue
			}
			body := fmt.Sprintf("Reviews and votes for **%s** (`%s`), a %s in the iNiR hub.\n\n"+
				"Vote with a thumbs up or down on this post (or `inir hub rate %s up`), and say what works and what doesn't below.\n\n"+
				"Its files: https://github.com/%s/tree/main/%s",
				orDefault(text(e.item, "name"), e.id), e.id, e.kind.Name, e.id, repo, r.rel(e.folder))
			var made struct {
				CreateDiscussion struct{ Discussion struct{ URL string } }
			}
			if err := g.graphql(openDiscussion, map[string]any{"repo": repoID, "category": categoryID, "title": e.id, "body": body}, &made); err != nil {
				r.warnf(e.folder, "could not open its Discussion: %v", err)
				continue
			}
			threads[e.id] = discussion{Title: e.id, URL: made.CreateDiscussion.Discussion.URL}
		}
	}
	out := map[string]Stats{}
	for _, e := range entries {
		t := threads[e.id]
		out[e.id] = Stats{Downloads: counts[e.id], Up: t.Up, Down: t.Down, Discussion: t.URL}
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
