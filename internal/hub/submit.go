package hub

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// HubRepo is the official hub on GitHub: where items are submitted, voted on and counted.
const HubRepo = "snowarch/inir-hub"

// Submit sends one item of a hub checkout to the official hub as a pull request: it checks the item as CI will,
// commits it on its own branch, pushes to the person's fork (made if missing) and opens the PR with the reviewer's
// card as its body. dryRun checks and prints the steps without running any of them.
func (r *Repo) Submit(dir string, dryRun bool, say func(string)) error {
	abs, _ := filepath.Abs(dir)
	entries := r.Validate([]string{abs})
	// What CI checks on the item itself (Check): its contracts, and against main its version, its lane and the lines
	// it adds.
	r.contracts(abs)
	if since := r.git("merge-base", "origin/main", "HEAD"); since != "" {
		r.bumped("origin/main", r.rel(abs))
		r.itemRules(since, r.rel(abs), false)
	}
	if len(r.Errors) > 0 {
		return fail(ExitError, "%d error(s): fix them before submitting", len(r.Errors))
	}
	if len(entries) != 1 {
		return fail(ExitUsage, "inir hub submit ITEM_DIR (one item folder of this hub)")
	}
	e := entries[0]
	version, name := text(e.item, "version"), orDefault(text(e.item, "name"), e.id)
	cards := r.Audit([]string{abs})
	body := "Submitted with `inir hub submit`.\n\n"
	if len(cards) == 1 {
		body += cards[0].Markdown()
	}
	say(fmt.Sprintf("%s %s checks out (%d warning(s))", e.id, version, len(r.Warnings)))

	run := func(args ...string) (string, error) {
		if dryRun {
			say("would run: " + strings.Join(args, " "))
			return "", nil
		}
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = r.Root
		out, err := cmd.Output()
		if err != nil {
			msg := err.Error()
			if x, ok := err.(*exec.ExitError); ok && len(x.Stderr) > 0 {
				msg = strings.TrimSpace(string(x.Stderr))
			}
			return "", fail(ExitError, "%s: %s", strings.Join(args[:2], " "), msg)
		}
		return strings.TrimSpace(string(out)), nil
	}

	if exec.Command("gh", "auth", "status").Run() != nil {
		say("gh is missing or logged out, so the rest is yours: commit the folder on a branch, push it to your fork of " +
			HubRepo + ", and open a pull request at https://github.com/" + HubRepo + "/compare (paste the card from `inir hub audit --markdown " + r.rel(abs) + "`)")
		return nil
	}

	branch := r.git("rev-parse", "--abbrev-ref", "HEAD")
	if branch == "main" || branch == "HEAD" || branch == "" {
		branch = "submit/" + e.id + "-" + version
		if _, err := run("git", "switch", "-c", branch); err != nil {
			return err
		}
	}
	if _, err := run("git", "add", "--", r.rel(abs)); err != nil {
		return err
	}
	if dryRun || exec.Command("git", "-C", r.Root, "diff", "--cached", "--quiet").Run() != nil {
		if _, err := run("git", "commit", "-m", fmt.Sprintf("feat(%s): %s %s", e.kind.Name, e.id, version)); err != nil {
			return err
		}
	}

	// Where to push: this checkout if it is a fork of the hub (or the hub and they can write to it), else a fork
	// gh makes now.
	var view struct {
		NameWithOwner string
		IsFork        bool
		Parent        struct {
			Owner struct{ Login string }
			Name  string
		}
		ViewerPermission string
	}
	viewCmd := exec.Command("gh", "repo", "view", "--json", "nameWithOwner,isFork,parent,viewerPermission")
	viewCmd.Dir = r.Root
	if b, err := viewCmd.Output(); err == nil {
		json.Unmarshal(b, &view)
	}
	remote, owner := "origin", strings.Split(view.NameWithOwner, "/")[0]
	forkOfHub := view.IsFork && view.Parent.Owner.Login+"/"+view.Parent.Name == HubRepo
	canWrite := view.NameWithOwner == HubRepo && slices.Contains([]string{"ADMIN", "MAINTAIN", "WRITE"}, view.ViewerPermission)
	if !forkOfHub && !canWrite {
		remote = "fork"
		if _, err := run("gh", "repo", "fork", HubRepo, "--remote", "--remote-name", remote); err != nil {
			return err
		}
		login, _ := exec.Command("gh", "api", "user", "-q", ".login").Output()
		owner = strings.TrimSpace(string(login))
	}
	if _, err := run("git", "push", "-u", remote, branch); err != nil {
		return err
	}
	url, err := run("gh", "pr", "create", "--repo", HubRepo, "--base", "main", "--head", owner+":"+branch,
		"--title", fmt.Sprintf("%s: %s %s", e.kind.Name, name, version), "--body", body)
	if err != nil {
		return err
	}
	if !dryRun {
		say("opened " + url + ": once CI passes, the reviewer merges it or comments what to fix")
	}
	return nil
}
