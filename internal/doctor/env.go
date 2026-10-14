// Package doctor checks an iNiR install the way setup's doctor does
// (sdata/lib/doctor.sh), step for step and with the same verdicts, without
// changing anything. Repairs stay with the bash doctor: Run asks it for the
// steps that failed (`setup doctor --only=N`), so they are the same code.
package doctor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Env is what the bash doctor reads from its environment and the install:
// XDG paths, DOTS_CORE_CONFDIR, REPO_ROOT, the runtime copy and OS_GROUP_ID.
type Env struct {
	Home, ConfigHome, StateHome, CacheHome, DataHome, BinHome string
	RuntimeHome                                               string // XDG_RUNTIME_DIR
	ConfDir                                                   string // ~/.config/inir (or the legacy one)
	RepoRoot                                                  string // the checkout setup works on
	SetupDir                                                  string // where `inir doctor` runs ./setup
	RuntimeDir                                                string // get_runtime_shell_dir, "" if none
	OS                                                        string // OS_GROUP_ID
	Getenv                                                    func(string) string
}

// NewEnv resolves the environment like setup does when `inir doctor` runs it.
func NewEnv() *Env {
	e := &Env{Getenv: os.Getenv}
	e.Home = os.Getenv("HOME")
	e.ConfigHome = e.envOr("XDG_CONFIG_HOME", filepath.Join(e.Home, ".config"))
	e.StateHome = e.envOr("XDG_STATE_HOME", filepath.Join(e.Home, ".local", "state"))
	e.CacheHome = e.envOr("XDG_CACHE_HOME", filepath.Join(e.Home, ".cache"))
	e.DataHome = e.envOr("XDG_DATA_HOME", filepath.Join(e.Home, ".local", "share"))
	e.BinHome = e.envOr("XDG_BIN_HOME", filepath.Join(e.Home, ".local", "bin"))
	e.RuntimeHome = e.envOr("XDG_RUNTIME_DIR", "/run/user/"+strconv.Itoa(os.Getuid()))
	e.ConfDir = e.envOr("DOTS_CORE_CONFDIR", e.configDir())
	e.RuntimeDir = e.runtimeShellDir()
	e.SetupDir = e.setupDir()
	if d := os.Getenv("INIR_SETUP_DIR"); d != "" && isExec(filepath.Join(d, "setup")) {
		e.SetupDir = d // setup handed its doctor over: its checkout, not the installed one
	}
	e.RepoRoot = e.bootstrapRepoRoot(e.SetupDir)
	e.OS = DetectOS()
	return e
}

