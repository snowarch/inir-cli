package doctor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strings"
	"time"
)

// Step is one numbered check of the bash doctor. Fixer marks the steps whose
// bash function repairs what it finds; the others only report.
type Step struct {
	N     int
	ID    string
	Title string
	Fixer bool
	check func(*checker, *step)
}

// Steps are run_doctor_with_fixes' 25 steps, in its order and with its titles.
var Steps = []Step{
	{1, "dependencies", "Checking dependencies", true, (*checker).dependencies},
	{2, "fonts", "Checking fonts", true, (*checker).fonts},
	{3, "repo", "Checking repo checkout", false, (*checker).repo},
	{4, "critical-files", "Checking critical files", false, (*checker).criticalFiles},
	{5, "script-permissions", "Checking script permissions", true, (*checker).scriptPermissions},
	{6, "launcher", "Checking launcher", true, (*checker).launcher},
	{7, "user-config", "Checking user config", false, (*checker).userConfig},
	{8, "state-directories", "Checking state directories", true, (*checker).stateDirectories},
	{9, "version-tracking", "Checking version tracking", true, (*checker).versionTracking},
	{10, "manifest", "Checking file manifest", true, (*checker).manifest},
	{11, "service", "Checking user service", true, (*checker).service},
	{12, "niri", "Checking Niri compositor", true, (*checker).niriRunning},
	{13, "python", "Checking Python packages", true, (*checker).python},
	{14, "stale-quickshell", "Checking stale local quickshell", false, (*checker).staleQuickshell},
	{15, "qt-abi", "Checking Quickshell/Qt ABI", true, (*checker).qtABI},
	{16, "polkit", "Checking password prompts", false, (*checker).polkit},
	{17, "quickshell", "Checking Quickshell", true, (*checker).quickshellLoads},
	{18, "theme-colors", "Checking theme colors", true, (*checker).themeColors},
	{19, "qt-theming", "Checking Qt theming", true, (*checker).qtTheming},
	{20, "conflicting-services", "Checking conflicting services", true, (*checker).conflictingServices},
	{21, "conflicting-shells", "Checking conflicting shells", false, (*checker).conflictingShells},
	{22, "wallpapers", "Checking wallpaper health", true, (*checker).wallpapers},
	{23, "environment", "Checking environment variables", true, (*checker).environment},
	{24, "niri-config", "Checking Niri config", false, (*checker).niriConfig},
	{25, "graphics", "Checking graphics renderer", false, (*checker).graphics},
}

type checker struct {
	e                 *Env
	offline           bool
	runtimeReported   bool
	jq                bool
	usableUserSystemd *bool
}

func (c *checker) systemd() bool {
	if c.usableUserSystemd == nil {
		v := c.e.usableSystemdUserManager()
		c.usableUserSystemd = &v
	}
	return *c.usableUserSystemd
}

// runtimeOr is doctor_runtime_dir_or_fail: the missing payload fails once.
func (c *checker) runtimeOr(s *step, name string) (string, bool) {
	if c.e.RuntimeDir != "" {
		return c.e.RuntimeDir, true
	}
	if !c.runtimeReported {
		s.fail("Runtime payload missing (run ./setup install)")
		c.runtimeReported = true
	}
	if name != "" {
		s.info(name + " skipped (runtime payload missing)")
	}
	return "", false
}

// repoRoot is doctor_repo_root.
func (c *checker) repoRoot() string {
	if isFile(filepath.Join(c.e.RepoRoot, "shell.qml")) {
		return c.e.RepoRoot
	}
	return ""
}

// --- 1 ------------------------------------------------------------------------

type dep struct{ cmd, name string }

// Dependencies is check_dependencies' command list, in its order.
var Dependencies = []dep{
	{"qs", "Quickshell"}, {"niri", "Niri"}, {"nmcli", "NetworkManager"}, {"wpctl", "WirePlumber"},
	{"pw-dump", "PipeWire tools"},
	{"jq", "jq"}, {"rsync", "rsync"}, {"curl", "curl"}, {"git", "git"}, {"python3", "python3"},
	{"fish", "fish"}, {"magick", "ImageMagick"}, {"grim", "grim"}, {"cliphist", "cliphist"},
	{"wl-copy", "wl-clipboard"}, {"wl-paste", "wl-clipboard"}, {"awww", "awww"}, {"awww-daemon", "awww"},
	{"hyprpicker", "hyprpicker"}, {"playerctl", "playerctl"}, {"notify-send", "libnotify"},
	{"flock", "util-linux"}, {"wlsunset", "wlsunset"}, {"easyeffects", "EasyEffects"}, {"uv", "uv"},
	{"cava", "cava"}, {"qalc", "qalculate"}, {"yt-dlp", "yt-dlp"}, {"socat", "socat"},
	{"brightnessctl", "brightnessctl"}, {"slurp", "slurp"}, {"wf-recorder", "wf-recorder"},
	{"ffmpeg", "ffmpeg"}, {"swappy", "swappy"}, {"tesseract", "tesseract"},
	{"blueman-manager", "Blueman"}, {"gowall", "gowall"}, {"kwriteconfig6", "KConfig"},
	{"ddcutil", "ddcutil"}, {"missioncenter", "mission-center"},
	{"nm-connection-editor", "nm-connection-editor"}, {"xdg-settings", "xdg-utils"}, {"mpv", "mpv"},
	{"swaylock", "swaylock"}, {"swayidle", "swayidle"}, {"songrec", "SongRec"},
	{"trans", "translate-shell"},
}

var ocrModels = []struct{ lang, id, name string }{
	{"eng", "ocr-eng", "OCR English data"},
	{"spa", "ocr-spa", "OCR Spanish data"},
	{"rus", "ocr-rus", "OCR Russian data"},
	{"jpn", "ocr-jpn", "OCR Japanese data"},
	{"jpn_vert", "ocr-jpn-vert", "OCR Japanese vertical data"},
	{"chi_sim", "ocr-chi-sim", "OCR Simplified Chinese data"},
	{"chi_sim_vert", "ocr-chi-sim-vert", "OCR Simplified Chinese vertical data"},
	{"chi_tra", "ocr-chi-tra", "OCR Traditional Chinese data"},
	{"chi_tra_vert", "ocr-chi-tra-vert", "OCR Traditional Chinese vertical data"},
}

