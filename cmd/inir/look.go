package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/snowarch/inir-cli/internal/buildinfo"
	"github.com/snowarch/inir-cli/internal/look"
	"github.com/snowarch/inir-cli/internal/ui"
)

const lookUsage = `usage: inir look export [FILE|-] [--name NAME] [--wallpaper URL]
       inir look import FILE|URL|- [--dry-run] [--no-wallpaper]
       inir look history [--json]
       inir look restore N [--dry-run] [--no-wallpaper]`

func runLook(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, lookUsage)
		return 2
	}
	var flags []string
	var pos []string
	opts := map[string]string{}
	for i := 1; i < len(args); i++ {
		switch a := args[i]; a {
		case "-h", "--help":
			fmt.Println(lookUsage)
			return 0
		case "--name", "--wallpaper":
			if i+1 >= len(args) {
				ui.Log("inir look").Error(a + " takes a value")
				return 2
			}
			i++
			opts[a] = args[i]
		case "--dry-run", "--no-wallpaper", "--json":
			flags = append(flags, a)
		default:
			if strings.HasPrefix(a, "--") {
				ui.Log("inir look "+args[0]).Error("unknown flag", "flag", a)
				return 2
			}
			pos = append(pos, a)
		}
	}
	has := func(f string) bool {
		for _, x := range flags {
			if x == f {
				return true
			}
		}
		return false
	}
	switch args[0] {
	case "export":
		if len(pos) > 1 {
			break
		}
		out := ""
		if len(pos) == 1 {
			out = pos[0]
		}
		return lookExport(out, opts["--name"], opts["--wallpaper"])
	case "import":
		if len(pos) != 1 {
			break
		}
		data, label, err := readLook(pos[0])
		if err != nil {
			ui.Log("inir look import").Error(err)
			return 1
		}
		f, err := look.Read(data)
		if err != nil {
			ui.Log("inir look import").Error(err, "from", label)
			return 1
		}
		return applyLook(f, label, "before importing "+label, has("--dry-run"), has("--no-wallpaper"))
	case "history":
		return lookHistory(has("--json"))
	case "restore":
		n, err := strconv.Atoi(strings.TrimPrefix(strings.Join(pos, ""), "#"))
		if len(pos) != 1 || err != nil {
			break
		}
		hist, _ := look.History()
		if n < 1 || n > len(hist) {
			ui.Log("inir look restore").Error(fmt.Sprintf("no snapshot #%d (inir look history lists %d)", n, len(hist)))
			return 1
		}
		return applyLook(hist[n-1].File, fmt.Sprintf("snapshot #%d", n), fmt.Sprintf("before restoring #%d", n),
			has("--dry-run"), has("--no-wallpaper"))
	}
	fmt.Fprintln(os.Stderr, lookUsage)
	return 2
}

func loadConfig() (*look.Object, error) {
	p := look.ConfigPath()
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("no config at %s", p)
	}
	v, err := look.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	cfg, ok := v.(*look.Object)
	if !ok {
		return nil, fmt.Errorf("%s is not a JSON object", p)
	}
	return cfg, nil
}

// shellDefaults is the installed shell's defaults/config.json, nil without one.
func shellDefaults() *look.Object {
	p := installedFile("defaults", "config.json")
	if p == "" {
		return nil
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil
	}
	v, _ := look.Parse(data)
	o, _ := v.(*look.Object)
	return o
}

func lookExport(out, name, link string) int {
	cfg, err := loadConfig()
	if err != nil {
		ui.Log("inir look export").Error(err)
		return 1
	}
	defaults := shellDefaults()
	f := look.Export(cfg, defaults)
	f.Name = name
	if link != "" {
		f.Wallpaper.URL = link
	}
	data := f.Bytes()
	if out == "-" || (out == "" && !isTTY(os.Stdout)) {
		os.Stdout.Write(data)
		return 0
	}
	if out == "" {
		what := f.Design
		if what == "" {
			what = f.Family
		}
		out = fmt.Sprintf("inir-%s-%s.look.json", what, f.Made.Format("20060102-1504"))
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		ui.Log("inir look export").Error(err)
		return 1
	}
	t, w := ui.Current(), os.Stdout
	ui.Header(w, t, "look", "exported to "+out)
	lipgloss.Fprintln(w)
	kv(w, t, "family", familyLine(f))
	n := countLeaves(f.Config)
	what := fmt.Sprintf("%d that differ from the defaults", n)
	if defaults == nil {
		what = fmt.Sprintf("%d (no installed shell to compare with, so all of them)", n)
	}
	kv(w, t, "settings", what)
	switch {
	case f.Wallpaper.URL != "":
		kv(w, t, "wallpaper", f.Wallpaper.URL)
	case f.Wallpaper.Path != "":
		kv(w, t, "wallpaper", tilde(f.Wallpaper.Path)+lipgloss.NewStyle().Foreground(t.Warning).
			Render("  only on this machine: --wallpaper URL adds a link"))
	}
	kv(w, t, "size", fmt.Sprintf("%.1f kB", float64(len(data))/1000))
	return 0
}

