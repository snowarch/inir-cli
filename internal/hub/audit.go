package hub

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"
)

// The reviewer's eyes, in code: what an item may never do, what it reaches, and how much of it reads as generated
// without care. Errors stop a pull request; the card is what a maintainer reads before approving one.

// Card is what `inir hub audit` prints for one item.
type Card struct {
	ID          string   `json:"id"`
	Kind        string   `json:"kind"`
	Permissions []string `json:"permissions"`
	Commands    []string `json:"commands"`
	Hosts       []string `json:"hosts"`
	Paths       []string `json:"paths"`
	Files       int      `json:"files"`
	Lines       int      `json:"lines"`
	Slop        int      `json:"slop"`
	SlopWhy     []string `json:"slopWhy"`
	Reviewed    bool     `json:"reviewed"`
	Version     string   `json:"version"`
	Errors      []string `json:"errors"`
	Warnings    []string `json:"warnings"`
}

const (
	slopWarn  = 4
	slopError = 8
	codeMax   = 256 * 1024
)

// Never accepted, whatever the listing declares: taking what isn't the shell's, staying behind, hiding.
var never = []struct {
	re  *regexp.Regexp
	why string
}{
	{regexp.MustCompile(`(curl|wget)[^\n|]*\|\s*(ba|z|fi)?sh\b`), "pipes a download into a shell"},
	{regexp.MustCompile(`\beval\s*\(|\bnew\s+Function\s*\(|\bFunction\s*\(\s*["']`), "runs code built from a string"},
	{regexp.MustCompile(`Qt\.createQmlObject\s*\(`), "builds QML from a string"},
	{regexp.MustCompile(`(source|url)\s*:\s*["']https?://[^"']*\.(qml|js)["']|Qt\.include\s*\(\s*["']https?:`), "loads code from the internet"},
	{regexp.MustCompile(`atob\s*\(|base64\s+(-d|--decode)|b64decode\s*\(|\bxxd\s+-r`), "decodes hidden content"},
	{regexp.MustCompile(`String\.fromCharCode\s*\(\s*(\d+\s*,\s*){7,}`), "spells text out in character codes"},
	{regexp.MustCompile(`(?i)\.ssh/|\.gnupg|\bid_(rsa|ed25519|ecdsa)\b|\.local/share/keyrings|\bsecret-tool\s+lookup|\bkwallet-query\b|\.password-store`), "reads keys or passwords"},
	{regexp.MustCompile(`(?i:cookies\.sqlite|logins\.json|key4\.db|cert9\.db|--cookies-from-browser|browser_cookie3)|/(Cookies|Login Data|Web Data|Local State)\b`), "reads a browser's cookies or saved logins"},
	{regexp.MustCompile(`(?i)\.config/gh/hosts\.yml|\.git-credentials|\.netrc\b|\.aws/credentials|\.docker/config\.json|\.kube/config|/etc/shadow`), "reads tokens or credentials"},
	{regexp.MustCompile(`\b(sudo|pkexec|doas)(\s|["']\s*,)|\bsu\s+-|chmod\s+(u\+s|g\+s|\+s|[2467][0-7]{3})\b`), "asks for root"},
	{regexp.MustCompile(`\.config/autostart|systemctl\s+(--user\s+)?(enable|link)|\.config/systemd/|\bcrontab\b|/etc/cron|/etc/profile|[~/]\.(bash|zsh)rc\b|[~/]\.(bash_|z)?profile\b|[~/]\.bash_login\b|[~/]\.x(initrc|profile|session)\b|fish/config\.fish|spawn-at-startup`), "installs itself to run outside the shell"},
	{regexp.MustCompile(`(?i)discord(app)?\.com/api/webhooks|api\.telegram\.org/bot|pastebin\.com|hastebin|transfer\.sh|0x0\.st|ngrok|webhook\.site|requestbin|pipedream\.net|ipinfo\.io|ipify\.org`), "sends data to a drop point"},
	{regexp.MustCompile(`(?i)xmrig|stratum\+tcp|cryptonight|\bminerd\b|nicehash|coinhive`), "mines cryptocurrency"},
	{regexp.MustCompile(`rm\s+-[a-zA-Z]*r[a-zA-Z]*\s+(-[a-zA-Z]+\s+)*("?(~|\$HOME|\$\{HOME\})"?/?(\s|$|\*|["'])|/(\s|$|\*|["']))|\bmkfs\.|\bdd\s+[^\n]*of=/dev/|:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}`), "destroys files or disks"},
	{regexp.MustCompile(`/dev/input/|\bevtest\b|libinput\s+debug-events|\bxinput\s+test|\bwev\b|\bshowkey\b`), "listens to the keyboard or mouse"},
	{regexp.MustCompile(`\b(nc|ncat|netcat|socat)\s+(-[a-z]*e|-[a-z]*c\b)|/dev/tcp/`), "opens a remote shell"},
	{regexp.MustCompile(`["'][A-Za-z0-9+/]{240,}={0,2}["']|\b[0-9a-fA-F]{320,}\b|(\\x[0-9a-fA-F]{2}){16,}|(\\u[0-9a-fA-F]{4}){16,}`), "carries an encoded blob"},
}