func (c *checker) dependencies(s *step) {
	cmds := append([]dep{}, Dependencies...)
	switch c.e.OS {
	case "arch":
		cmds = append(cmds, dep{"checkupdates", "pacman-contrib"})
	case "void":
		cmds = append(cmds, dep{"ydotool", "ydotool"}, dep{"secret-tool", "libsecret"},
			dep{"gnome-keyring-daemon", "gnome-keyring"}, dep{"powerprofilesctl", "power-profiles-daemon"})
	}
	// the login starts the shell through niri-session and niri.service; Void starts niri itself
	if c.e.OS != "void" {
		cmds = append(cmds, dep{"niri-session", "Niri session"})
	}
	var names, ids []string
	missing := func(name, id string) { names = append(names, name); ids = append(ids, id) }
	for _, d := range cmds {
		if lookPath(d.cmd) == "" {
			missing(d.name, d.cmd)
		}
	}

	if c.e.OS == "void" {
		webengine, layershell := false, false
		for _, root := range []string{"/usr/lib/qt6/qml", "/usr/lib64/qt6/qml", "/usr/local/lib/qt6/qml", "/usr/local/lib64/qt6/qml"} {
			webengine = webengine || isFile(filepath.Join(root, "QtWebEngine", "qmldir"))
			layershell = layershell || isFile(filepath.Join(root, "org", "kde", "layershell", "qmldir"))
		}
		if !webengine {
			missing("Qt WebEngine QML", "qt-webengine")
		}
		if !layershell {
			missing("LayerShellQt QML", "layer-shell-qt")
		}
		kcm := false
		for _, root := range append([]string{qtPluginDir("qtpaths6")}, "/usr/lib64/qt6/plugins", "/usr/lib/qt6/plugins", "/usr/lib/x86_64-linux-gnu/qt6/plugins") {
			if root != "" && isFile(filepath.Join(root, "org.kde.kdecoration3.kcm", "kcm_darklydecoration.so")) {
				kcm = true
				break
			}
		}
		if !kcm {
			missing("Darkly settings KCM", "darkly")
		}
		want := ""
		if data, err := os.ReadFile(filepath.Join(c.e.RepoRoot, "sdata", "dist-void", "install-deps.sh")); err == nil {
			if m := regexp.MustCompile(`(?m)^YDOTOOL_VERSION="([^"]*)"`).FindSubmatch(data); m != nil {
				want = string(m[1])
			}
		}
		have, _ := stdout(10*time.Second, "ydotoold", "--version")
		have = strings.TrimRight(have, "\n")
		if want != "" && have != "v"+want && !contains(ids, "ydotool") {
			missing("ydotool v"+want, "ydotool")
		}
	}

	lv2 := false
	for _, dir := range []string{"/usr/lib/lv2", "/usr/lib64/lv2", "/usr/local/lib/lv2", "/usr/local/lib64/lv2"} {
		if globAny(filepath.Join(dir, "lsp-plugins*.lv2")) || globAny(filepath.Join(dir, "lsp*.lv2")) {
			lv2 = true
			break
		}
	}
	if !lv2 && lookPath("easyeffects") == "" && lookPath("flatpak") != "" {
		if _, code := run(10*time.Second, "flatpak", "info", "com.github.wwmm.easyeffects"); code == 0 {
			lv2 = true
		}
	}
	if !lv2 {
		missing("Linux Studio Plugins LV2", "lsp-plugins-lv2")
	}

	// Not commands either: the platform theme Qt apps take the colors through, and a color emoji font.
	if !anyFile(pluginDirs("platformthemes"), "KDEPlasmaPlatformTheme6.so") {
		missing("plasma-integration", "plasma-integration")
	}
	if lookPath("fc-list") != "" {
		if out, _ := run(10*time.Second, "fc-list", ":", "family"); !strings.Contains(strings.ToLower(out), "noto color emoji") {
			missing("Noto Color Emoji", "font-noto-color-emoji")
		}
	}

	langs := map[string]bool{}
	if lookPath("tesseract") != "" {
		out, _ := stdout(10*time.Second, "tesseract", "--list-langs")
		for i, l := range lines(out) {
			if i > 0 {
				langs[l] = true
			}
		}
		matches, _ := filepath.Glob(filepath.Join(c.e.DataHome, "inir", "tessdata", "*.traineddata"))
		for _, m := range matches {
			langs[strings.TrimSuffix(filepath.Base(m), ".traineddata")] = true
		}
	}
	for _, m := range ocrModels {
		if !langs[m.lang] {
			missing(m.name, m.id)
		}
	}

	if len(names) == 0 {
		s.pass("All dependencies available")
		return
	}
	s.missing = ids
	text := "Missing: " + strings.Join(names, " ")
	switch c.e.OS {
	case "arch", "fedora", "debian", "ubuntu", "void":
		s.fixable(text, "All dependencies available")
	default:
		s.fail(text)
	}
	switch c.e.OS {
	case "arch":
		s.hint("Run: ./setup install (installs the matching Arch packages)")
	case "fedora":
		s.hint("Run: sudo dnf install ... (see ./setup install)")
	case "debian", "ubuntu":
		s.hint("Run: sudo apt install ... (see ./setup install)")
	case "void":
		s.hint("Run: ./setup install (repairs the matching XBPS providers)")
	default:
		s.hint("Install these tools using your package manager")
		s.info("Automatic install not available for " + c.e.OS + ". Install manually.")
	}
}

// --- 2 ------------------------------------------------------------------------

type font struct{ pattern, display string }

var (
	criticalFonts  = []font{{"Material Symbols Rounded", "Material Symbols Rounded"}, {"JetBrainsMono Nerd", "JetBrainsMono Nerd Font"}}
	importantFonts = []font{{"Roboto Flex", "Roboto Flex"}, {"Gabarito", "Gabarito"}, {"Oxanium", "Oxanium"}, {"Noto Color Emoji", "Noto Color Emoji"}}
	optionalFonts  = []font{{"Rubik", "Rubik"}, {"Space Grotesk", "Space Grotesk"}, {"Readex Pro", "Readex Pro"}, {"Twemoji", "Twemoji"}}
	// Fonts _try_install_font_package can fetch without an AUR helper.
	downloadableFonts = map[string]bool{"Material Symbols Rounded": true, "JetBrainsMono Nerd Font": true,
		"Roboto Flex": true, "Gabarito": true, "Oxanium": true}
)

func (c *checker) fonts(s *step) {
	if lookPath("fc-list") == "" {
		s.fail("fontconfig not installed (cannot verify fonts)")
		return
	}
	cache, _ := stdout(30*time.Second, "fc-list", ":", "family")
	installed := func(pattern string) bool {
		re, err := regexp.Compile("(?i)" + bre(pattern))
		return err == nil && re.MatchString(cache)
	}
	var crit, imp, opt, configured []string
	for _, f := range criticalFonts {
		if !installed(f.pattern) {
			crit = append(crit, f.display)
		}
	}
	for _, f := range importantFonts {
		if !installed(f.pattern) {
			imp = append(imp, f.display)
		}
	}
	for _, f := range optionalFonts {
		if !installed(f.pattern) {
			opt = append(opt, f.display)
		}
	}
	if cfg := filepath.Join(c.e.ConfDir, "config.json"); isFile(cfg) && c.jq {
		for _, f := range configuredFonts(cfg, c.e.RuntimeDir) {
			switch strings.ToLower(f) {
			case "sans", "sans-serif", "serif", "monospace", "system-ui", "auto":
				continue
			}
			if !installed(f) {
				configured = append(configured, f)
			}
		}
	}
	total := len(crit) + len(imp) + len(configured)
	if total == 0 && len(opt) == 0 {
		s.pass("All fonts installed")
		return
	}
	if total == 0 {
		s.warn("Optional fonts missing: " + strings.Join(opt, " "))
		s.pass("Required and configured fonts OK")
		return
	}
	helper := c.e.OS == "arch" && (lookPath("yay") != "" || lookPath("paru") != "")
	canFix := func(name string) bool { return helper || downloadableFonts[name] }
	report := func(names []string, label, hint string) {
		var fix, stuck []string
		for _, n := range names {
			if canFix(n) {
				fix = append(fix, n)
			} else {
				stuck = append(stuck, n)
			}
		}
		if len(fix) > 0 {
			s.fixable(label+strings.Join(fix, " "), fmt.Sprintf("Installed %d font(s)", len(fix)))
		}
		if len(stuck) > 0 {
			s.fail(label + strings.Join(stuck, " "))
			s.hint(hint)
		}
	}
	report(crit, "CRITICAL fonts missing: ", "Shell icons will be broken without these")
	report(imp, "Important fonts missing: ", "Install manually or run: ./setup install")
	// A configured family the doctor installs anyway (it is one of the above) comes back with it.
	missingNow := map[string]bool{}
	for _, n := range append(append([]string{}, crit...), imp...) {
		missingNow[n] = canFix(n)
	}
	var stuck []string
	for _, f := range configured {
		if !missingNow[f] {
			stuck = append(stuck, f)
		}
	}
	if len(stuck) > 0 {
		s.fail("Configured fonts unavailable: " + strings.Join(stuck, " "))
		s.hint("Choose an installed family or install the selected font")
	}
	if len(opt) > 0 {
		s.warn("Optional fonts missing: " + strings.Join(opt, " "))
	}
}