func (e *Env) envOr(key, fallback string) string {
	if v := e.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// configDir is resolve_inir_config_dir (sdata/lib/environment-variables.sh).
func (e *Env) configDir() string {
	newDir := filepath.Join(e.ConfigHome, "inir")
	legacy := filepath.Join(e.ConfigHome, "illogical-impulse")
	if isSymlink(legacy) && isDir(newDir) {
		return newDir
	}
	if isDir(legacy) {
		return legacy
	}
	return newDir
}

// runtimeShellDir is get_runtime_shell_dir (sdata/lib/versioning.sh).
func (e *Env) runtimeShellDir() string {
	if o := e.Getenv("INIR_RUNTIME_DIR"); o != "" && isFile(filepath.Join(o, "shell.qml")) {
		return o
	}
	for _, c := range e.runtimeCandidates() {
		if isFile(filepath.Join(c, "shell.qml")) {
			return c
		}
	}
	return ""
}

func (e *Env) runtimeCandidates() []string {
	return []string{
		filepath.Join(e.ConfigHome, "quickshell", "inir"),
		e.envOr("INIR_SYSTEM_RUNTIME_DIR_LOCAL", "/usr/local/share/quickshell/inir"),
		e.envOr("INIR_SYSTEM_RUNTIME_DIR", "/usr/share/quickshell/inir"),
		filepath.Join(e.ConfigHome, "quickshell", "ii"),
		e.envOr("INIR_LEGACY_SYSTEM_RUNTIME_DIR_LOCAL", "/usr/local/share/quickshell/ii"),
		e.envOr("INIR_LEGACY_SYSTEM_RUNTIME_DIR", "/usr/share/quickshell/ii"),
	}
}

// storedRepo reads repoPath from ~/.config/inir/version.json, as setup's
// bootstrap and scripts/inir's resolve_setup_dir do.
func (e *Env) storedRepo() string {
	var v map[string]any
	if !readJSON(filepath.Join(e.configDir(), "version.json"), &v) {
		return ""
	}
	repo := firstString(v, "repoPath", "repo_path")
	if strings.HasPrefix(repo, "~") {
		repo = e.Home + repo[1:]
	}
	return repo
}

func isCheckout(dir string) bool {
	return dir != "" && exists(filepath.Join(dir, ".git")) && isFile(filepath.Join(dir, "setup")) &&
		isFile(filepath.Join(dir, "shell.qml"))
}

// setupDir is where scripts/inir runs ./setup (resolve_setup_dir): the
// installed shell when it carries setup, else the stored checkout.
func (e *Env) setupDir() string {
	if e.RuntimeDir != "" && isExec(filepath.Join(e.RuntimeDir, "setup")) {
		return e.RuntimeDir
	}
	if repo := e.storedRepo(); isCheckout(repo) && isExec(filepath.Join(repo, "setup")) {
		return repo
	}
	return filepath.Join(e.ConfigHome, "quickshell", "inir")
}

// bootstrapRepoRoot is setup's resolve_bootstrap_repo_root.
func (e *Env) bootstrapRepoRoot(scriptDir string) string {
	if isCheckout(scriptDir) {
		return scriptDir
	}
	if repo := e.storedRepo(); isCheckout(repo) {
		return repo
	}
	return scriptDir
}

// Version reads the installed version record the way versioning.sh does
// (get_installed_version_file and read_installed_version_field).
type Version struct {
	e    *Env
	file string
	data map[string]any
}

func (e *Env) Version() *Version {
	v := &Version{e: e, file: e.installedVersionFile()}
	if v.file != "" {
		readJSON(v.file, &v.data)
	}
	return v
}

func (e *Env) installedVersionFile() string {
	runtimeFile := ""
	if e.RuntimeDir != "" {
		runtimeFile = filepath.Join(e.RuntimeDir, "version.json")
	}
	if e.Getenv("INIR_RUNTIME_DIR") != "" && isFile(runtimeFile) {
		return runtimeFile
	}
	if runtimeFile != "" {
		var m map[string]any
		if readJSON(runtimeFile, &m) && firstString(m, "installMode", "install_mode") == "package-managed" {
			return runtimeFile
		}
	}
	for _, f := range []string{
		filepath.Join(e.configDir(), "version.json"),
		filepath.Join(e.ConfigHome, "quickshell", "inir", "version.json"),
		filepath.Join(e.envOr("INIR_SYSTEM_RUNTIME_DIR_LOCAL", "/usr/local/share/quickshell/inir"), "version.json"),
		filepath.Join(e.envOr("INIR_SYSTEM_RUNTIME_DIR", "/usr/share/quickshell/inir"), "version.json"),
	} {
		if isFile(f) {
			return f
		}
	}
	return ""
}

// Field is read_installed_version_field: the first key with a value.
func (v *Version) Field(keys ...string) string {
	if v.data == nil {
		return ""
	}
	return firstString(v.data, keys...)
}

// InstallMode is get_installed_install_mode.
func (v *Version) InstallMode() string {
	if m := v.Field("installMode", "install_mode"); m != "" {
		return m
	}
	if v.Field("packageName", "package_name") != "" {
		return "package-managed"
	}
	target := realpath(v.e.RuntimeDir)
	if repo := realpath(v.e.RepoRoot); repo != "" && target != "" && repo == target {
		return "repo-link"
	}
	if v.Field("repoPath", "repo_path") != "" {
		return "repo-copy"
	}
	return "unknown"
}

// UpdateStrategy is get_installed_update_strategy.
func (v *Version) UpdateStrategy() string {
	if s := v.Field("updateStrategy", "update_strategy"); s != "" {
		return s
	}
	switch v.InstallMode() {
	case "repo-copy", "repo-link":
		return "repo-setup"
	case "package-managed":
		return "package-manager"
	}
	return "unknown"
}

// installModeForSync is what setup's sync helpers use: the recorded mode, or
// get_install_mode when nothing is recorded.
func (e *Env) installModeForSync() string {
	if m := e.Version().InstallMode(); m != "" && m != "unknown" {
		return m
	}
	if m := e.Getenv("INIR_INSTALL_MODE"); m != "" {
		return m
	}
	target := realpath(filepath.Join(e.ConfigHome, "quickshell", "inir"))
	if repo := realpath(e.RepoRoot); repo != "" && target != "" && repo == target {
		return "repo-link"
	}
	if isCheckout(e.RepoRoot) {
		return "repo-copy"
	}
	return "unknown"
}

// DetectOS is detect_distro's OS_GROUP_ID (sdata/lib/dist-determine.sh), in
// the same order: arch, fedora, debian/ubuntu, opensuse, void, gentoo, nixos, alpine.
func DetectOS() string {
	osRelease, _ := os.ReadFile("/etc/os-release")
	rel := strings.ToLower(string(osRelease))
	switch {
	case exists("/etc/arch-release") || lookPath("pacman") != "":
		return "arch"
	case exists("/etc/fedora-release") || strings.Contains(rel, "fedora"):
		return "fedora"
	case exists("/etc/debian_version"):
		if strings.Contains(rel, "ubuntu") {
			return "ubuntu"
		}
		return "debian"
	case strings.Contains(rel, "opensuse") || exists("/etc/SuSE-release"):
		return "opensuse"
	case exists("/etc/void-release") || strings.Contains(rel, "void"):
		return "void"
	case exists("/etc/gentoo-release"):
		return "gentoo"
	case exists("/etc/NIXOS") || strings.Contains(rel, "nixos"):
		return "nixos"
	case exists("/etc/alpine-release"):
		return "alpine"
	}
	return "unknown"
}

// usableSystemdUserManager is has_usable_systemd_user_manager: the private
// socket exists and show-environment answers (a timeout is a busy manager).
func (e *Env) usableSystemdUserManager() bool {
	if !isSocket(filepath.Join(e.RuntimeHome, "systemd", "private")) {
		return false
	}
	_, code := run(3*time.Second, "systemctl", "--user", "show-environment")
	return code == 0 || code == 124
}

// --- small helpers -----------------------------------------------------------

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func isSymlink(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

func isSocket(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode()&os.ModeSocket != 0
}

// isExec is bash's -x for the current user.
func isExec(p string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return err == nil && st.IsDir()
	}
	return accessX(p)
}

// nonEmpty is bash's -s.
func nonEmpty(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Size() > 0
}

func realpath(p string) string {
	if p == "" {
		return ""
	}
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	abs, _ := filepath.Abs(r)
	return abs
}

// readlinkF is `readlink -f`: the resolved path, or "" when it cannot resolve.
func readlinkF(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return ""
	}
	abs, _ := filepath.Abs(r)
	return abs
}

