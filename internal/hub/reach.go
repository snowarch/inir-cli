package hub

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// What an item reaches outside its own folder, and how much a reviewer is asked to read. Every rule here came from
// pull requests that passed every other check and still reached into the rest of the shell (11-04: two corners of
// 11k and 7k lines, carrying copies of iRiS's own files, importing its insides and writing its state).

// A pull request from outside adds at most this many lines of code; one file holds at most fileMax lines.
const (
	pullMax = 2500
	fileMax = 600
)

// codeExts are the files a reviewer reads as code. Translations, docs, images and data are not counted.
var codeExts = []string{".qml", ".js", ".mjs", ".py", ".sh", ".bash", ".fish", ".go", ".rs", ".lua", ".ts"}

func isCode(p string) bool { return slices.Contains(codeExts, filepath.Ext(p)) }

// offered: the parts of each family an item may import (CONTRIBUTING.md › Adding to a family). The rest of a family
// is its inside: it moves with every release of that family, and an item that imports it stops loading for everyone
// who installed it.
var offered = map[string][]string{
	"ii":     {"kit"},
	"iris":   {"style", "components", "frame"},
	"waffle": {"looks"},
}

var (
	familyImportRE = regexp.MustCompile(`^\s*import\s+qs\.modules\.(ii|iris|waffle)((?:\.\w+)*)`)
	// ["qs", "-c", …], "quickshell -p …", `qs ipc call …`: the shell's binary started by name.
	shellBinaryRE = regexp.MustCompile(`["'](qs|quickshell)["']\s*,|\b(qs|quickshell)\s+(-c|--config|-p|--path|ipc|msg)\b`)
)

// addedByFile: the line numbers this branch adds to each file of the item (new files whole, untracked included).
func (r *Repo) addedByFile(since, item string) map[string][]int {
	added := map[string][]int{}
	file := ""
	for _, line := range strings.Split(r.git("diff", "-U0", "--no-color", since, "--", item), "\n") {
		switch {
		case strings.HasPrefix(line, "+++ "):
			file = strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "b/")
		case strings.HasPrefix(line, "@@ "):
			// @@ -a,b +c,d @@
			f := strings.Fields(line)
			if len(f) < 3 {
				continue
			}
			spec := strings.TrimPrefix(f[2], "+")
			start, count := spec, "1"
			if i := strings.Index(spec, ","); i >= 0 {
				start, count = spec[:i], spec[i+1:]
			}
			s, _ := strconv.Atoi(start)
			n, _ := strconv.Atoi(count)
			for k := 0; k < n; k++ {
				added[file] = append(added[file], s+k)
			}
		}
	}
	for _, p := range strings.Split(r.git("ls-files", "--others", "--exclude-standard", "--", item), "\n") {
		if p == "" {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(r.Root, p))
		for k := range strings.Count(string(b), "\n") + 1 {
			added[p] = append(added[p], k+1)
		}
	}
	return added
}

// size: what a reviewer can read whole. A big item lands as the smallest version that works, then each addition on
// its own pull request; a file does one job.
func (r *Repo) size(added map[string][]int, item string, maintainer bool) {
	total := 0
	for _, rel := range sortedKeysInt(added) {
		if !isCode(rel) {
			continue
		}
		total += len(added[rel])
		b, err := os.ReadFile(filepath.Join(r.Root, rel))
		if err != nil {
			continue
		}
		if n := strings.Count(string(b), "\n"); n > fileMax {
			r.errorf(filepath.Join(r.Root, rel), "%d lines in one file (at most %d): split it by job (its state, each page, each part) so each file can be read, reviewed and fixed on its own", n, fileMax)
		}
	}
	if !maintainer && total > pullMax {
		r.errorf(filepath.Join(r.Root, item), "%d lines of code in one pull request (at most %d, translations and docs not counted): nobody can review that whole, and what nobody read reaches every desktop that installs it. Send the smallest version that works first (one page, one source), then each addition on its own pull request that raises the version", total, pullMax)
	}
}

// reach: an item's added lines import only what a family offers items, and never start the shell's binary. An
// import is reported once, at its first line, with how many other files carry it.
func (r *Repo) reach(added map[string][]int) {
	inside := map[string][]string{}
	defer func() {
		for _, module := range sortedKeysStrings(inside) {
			at := inside[module]
			more := ""
			if len(at) > 1 {
				more = fmt.Sprintf(" (and %d more files)", len(at)-1)
			}
			family := strings.Split(module, ".")[2]
			r.errorf(at[0], "imports %s%s, the inside of the %s family: it changes with every release of it, and your item then stops loading on every desktop that installed it. Items import only what a family offers them (%s); anything else you need from it, ask for in an issue", module, more, family, offeredList())
		}
	}()
	for _, rel := range sortedKeysInt(added) {
		if !isCode(rel) {
			continue
		}
		p := filepath.Join(r.Root, rel)
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		lines := strings.Split(string(b), "\n")
		for _, n := range added[rel] {
			if n < 1 || n > len(lines) {
				continue
			}
			line := lines[n-1]
			where := fmt.Sprintf("%s:%d", p, n)
			if m := familyImportRE.FindStringSubmatch(line); m != nil {
				first, _, _ := strings.Cut(strings.TrimPrefix(m[2], "."), ".")
				if module := "qs.modules." + m[1] + m[2]; !slices.Contains(offered[m[1]], first) {
					inside[module] = append(inside[module], where)
				}
			}
			if strings.HasPrefix(strings.TrimSpace(line), "//") || strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if shellBinaryRE.MatchString(line) {
				r.errorf(where, "starts the shell's binary by name: on iNiR it is qs-rs inside ~/.local/share/inir, or Quickshell on an older install, never a `qs` you can count on. Reach the shell through the inir command: [\"inir\", \"<target>\", \"<function>\"]")
			}
		}
	}
}

