package main

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/snowarch/inir-cli/internal/hub"
)

const hubUsage = `Usage: inir hub <verb> [args] [--json]

  status [--max-age SECONDS|--cached]   the catalogue merged with what is installed
  sources                               where the catalogue comes from (hub.sources in config adds more)
  sync                                  fetch every source now
  list [--installed|--updates] [--kind KIND]
  search TEXT...
  info ID
  install ID...
  update [ID...]                        every update when no id is given
  remove ID...
  fetch ID... [--for VERSION]           download into the cache only, checked against that iNiR version
  link DIR...                           link a plugin folder of your own into the shell, to work on it
  open [ID]                             the Hub page in your family's Settings, at one item if given
  refresh | changed KIND                ask the running shell to read the hub again

In a hub repo:
  validate [--fix] [ITEM_DIR...]        check every item, or the given ones (--fix writes missing qmldir files)
  bumps [--base REF]                    changed items must raise their version (default origin/main)
  new KIND ID [--author GH]             start an item from templates/KIND
  build [--out DIR]                     index.json, packages, previews, gallery (default dist)
  preview ITEM_DIR --wallpaper IMG [--shot PNG] [--side left|right]

Exit codes: 0 ok, 1 error, 2 usage, 3 a local item of that name exists that the hub did not install,
4 needs a newer iNiR, 5 network, 6 package failed its checksum, 7 unknown id.`