// Trojan Source and invisible letters: code that reads differently from what runs. Joiners only count between
// ASCII letters, where they split one name into two; in emoji and in scripts that need them they are text.
var invisible = regexp.MustCompile(`[\x{202A}-\x{202E}\x{2066}-\x{2069}\x{200B}\x{2060}\x{180E}\x{3164}\x{115F}\x{1160}\x{00AD}]|(?s:.)\x{FEFF}|[A-Za-z0-9_][\x{200C}\x{200D}][A-Za-z0-9_]`)

// A public address written as numbers hides who it is; the person's own machine and network are not that.
var bareIP = regexp.MustCompile(`https?://(\d{1,3}(?:\.\d{1,3}){3})`)

var hostRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+[a-z]{2,}$`)

var urlRE = regexp.MustCompile(`https?://[A-Za-z0-9.\-]+(:\d+)?[^\s"'<>)]*`)

// What may sit in an item besides code: pictures, fonts, sounds, text.
var plainExt = []string{".webp", ".png", ".jpg", ".jpeg", ".svg", ".gif", ".ttf", ".otf", ".woff", ".woff2",
	".ogg", ".wav", ".mp3", ".flac", ".json", ".jsonc", ".md", ".txt", ".conf", ".ini", ".toml", ".css", ".html", ".kdl", ".frag",
	".vert", ".qsb", "", ".license"}

// Generated-without-care tells. Each says what a reviewer saw; points add up to the score.
var (
	debugRE = regexp.MustCompile(`\bconsole\.(log|debug)\s*\(|\bprint\s*\(\s*["']debug`)
	todoRE  = regexp.MustCompile(`\b(TODO|FIXME|XXX|HACK)\b|(?i:not (yet )?implemented|lorem ipsum|your (code|logic) here|placeholder (logic|implementation|value)|implement (this|me))`)
	aiRE    = regexp.MustCompile(`(?i)\bas an ai\b|here'?s (the|a|an|how) |this (component|file|module|widget|function|class) (provides|implements|handles|is responsible)|\bcomprehensive\b|\brobust\b|\bseamless(ly)?\b|\bleverag(e|es|ing)\b|elegant (solution|way)|\benhanced?\b|best practices|step \d+:|\bensures? that\b`)
	// Pictographs and the ticks and sparkles generated text sprinkles; a ✕ naming a key on screen is not one.
	emojiRE    = regexp.MustCompile(`[\x{1F300}-\x{1FAFF}\x{2705}\x{274C}\x{2728}\x{26A1}\x{2B50}\x{26A0}\x{2714}\x{27A1}]`)
	bannerRE   = regexp.MustCompile(`(?m)^\s*(//|#|/\*|\*)\s*[=\-*#~_]{16,}`)
	deadRE     = regexp.MustCompile(`(?m)^\s*//\s*[A-Za-z_][\w.]*\s*(\(.*\)\s*;?|:\s*\S.*|=\s*\S.*|\{)\s*$`)
	literalRE  = regexp.MustCompile(`(?m)^\s*(color|border\.color|[A-Za-z]*[Cc]olor)\s*:\s*["']#[0-9a-fA-F]{3,8}["']`)
	commentRE  = regexp.MustCompile(`^\s*(//|/\*|\*)`)
	hashRE     = regexp.MustCompile(`^\s*#`)
	marketRE   = regexp.MustCompile(`(?i)\b(ultimate|revolutionary|blazing(ly)?|next[- ]gen|powerful|amazing|stunning|beautiful|seamless|cutting[- ]edge|game[- ]?changer|supercharge|effortless(ly)?|world[- ]class|unleash)\b`)
	listEmojiR = emojiRE
)