func offeredList() string {
	var out []string
	for _, family := range sortedKeysStrings(offered) {
		for _, part := range offered[family] {
			out = append(out, "qs.modules."+family+"."+part)
		}
	}
	return strings.Join(out, ", ") + ", and the base's qs, qs.services and qs.modules.common"
}

func sortedKeysStrings(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Copies. A file is read as runs of shingleLen meaningful lines (no blanks, braces, imports or comments); a file the
// branch adds whose runs are mostly in one file elsewhere in the hub is that file copied. A copy drifts from its
// original the day either changes, and the shell ends up with two of one thing.
const (
	shingleLen  = 4
	copyMinRuns = 24   // smaller files are too short to tell a copy from a common pattern
	copyShare   = 0.45 // of the file's runs found in one other file
	commonRun   = 12   // a run found in more files than this is boilerplate, not a copy
)

func meaningful(text, ext string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		t := strings.Join(strings.Fields(line), " ")
		switch {
		case t == "", strings.Trim(t, "{}()[],;") == "",
			strings.HasPrefix(t, "//"), strings.HasPrefix(t, "/*"), strings.HasPrefix(t, "*"),
			strings.HasPrefix(t, "import "), strings.HasPrefix(t, "pragma "),
			ext == ".py" && (strings.HasPrefix(t, "#") || strings.HasPrefix(t, "from ")),
			(ext == ".sh" || ext == ".bash" || ext == ".fish") && strings.HasPrefix(t, "#"):
			continue
		}
		out = append(out, t)
	}
	return out
}

func runsOf(lines []string) []uint64 {
	seen := map[uint64]bool{}
	var out []uint64
	for i := 0; i+shingleLen <= len(lines); i++ {
		h := fnv.New64a()
		for _, l := range lines[i : i+shingleLen] {
			h.Write([]byte(l))
			h.Write([]byte{'\n'})
		}
		if s := h.Sum64(); !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// copies flags a file this branch adds whose code is mostly another file of the hub's (a family's or another
// item's). Templates are made to be copied and stay out.
func (r *Repo) copies(added map[string][]int, item string) {
	var fresh []string
	for _, rel := range sortedKeysInt(added) {
		if isCode(rel) && len(added[rel]) >= copyMinRuns {
			fresh = append(fresh, rel)
		}
	}
	if len(fresh) == 0 {
		return
	}
	var others []string
	where := map[uint64][]int32{}
	for _, dir := range r.itemDirs() {
		rel := r.rel(dir)
		if rel == item {
			continue
		}
		for _, p := range filesOf(dir) {
			if !isCode(p) || r.ignored()[r.rel(p)] {
				continue
			}
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			id := int32(len(others))
			others = append(others, r.rel(p))
			for _, s := range runsOf(meaningful(string(b), filepath.Ext(p))) {
				if list := where[s]; len(list) <= commonRun {
					where[s] = append(list, id)
				}
			}
		}
	}
	for _, rel := range fresh {
		b, err := os.ReadFile(filepath.Join(r.Root, rel))
		if err != nil {
			continue
		}
		runs := runsOf(meaningful(string(b), filepath.Ext(rel)))
		if len(runs) < copyMinRuns {
			continue
		}
		hits := map[int32]int{}
		for _, s := range runs {
			if list := where[s]; len(list) <= commonRun {
				for _, id := range list {
					hits[id]++
				}
			}
		}
		best, most := int32(-1), 0
		for id, n := range hits {
			if n > most || (n == most && id < best) {
				best, most = id, n
			}
		}
		share := float64(most) / float64(len(runs))
		if best < 0 || share < copyShare {
			continue
		}
		source := others[best]
		advice := "If it is a part the family offers items, import it instead; if not, ask for it in an issue"
		if !strings.HasPrefix(source, "families/") {
			advice = "Two items carrying one file each belong in one item, or the part belongs where both can import it: ask in an issue"
		}
		r.errorf(filepath.Join(r.Root, rel), "%d%% of it is %s: a copy drifts from its original the day either one changes, and the shell ends up with two of one thing. %s", int(share*100+0.5), source, advice)
	}
}
