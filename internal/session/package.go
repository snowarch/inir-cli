package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var packageManaged = regexp.MustCompile(`"install(M|_m)ode"[[:space:]]*:[[:space:]]*"package-managed"`)

// SyncPackage brings the person's copy of a packaged shell (Nix, make install: the base ships read-only beside the
// launcher) in step with the package before the shell starts on it, when the package moved since (the stamp in
// $XDG_STATE_HOME/inir/package). setup does it. An error means there is no copy to start.
func SyncPackage(e Env, launcherRoot, userRuntime string) error {
	if e["INIR_RUNTIME_DIR"] != "" || launcherRoot == "" {
		return nil
	}
	userReal := realpath(userRuntime)
	payload := ""
	for _, c := range []string{launcherRoot, filepath.Join(launcherRoot, "share", "quickshell", "inir")} {
		// the person's copy carries the package's version.json too: it is never its own payload
		if realpath(c) == userReal {
			continue
		}
		v, err := os.ReadFile(filepath.Join(c, "version.json"))
		if _, gitErr := os.Lstat(filepath.Join(c, ".git")); err == nil && gitErr != nil && isFile(filepath.Join(c, "shell.qml")) &&
			executable(filepath.Join(c, "setup")) && packageManaged.Match(v) {
			payload = c
			break
		}
	}
	if payload == "" {
		return nil
	}
	stampFile := filepath.Join(e.home("XDG_STATE_HOME", ".local/state"), "inir", "package")
	have, _ := os.ReadFile(stampFile)
	if isFile(filepath.Join(userRuntime, "shell.qml")) && strings.TrimRight(string(have), "\n") == PackageStamp(payload) {
		return nil
	}
	fmt.Fprintf(os.Stderr, "iNiR: bringing your copy in step with the package at %s\n", payload)
	cmd := exec.Command(filepath.Join(payload, "setup"), "update", "-y")
	cmd.Env = append(e.List(), "INIR_SESSION_SYNC=1")
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if cmd.Run() != nil {
		if !isFile(filepath.Join(userRuntime, "shell.qml")) {
			return fmt.Errorf("could not set up your copy of the package (see above)")
		}
		fmt.Fprintln(os.Stderr, "iNiR: could not bring your copy in step (see above); starting the one you have")
	}
	return nil
}

// PackageStamp names a payload as scripts/lib/config-path.sh inir_package_stamp does: its real path and the
// POSIX cksum of its version.json.
func PackageStamp(dir string) string {
	dir = realpath(dir)
	sum := ""
	if b, err := os.ReadFile(filepath.Join(dir, "version.json")); err == nil {
		sum = fmt.Sprint(Cksum(b))
	}
	return dir + " " + sum
}

// Cksum is POSIX cksum's CRC: polynomial 0x04C11DB7, the length appended, inverted.
func Cksum(data []byte) uint32 {
	var crc uint32
	add := func(b byte) {
		crc ^= uint32(b) << 24
		for i := 0; i < 8; i++ {
			if crc&0x80000000 != 0 {
				crc = crc<<1 ^ 0x04C11DB7
			} else {
				crc <<= 1
			}
		}
	}
	for _, b := range data {
		add(b)
	}
	for n := len(data); n > 0; n >>= 8 {
		add(byte(n))
	}
	return ^crc
}

func realpath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