// reviewed.json at the hub's root: id@version a maintainer read and accepted despite its score.
func (r *Repo) reviewed() map[string]bool {
	out := map[string]bool{}
	var f struct{ Items []string }
	if b, err := os.ReadFile(filepath.Join(r.Root, "reviewed.json")); err == nil && json.Unmarshal(b, &f) == nil {
		for _, s := range f.Items {
			out[s] = true
		}
	}
	return out
}

// audit checks every file of an item and returns the permissions its code needs.
func (r *Repo) audit(e *entry, folder, hubFile string, hub map[string]any, declared map[string]bool) map[string]bool {
	card := &Card{ID: e.id, Kind: e.kind.Name}
	for _, p := range permissionOrder {
		if declared[p] {
			card.Permissions = append(card.Permissions, p)
		}
	}
	e.card = card
	found := map[string]bool{}
	hosts := map[string]bool{}
	commands := map[string]bool{}
	paths := map[string]bool{}
	slop := map[string]int{}
	var debug, dead, literals, comments, lines, todos, ais, emoji, banners int
	var giant []string
	windows := map[uint64]int{}
	ignored := r.ignored()

	filepath.WalkDir(folder, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if rel, err := filepath.Rel(r.Root, p); err == nil && ignored[filepath.ToSlash(rel)] {
			return nil
		}
		if d.IsDir() {
			if name == ".git" {
				return filepath.SkipDir
			}
			if strings.HasPrefix(name, ".") && p != folder {
				r.errorf(p, "hidden folders are not accepted: everything that installs is in sight")
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			r.errorf(p, "symlinks are not accepted: they can point anywhere on someone's disk")
			return nil
		}
		if !d.Type().IsRegular() {
			r.errorf(p, "only plain files are accepted")
			return nil
		}
		if strings.HasPrefix(name, ".") && name != ".gitignore" {
			r.errorf(p, "hidden files are not accepted: everything that installs is in sight")
		}
		card.Files++
		st, _ := os.Stat(p)
		ext := strings.ToLower(filepath.Ext(p))
		b, _ := os.ReadFile(p)
		code := slices.Contains(codeExt, ext)
		if !code {
			if !slices.Contains(plainExt, ext) {
				r.errorf(p, "%s files are not accepted: send source, pictures, fonts or sounds", orDefault(ext, "extensionless"))
			} else if ext == "" && isBinary(b) {
				r.errorf(p, "a binary is not accepted: send its source")
			}
			if bytes.HasPrefix(b, []byte("\x7fELF")) || bytes.HasPrefix(b, []byte("MZ")) {
				r.errorf(p, "an executable is not accepted: send its source")
			}
			if ext == ".json" || ext == ".md" || ext == ".html" || ext == ".css" {
				r.scanNever(p, b)
			}
			return nil
		}
		if !utf8.Valid(b) || isBinary(b) {
			r.errorf(p, "not text: send readable source")
			return nil
		}
		if st.Mode()&0o111 != 0 && ext != ".sh" && ext != ".py" && ext != ".fish" {
			r.errorf(p, "marked executable: only scripts may be")
		}
		r.scanNever(p, b)
		for perm, re := range detect {
			if re.Match(b) && !(perm == "files" && ownOptionsOnly(b, e.id)) {
				found[perm] = true
			}
		}
		if (ext == ".sh" || ext == ".py" || ext == ".fish") && e.kind.Name != "webapp" {
			found["process"] = true
			if scriptWrites(ext, b) {
				found["files"] = true
			}
		}
		src := string(b)
		fileLines := strings.Split(src, "\n")
		for _, line := range fileLines {
			if n := utf8.RuneCountInString(line); n > 5000 || n > 400 && strings.Count(line, " ")*100/n < 4 {
				r.errorf(p, "minified or generated in one line: send the readable source")
				break
			}
		}
		for _, m := range urlRE.FindAllString(src, -1) {
			if u, err := url.Parse(m); err == nil && u.Hostname() != "" && !localHost(u.Hostname()) {
				hosts[strings.ToLower(u.Hostname())] = true
			}
		}
		for _, m := range commandRE.FindAllStringSubmatch(src, -1) {
			commands[m[1]] = true
		}
		for _, m := range pathRE.FindAllString(src, -1) {
			paths[m] = true
		}
		// Tells. Counted over the whole item and weighed by its size: a big family may say TODO
		// once, a 90-line widget saying it four times was not finished.
		lines += len(fileLines)
		debug += len(debugRE.FindAllString(src, -1))
		todos += len(todoRE.FindAllString(src, -1))
		var body []string
		comment := commentRE
		if ext == ".sh" || ext == ".py" || ext == ".fish" {
			comment = hashRE
		}
		for _, line := range fileLines {
			t := strings.TrimSpace(line)
			if comment.MatchString(line) {
				comments++
				if aiRE.MatchString(line) {
					ais++
				}
				if emojiRE.MatchString(line) {
					emoji++
				}
				continue
			}
			if len(t) >= 8 && strings.Trim(t, "{}()[];, ") != "" {
				body = append(body, t)
			}
		}
		// Copy and paste: the same six lines of code in more than one place.
		for i := 0; i+6 <= len(body); i++ {
			h := fnv.New64a()
			h.Write([]byte(strings.Join(body[i:i+6], "\n")))
			windows[h.Sum64()]++
		}
		banners += len(bannerRE.FindAllString(src, -1))
		dead += len(deadRE.FindAllString(src, -1))
		if ext == ".qml" {
			literals += len(literalRE.FindAllString(src, -1))
		}
		if st.Size() > codeMax || len(fileLines) > 1500 {
			giant = append(giant, fmt.Sprintf("%s (%d lines)", filepath.Base(p), len(fileLines)))
		}
		if bytes.Contains(b, []byte("\r\n")) {
			r.warnf(p, "Windows line endings")
		}
		return nil
	})
	card.Lines = lines

	// Hosts: the code reaches only what the listing names.
	listed := map[string]bool{}
	hs, ok := list(hub, "hosts")
	var declaredHosts []string
	for _, h := range hs {
		s, _ := h.(string)
		if s = strings.ToLower(s); !hostRE.MatchString(s) {
			ok = false
			continue
		}
		listed[s] = true
		declaredHosts = append(declaredHosts, s)
	}
	if !ok {
		r.errorf(hubFile, `"hosts" lists host names, like "api.open-meteo.com"`)
	}
	if len(declaredHosts) > 0 {
		sort.Strings(declaredHosts)
		e.item["hosts"] = declaredHosts
	}
	if u, err := url.Parse(text(hub, "url")); err == nil && u.Hostname() != "" {
		listed[strings.ToLower(u.Hostname())] = true
		hosts[strings.ToLower(u.Hostname())] = true
	}
	var unlisted []string
	for h := range hosts {
		card.Hosts = append(card.Hosts, h)
		if !hostListed(h, listed) && !docHost(h) {
			unlisted = append(unlisted, h)
		}
	}
	sort.Strings(card.Hosts)
	sort.Strings(unlisted)
	if len(unlisted) > 0 && found["network"] {
		r.errorf(hubFile, `the code reaches %s: list every host in "hosts"`, strings.Join(unlisted, ", "))
	}
	for c := range commands {
		card.Commands = append(card.Commands, c)
	}
	sort.Strings(card.Commands)
	for p := range paths {
		card.Paths = append(card.Paths, p)
	}
	sort.Strings(card.Paths)

	// The score.
	over := func(n, free, per int) int { return max(0, n-free-lines/per) }
	if n := over(debug, 3, 1500); n > 0 {
		slop[fmt.Sprintf("%d debug prints", debug)] += min(5, n)
	}
	if n := over(todos, 0, 3000); n > 0 {
		slop[fmt.Sprintf("%d TODOs or placeholders", todos)] += min(4, 2*n)
	}
	if n := over(ais, 0, 2500); n > 0 {
		slop[fmt.Sprintf("%d comments read like a chatbot wrote them", ais)] += min(4, n)
	}
	if emoji > 0 {
		slop["emoji in comments"] += 2
	}
	if n := over(banners, 4, 1000); n > 0 {
		slop[fmt.Sprintf("%d banner comments", banners)] += min(3, n)
	}
	if over(dead, 8, 500) > 0 {
		slop[fmt.Sprintf("%d lines of commented-out code", dead)] += 2
	}
	if n := over(literals, 6, 400); n > 0 {
		slop[fmt.Sprintf("%d literal colours instead of the theme's", literals)] += min(4, n)
	}
	if lines > 60 && comments*100/lines > 45 {
		slop[fmt.Sprintf("%d%% of the lines are comments", comments*100/lines)] += 2
	}
	copied, total := 0, 0
	for _, n := range windows {
		total += n
		copied += n - 1
	}
	if total > 0 && copied >= 10 && copied*100/total >= 12 {
		slop[fmt.Sprintf("%d%% of the code is copied blocks", copied*100/total)] += min(4, copied*25/total)
	}
	if len(giant) > 0 {
		sort.Strings(giant)
		slop["too big to review: "+strings.Join(giant, ", ")] += min(4, 2*len(giant))
	}
	listing := text(hub, "summary") + " " + text(hub, "description")
	if ms := marketRE.FindAllString(listing, -1); len(ms) > 0 {
		slop["marketing words: "+strings.ToLower(strings.Join(ms, ", "))] += len(ms)
	}
	if listEmojiR.MatchString(listing) {
		slop["emoji in the listing"]++
	}
	for why, n := range slop {
		card.Slop += n
		card.SlopWhy = append(card.SlopWhy, fmt.Sprintf("%s (+%d)", why, n))
	}
	sort.Strings(card.SlopWhy)
	card.Reviewed = r.reviewed()[e.id+"@"+text(hub, "version")]
	why := strings.Join(card.SlopWhy, "; ")
	switch {
	case card.Slop >= slopError && !card.Reviewed:
		r.errorf(folder, "reads as generated without review (score %d): %s. Clean it up, or a maintainer lists %s@%s in reviewed.json",
			card.Slop, why, e.id, text(hub, "version"))
	case card.Slop >= slopWarn:
		r.warnf(folder, "quality score %d: %s", card.Slop, why)
	}
	return found
}