func applyLook(f *look.File, label, reason string, dryRun, keepWallpaper bool) int {
	log := ui.Log("inir look")
	cfgPath := look.ConfigPath()
	defaults := shellDefaults()
	_, running := runningShell()
	wall, fetch := "", ""
	if !keepWallpaper {
		wall, fetch = findWallpaper(f.Wallpaper)
	}
	if fetch != "" && !dryRun {
		if err := download(fetch, wall, 200<<20); err != nil {
			log.Warn("wallpaper not downloaded, yours stays", "err", err)
			wall, fetch = "", ""
		}
	}
	var changes []look.Change
	var skips []look.Skip
	var snapshot, familyTo, wallFrom string
	var after *look.File
	err := look.Edit(cfgPath, func(cfg *look.Object) (bool, error) {
		before := look.Export(cfg, nil)
		before.Name = reason
		wallFrom = before.Wallpaper.Path
		if wall == wallFrom {
			wall = ""
		}
		changes, skips = look.Apply(cfg, defaults, f)
		after = look.Export(cfg, defaults)
		if dryRun || (len(changes) == 0 && wall == "") {
			return false, nil
		}
		var err error
		if snapshot, err = look.Save(before); err != nil {
			return false, fmt.Errorf("snapshot first: %w", err)
		}
		// A running shell switches family and wallpaper itself, with its
		// transition and its colors; on disk they just change.
		if running && f.Family != "" && f.Family != before.Family {
			familyTo = f.Family
			look.Set(cfg, before.Family, "panelFamily")
		}
		if wall != "" && !running {
			look.Set(cfg, wall, "background", "wallpaperPath")
		}
		return true, nil
	})
	if err != nil {
		log.Error(err)
		return 1
	}
	for _, s := range skips {
		log.Warn("left alone", "key", s.Path, "why", s.Why)
	}
	t, w := ui.Current(), os.Stdout
	if dryRun {
		ui.Header(w, t, "look", fmt.Sprintf("%s would change %d settings", label, len(changes)))
		lipgloss.Fprintln(w)
		printChanges(w, t, changes)
		if wall != "" {
			how := ""
			if fetch != "" {
				how = "  (downloaded from " + fetch + ")"
			}
			kv(w, t, "wallpaper", tilde(wall)+how)
		}
		return 0
	}
	if snapshot == "" {
		ui.Header(w, t, "look", label+" is already your look")
		return 0
	}
	if familyTo != "" || wall != "" {
		time.Sleep(300 * time.Millisecond) // let the shell read the new config first
	}
	if familyTo != "" && !shellCall("panelFamily", "set", familyTo) {
		look.Edit(cfgPath, func(cfg *look.Object) (bool, error) { look.Set(cfg, familyTo, "panelFamily"); return true, nil })
	}
	if wall != "" && running && !shellCall("wallpaperSelector", "set", wall) {
		look.Edit(cfgPath, func(cfg *look.Object) (bool, error) {
			look.Set(cfg, wall, "background", "wallpaperPath")
			return true, nil
		})
	}
	ui.Header(w, t, "look", "applied "+label)
	lipgloss.Fprintln(w)
	kv(w, t, "changed", fmt.Sprintf("%d settings", len(changes)))
	kv(w, t, "family", familyLine(after))
	if wall != "" {
		how := ""
		if fetch != "" {
			how = "  (downloaded)"
		}
		kv(w, t, "wallpaper", tilde(wall)+how)
	}
	kv(w, t, "undo", "inir look restore 1")
	return 0
}

func familyLine(f *look.File) string {
	if f.Design != "" {
		return f.Family + " · design " + f.Design
	}
	return f.Family
}

// findWallpaper is the look's wallpaper on this machine, or where to put it and
// the link to fetch it from.
func findWallpaper(wp look.Wallpaper) (path, fetch string) {
	if wp.Path != "" {
		if _, err := os.Stat(wp.Path); err == nil {
			return wp.Path, ""
		}
	}
	if wp.URL == "" {
		return "", ""
	}
	pics := os.Getenv("XDG_PICTURES_DIR")
	if pics == "" {
		pics = filepath.Join(os.Getenv("HOME"), "Pictures")
	}
	path = filepath.Join(pics, "Wallpapers", filepath.Base(strings.SplitN(wp.URL, "?", 2)[0]))
	if _, err := os.Stat(path); err == nil {
		return path, ""
	}
	return path, wp.URL
}

