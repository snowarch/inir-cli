package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// service is check_service_unit_health with what its helpers would change
// (setup's sync_user_inir_service_from_repo_if_present and
// ensure_user_inir_service_enabled, functions.sh's repair_legacy_niri_shell_startup).
func (c *checker) service(s *step) {
	if !c.systemd() {
		if c.e.OS == "void" {
			if _, code := run(10*time.Second, "nmcli", "-t", "-f", "STATE", "general"); lookPath("nmcli") != "" && code == 0 {
				s.pass("Void NetworkManager provider running")
			} else {
				s.fail("NetworkManager is installed but its Void runit service is not running")
				s.hint("Run: ./setup install and accept the NetworkManager migration prompt")
			}
			return
		}
		s.pass("User service checks skipped (no usable systemd user manager)")
		return
	}

	strategy := c.e.Version().UpdateStrategy()
	userDir := filepath.Join(c.e.ConfigHome, "systemd", "user")
	servicePath := filepath.Join(userDir, "inir.service")
	expected := ""
	if _, code := run(5*time.Second, "systemctl", "--user", "cat", "niri.service"); code == 0 {
		expected = "niri.service"
	}
	if c.serviceMasked() {
		s.fail("User inir.service is masked")
		s.hint("Run: inir service install && inir service enable")
		return
	}

	asset := filepath.Join(c.e.RepoRoot, "assets", "systemd", "inir.service")
	written := false // the doctor writes the unit before it reads systemd's view of it
	if strategy == "package-manager" {
		frag := systemdShow("FragmentPath", true)
		if !isFile(frag) {
			s.fail("Packaged inir.service is missing")
			s.hint("Reinstall the iNiR shell package")
			return
		}
		if frag == servicePath {
			packaged := ""
			for _, p := range []string{"/usr/lib/systemd/user/inir.service", "/usr/local/lib/systemd/user/inir.service", "/lib/systemd/user/inir.service"} {
				if isFile(p) {
					packaged = p
					break
				}
			}
			if packaged != "" && isFile(asset) && sameContent(servicePath, asset) && sameContent(packaged, asset) {
				s.fixable("A user inir.service duplicates the package's", "Removed redundant user service shadowing the package")
				frag = packaged
				written = true
			} else {
				s.fail("Local inir.service shadows the package-managed unit")
				s.hint("Review " + servicePath + ", then run: inir service uninstall && inir service enable")
				return
			}
		}
		servicePath = frag
	} else if !isFile(servicePath) {
		if !isFile(asset) {
			s.fail("User inir.service missing")
			s.hint("Run: inir service install")
			return
		}
		s.fixable("User inir.service missing", "Restored missing user inir.service")
		written = true
	}

	// Restoring the unit also wires it, silently, when there is a niri.service to wire to.
	wiringDone := written && strategy != "package-manager" && expected != ""
	if strategy != "package-manager" {
		if c.legacyNiriStartup() {
			s.fixable("Niri still starts the shell itself", "Removed legacy Niri shell startup")
		}
		if !isFile(asset) {
			s.fail("Could not refresh user inir.service from repo")
			return
		}
		if !written && c.serviceSyncChanges(asset, servicePath) {
			s.fixable("User inir.service differs from repo", "Refreshed user inir.service from repo")
			written = true
		}
	}
	if !wiringDone && c.wiringChanges(servicePath, written) {
		s.fixable("inir.service is not wired to niri.service", "Repaired inir.service Niri wiring")
		wiringDone = true
	}

	active := systemdShow("ActiveState", true)
	result := systemdShow("Result", true)
	kill := systemdShow("KillMode", false)
	frag := systemdShow("FragmentPath", false)
	if written {
		// After the doctor writes the unit and reloads, systemd reads the repo's.
		kill = unitKillMode(asset)
		frag = servicePath
	}
	if active == "failed" || result == "start-limit-hit" {
		if result == "" {
			result = "unknown result"
		}
		s.fail("Shell service failed (" + result + ")")
		s.hint("Run: inir logs")
	}
	if kill != "" && kill != "process" {
		s.fail("inir.service KillMode is '" + kill + "'")
		s.hint("Run: inir service install")
	} else {
		s.pass("User service file present")
	}
	if frag != "" && frag != servicePath {
		s.warn("systemd is loading inir.service from " + frag)
	}

	if !wiringDone {
		has, stale := false, []string(nil)
		dirs, _ := filepath.Glob(filepath.Join(userDir, "*.wants"))
		for _, d := range dirs {
			if !isDir(d) || !exists(filepath.Join(d, "inir.service")) {
				continue
			}
			name := strings.TrimSuffix(filepath.Base(d), ".wants")
			if expected != "" && name == expected {
				has = true
			} else {
				stale = append(stale, name)
			}
		}
		if expected != "" && !has {
			s.fixable("inir.service not wired to "+expected, "Enabled inir.service for "+expected)
		}
		if len(stale) > 0 {
			s.fail("Stale service links found: " + strings.Join(stale, " "))
			s.hint("Run: inir service disable && inir service enable")
		}
	}
	if expected == "" {
		s.warn("No supported compositor service detected for enablement wiring")
	}
}