func (r *Repo) scanNever(p string, b []byte) {
	for _, f := range never {
		if loc := f.re.FindIndex(b); loc != nil {
			r.errorf(p, "not accepted: %s (line %d)", f.why, bytes.Count(b[:loc[0]], []byte("\n"))+1)
		}
	}
	for _, m := range bareIP.FindAllSubmatchIndex(b, -1) {
		if ip := net.ParseIP(string(b[m[2]:m[3]])); ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast() {
			r.errorf(p, "not accepted: talks to a bare IP address (line %d)", bytes.Count(b[:m[0]], []byte("\n"))+1)
			break
		}
	}
	if loc := invisible.FindIndex(b); loc != nil {
		r.errorf(p, "not accepted: invisible or direction-changing characters (line %d)", bytes.Count(b[:loc[0]], []byte("\n"))+1)
	}
}

var (
	commandRE = regexp.MustCompile(`(?:command\s*:\s*\[|execDetached\s*\(\s*\[|exec\s*\(\s*\[)\s*["']([^"'\s]+)`)
	pathRE    = regexp.MustCompile(`(?:~|\$HOME|StandardPaths\.\w+|/etc|/usr|/var|/tmp|/proc|/sys)/[A-Za-z0-9._\-/]*`)
)

func hostListed(h string, listed map[string]bool) bool {
	for l := range listed {
		if h == l || strings.HasSuffix(h, "."+l) {
			return true
		}
	}
	return false
}