func download(url, dest string, limit int64) error {
	data, err := fetchHTTPS(url, limit)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(dest+".part", data, 0o644); err != nil {
		return err
	}
	return os.Rename(dest+".part", dest)
}

func fetchHTTPS(url string, limit int64) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, fmt.Errorf("only https links: %s", url)
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "inir-cli/"+buildinfo.String())
	resp, err := (&http.Client{Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = fmt.Errorf("%s is larger than %d MB", url, limit>>20)
	}
	return data, err
}

// readLook reads a look from a file, an https link or stdin (-).
func readLook(src string) ([]byte, string, error) {
	switch {
	case src == "-":
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
		return data, "stdin", err
	case strings.HasPrefix(src, "https://"), strings.HasPrefix(src, "http://"):
		data, err := fetchHTTPS(src, 1<<20)
		return data, filepath.Base(strings.SplitN(src, "?", 2)[0]), err
	}
	data, err := os.ReadFile(src)
	return data, filepath.Base(src), err
}

func lookHistory(asJSON bool) int {
	hist, err := look.History()
	if err != nil {
		ui.Log("inir look history").Error(err)
		return 1
	}
	if asJSON {
		type row struct {
			N         int       `json:"n"`
			Made      time.Time `json:"made"`
			Why       string    `json:"why"`
			Family    string    `json:"family"`
			Design    string    `json:"design,omitempty"`
			Wallpaper string    `json:"wallpaper,omitempty"`
			File      string    `json:"file"`
		}
		rows := []row{}
		for i, s := range hist {
			rows = append(rows, row{i + 1, s.Made, s.Name, s.Family, s.Design, s.Wallpaper.Path, s.Path})
		}
		e := json.NewEncoder(os.Stdout)
		e.SetIndent("", "  ")
		e.Encode(rows)
		return 0
	}
	t, w := ui.Current(), os.Stdout
	if len(hist) == 0 {
		ui.Header(w, t, "look", "no snapshots yet: every import and restore takes one first")
		return 0
	}
	ui.Header(w, t, "look", fmt.Sprintf("%d %s, newest first · inir look restore N", len(hist), plural(len(hist), "snapshot")))
	lipgloss.Fprintln(w)
	num := lipgloss.NewStyle().Foreground(t.Accent).Bold(true).Width(4).Align(lipgloss.Right)
	when := lipgloss.NewStyle().Foreground(t.Faint).Width(14)
	why := lipgloss.NewStyle().Foreground(t.Text)
	fam := lipgloss.NewStyle().Foreground(t.Muted)
	for i, s := range hist {
		lipgloss.Fprintln(w, num.Render(strconv.Itoa(i+1))+"  "+when.Render(ago(s.Made))+why.Render(s.Name)+
			"  "+fam.Render(familyLine(s.File)))
	}
	return 0
}

func printChanges(w io.Writer, t ui.Theme, changes []look.Change) {
	key := lipgloss.NewStyle().Foreground(t.Text)
	old := lipgloss.NewStyle().Foreground(t.Error)
	nw := lipgloss.NewStyle().Foreground(t.Success)
	arrow := lipgloss.NewStyle().Foreground(t.Faint).Render(" → ")
	for _, c := range changes {
		from := short(look.Compact(c.Old))
		if c.Added {
			from = "unset"
		}
		lipgloss.Fprintln(w, "  "+key.Render(c.Path)+"  "+old.Render(from)+arrow+nw.Render(short(look.Compact(c.New))))
	}
}

func kv(w io.Writer, t ui.Theme, k, v string) {
	lipgloss.Fprintln(w, "  "+lipgloss.NewStyle().Foreground(t.Muted).Width(11).Render(k)+
		lipgloss.NewStyle().Foreground(t.Text).Render(v))
}

func short(s string) string {
	if r := []rune(s); len(r) > 40 {
		return string(r[:39]) + "…"
	}
	return s
}

func countLeaves(o *look.Object) int {
	n := 0
	for _, k := range o.Keys() {
		v, _ := o.Get(k)
		if sub, ok := v.(*look.Object); ok {
			n += countLeaves(sub)
		} else {
			n++
		}
	}
	return n
}

func tilde(p string) string {
	if home := os.Getenv("HOME"); home != "" && strings.HasPrefix(p, home+"/") {
		return "~" + p[len(home):]
	}
	return p
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 48*time.Hour:
		return "yesterday"
	}
	return t.Format("Jan 2 15:04")
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

func isTTY(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
