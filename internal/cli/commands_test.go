package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The table must hold every word scripts/inir dispatches itself, or an IPC
// call would take a launcher command. Needs INIR_SHELL_REPO (a checkout).
func TestTableMatchesLauncherDispatch(t *testing.T) {
	repo := os.Getenv("INIR_SHELL_REPO")
	if repo == "" {
		t.Skip("set INIR_SHELL_REPO to an iNiR checkout")
	}
	data, err := os.ReadFile(filepath.Join(repo, "scripts", "inir"))
	if err != nil {
		t.Fatal(err)
	}
	src := string(data)
	start := strings.Index(src, "\ncase \"$command_name\" in\n")
	if start < 0 {
		t.Fatal("dispatch not found")
	}
	end := strings.Index(src[start:], "\nesac\n")
	label := regexp.MustCompile(`(?m)^    ([a-z][a-z|"-]*)\)`)
	var words []string
	for _, m := range label.FindAllStringSubmatch(src[start:start+end], -1) {
		for _, w := range strings.Split(m[1], "|") {
			if w != `""` && !strings.HasPrefix(w, "-") {
				words = append(words, w)
			}
		}
	}
	sort.Strings(words)
	if len(words) < 30 {
		t.Fatalf("parsed only %v", words)
	}
	launcher := map[string]bool{}
	for _, w := range words {
		launcher[w] = true
		if _, ok := Lookup(w); !ok {
			t.Errorf("scripts/inir handles %q, the table does not", w)
		}
	}
	for _, c := range Commands {
		if !launcher[c.Name] {
			t.Errorf("the table has %q, scripts/inir does not dispatch it", c.Name)
		}
	}
}
