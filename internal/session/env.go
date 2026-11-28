// Package session starts the shell under its supervisor (inir.service): the environment it runs with, the check of
// its runtime against the system's Qt, and the cleanup of what a previous shell left behind. It replaces the bash
// launcher's `run --session` and `cleanup-orphans` (docs/adr/0007 in the shell repo).
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// root prefixes the system paths read here (/sys, /usr, /var), so tests run on a fixture tree.
var root = ""

// Env is a process environment as a map.
type Env map[string]string

// NewEnv reads KEY=VALUE pairs; the first of a key wins, as getenv sees it.
func NewEnv(list []string) Env {
	e := make(Env, len(list))
	for _, kv := range list {
		if k, v, ok := strings.Cut(kv, "="); ok {
			if _, seen := e[k]; !seen {
				e[k] = v
			}
		}
	}
	return e
}

// Get is the value of k, empty when unset.
func (e Env) Get(k string) string { return e[k] }

// Has reports whether k is set, even to the empty string.
func (e Env) Has(k string) bool { _, ok := e[k]; return ok }

// Unset removes each key.
func (e Env) Unset(keys ...string) {
	for _, k := range keys {
		delete(e, k)
	}
}

// List is the environment as exec takes it, sorted.
func (e Env) List() []string {
	out := make([]string, 0, len(e))
	for k, v := range e {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func (e Env) setIfEmpty(k, v string) bool {
	if e[k] != "" {
		return false
	}
	e[k] = v
	return true
}

func (e Env) home(key, dir string) string {
	if v := e[key]; v != "" {
		return v
	}
	return filepath.Join(e["HOME"], dir)
}

// Prepare turns the supervisor's environment into the shell's, in the launcher's order: the GPU policy, the inir
// binary and launcher the shell calls, Qt paths, the compositor's app environment, memory and driver tuning, the
// platform theme, and what the user manager and Niri publish.
func Prepare(e Env, qs, configDir, cli, launcher string, sd *Systemd) {
	gpuPolicy(e)
	e["INIR_CLI"] = cli
	if l := filepath.Join(configDir, "scripts", "inir"); executable(l) {
		launcher = l
	}
	e["INIR_BASH_LAUNCHER"] = launcher
	e.Unset("INIR_CLI_DELEGATED")
	// A runtime with its own Qt (qt.conf beside it: setup's qs-rs) loads only its own plugins and QML modules: the
	// session's paths (Nix sets them for its Qt) hold builds for another Qt, which crash it. Apps get them back
	// (ShellExec).
	if OwnQt(qs) {
		e.Unset("QT_PLUGIN_PATH", "QML2_IMPORT_PATH", "QML_IMPORT_PATH")
	}
	niriAppEnvironment(e)
	// One 1024² atlas per window holds every icon and glyph the shell batches; Qt's 2048² default costs 16 MB of
	// GPU memory for each of its ~9 windows.
	e.setIfEmpty("QSG_ATLAS_WIDTH", "1024")
	e.setIfEmpty("QSG_ATLAS_HEIGHT", "1024")
	// jemalloc's defaults keep what the QML engine and video decoders free (~290 MB on 16 threads): two arenas and
	// a 1 s decay give it back at no measurable CPU cost.
	e.setIfEmpty("MALLOC_CONF", "background_thread:true,narenas:2,dirty_decay_ms:1000,muzzy_decay_ms:0,tcache_max:4096")
	mesaGlthread(e)
	platformTheme(e)
	if e["XDG_DATA_DIRS"] == "" {
		dirs := "/usr/local/share:/usr/share"
		for _, d := range []string{filepath.Join(e["HOME"], ".local/share/flatpak/exports/share"), "/var/lib/flatpak/exports/share"} {
			if isDir(root + d) {
				dirs = d + ":" + dirs
			}
		}
		e["XDG_DATA_DIRS"] = dirs
	}
	sessionEnvironment(e, sd)
	if socket, display, ok := sd.NiriSockets(e); ok {
		e["NIRI_SOCKET"] = socket
		e["WAYLAND_DISPLAY"] = display
	}
	e.setIfEmpty("ELECTRON_OZONE_PLATFORM_HINT", "auto")
}

// HotReload turns file watching off for a deployed payload; a git checkout keeps it (live edits).
func HotReload(e Env, dir string) {
	if e.Has("INIR_DISABLE_HOT_RELOAD") || dir == "" {
		return
	}
	if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
		return
	}
	e["INIR_DISABLE_HOT_RELOAD"] = "1"
}

// OwnQt reports whether the runtime carries its own Qt (a qt.conf beside the resolved binary).
func OwnQt(qs string) bool {
	real, err := filepath.EvalSymlinks(qs)
	if err != nil {
		real = qs
	}
	_, err = os.Stat(filepath.Join(filepath.Dir(real), "qt.conf"))
	return err == nil
}

// ConfigFile is iNiR's config.json: ~/.config/inir, or the legacy illogical-impulse folder while it is a real one.
func ConfigFile(e Env) string {
	cfg := e.home("XDG_CONFIG_HOME", ".config")
	neu, old := filepath.Join(cfg, "inir"), filepath.Join(cfg, "illogical-impulse")
	dir := neu
	if st, err := os.Lstat(old); err == nil {
		if st.Mode()&os.ModeSymlink != 0 && isDir(neu) {
			dir = neu
		} else if isDir(old) {
			dir = old
		}
	}
	return filepath.Join(dir, "config.json")
}

var pciAddr = regexp.MustCompile(`[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-9a-f]`)

// gpuPolicy pins Qt and Mesa to the iGPU of a hybrid laptop so the dGPU can stay suspended, when the person turned
// GPU monitoring off (resources.monitorGpu; #106, #136). INIR_GPU_FORCE_DEFAULT=1 opts out. The names set go in
// INIR_SHELL_GPU_POLICY_VARS, which the shell removes before it starts an app.
func gpuPolicy(e Env) {
	if e["INIR_GPU_FORCE_DEFAULT"] == "1" {
		return
	}
	var cfg struct {
		Resources struct {
			MonitorGpu *bool `json:"monitorGpu"`
		} `json:"resources"`
	}
	data, err := os.ReadFile(ConfigFile(e))
	if err != nil || json.Unmarshal(data, &cfg) != nil || cfg.Resources.MonitorGpu == nil || *cfg.Resources.MonitorGpu {
		return
	}
	igpu, dgpu := "", false
	cards, _ := filepath.Glob(root + "/sys/class/drm/card*")
	for _, card := range cards {
		b, err := os.ReadFile(filepath.Join(card, "device", "boot_vga"))
		if err != nil {
			continue
		}
		switch strings.TrimSpace(string(b)) {
		case "1":
			if igpu == "" {
				dev, _ := filepath.EvalSymlinks(filepath.Join(card, "device"))
				if m := pciAddr.FindAllString(dev, -1); len(m) > 0 {
					igpu = m[len(m)-1]
				}
			}
		case "0":
			dgpu = true
		}
	}
	if !dgpu || igpu == "" {
		return
	}
	var set []string
	for _, kv := range [][2]string{
		// Vulkan loader: skip NVIDIA's ICD, which opens /dev/nvidia* while it enumerates.
		{"VK_LOADER_DRIVERS_DISABLE", "*nvidia*"},
		{"MESA_VK_DEVICE_SELECT", "pci-" + igpu},
		{"MESA_VK_DEVICE_SELECT_FORCE_DEFAULT_DEVICE", "1"},
		{"__GLX_VENDOR_LIBRARY_NAME", "mesa"},
		{"__NV_PRIME_RENDER_OFFLOAD", "0"},
	} {
		if e.setIfEmpty(kv[0], kv[1]) {
			set = append(set, kv[0])
		}
	}
	// Qt's FFmpeg decodes through VAAPI on the iGPU, or in software, never CUDA.
	if !e.Has("QT_FFMPEG_DECODING_HW_DEVICE_TYPES") {
		e["QT_FFMPEG_DECODING_HW_DEVICE_TYPES"] = "vaapi"
		set = append(set, "QT_FFMPEG_DECODING_HW_DEVICE_TYPES")
	}
	for _, kv := range [][2]string{{"__VK_LAYER_NV_optimus", "non_NVIDIA_only"}, {"VDPAU_DRIVER", "none"}} {
		if e.setIfEmpty(kv[0], kv[1]) {
			set = append(set, kv[0])
		}
	}
	if len(set) > 0 {
		e["INIR_SHELL_GPU_POLICY_VARS"] = strings.Join(set, " ")
	}
}

var niriInclude = regexp.MustCompile(`(?m)^[[:space:]]*include[[:space:]]+"config\.d/40-environment\.kdl"[[:space:]]*(//.*)?$`)

// niriAppEnvironment mirrors the app-facing values of Niri's environment block: inir.service starts from systemd,
// not as Niri's child. Umbriel publishes its own to the user manager first.
func niriAppEnvironment(e Env) {
	if e["UMBRIEL_SOCKET"] != "" {
		return
	}
	dir := filepath.Join(e.home("XDG_CONFIG_HOME", ".config"), "niri")
	rootCfg, modular := filepath.Join(dir, "config.kdl"), filepath.Join(dir, "config.d", "40-environment.kdl")
	cfg := ""
	rootData, rootErr := os.ReadFile(rootCfg)
	if isFile(modular) && (rootErr != nil || niriInclude.Match(rootData)) {
		cfg = modular
	} else if rootErr == nil {
		cfg = rootCfg
	}
	if cfg == "" {
		return
	}
	data, err := os.ReadFile(cfg)
	if err != nil {
		return
	}
	for _, name := range []string{"XDG_MENU_PREFIX", "QT_QPA_PLATFORM", "QT_QPA_PLATFORMTHEME", "QT_STYLE_OVERRIDE",
		"ELECTRON_OZONE_PLATFORM_HINT", "_JAVA_AWT_WM_NONREPARENTING"} {
		re := regexp.MustCompile(`(?m)^[[:space:]]*` + regexp.QuoteMeta(name) + `[[:space:]]+"([^"]*)"`)
		if m := re.FindAllSubmatch(data, -1); len(m) > 0 {
			if v := string(m[len(m)-1][1]); v != "" {
				e[name] = v
			}
		}
	}
}

// mesaGlthread turns Mesa's GL worker thread off for the shell (~50 MB across its windows, nothing gained behind
// Qt's render threads) through a driconf profile: the mesa_glthread variable prints a notice per GL context.
func mesaGlthread(e Env) {
	if e["mesa_glthread"] != "" || e["DRIRC_CONFIGDIR"] != "" {
		return
	}
	sys := ""
	for _, d := range []string{"/usr/share/drirc.d", "/usr/local/share/drirc.d", "/run/opengl-driver/share/drirc.d"} {
		if isDir(root + d) {
			sys = root + d
			break
		}
	}
	dir := filepath.Join(runtimeDirOf(e), "inir", "drirc.d")
	if sys == "" || writeDrirc(sys, dir) != nil {
		e["mesa_glthread"] = "false"
		return
	}
	e["DRIRC_CONFIGDIR"] = dir
}

func writeDrirc(sys, dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	confs, _ := filepath.Glob(filepath.Join(sys, "*.conf"))
	for _, c := range confs {
		if err := os.Symlink(c, filepath.Join(dir, filepath.Base(c))); err != nil {
			return err
		}
	}
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" standalone=\"yes\"?>\n<driconf><device>\n")
	for _, app := range [][2]string{{"Quickshell", "qs"}, {"Quickshell", "quickshell"}, {"iNiR", "inir-shell"}} {
		b.WriteString(`<application name="` + app[0] + `" executable="` + app[1] + `"><option name="mesa_glthread_app_profile" value="0"/></application>` + "\n")
	}
	b.WriteString("</device></driconf>\n")
	return os.WriteFile(filepath.Join(dir, "99-inir.conf"), []byte(b.String()), 0o644)
}