// Links a person reads (a README, a license, a source page) are not reached by the code.
func docHost(h string) bool {
	for _, d := range []string{"github.com", "gitlab.com", "codeberg.org", "inir.space", "www.gnu.org", "opensource.org",
		"creativecommons.org", "spdx.org", "www.w3.org", "w3.org", "qt.io", "doc.qt.io"} {
		if h == d {
			return true
		}
	}
	return false
}

// The person's own machine and network, and documentation's example names: nothing leaves the house.
func localHost(h string) bool {
	if ip := net.ParseIP(h); ip != nil {
		return ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast()
	}
	return h == "localhost" || strings.HasSuffix(h, ".local") || h == "example.com" || strings.HasSuffix(h, ".example.com")
}

// Files git ignores in the hub (the maintainers' private notes linked in, build leftovers) never ship.
func (r *Repo) ignored() map[string]bool {
	if r.ignoredFiles == nil {
		r.ignoredFiles = map[string]bool{}
		for _, f := range strings.Split(r.git("ls-files", "-z", "--others", "--ignored", "--exclude-standard"), "\x00") {
			if f != "" {
				r.ignoredFiles[f] = true
			}
		}
	}
	return r.ignoredFiles
}

func isBinary(b []byte) bool {
	n := min(len(b), 8000)
	return bytes.IndexByte(b[:n], 0) >= 0
}