// unitKillMode is the KillMode a unit file sets, systemd's default without one.
func unitKillMode(unit string) string {
	data, err := os.ReadFile(unit)
	if err != nil {
		return ""
	}
	mode := "control-group"
	for _, l := range lines(string(data)) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "KillMode="); ok {
			mode = strings.TrimSpace(v)
		}
	}
	return mode
}

// systemdShow reads one property of inir.service. value: `--value`, else the
// text after `=` as `cut -d= -f2` gives it.
func systemdShow(prop string, value bool) string {
	args := []string{"--user", "show", "-p", prop}
	if value {
		args = append(args, "--value")
	}
	out, _ := stdout(5*time.Second, "systemctl", append(args, "inir.service")...)
	out = strings.TrimRight(out, "\n")
	if !value {
		parts := strings.SplitN(out, "=", 3)
		if len(parts) < 2 {
			return ""
		}
		return parts[1]
	}
	return out
}

// serviceMasked is inir_user_service_is_masked.
func (c *checker) serviceMasked() bool {
	unit := filepath.Join(c.e.ConfigHome, "systemd", "user", "inir.service")
	if isSymlink(unit) && readlinkF(unit) == "/dev/null" {
		return true
	}
	if !c.systemd() {
		return false
	}
	state, _ := stdout(5*time.Second, "systemctl", "--user", "is-enabled", "inir.service")
	state = strings.TrimSpace(state)
	return state == "masked" || state == "masked-runtime"
}

var legacyStartup = regexp.MustCompile(`^[[:space:]]*spawn-at-startup[[:space:]]+"([^"]*/)?inir"[[:space:]]+"start"([[:space:]]*//.*)?[[:space:]]*$|^[[:space:]]*spawn-at-startup[[:space:]]+"qs"[[:space:]]+"-c"[[:space:]]+"(ii|inir)"([[:space:]]*//.*)?[[:space:]]*$`)

func (c *checker) legacyNiriStartup() bool {
	for _, f := range []string{filepath.Join(c.e.ConfigHome, "niri", "config.d", "50-startup.kdl"), filepath.Join(c.e.ConfigHome, "niri", "config.kdl")} {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, l := range lines(string(data)) {
			if legacyStartup.MatchString(l) {
				return true
			}
		}
	}
	return false
}

// serviceSyncChanges is whether sync_user_inir_service_from_repo_if_present
// would write the unit or its override.
func (c *checker) serviceSyncChanges(asset, target string) bool {
	launcher := filepath.Join(c.e.BinHome, "inir")
	override := filepath.Join(filepath.Dir(target), "inir.service.d", "override.conf")
	if c.e.installModeForSync() == "repo-link" {
		if !isSymlink(target) || readlinkF(asset) != readlinkF(target) {
			return true
		}
		want := "# Managed locally: iNiR repo-link launcher override\n[Service]\nExecStart=\nExecStart=" + launcher +
			" run --session\nExecStopPost=\nExecStopPost=-" + launcher + " cleanup-orphans\n"
		have, err := os.ReadFile(override)
		return err != nil || string(have) != want
	}
	data, err := os.ReadFile(asset)
	if err != nil {
		return false
	}
	data = regexp.MustCompile(`(?m)^ExecStart=.*$`).ReplaceAllLiteral(data, []byte("ExecStart="+launcher+" run --session"))
	data = regexp.MustCompile(`(?m)^ExecStopPost=-.*$`).ReplaceAllLiteral(data, []byte("ExecStopPost=-"+launcher+" cleanup-orphans"))
	have, err := os.ReadFile(target)
	if isSymlink(target) || err != nil || !bytes.Equal(data, have) {
		return true
	}
	if o, err := os.ReadFile(override); err == nil && regexp.MustCompile(`(?m)^# Managed locally:`).Match(o) {
		return true
	}
	return false
}

// wiringChanges is whether ensure_user_inir_service_enabled would move or
// make the niri.service.wants link.
func (c *checker) wiringChanges(servicePath string, written bool) bool {
	if !c.systemd() || (!isFile(servicePath) && !written) {
		return false
	}
	if _, code := run(5*time.Second, "systemctl", "--user", "cat", "niri.service"); code != 0 {
		return false
	}
	userDir := filepath.Join(c.e.ConfigHome, "systemd", "user")
	correct := filepath.Join(userDir, "niri.service.wants")
	dirs, _ := filepath.Glob(filepath.Join(userDir, "*.wants"))
	for _, d := range dirs {
		if isDir(d) && exists(filepath.Join(d, "inir.service")) && realpath(d) != realpath(correct) {
			return true
		}
	}
	link := filepath.Join(correct, "inir.service")
	return !isSymlink(link) || readlinkF(link) != readlinkF(servicePath)
}