// runHub is `inir hub`. What only talks to the running shell (open, refresh, changed, the hub IPC
// target) stays with the bash launcher.
func runHub(args []string) (int, bool) {
	asJSON := slices.Contains(args, "--json")
	args = slices.DeleteFunc(slices.Clone(args), func(a string) bool { return a == "--json" })
	verb, rest := "", []string{}
	if len(args) > 0 {
		verb, rest = args[0], args[1:]
	}
	switch verb {
	case "", "-h", "--help", "help":
		fmt.Println(hubUsage)
		return 0, true
	case "validate", "bumps", "new", "build", "preview":
		return runHubRepo(verb, rest), true
	case "status", "sources", "sync", "list", "search", "info", "install", "update", "remove", "fetch", "link":
	default:
		return 0, false
	}
	code, err := hubClient(verb, rest, asJSON)
	if err != nil {
		code = hub.Code(err)
		if asJSON {
			printJSON(map[string]any{"ok": false, "error": err.Error(), "code": code})
		}
		fmt.Fprintln(os.Stderr, "inir hub: "+err.Error())
	}
	return code, true
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func usageErr(format string, a ...any) error {
	return &hub.Error{Msg: fmt.Sprintf(format, a...) + " (inir hub --help)", Code: hub.ExitUsage}
}

func hubClient(verb string, args []string, asJSON bool) (int, error) {
	env := hub.Default()
	switch verb {
	case "status", "sources", "sync":
		maxAge := hub.DefaultMaxAge
		if verb == "sync" {
			maxAge = 0
		}
		for i := 0; i < len(args); i++ {
			switch a := args[i]; {
			case a == "--cached":
				maxAge = hub.Cached
			case (a == "--max-age" && i+1 < len(args)) || strings.HasPrefix(a, "--max-age="):
				v := strings.TrimPrefix(a, "--max-age=")
				if a == "--max-age" {
					i++
					v = args[i]
				}
				n, err := strconv.Atoi(v)
				if err != nil {
					return 0, usageErr("--max-age takes seconds")
				}
				maxAge = time.Duration(n) * time.Second
			default:
				return 0, usageErr("unknown argument %s", a)
			}
		}
		raw, states := env.Catalogue(maxAge)
		items := env.Annotate(raw)
		if asJSON {
			printJSON(map[string]any{"ok": true, "inir": env.Version, "sources": states, "items": orNoItems(items), "updates": hub.Updates(items)})
		} else {
			for _, s := range states {
				when := "never"
				if s.Fetched > 0 {
					when = time.Unix(s.Fetched, 0).Format("2006-01-02 15:04")
				}
				line := fmt.Sprintf("%s: %d items, fetched %s", orText(s.Name, s.Source), s.Count, when)
				if s.Error != "" {
					line += " (" + s.Error + ")"
				}
				fmt.Println(line)
			}
			if n := hub.Updates(items); n > 0 {
				fmt.Printf("%d update(s): inir hub update\n", n)
			}
		}
		for _, s := range states {
			if s.Error == "" || s.Count > 0 {
				return 0, nil
			}
		}
		return hub.ExitNetwork, nil
	case "list", "search", "info":
		raw, _ := env.Catalogue(hub.DefaultMaxAge)
		items := env.Annotate(raw)
		if verb == "info" {
			if len(args) != 1 {
				return 0, usageErr("inir hub info ID")
			}
			for _, item := range items {
				if item["id"] == args[0] {
					if asJSON {
						printJSON(map[string]any{"ok": true, "item": item})
					} else {
						b, _ := json.MarshalIndent(item, "", "  ")
						fmt.Println(string(b))
					}
					return 0, nil
				}
			}
			return 0, &hub.Error{Msg: fmt.Sprintf(`no item "%s" in the hub; try: inir hub search`, args[0]), Code: hub.ExitUnknown}
		}
		var keep func(hub.Item) bool
		if verb == "search" {
			if len(args) == 0 {
				return 0, usageErr("inir hub search TEXT...")
			}
			keep = func(item hub.Item) bool {
				text := strings.ToLower(strings.Join(append([]string{fmt.Sprint(item["id"]), str(item, "name"),
					str(item, "summary"), str(item, "description")}, append(strs(item, "tags"), strs(item, "authors")...)...), " "))
				for _, t := range args {
					if !strings.Contains(text, strings.ToLower(t)) {
						return false
					}
				}
				return true
			}
		} else {
			installed, updates, kind := false, false, ""
			for i := 0; i < len(args); i++ {
				switch a := args[i]; {
				case a == "--installed":
					installed = true
				case a == "--updates":
					updates = true
				case a == "--kind" && i+1 < len(args):
					i++
					kind = args[i]
					if _, ok := hub.KindOf(kind); !ok {
						return 0, usageErr("--kind is one of: %s", strings.Join(hub.KindNames(), ", "))
					}
				default:
					return 0, usageErr("unknown argument %s", a)
				}
			}
			keep = func(item hub.Item) bool {
				return (!installed || str(item, "installed") != "") && (!updates || item["update"] == true) &&
					(kind == "" || item["kind"] == kind)
			}
		}
		items = slices.DeleteFunc(items, func(item hub.Item) bool { return !keep(item) })
		if asJSON {
			printJSON(map[string]any{"ok": true, "items": orNoItems(items)})
		} else {
			printItems(items)
		}
		return 0, nil
	}

	var results []hub.Result
	var err error
	switch {
	case verb == "update":
		results, err = env.Update(args)
	case len(args) == 0:
		return 0, usageErr("inir hub %s ID...", verb)
	case verb == "install":
		results, err = env.Install(args)
	case verb == "fetch":
		version := ""
		if i := slices.Index(args, "--for"); i >= 0 && i+1 < len(args) {
			version, args = args[i+1], slices.Delete(slices.Clone(args), i, i+2)
		}
		if len(args) == 0 {
			return 0, usageErr("inir hub fetch ID... [--for VERSION]")
		}
		// nothing installed yet, so the shell is not told
		results, err := env.Fetch(args, version)
		switch {
		case err != nil:
			return 0, err
		case asJSON:
			printJSON(map[string]any{"ok": true, "results": append([]hub.Result{}, results...)})
		default:
			for _, r := range results {
				fmt.Printf("fetched %s %s\n", r.ID, r.Version)
			}
		}
		return 0, nil
	case verb == "link":
		results, err = env.LinkLocal(args)
	default:
		results, err = env.Remove(args)
	}
	// Hub.qml runs this itself and reads the catalogue again on its own.
	if len(results) > 0 && os.Getenv("INIR_HUB_FROM_SHELL") == "" {
		shellCall("hub", "changed", "all")
	}
	if err != nil {
		return 0, err
	}
	if asJSON {
		if results == nil {
			results = []hub.Result{}
		}
		printJSON(map[string]any{"ok": true, "results": results})
		return 0, nil
	}
	if len(results) == 0 {
		fmt.Println("everything is up to date")
	}
	for _, r := range results {
		switch {
		case r.Removed:
			fmt.Println("removed " + r.ID)
		case r.Changed && r.From != "":
			fmt.Printf("updated %s %s\n", r.ID, r.Version)
		case r.Changed:
			fmt.Printf("installed %s %s\n", r.ID, r.Version)
		default:
			fmt.Printf("%s %s is already installed\n", r.ID, r.Version)
		}
	}
	return 0, nil
}

func orNoItems(items []hub.Item) []hub.Item {
	if items == nil {
		return []hub.Item{}
	}
	return items
}

func orText(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func str(item hub.Item, key string) string {
	s, _ := item[key].(string)
	return s
}

func strs(item hub.Item, key string) []string {
	var out []string
	l, _ := item[key].([]any)
	for _, v := range l {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

func printItems(items []hub.Item) {
	if len(items) == 0 {
		fmt.Println("nothing here")
		return
	}
	width := 0
	for _, item := range items {
		width = max(width, len(str(item, "id")))
	}
	slices.SortFunc(items, func(a, b hub.Item) int {
		return strings.Compare(str(a, "kind")+"\x00"+str(a, "id"), str(b, "kind")+"\x00"+str(b, "id"))
	})
	for _, item := range items {
		mark := " "
		if item["update"] == true {
			mark = "↑"
		} else if str(item, "installed") != "" {
			mark = "●"
		}
		fmt.Printf("%s %-*s  %-10s  %-8s  %s\n", mark, width, str(item, "id"), str(item, "kind"), str(item, "version"), str(item, "summary"))
	}
}

func runHubRepo(verb string, args []string) int {
	wd, _ := os.Getwd()
	repo, err := hub.FindRepo(wd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "inir hub: "+err.Error())
		return hub.Code(err)
	}
	flag := func(name, fallback string) string {
		for i, a := range args {
			if a == name && i+1 < len(args) {
				v := args[i+1]
				args = slices.Delete(args, i, i+2)
				return v
			}
		}
		return fallback
	}
	switch verb {
	case "validate":
		if i := slices.Index(args, "--fix"); i != -1 {
			repo.Fix, args = true, slices.Delete(args, i, i+1)
		}
		entries := repo.Validate(args)
		repo.Report()
		if len(repo.Errors) > 0 {
			fmt.Fprintf(os.Stderr, "%d error(s)\n", len(repo.Errors))
			return 1
		}
		line := fmt.Sprintf("%d item(s) ok", len(entries))
		if len(repo.Warnings) > 0 {
			line += fmt.Sprintf(", %d warning(s)", len(repo.Warnings))
		}
		fmt.Println(line)
		return 0
	case "bumps":
		repo.Bumps(flag("--base", "origin/main"))
		repo.Report()
		if len(repo.Errors) > 0 {
			return 1
		}
		return 0
	case "new":
		author := flag("--author", "")
		if len(args) != 2 {
			fmt.Fprintln(os.Stderr, "usage: inir hub new KIND ID [--author GH]")
			return 2
		}
		dest, err := repo.New(args[0], args[1], author)
		if err != nil {
			fmt.Fprintln(os.Stderr, "inir hub: "+err.Error())
			return hub.Code(err)
		}
		fmt.Println("created " + dest)
		fmt.Println("next: edit it, add preview.webp, then: inir hub validate " + dest)
		return 0
	case "build":
		out := flag("--out", repo.Root+"/dist")
		n, err := repo.Build(out)
		repo.Report()
		if err != nil {
			fmt.Fprintln(os.Stderr, err.Error())
			return 1
		}
		fmt.Printf("built %d item(s) into %s\n", n, out)
		return 0
	}
	wallpaper, shot, side := flag("--wallpaper", ""), flag("--shot", ""), flag("--side", "left")
	if len(args) != 1 || wallpaper == "" || (side != "left" && side != "right") {
		fmt.Fprintln(os.Stderr, "usage: inir hub preview ITEM_DIR --wallpaper IMG [--shot PNG] [--side left|right]")
		return 2
	}
	out, err := repo.Preview(args[0], wallpaper, shot, side)
	if err != nil {
		fmt.Fprintln(os.Stderr, "inir hub: "+err.Error())
		return hub.Code(err)
	}
	st, _ := os.Stat(out)
	fmt.Printf("wrote %s (%d KB): look at it at card size before you commit it\n", out, st.Size()/1024)
	return 0
}
