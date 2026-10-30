package hub

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var discussionRE = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/discussions/(\d+)$`)

const reactionsQuery = `query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) { discussion(number: $number) { id reactionGroups { content viewerHasReacted } } }
}`

// Rate votes up or down on an item's Discussion (or clears the vote) as the person gh is logged in as. Without gh,
// or logged out, it opens the Discussion in the browser so they vote there.
func (e *Env) Rate(id, vote string) (string, error) {
	if vote != "up" && vote != "down" && vote != "clear" {
		return "", fail(ExitUsage, "inir hub rate ID up|down|clear")
	}
	raw, _ := e.Catalogue(DefaultMaxAge)
	var url, name string
	found := false
	for _, item := range raw {
		if str(item, "id") == id {
			url, name, found = str(item, "discussion"), orDefault(str(item, "name"), id), true
			break
		}
	}
	if !found {
		return "", fail(ExitUnknown, `no item "%s" in the hub; try: inir hub search`, id)
	}
	m := discussionRE.FindStringSubmatch(url)
	if m == nil {
		return "", fail(ExitError, "%s has no reviews page yet: the hub opens one the day after it is published", name)
	}
	if exec.Command("gh", "auth", "status").Run() != nil {
		if exec.Command("xdg-open", url).Start() == nil {
			return fmt.Sprintf("opened %s's reviews: vote with a thumbs up or down on the first post", name), nil
		}
		return "vote with a thumbs up or down on the first post at " + url, nil
	}
	number, _ := strconv.Atoi(m[3])
	var data struct {
		Repository struct {
			Discussion struct {
				ID             string
				ReactionGroups []struct {
					Content          string
					ViewerHasReacted bool
				}
			}
		}
	}
	if err := ghGraphQL(reactionsQuery, map[string]any{"owner": m[1], "name": m[2], "number": number}, &data); err != nil {
		return "", err
	}
	subject := data.Repository.Discussion.ID
	has := map[string]bool{}
	for _, g := range data.Repository.Discussion.ReactionGroups {
		has[g.Content] = g.ViewerHasReacted
	}
	want := map[string]string{"up": "THUMBS_UP", "down": "THUMBS_DOWN"}[vote]
	for _, content := range []string{"THUMBS_UP", "THUMBS_DOWN"} {
		verb := ""
		switch {
		case content == want && !has[content]:
			verb = "addReaction"
		case content != want && has[content]:
			verb = "removeReaction"
		}
		if verb == "" {
			continue
		}
		q := fmt.Sprintf(`mutation($subject: ID!) { %s(input: {subjectId: $subject, content: %s}) { clientMutationId } }`, verb, content)
		if err := ghGraphQL(q, map[string]any{"subject": subject}, nil); err != nil {
			return "", err
		}
	}
	switch vote {
	case "up":
		return "you liked " + name, nil
	case "down":
		return "you didn't like " + name + ": say why on its reviews page so it can get better (" + url + ")", nil
	}
	return "your vote on " + name + " is gone", nil
}

// ghGraphQL runs a query as the person gh is logged in as.
func ghGraphQL(query string, vars map[string]any, out any) error {
	args := []string{"api", "graphql", "-f", "query=" + query}
	for k, v := range vars {
		flag := "-f"
		if _, isInt := v.(int); isInt {
			flag = "-F"
		}
		args = append(args, flag, fmt.Sprintf("%s=%v", k, v))
	}
	b, err := exec.Command("gh", args...).Output()
	if err != nil {
		msg := err.Error()
		if x, ok := err.(*exec.ExitError); ok && len(x.Stderr) > 0 {
			msg = strings.TrimSpace(string(x.Stderr))
		}
		return fail(ExitNetwork, "gh: %s", msg)
	}
	if out == nil {
		return nil
	}
	var reply struct{ Data json.RawMessage }
	if err := json.Unmarshal(b, &reply); err != nil {
		return fail(ExitNetwork, "gh: %v", err)
	}
	return json.Unmarshal(reply.Data, out)
}