// platformTheme gives Qt a platform theme when the session has none: without one QIcon::fromTheme resolves no
// icon. INIR_QT_QPA_PLATFORMTHEME and INIR_QT_STYLE_OVERRIDE override.
func platformTheme(e Env) {
	if e.Has("INIR_QT_QPA_PLATFORMTHEME") {
		e["QT_QPA_PLATFORMTHEME"] = e["INIR_QT_QPA_PLATFORMTHEME"]
	} else if e["QT_QPA_PLATFORMTHEME"] == "" {
		kde := lookPath(e, "kwriteconfig6")
		for _, lib := range []string{"/usr/lib", "/usr/lib64", "/usr/lib/x86_64-linux-gnu"} {
			kde = kde || isFile(root+lib+"/qt6/plugins/platformthemes/KDEPlasmaPlatformTheme6.so")
		}
		if kde {
			e["QT_QPA_PLATFORMTHEME"] = "kde"
		} else if lookPath(e, "gsettings") && (isFile(root+"/usr/lib/qt6/plugins/platformthemes/libqgtk3.so") ||
			isFile(root+"/usr/lib64/qt6/plugins/platformthemes/libqgtk3.so")) {
			e["QT_QPA_PLATFORMTHEME"] = "gtk3"
		}
	}
	if e.Has("INIR_QT_STYLE_OVERRIDE") {
		e["QT_STYLE_OVERRIDE"] = e["INIR_QT_STYLE_OVERRIDE"]
	}
}