// configuredFonts is the jq query of check_fonts, sorted and unique: Material's fonts from
// the base config, every other family's from the key its plugin.json names (settings.font),
// or the font it falls back to when that key is unset.
func configuredFonts(path, runtime string) []string {
	var cfg map[string]any
	if !readJSON(path, &cfg) {
		return nil
	}
	type font struct {
		Key      string
		Fallback any
	}
	keys := []font{{Key: "appearance.typography.mainFont"}, {Key: "appearance.typography.titleFont"}, {Key: "appearance.typography.monospaceFont"}}
	manifests, _ := filepath.Glob(filepath.Join(runtime, "modules", "*", "plugin.json"))
	for _, m := range manifests {
		var p struct {
			Settings struct {
				Font font `json:"font"`
			} `json:"settings"`
		}
		if runtime != "" && readJSON(m, &p) && p.Settings.Font.Key != "" {
			keys = append(keys, p.Settings.Font)
		}
	}
	set := map[string]bool{}
	for _, k := range keys {
		var v any = cfg
		for _, part := range strings.Split(k.Key, ".") {
			m, _ := v.(map[string]any)
			v = m[part]
		}
		if s, ok := v.(string); !ok || s == "" {
			v = k.Fallback
		}
		if s, ok := v.(string); ok && s != "" {
			set[s] = true
		}
	}
	var out []string
	for f := range set {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// bre turns a grep basic regular expression into Go syntax for the plain
// names doctor matches: only `.`, `*`, `^`, `$` and brackets keep a meaning.
func bre(p string) string {
	var b strings.Builder
	for _, r := range p {
		switch r {
		case '+', '?', '(', ')', '{', '}', '|':
			b.WriteString(`\` + string(r))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// --- 3 ------------------------------------------------------------------------

func (c *checker) repo(s *step) {
	if c.e.Version().UpdateStrategy() == "package-manager" {
		s.pass("Repo checkout state not required for package-managed installs")
		return
	}
	root := c.e.RepoRoot
	if !exists(filepath.Join(root, ".git")) {
		s.fail("Repo checkout is missing git metadata")
		s.hint("Run setup from a real iNiR checkout, not a random copy")
		return
	}
	branch, code := stdout(10*time.Second, "git", "-C", root, "rev-parse", "--abbrev-ref", "HEAD")
	branch = strings.TrimSpace(branch)
	if code != 0 || branch == "" {
		branch = "unknown"
	}
	tracked := branch
	if branch == "HEAD" || branch == "unknown" {
		tracked = "main"
	}
	if branch == "HEAD" {
		s.fail("Repo checkout is detached HEAD")
		s.hint("setup update cannot pull automatically until you check out a branch")
		return
	}
	state := 2
	if !c.offline {
		state = remoteState(root, tracked)
	}
	switch state {
	case 0:
		s.pass("Repo checkout behind origin/" + tracked + " (update available)")
		s.hint("Run: ./setup update")
	case 1:
		s.pass("Repo checkout tracks origin/" + tracked)
	case 2:
		s.pass("Repo remote check skipped")
	case 3:
		s.pass("Repo checkout has local commits ahead of origin/" + tracked)
	case 4:
		s.fail("Repo checkout diverged from origin/" + tracked)
		s.hint("Run: inir update --realign")
	}
	if branch != "main" && branch != "master" {
		s.warn("Tracking non-release branch: " + branch)
	}
}

// remoteState is check_remote_updates (sdata/lib/snapshots.sh): 0 behind,
// 1 even, 2 could not check, 3 ahead, 4 diverged.
func remoteState(root, branch string) int {
	if !exists(filepath.Join(root, ".git")) {
		return 2
	}
	if _, code := run(60*time.Second, "git", "-C", root, "fetch", "origin", "--quiet"); code != 0 {
		return 2
	}
	out, code := stdout(10*time.Second, "git", "-C", root, "rev-list", "--left-right", "--count", "HEAD...origin/"+branch)
	if code != 0 {
		return 2
	}
	var local, remote int
	fmt.Sscan(out, &local, &remote)
	switch {
	case local == 0 && remote == 0:
		return 1
	case local == 0:
		return 0
	case remote == 0:
		return 3
	}
	return 4
}

// --- 4, 5 ---------------------------------------------------------------------

func (c *checker) criticalFiles(s *step) {
	target, ok := c.runtimeOr(s, "Critical files")
	if !ok {
		return
	}
	missing := 0
	for _, f := range []string{"shell.qml", "GlobalStates.qml", "modules/common/Config.qml", "services/NiriService.qml"} {
		if !isFile(filepath.Join(target, f)) {
			s.fail("Missing: " + f)
			missing++
		}
	}
	// families come from the hub: the one in use, and every plugin linked in, must reach its plugin.json
	family := "ii"
	if cfg := filepath.Join(c.e.ConfDir, "config.json"); isFile(cfg) {
		// no jq or a broken config: the family can't be told, so it isn't judged (like bash)
		family = ""
		var m struct {
			PanelFamily any `json:"panelFamily"`
		}
		if c.jq && readJSON(cfg, &m) {
			family = "ii"
			if s, ok := m.PanelFamily.(string); ok && s != "" {
				family = s
			}
		}
	}
	if family != "" && !isFile(filepath.Join(target, "modules", family, "plugin.json")) {
		s.fail("Desktop family not installed: " + family + " (run: inir hub install " + family + ")")
		missing++
	}
	modules, _ := os.ReadDir(filepath.Join(target, "modules"))
	for _, m := range modules {
		if _, err := os.Stat(filepath.Join(target, "modules", m.Name())); m.Type()&os.ModeSymlink != 0 && err != nil {
			s.fail("Plugin link broken: modules/" + m.Name() + " (run: inir hub install " + m.Name() + ")")
			missing++
		}
	}
	if missing == 0 {
		s.pass("Critical files present")
	}
}

func (c *checker) scriptPermissions(s *step) {
	target, ok := c.runtimeOr(s, "Script permissions")
	if !ok {
		return
	}
	scripts := filepath.Join(target, "scripts")
	if !isDir(scripts) {
		return
	}
	bad := 0
	filepath.WalkDir(scripts, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		script := strings.HasSuffix(name, ".sh") || strings.HasSuffix(name, ".fish") ||
			(strings.HasSuffix(name, ".py") && !matchGlob("test-*.py", name))
		if script && !accessX(p) {
			bad++
		}
		return nil
	})
	if bad > 0 {
		s.fixable(fmt.Sprintf("%d script(s) not executable", bad), fmt.Sprintf("Fixed permissions on %d script(s)", bad))
	} else {
		s.pass("Script permissions OK")
	}
}

// --- 6 ------------------------------------------------------------------------

func (c *checker) launcher(s *step) {
	strategy := c.e.Version().UpdateStrategy()
	cmd := lookPath("inir")
	expected := filepath.Join(c.e.BinHome, "inir")
	repoLauncher := filepath.Join(c.e.RepoRoot, "scripts", "inir")
	runtimeLauncher := filepath.Join(c.e.RuntimeDir, "scripts", "inir")

	if strategy == "package-manager" {
		if cmd != "" {
			s.pass("Launcher available")
		} else {
			s.fail("inir launcher not found in PATH")
			s.hint("Run the package install flow again, or install the launcher manually")
		}
		return
	}
	if !isFile(repoLauncher) {
		if cmd != "" || isExec(runtimeLauncher) {
			s.pass("Launcher available")
		} else {
			s.fail("Launcher missing")
		}
		return
	}
	refreshed := false
	if !isExec(expected) {
		s.fixable("Launcher missing: "+expected, "Installed launcher to "+expected)
		refreshed = true
	} else if !sameContent(repoLauncher, expected) {
		s.fixable("Launcher content differs from repo", "Refreshed launcher from repo")
		refreshed = true
	}
	if cmd == "" {
		s.fail("Launcher not in PATH")
		s.hint("Installed launcher exists at " + expected)
		return
	}
	real := readlinkF(cmd)
	if real == "" {
		real = cmd
	}
	// After the refresh the launcher on PATH is the repo's, when it is that one.
	if !(refreshed && real == readlinkF(expected)) && !sameContent(repoLauncher, real) {
		s.fail("PATH resolves an outdated launcher: " + cmd)
		s.hint("Expected launcher content from " + repoLauncher)
		return
	}
	if c.desktopIntegrationStale() {
		s.fixable("Desktop integration out of date", "Refreshed desktop integration")
	}
	s.pass("Launcher current")
}

// desktopIntegrationStale is whether sync_user_desktop_integration_from_repo
// (sdata/lib/functions.sh) would write the icon or a .desktop file.
func (c *checker) desktopIntegrationStale() bool {
	launcher := filepath.Join(c.e.BinHome, "inir")
	icon := filepath.Join(c.e.RepoRoot, "assets", "icons", "desktop-symbolic.svg")
	if isFile(icon) {
		target := filepath.Join(c.e.DataHome, "icons", "hicolor", "scalable", "apps", "inir.svg")
		if !isFile(target) || !sameContent(icon, target) {
			return true
		}
	}
	for _, name := range []string{"inir", "inir-settings"} {
		src := filepath.Join(c.e.RepoRoot, "assets", "applications", name+".desktop")
		data, err := os.ReadFile(src)
		if err != nil {
			continue
		}
		command := "service restart"
		if name == "inir-settings" {
			command = "settings"
		}
		want := regexp.MustCompile(`(?m)^Exec=.*$`).ReplaceAllLiteral(data, []byte("Exec="+launcher+" "+command))
		have, err := os.ReadFile(filepath.Join(c.e.DataHome, "applications", name+".desktop"))
		if err != nil || !bytes.Equal(want, have) {
			return true
		}
	}
	return false
}

// --- 7, 8 ---------------------------------------------------------------------

func (c *checker) userConfig(s *step) {
	cfg := filepath.Join(c.e.ConfDir, "config.json")
	if !isFile(cfg) {
		s.pass("User config (using defaults)")
		return
	}
	if c.jq && !jsonStream(cfg) {
		s.fail("Invalid JSON: " + cfg)
		s.hint("Backup and delete to reset")
		return
	}
	s.pass("User config valid")
}

// jsonStream is `jq empty`: zero or more JSON values and nothing else.
func jsonStream(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for {
		var v any
		if err := dec.Decode(&v); err == io.EOF {
			return true
		} else if err != nil {
			return false
		}
	}
}

func (c *checker) stateDirectories(s *step) {
	created := 0
	for _, d := range []string{filepath.Join(c.e.StateHome, "quickshell", "user"), filepath.Join(c.e.CacheHome, "quickshell"), c.e.ConfDir} {
		if !isDir(d) {
			created++
		}
	}
	if created > 0 {
		s.fixable(fmt.Sprintf("%d state directory(ies) missing", created), fmt.Sprintf("Created %d directory(ies)", created))
	} else {
		s.pass("State directories exist")
	}
}

// --- 9, 10 --------------------------------------------------------------------

// coreMetadata is version_file_has_core_metadata: jq reads the fields, and
// without jq two greps look for them.
func (c *checker) coreMetadata(path string) bool {
	if !c.jq {
		data, err := os.ReadFile(path)
		return err == nil && regexp.MustCompile(`"version"[[:space:]]*:[[:space:]]*"[^"]+"`).Match(data) &&
			regexp.MustCompile(`"commit"[[:space:]]*:[[:space:]]*"[^"]+"`).Match(data)
	}
	var m map[string]any
	return readJSON(path, &m) && firstString(m, "version") != "" && firstString(m, "commit") != ""
}

func (c *checker) versionTracking(s *step) {
	file := filepath.Join(c.e.ConfDir, "version.json")
	legacy := filepath.Join(c.e.ConfigHome, "illogical-impulse", "version.json")
	marker := filepath.Join(c.e.ConfDir, "installed_true")
	runtimeFile := ""
	if c.e.RuntimeDir != "" {
		runtimeFile = filepath.Join(c.e.RuntimeDir, "version.json")
	}
	effective := file
	if !isFile(file) && isFile(legacy) {
		s.fixable("Version tracking only in the legacy config directory", "Migrated version tracking to active config directory")
		effective = legacy
	}
	repair := ""
	switch {
	case isFile(marker) && isFile(effective) && !c.coreMetadata(effective):
		repair = "incomplete"
	case isFile(marker) && !isFile(effective):
		repair = "missing"
	}
	switch {
	case repair == "":
		s.pass("Version tracking OK")
	case isFile(runtimeFile) && c.coreMetadata(runtimeFile):
		if repair == "incomplete" {
			s.fixable("Version tracking incomplete", "Repaired version tracking from runtime metadata")
		} else {
			s.fixable("Version tracking missing", "Created version tracking from runtime metadata")
		}
	case repair == "incomplete":
		s.fixable("Version tracking incomplete", "Repaired incomplete version tracking")
	default:
		s.fixable("Version tracking missing", "Created version tracking")
	}
}

func (c *checker) manifest(s *step) {
	target, ok := c.runtimeOr(s, "File manifest")
	if !ok {
		return
	}
	if c.e.Version().UpdateStrategy() == "package-manager" {
		s.pass("File manifest not required for externally managed install")
		return
	}
	if isFile(filepath.Join(c.e.ConfDir, "installed_true")) && !isFile(filepath.Join(target, ".inir-manifest")) {
		s.fixable("File manifest missing", "Created file manifest")
		return
	}
	s.pass("File manifest OK")
}

// --- 12 -----------------------------------------------------------------------

func (c *checker) niriRunning(s *step) {
	if c.systemd() {
		if _, code := run(5*time.Second, "systemctl", "--user", "is-active", "--quiet", "niri.service"); code == 0 {
			sock, wayland, ok := c.niriServiceEnvironment()
			if !ok {
				s.fail("niri.service is running but its session sockets could not be verified")
				return
			}
			env, _ := stdout(5*time.Second, "systemctl", "--user", "show-environment")
			if envValue(env, "NIRI_SOCKET") != sock || envValue(env, "WAYLAND_DISPLAY") != wayland {
				s.fixable("Niri session environment is missing from the user manager", "Repaired Niri session environment in the user manager")
				return
			}
			s.pass("Niri compositor running")
			return
		}
	}
	if sock := c.e.Getenv("NIRI_SOCKET"); sock != "" && isSocket(sock) {
		s.pass("Niri compositor running")
	} else {
		s.fail("Niri not detected (run inside Niri session)")
	}
}

// niriServiceEnvironment is inir_resolve_niri_service_environment
// (scripts/lib/niri-session-env.sh): the one niri socket of niri.service's
// main process and the Wayland display next to it.
func (c *checker) niriServiceEnvironment() (string, string, bool) {
	pid, _ := stdout(5*time.Second, "systemctl", "--user", "show", "-p", "MainPID", "--value", "niri.service")
	pid = strings.TrimSpace(pid)
	if !regexp.MustCompile(`^[1-9][0-9]*$`).MatchString(pid) {
		return "", "", false
	}
	matches, _ := filepath.Glob(filepath.Join(c.e.RuntimeHome, "niri.*."+pid+".sock"))
	found, sock, wayland := 0, "", ""
	for _, m := range matches {
		if !isSocket(m) {
			continue
		}
		display := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), "niri."), "."+pid+".sock")
		if !strings.HasPrefix(display, "wayland-") || !isSocket(filepath.Join(c.e.RuntimeHome, display)) {
			continue
		}
		sock, wayland = m, display
		found++
	}
	return sock, wayland, found == 1
}