// Audit validates items and hands back each one's card with the problems found in it.
func (r *Repo) Audit(paths []string) []*Card {
	var out []*Card
	for _, e := range r.Validate(paths) {
		if e.card == nil {
			continue
		}
		c := e.card
		c.Version = text(e.item, "version")
		rel := r.rel(e.folder)
		for _, s := range r.Errors {
			if strings.HasPrefix(s, rel+"/") || strings.HasPrefix(s, rel+":") {
				c.Errors = append(c.Errors, s)
			}
		}
		for _, s := range r.Warnings {
			if strings.HasPrefix(s, rel+"/") || strings.HasPrefix(s, rel+":") {
				c.Warnings = append(c.Warnings, s)
			}
		}
		out = append(out, c)
	}
	return out
}

func (c *Card) verdict() string {
	switch {
	case len(c.Errors) > 0:
		return fmt.Sprintf("refused, %d problem(s)", len(c.Errors))
	case c.Slop >= slopWarn:
		return fmt.Sprintf("passes, quality score %d", c.Slop)
	default:
		return "passes"
	}
}

func (c *Card) rows() [][2]string {
	var may []string
	for _, p := range c.Permissions {
		may = append(may, strings.ToLower(Permissions[p]))
	}
	quality := "nothing generated-looking"
	if len(c.SlopWhy) > 0 {
		quality = fmt.Sprintf("score %d: %s", c.Slop, strings.Join(c.SlopWhy, "; "))
	}
	if c.Reviewed {
		quality += " (reviewed)"
	}
	return [][2]string{
		{"may", orDefault(strings.Join(may, ", "), "nothing beyond drawing")},
		{"runs", orDefault(strings.Join(c.Commands, ", "), "no programs")},
		{"reaches", orDefault(strings.Join(c.Hosts, ", "), "no hosts")},
		{"touches", orDefault(strings.Join(c.Paths, ", "), "no paths")},
		{"size", fmt.Sprintf("%d files, %d lines of code", c.Files, c.Lines)},
		{"quality", quality},
	}
}