// sessionEnvironment copies what the user manager publishes (the compositor owns those values) into what is
// missing, and puts the manager's PATH (login, compositor: ~/.local/bin, flatpak) first, keeping this process's
// own entries after it (the system dirs, a package's tools).
func sessionEnvironment(e Env, sd *Systemd) {
	sys := sd.Environment()
	if len(sys) == 0 {
		return
	}
	for _, k := range []string{"WAYLAND_DISPLAY", "NIRI_SOCKET", "UMBRIEL_SOCKET", "DISPLAY", "XDG_SESSION_TYPE",
		"XDG_CURRENT_DESKTOP", "XDG_SESSION_DESKTOP", "DESKTOP_SESSION", "XCURSOR_THEME", "XCURSOR_SIZE", "LANG",
		"LC_ALL", "XDG_MENU_PREFIX", "ELECTRON_OZONE_PLATFORM_HINT", "QT_STYLE_OVERRIDE", "GDK_BACKEND"} {
		if v := sys[k]; e[k] == "" && v != "" {
			e[k] = v
		}
	}
	if p := sys["PATH"]; p != "" && p != e["PATH"] {
		merged := strings.Split(p, ":")
		have := make(map[string]bool, len(merged))
		for _, d := range merged {
			have[d] = true
		}
		for _, d := range strings.Split(e["PATH"]+":/usr/local/bin:/usr/bin", ":") {
			if d != "" && !have[d] {
				have[d] = true
				merged = append(merged, d)
			}
		}
		e["PATH"] = strings.Join(merged, ":")
	}
}

func lookPath(e Env, name string) bool {
	for _, d := range filepath.SplitList(e["PATH"]) {
		if d != "" && executable(filepath.Join(d, name)) {
			return true
		}
	}
	return false
}

func runtimeDirOf(e Env) string {
	if d := e["XDG_RUNTIME_DIR"]; d != "" {
		return d
	}
	return "/run/user/" + itoa(os.Getuid())
}

func executable(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

func isDir(p string) bool  { st, err := os.Stat(p); return err == nil && st.IsDir() }
func isFile(p string) bool { st, err := os.Stat(p); return err == nil && st.Mode().IsRegular() }