func envValue(env, key string) string {
	for _, l := range lines(env) {
		if strings.HasPrefix(l, key+"=") {
			return strings.TrimPrefix(l, key+"=")
		}
	}
	return ""
}

// --- 13 -----------------------------------------------------------------------

func (c *checker) python(s *step) {
	venv := filepath.Join(c.e.StateHome, "quickshell", ".venv")
	req := ""
	if c.e.RuntimeDir != "" && isFile(filepath.Join(c.e.RuntimeDir, "sdata", "uv", "requirements.txt")) {
		req = filepath.Join(c.e.RuntimeDir, "sdata", "uv", "requirements.txt")
	} else if root := c.repoRoot(); root != "" && isFile(filepath.Join(root, "sdata", "uv", "requirements.txt")) {
		req = filepath.Join(root, "sdata", "uv", "requirements.txt")
	} else {
		c.runtimeOr(s, "Python packages")
		s.pass("Python (no requirements.txt)")
		return
	}
	uv := lookPath("uv") != ""
	fresh := false
	if isDir(filepath.Join(venv, "bin")) {
		if _, code := run(10*time.Second, filepath.Join(venv, "bin", "python"), "--version"); code != 0 {
			s.fail("Broken Python venv detected")
			fresh = true
		}
	}
	if fresh || !isDir(venv) {
		if !uv {
			s.fail("Python venv missing (install uv)")
			return
		}
		s.fixable("Python venv missing", "Created Python venv")
		fresh = true
	}
	if !fresh && !c.accelHooksOK() {
		s.fixable("inir-accel hooks do not match what is installed", "inir-accel hooks match what is installed again")
	}
	switch {
	case !uv:
		s.fail("uv not installed, cannot check Python packages")
	case fresh:
		s.fixable("Python packages below requirements", "Python packages brought up to requirements")
	default:
		plan, _ := runEnv(60*time.Second, []string{"VIRTUAL_ENV=" + venv}, "uv", "pip", "install", "--dry-run", "--offline", "-r", req)
		if strings.Contains(plan, "Would make no changes") {
			s.pass("Python packages OK")
		} else {
			s.fixable("Python packages below requirements", "Python packages brought up to requirements")
		}
	}
	if denoCompatible(lookPath("deno")) {
		s.pass("YT Music JS runtime OK")
	} else {
		s.fixable("YT Music JS runtime missing or older than Deno 2.3", "Installed current Deno runtime for YT Music")
	}
}