func sameContent(a, b string) bool {
	da, err1 := os.ReadFile(a)
	db, err2 := os.ReadFile(b)
	return err1 == nil && err2 == nil && bytes.Equal(da, db)
}

func lookPath(name string) string {
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return p
}

// run executes a command with a timeout and returns stdout+stderr and its exit
// code (124 on timeout, like timeout(1); 127 when it does not exist).
func run(timeout time.Duration, name string, args ...string) (string, int) {
	return runEnv(timeout, nil, name, args...)
}

func runEnv(timeout time.Duration, env []string, name string, args ...string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), 124
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode()
		}
		return string(out), 127
	}
	return string(out), 0
}

// stdout runs a command and returns only its standard output.
func stdout(timeout time.Duration, name string, args ...string) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if ctx.Err() == context.DeadlineExceeded {
		return string(out), 124
	}
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return string(out), ee.ExitCode()
		}
		return string(out), 127
	}
	return string(out), 0
}

func readJSON(path string, v any) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(data, v) == nil
}

// firstString is jq's `.a // .b`: the first key holding something other than
// null, false or "".
func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			if v != "" {
				return v
			}
		case nil:
		case bool:
			if v {
				return "true"
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		default:
			b, _ := json.Marshal(v)
			return string(b)
		}
	}
	return ""
}

func lines(s string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}

// accessX is access(2) with X_OK, which is what find -executable and test -x ask.
func accessX(p string) bool { return syscall.Access(p, 1) == nil }