// Text is the card as a reviewer reads it in a terminal.
func (c *Card) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s (%s): %s\n", c.ID, c.Version, c.Kind, c.verdict())
	for _, row := range c.rows() {
		fmt.Fprintf(&b, "  %-8s %s\n", row[0], row[1])
	}
	for _, s := range c.Errors {
		fmt.Fprintf(&b, "  error    %s\n", s)
	}
	for _, s := range c.Warnings {
		fmt.Fprintf(&b, "  warning  %s\n", s)
	}
	return b.String()
}

// Markdown is the card for a pull request's job summary.
func (c *Card) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "### `%s` %s (%s): %s\n\n| | |\n|---|---|\n", c.ID, c.Version, c.Kind, c.verdict())
	for _, row := range c.rows() {
		fmt.Fprintf(&b, "| %s | %s |\n", row[0], strings.ReplaceAll(row[1], "|", "\\|"))
	}
	for _, s := range c.Errors {
		fmt.Fprintf(&b, "\n- **error** %s", s)
	}
	for _, s := range c.Warnings {
		fmt.Fprintf(&b, "\n- warning %s", s)
	}
	b.WriteString("\n\n")
	return b.String()
}

var fileViewRE, folderModelRE = regexp.MustCompile(`\bFileView\s*\{`), regexp.MustCompile(`\bFolderListModel\s*\{`)

// ownOptionsOnly: every FileView in the file reads the item's own options file (Directories.pluginFile("<id>")), the
// lane the base gives each plugin; that is no file outside its folder to declare.
// A script writes files with its own language's words, which the QML detector never sees: a shell redirect to a file,
// cp, mv, rm, ln, tee, touch, mkdir, rsync or sed -i; Python's open() for writing, write_text, os and shutil.
var (
	shRedirectRE = regexp.MustCompile(`(?:^|[^0-9&<>=\-])>>?\s*([^\s;|&()<>]+)`)
	shWriteRE    = regexp.MustCompile(`(?:^|[\s;|&(]|\$\()(?:cp|mv|rm|ln|tee|touch|mkdir|rsync|truncate)\s|\bsed\s+(?:-[a-zA-Z]*i|--in-place)`)
	pyWriteRE    = regexp.MustCompile(`\bopen\([^)]*,\s*(?:mode\s*=\s*)?['"][^'"]*[wax+]|\.write_(?:text|bytes)\(|\bos\.(?:remove|unlink|rename|replace|makedirs|mkdir|rmdir)\(|\bshutil\.\w+\(|\.(?:unlink|mkdir|rename|touch)\(`)
)

func scriptWrites(ext string, b []byte) bool {
	for _, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "#") {
			continue
		}
		if ext == ".py" {
			if pyWriteRE.MatchString(t) {
				return true
			}
			continue
		}
		if shWriteRE.MatchString(t) {
			return true
		}
		for _, m := range shRedirectRE.FindAllStringSubmatch(t, -1) {
			if target := strings.Trim(m[1], `"'`); !strings.HasPrefix(target, "&") && target != "/dev/null" && target != "/dev/stderr" && target != "/dev/stdout" {
				return true
			}
		}
	}
	return false
}

func ownOptionsOnly(b []byte, id string) bool {
	own := regexp.MustCompile(`\bpath\s*:\s*Directories\.pluginFile\(\s*"` + regexp.QuoteMeta(id) + `"\s*\)`)
	views := len(fileViewRE.FindAll(b, -1))
	return views > 0 && views == len(own.FindAll(b, -1)) && !folderModelRE.Match(b)
}