// denoCompatible is ytmusic-deno-compatible: Deno 2.3.0 or newer.
func denoCompatible(bin string) bool {
	if bin == "" || !accessX(bin) {
		return false
	}
	out, _ := stdout(10*time.Second, bin, "--version")
	ls := lines(out)
	if len(ls) == 0 {
		return false
	}
	f := strings.Fields(ls[0])
	if len(f) < 2 {
		return false
	}
	var v [3]int
	for i, p := range strings.SplitN(f[1], ".", 4) {
		if i >= 3 {
			break
		}
		n := 0
		if _, err := fmt.Sscanf(p, "%d", &n); err != nil || fmt.Sprint(n) != p {
			return false
		}
		v[i] = n
	}
	return v[0] > 2 || (v[0] == 2 && v[1] >= 3)
}

// --- 14, 15, 16, 17 -----------------------------------------------------------

var qsVersion = regexp.MustCompile(`\b0\.\d+\.\d+\b`)

func stringsFirst(bin string, re *regexp.Regexp, whole bool) string {
	out, _ := stdout(30*time.Second, "strings", bin)
	for _, l := range lines(out) {
		if whole {
			if re.MatchString(l) {
				return l
			}
		} else if m := re.FindString(l); m != "" {
			return m
		}
	}
	return ""
}

func (c *checker) staleQuickshell(s *step) {
	qs := lookPath("qs")
	if qs == "" {
		s.pass("No stale local quickshell")
		return
	}
	real := readlinkF(qs)
	if real == "" {
		real = qs
	}
	if !strings.HasPrefix(real, c.e.Home+"/.local/bin/") && !strings.HasPrefix(real, "/usr/local/bin/") {
		s.pass("No stale local quickshell")
		return
	}
	system := ""
	if isExec("/usr/bin/qs") {
		system = "/usr/bin/qs"
	} else if isExec("/usr/bin/quickshell") {
		system = "/usr/bin/quickshell"
	}
	if system == "" {
		s.pass("No stale local quickshell")
		return
	}
	if lookPath("strings") != "" {
		local, sys := stringsFirst(real, qsVersion, false), stringsFirst(system, qsVersion, false)
		if local != "" && sys != "" && local != sys {
			s.fail("Stale local quickshell shadows system package")
			s.warn("Local:  " + real + " (" + local + ")")
			s.warn("System: " + system + " (" + sys + ")")
			s.hint("Fix: rm " + real)
			if isSymlink(qs) && qs != real {
				s.hint("Also: rm " + qs)
			}
			return
		}
	}
	s.pass("No stale local quickshell")
}

