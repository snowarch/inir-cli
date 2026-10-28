package main

import (
	"encoding/json"
	"os"
	"slices"

	"github.com/snowarch/inir-cli/internal/cli"
)

type docFlag struct {
	Name   string   `json:"name"`
	Desc   string   `json:"desc"`
	Value  string   `json:"value,omitempty"`
	Values []string `json:"values,omitempty"`
}

type docCommand struct {
	Name  string       `json:"name"`
	Desc  string       `json:"desc"`
	Flags []docFlag    `json:"flags,omitempty"`
	Subs  []docCommand `json:"subs,omitempty"`
}

type docGroup struct {
	Title    string       `json:"title"`
	Commands []docCommand `json:"commands"`
}

func docFlags(flags []cli.Flag) []docFlag {
	var out []docFlag
	for _, f := range flags {
		if f.Name == "--help" {
			continue
		}
		out = append(out, docFlag{Name: f.Name, Desc: f.Desc, Value: f.Value, Values: f.Values})
	}
	return out
}

// runCommands prints what `inir help` shows, in its groups and order, as JSON: inir.space builds its page of
// commands from it (`inir __commands`). Shell actions are left out; their page comes from the shell's ipc.json.
func runCommands() int {
	var groups []docGroup
	for _, g := range helpGroups {
		group := docGroup{Title: g.Title}
		for _, name := range g.Names {
			c, ok := cli.LookupAny(name)
			if !ok || c.Hidden || slices.Contains(rootHidden, name) {
				continue
			}
			flags := c.Flags
			if name == "bind" {
				flags = append(slices.Clone(flags), cli.BindFlags...)
			}
			cmd := docCommand{Name: c.Name, Desc: c.Desc, Flags: docFlags(flags)}
			for _, s := range c.Subs {
				cmd.Subs = append(cmd.Subs, docCommand{Name: s.Name, Desc: s.Desc, Flags: docFlags(s.Flags)})
			}
			group.Commands = append(group.Commands, cmd)
		}
		if len(group.Commands) > 0 {
			groups = append(groups, group)
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", " ")
	if err := enc.Encode(groups); err != nil {
		return 1
	}
	return 0
}
