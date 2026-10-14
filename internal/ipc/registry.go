// Package ipc reads the shell's IPC surface from scripts/lib/ipc.json, the
// file the shell repo generates from its IpcHandler blocks.
package ipc

import (
	"encoding/json"
	"fmt"
	"os"
)

// SchemaVersion is the ipc.json schema this binary understands.
const SchemaVersion = 1

type Function struct {
	Name        string   `json:"name"`
	Args        []string `json:"args"`
	Description string   `json:"description"`
	Values      []string `json:"values"`
}

type Target struct {
	Name        string     `json:"name"`
	Family      string     `json:"family"`
	Description string     `json:"description"`
	Example     string     `json:"example"`
	Functions   []Function `json:"functions"`
}

type Registry struct {
	Schema  int               `json:"schema"`
	Targets []Target          `json:"targets"`
	Aliases map[string]string `json:"aliases"`

	byName map[string]*Target
}

// Load reads and indexes an ipc.json file.
func Load(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if r.Schema != SchemaVersion {
		return nil, fmt.Errorf("%s: schema %d, this inir reads %d", path, r.Schema, SchemaVersion)
	}
	r.byName = make(map[string]*Target, len(r.Targets))
	for i := range r.Targets {
		r.byName[r.Targets[i].Name] = &r.Targets[i]
	}
	return &r, nil
}

// Target resolves a target by name or kebab-case alias.
func (r *Registry) Target(name string) (*Target, bool) {
	if canonical, ok := r.Aliases[name]; ok {
		name = canonical
	}
	t, ok := r.byName[name]
	return t, ok
}

// Function finds a function of the target by name.
func (t *Target) Function(name string) (*Function, bool) {
	for i := range t.Functions {
		if t.Functions[i].Name == name {
			return &t.Functions[i], true
		}
	}
	return nil, false
}