var (
	abiMismatch = regexp.MustCompile(`(?i)built against Qt|Qt.*mismatch|incompatible Qt`)
	qtVersion   = regexp.MustCompile(`^6\.\d+\.\d+$`)
	tripleEnd   = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+$`)
)

// ABIMismatch is _doctor_abi_detect: a message when Quickshell was built for
// another Qt than the one installed, "" when they match or qs is missing.
func ABIMismatch() string {
	qs := lookPath("qs")
	if qs == "" {
		return ""
	}
	env := []string{"QT_QPA_PLATFORM=offscreen"}
	out, _ := runEnv(5*time.Second, env, "qs", "--version")
	for _, l := range lines(out) {
		if abiMismatch.MatchString(l) {
			return l
		}
	}
	build := ""
	if lookPath("strings") != "" {
		build = stringsFirst(qs, qtVersion, true)
	}
	runtime := ""
	if isSymlink("/usr/lib/libQt6Core.so.6") {
		runtime = tripleEnd.FindString(readlinkF("/usr/lib/libQt6Core.so.6"))
	}
	if runtime == "" && lookPath("pkg-config") != "" {
		v, _ := stdout(10*time.Second, "pkg-config", "--modversion", "Qt6Core")
		runtime = strings.TrimSpace(v)
	}
	if build != "" && runtime != "" && build != runtime {
		return "Quickshell built against Qt " + build + " but system has Qt " + runtime
	}
	return ""
}

// _PREBUILT_GO in sdata/lib/prebuilt.sh: the Go helpers inir-accel answers for
var accelGo = []string{"inir-washi", "inir-opencode-themegen", "inir-vscode-themegen"}

// accelHooksOK is prebuilt_accel_hooks_ok: the hooks point at accel/current while it is
// there, and none is left pointing at it once it went.
func (c *checker) accelHooksOK() bool {
	bin := filepath.Join(c.e.DataHome, "inir", "accel", "current", "inir-accel")
	state := filepath.Join(c.e.StateHome, "quickshell")
	hooks := []string{filepath.Join(state, ".venv", "bin", "python3")}
	for _, g := range accelGo {
		hooks = append(hooks, filepath.Join(state, "user", "generated", "bin", g))
	}
	if isExec(bin) {
		for i, h := range hooks {
			if i == 0 && !isDir(filepath.Dir(h)) {
				continue
			}
			if t, _ := os.Readlink(h); t != bin {
				return false
			}
		}
		return true
	}
	for _, h := range hooks {
		if t, _ := os.Readlink(h); strings.HasSuffix(t, "/accel/current/inir-accel") {
			return false
		}
	}
	return true
}

// pendingAddons is prebuilt_pending_addons: the titles of the add-ons this machine takes (their
// "when" config key is true) whose part is unpacked and they are not. An add-on has its part's version.
func (c *checker) pendingAddons() []string {
	if goruntime.GOARCH != "amd64" || (c.e.Getenv("INIR_PREBUILT") != "" && c.e.Getenv("INIR_PREBUILT") != "1") {
		return nil
	}
	var pins struct {
		Parts map[string]struct{ Dest, With, When, Title, Version, Sha256 string } `json:"parts"`
	}
	if c.repoRoot() == "" || !readJSON(filepath.Join(c.repoRoot(), "sdata", "prebuilt.json"), &pins) {
		return nil
	}
	var cfg map[string]any
	readJSON(filepath.Join(c.e.ConfDir, "config.json"), &cfg)
	var out []string
	for name, p := range pins.Parts {
		if p.With == "" || p.When == "" || p.Sha256 == "" {
			continue
		}
		var v any = cfg
		for _, k := range strings.Split(p.When, ".") {
			m, _ := v.(map[string]any)
			v = m[k]
		}
		dir := filepath.Join(c.e.DataHome, "inir", p.Dest, pins.Parts[p.With].Version)
		if v != true || !isFile(filepath.Join(dir, ".sha256")) {
			continue
		}
		if b, _ := os.ReadFile(filepath.Join(dir, "."+name+".sha256")); strings.TrimSpace(string(b)) == p.Sha256 {
			continue
		}
		if p.Title == "" {
			p.Title = name
		}
		out = append(out, p.Title)
	}
	sort.Strings(out)
	return out
}

// ownRuntime is runtime/current when setup put qs-rs there (sdata/lib/prebuilt.sh): the
// launcher starts it before any qs in PATH, and it carries its own Qt.
func (c *checker) ownRuntime() string {
	if p := filepath.Join(c.e.DataHome, "inir", "runtime", "current"); isSymlink(p) {
		return p
	}
	return ""
}

func (c *checker) qtABI(s *step) {
	if own := c.ownRuntime(); own != "" {
		ver, _ := os.Readlink(own)
		if _, code := runEnv(20*time.Second, []string{"QT_QPA_PLATFORM=offscreen"}, filepath.Join(own, "bin", "qs"), "--version"); code == 0 {
			s.pass("qs-rs " + ver + " runs on its own Qt")
			if addons := strings.Join(c.pendingAddons(), ", "); addons != "" {
				s.fixable("Missing: "+addons, "Got "+addons)
			}
			return
		}
		s.fixable("qs-rs "+ver+" does not start here",
			"qs-rs "+ver+" does not start here, so the shell runs on Quickshell; inir update tries qs-rs again")
		return
	}
	if lookPath("qs") == "" {
		return
	}
	if msg := ABIMismatch(); msg != "" {
		s.fail("Quickshell was built for another Qt version: " + msg)
		s.warn("Quickshell uses Qt internals and asks to be rebuilt after every Qt update. Until then it can crash.")
		s.hint("Fix: inir doctor --fix-abi")
		return
	}
	s.pass("Quickshell/Qt ABI compatible")
}

func (c *checker) polkit(s *step) {
	out, _ := stdout(5*time.Second, "pidof", "-s", "polkit-gnome-authentication-agent-1", "lxqt-policykit-agent",
		"polkit-kde-authentication-agent-1", "polkit-mate-authentication-agent-1", "lxpolkit")
	if pid := strings.TrimSpace(out); pid != "" {
		comm, _ := stdout(5*time.Second, "ps", "-o", "comm=", "-p", pid)
		s.pass("Password prompts: your own agent (" + strings.TrimSpace(comm) + ")")
		return
	}
	if cfg := filepath.Join(c.e.ConfDir, "config.json"); isFile(cfg) && c.jq {
		var m struct {
			Modules map[string]any `json:"modules"`
		}
		if readJSON(cfg, &m) {
			if v, ok := m.Modules["polkit"].(bool); ok && !v {
				s.pass("Password prompts: the shell's agent is off (modules.polkit)")
				return
			}
		}
	}
	if c.ownRuntime() != "" {
		s.pass("Password prompts: answered by the shell")
		return
	}
	qs := ""
	if p := lookPath("qs"); p != "" {
		qs = readlinkF(p)
	}
	if qs == "" {
		return
	}
	ldd, code := stdout(10*time.Second, "ldd", qs)
	if code != 0 {
		return
	}
	if strings.Contains(ldd, "libpolkit-agent-1") {
		s.pass("Password prompts: answered by the shell")
		return
	}
	s.fail("Quickshell was built without Polkit: apps asking for your password get no prompt")
	s.warn("Install a Quickshell built with Polkit (your distribution's package has it), or run an agent of your own.")
}

func (c *checker) quickshellLoads(s *step) {
	target, ok := c.runtimeOr(s, "Quickshell")
	if !ok {
		return
	}
	if r := readlinkF(target); r != "" {
		target = r
	}
	if c.e.Getenv("WAYLAND_DISPLAY") == "" && c.e.Getenv("DISPLAY") == "" && c.e.Getenv("NIRI_SOCKET") == "" {
		s.pass("Quickshell (skipped - no display)")
		return
	}
	qs := "qs"
	if own := c.ownRuntime(); own != "" && isExec(filepath.Join(own, "bin", "qs")) {
		qs = filepath.Join(own, "bin", "qs")
	}
	out, _ := stdout(10*time.Second, qs, "-p", target, "list")
	out = strings.TrimSpace(out)
	if out != "" && !strings.HasPrefix(out, "No running instances") {
		s.pass("Quickshell running")
		return
	}
	// The bash doctor starts it to see whether it loads; checking changes nothing.
	s.skip("Quickshell is not running; starting it is how the doctor sees whether it loads")
}

// --- 18 -----------------------------------------------------------------------

func (c *checker) themeColors(s *step) {
	colorsJSON := filepath.Join(c.e.StateHome, "quickshell", "user", "generated", "colors.json")
	colorsSCSS := filepath.Join(c.e.StateHome, "quickshell", "user", "generated", "material_colors.scss")
	darkly := filepath.Join(c.e.DataHome, "color-schemes", "Darkly.colors")
	scriptIn := func(rel string) string {
		if c.e.RuntimeDir != "" && isFile(filepath.Join(c.e.RuntimeDir, rel)) {
			return filepath.Join(c.e.RuntimeDir, rel)
		}
		if root := c.repoRoot(); root != "" && isFile(filepath.Join(root, rel)) {
			return filepath.Join(root, rel)
		}
		return ""
	}

	if isFile(colorsJSON) || isFile(colorsSCSS) {
		s.pass("Theme colors generated")
	} else {
		wallpaper, source := "", "configured wallpaper"
		if cfg := filepath.Join(c.e.ConfDir, "config.json"); isFile(cfg) && c.jq {
			var m struct {
				Background map[string]any `json:"background"`
			}
			if readJSON(cfg, &m) {
				wallpaper, _ = m.Background["wallpaperPath"].(string)
			}
		}
		if wallpaper == "" || !isFile(wallpaper) || !nonEmpty(wallpaper) {
			wallpaper = c.fallbackWallpaper()
			if wallpaper != "" {
				source = "bundled fallback wallpaper"
			}
		}
		switch {
		case wallpaper == "" || !isFile(wallpaper) || !nonEmpty(wallpaper):
			s.fail("Theme colors not generated")
			s.hint("Set a wallpaper via settings, then run: ./setup doctor")
			return
		case scriptIn("scripts/colors/generate_colors_material.py") == "" ||
			(!isExec(filepath.Join(c.e.StateHome, "quickshell", ".venv", "bin", "python3")) && lookPath("python3") == ""):
			s.fail("Theme colors regeneration failed")
			s.hint("Python color generation failed — check venv")
			return
		default:
			s.fixable("Theme colors not generated", "Regenerated theme colors from "+source)
		}
	}

	if isFile(darkly) {
		s.pass("Darkly Qt colors OK")
		return
	}
	if scriptIn("scripts/colors/apply-gtk-theme.sh") == "" {
		c.runtimeOr(s, "Darkly Qt colors")
		return
	}
	s.fixable("Darkly Qt colors missing", "Regenerated Darkly Qt colors")
}

// fallbackWallpaper is doctor_fallback_wallpaper: the first non-empty image
// in the shipped wallpapers.
func (c *checker) fallbackWallpaper() string {
	var dirs []string
	if c.e.RuntimeDir != "" {
		dirs = append(dirs, filepath.Join(c.e.RuntimeDir, "assets", "wallpapers"))
	}
	if root := c.repoRoot(); root != "" {
		dirs = append(dirs, filepath.Join(root, "assets", "wallpapers"))
	}
	for _, d := range dirs {
		for _, f := range images(d) {
			if nonEmpty(f) {
				return f
			}
		}
	}
	return ""
}

// images lists the jpg, jpeg, png and webp files right inside dir, like
// `find -maxdepth 1 -type f -iname ...` (in directory order).
func images(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, en := range entries {
		if !en.Type().IsRegular() {
			continue
		}
		switch strings.ToLower(filepath.Ext(en.Name())) {
		case ".jpg", ".jpeg", ".png", ".webp":
			out = append(out, filepath.Join(dir, en.Name()))
		}
	}
	return out
}

// --- 19 -----------------------------------------------------------------------

func qtPluginDir(tool string) string {
	if lookPath(tool) == "" {
		return ""
	}
	out, _ := stdout(10*time.Second, tool, "--plugin-dir")
	return strings.TrimSpace(out)
}

func pluginDirs(sub string) []string {
	var dirs []string
	dir := ""
	if lookPath("qtpaths6") != "" {
		dir = qtPluginDir("qtpaths6")
	} else if lookPath("qtpaths") != "" {
		dir = qtPluginDir("qtpaths")
	}
	if dir != "" {
		dirs = append(dirs, filepath.Join(dir, sub))
	}
	for _, d := range []string{"/usr/lib/qt6/plugins", "/usr/lib64/qt6/plugins", "/usr/lib/x86_64-linux-gnu/qt6/plugins"} {
		dirs = append(dirs, filepath.Join(d, sub))
	}
	return dirs
}

func anyFile(dirs []string, name string) bool {
	for _, d := range dirs {
		if isFile(filepath.Join(d, name)) {
			return true
		}
	}
	return false
}

func (c *checker) qtTheming(s *step) {
	plugin := anyFile(pluginDirs("platformthemes"), "KDEPlasmaPlatformTheme6.so")
	if !plugin {
		s.fail("plasma-integration not installed (Qt apps will have broken colors)")
		switch c.e.OS {
		case "arch":
			s.hint("Run: sudo pacman -S plasma-integration")
		case "fedora":
			s.hint("Run: sudo dnf install plasma-integration")
		case "debian", "ubuntu":
			s.hint("Run: sudo apt install plasma-integration")
		case "void":
			s.hint("Run: ./setup install (repairs the managed Void provider)")
		default:
			s.hint("Install plasma-integration using your package manager")
		}
	} else {
		cfg := filepath.Join(c.e.ConfigHome, "niri", "config.kdl")
		modular := filepath.Join(c.e.ConfigHome, "niri", "config.d", "40-environment.kdl")
		if isFile(modular) {
			data, err := os.ReadFile(cfg)
			if err != nil || regexp.MustCompile(`(?m)^[[:space:]]*include[[:space:]]+"config\.d/40-environment\.kdl"[[:space:]]*$`).Match(data) {
				cfg = modular
			}
		}
		if data, err := os.ReadFile(cfg); err == nil && bytes.Contains(data, []byte(`QT_QPA_PLATFORMTHEME "qt6ct"`)) {
			s.fixable("QT_QPA_PLATFORMTHEME is qt6ct", "Switched QT_QPA_PLATFORMTHEME from qt6ct to kde")
		} else {
			s.pass("Qt theming OK (plasma-integration + kde platform)")
		}
	}
	if !anyFile(pluginDirs("styles"), "darkly6.so") {
		s.fail("Darkly Qt style not installed (Qt apps won't have Material You style)")
		switch c.e.OS {
		case "arch":
			s.hint("Run: yay -S darkly-bin")
		case "void":
			s.hint("Run: ./setup install (repairs the pinned Void Darkly provider)")
		default:
			s.hint("Install darkly from: https://github.com/AlessioC31/darkly")
		}
	} else {
		s.pass("Darkly Qt style OK")
	}
	if plugin {
		if lookPath("keditfiletype") != "" || lookPath("keditfiletype6") != "" {
			s.pass("kde-cli-tools OK")
		} else {
			s.fail("kde-cli-tools not installed (Dolphin 'Open With' dialog won't work)")
			switch c.e.OS {
			case "arch":
				s.hint("Run: sudo pacman -S kde-cli-tools")
			case "fedora":
				s.hint("Run: sudo dnf install kde-cli-tools")
			case "debian", "ubuntu":
				s.hint("Run: sudo apt install kde-cli-tools")
			case "opensuse":
				s.hint("Run: sudo zypper install kde-cli-tools6")
			case "void":
				s.hint("Run: sudo xbps-install -S kde-cli-tools")
			default:
				s.hint("Install kde-cli-tools using your package manager")
			}
		}
	}
}

// --- 20, 21, 22 ---------------------------------------------------------------

func (c *checker) conflictingServices(s *step) {
	var running []string
	for _, name := range []string{"dunst", "mako", "swaync"} {
		if _, code := run(5*time.Second, "pgrep", "-x", name); code == 0 {
			running = append(running, name)
		}
	}
	if len(running) == 0 {
		s.pass("No conflicting notification daemons")
		return
	}
	how := "restart manually if desired"
	if c.systemd() {
		how = "re-enable with systemctl --user if desired"
	}
	s.fixable("Conflicting notification daemons running: "+strings.Join(running, " "),
		"Stopped conflicting: "+strings.Join(running, " ")+" (iNiR has built-in notifications; "+how+")")
}

var conflictingShellPackages = []string{"cachyos-niri-noctalia", "noctalia-shell", "noctalia-qs", "noctalia-qs-git",
	"dms-shell", "dms-shell-git", "caelestia-shell", "caelestia-shell-git", "bms-shell-bin"}

func (c *checker) conflictingShells(s *step) {
	var found []string
	switch {
	case lookPath("pacman") != "":
		for _, p := range conflictingShellPackages {
			if _, code := run(10*time.Second, "pacman", "-Qi", p); code == 0 {
				found = append(found, p)
			}
		}
	case lookPath("xbps-query") != "":
		for _, p := range conflictingShellPackages {
			if _, code := run(10*time.Second, "xbps-query", "-p", "pkgver", p); code == 0 {
				found = append(found, p)
			}
		}
	default:
		s.pass("Conflicting shells (package manager unsupported, skipped)")
		return
	}
	if len(found) > 0 {
		s.fail("Conflicting Quickshell shells installed: " + strings.Join(found, " "))
		s.hint("These must be removed for iNiR to work. Run: ./setup install")
		return
	}
	s.pass("No conflicting Quickshell shells")
}

func (c *checker) wallpapers(s *step) {
	pictures := filepath.Join(c.e.Home, "Pictures")
	if lookPath("xdg-user-dir") != "" {
		if out, code := stdout(5*time.Second, "xdg-user-dir", "PICTURES"); code == 0 {
			pictures = strings.TrimRight(out, "\n")
		}
	}
	dir := filepath.Join(pictures, "Wallpapers")
	if c.e.RuntimeDir == "" && c.repoRoot() == "" {
		c.runtimeOr(s, "Wallpaper health")
		return
	}
	if !isDir(dir) {
		s.pass("Wallpapers (dir not created yet)")
		return
	}
	broken := 0
	for _, f := range images(dir) {
		if !nonEmpty(f) {
			broken++
		}
	}
	if broken > 0 {
		s.fixable(fmt.Sprintf("%d corrupt wallpaper(s)", broken), fmt.Sprintf("Repaired %d/%d corrupt wallpaper(s)", broken, broken))
		return
	}
	s.pass("Wallpapers healthy")
}

// --- 23 -----------------------------------------------------------------------

var (
	legacyMallocLine = regexp.MustCompile(`(?m)^[[:space:]]*MALLOC_ARENA_MAX=2[[:space:]]*$|^[[:space:]]*MALLOC_MMAP_THRESHOLD_=131072[[:space:]]*$`)
)

func (c *checker) environment(s *step) {
	legacy := false
	if data, err := os.ReadFile(filepath.Join(c.e.ConfigHome, "environment.d", "quickshell-mem.conf")); err == nil && legacyMallocLine.Match(data) {
		legacy = true
	}
	if c.e.Getenv("MALLOC_ARENA_MAX") == "2" || c.e.Getenv("MALLOC_MMAP_THRESHOLD_") == "131072" {
		legacy = true
	}
	if c.systemd() {
		env, _ := stdout(5*time.Second, "systemctl", "--user", "show-environment")
		for _, l := range lines(env) {
			if l == "MALLOC_ARENA_MAX=2" || l == "MALLOC_MMAP_THRESHOLD_=131072" {
				legacy = true
			}
		}
	}
	profiles := 0
	for _, rc := range []string{filepath.Join(c.e.Home, ".bashrc")} {
		if data, err := os.ReadFile(rc); err == nil && !bytes.Contains(data, []byte("INIR_VENV")) {
			profiles++
		}
	}
	if lookPath("fish") != "" && !isFile(filepath.Join(c.e.ConfigHome, "fish", "conf.d", "inir-env.fish")) {
		profiles++
	}
	if data, err := os.ReadFile(filepath.Join(c.e.Home, ".zshrc")); err == nil && !bytes.Contains(data, []byte("INIR_VENV")) {
		profiles++
	}
	if legacy {
		s.fixable("Legacy global Quickshell allocator tuning is set", "Removed legacy global Quickshell allocator tuning")
	}
	if profiles > 0 {
		s.fixable(fmt.Sprintf("%d shell profile(s) without the iNiR environment", profiles), fmt.Sprintf("Added environment variables to %d shell profile(s)", profiles))
	} else {
		s.pass("Shell environment variables OK")
	}
}

// --- 24, 25 -------------------------------------------------------------------

func (c *checker) niriConfig(s *step) {
	if !isFile(filepath.Join(c.e.ConfigHome, "niri", "config.kdl")) {
		s.pass("Niri config (not installed)")
		return
	}
	if lookPath("niri") == "" {
		s.pass("Niri config (niri not installed, skipping validation)")
		return
	}
	out, code := run(30*time.Second, "niri", "validate")
	if code == 0 {
		s.pass("Niri config valid")
		return
	}
	s.fail("Niri config has errors")
	var errs []string
	for _, l := range lines(out) {
		if strings.Contains(strings.ToLower(l), "error") && len(errs) < 2 {
			errs = append(errs, l)
		}
	}
	s.hint(strings.Join(errs, "\n"))
}

func (c *checker) graphics(s *step) {
	if c.e.OS != "void" {
		name := c.e.OS
		if name == "" {
			name = "this distro"
		}
		s.pass("Graphics preflight not required for " + name)
		return
	}
	probe := filepath.Join(c.e.ConfDir, "scripts", "check-void-graphics.sh")
	if !isFile(probe) {
		probe = filepath.Join(c.e.RepoRoot, "scripts", "check-void-graphics.sh")
	}
	if !isFile(probe) {
		s.fail("Void graphics preflight helper is missing")
		return
	}
	out, code := run(60*time.Second, "bash", probe)
	first, rest, _ := strings.Cut(strings.TrimRight(out, "\n"), "\n")
	if code == 0 {
		s.pass(first)
		return
	}
	s.fail(first)
	if rest != "" {
		s.hint(rest)
	}
}

// --- helpers --------------------------------------------------------------------

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func globAny(pattern string) bool {
	m, _ := filepath.Glob(pattern)
	return len(m) > 0
}

func matchGlob(pattern, name string) bool {
	ok, _ := filepath.Match(pattern, name)
	return ok
}
