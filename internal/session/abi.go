package session

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	qtMismatch  = regexp.MustCompile(`(?i)built against Qt|Qt.*mismatch|incompatible Qt`)
	qtVersion   = regexp.MustCompile(`^6\.\d+\.\d+$`)
	qtLibSuffix = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+$`)
	qsVersion   = regexp.MustCompile(`\b0\.\d+\.\d+\b`)
)

// CheckABI warns when Quickshell was built for another Qt than the system's: it uses Qt's private API and crashes
// on the first interaction after a Qt update (#93). It never blocks the start. A runtime with its own Qt (qs-rs)
// never meets the system's. A pass is cached on the mtimes of both binaries, so the same pair costs nothing.
func CheckABI(qs, cacheHome string, w io.Writer) {
	if OwnQt(qs) {
		return
	}
	qtLib := ""
	for _, p := range []string{"/usr/lib/libQt6Core.so.6", "/usr/lib64/libQt6Core.so.6"} {
		if _, err := os.Lstat(root + p); err == nil {
			qtLib = root + p
			break
		}
	}
	qtReal, _ := filepath.EvalSymlinks(qtLib)
	cache := filepath.Join(cacheHome, "inir", "abi-check")
	key := fmt.Sprintf("v2:%d:%d", mtime(os.Lstat, qs), mtime(os.Stat, qtReal))
	if b, err := os.ReadFile(cache); err == nil && string(b) == key {
		return
	}
	msg := ""
	out, _ := exec.Command(qs, "--version").CombinedOutput()
	for _, line := range strings.Split(string(out), "\n") {
		if qtMismatch.MatchString(line) {
			msg = line
			break
		}
	}
	if msg == "" {
		built := firstString(qs, qtVersion)
		runtime := qtLibSuffix.FindString(qtReal)
		if runtime == "" {
			if b, err := exec.Command("pkg-config", "--modversion", "Qt6Core").Output(); err == nil {
				runtime = strings.TrimSpace(string(b))
			}
		}
		if built != "" && runtime != "" && built != runtime {
			msg = "Quickshell built against Qt " + built + " but system has Qt " + runtime
		}
	}
	if msg == "" {
		_ = os.MkdirAll(filepath.Dir(cache), 0o755)
		_ = os.WriteFile(cache, []byte(key), 0o644)
		return
	}
	fmt.Fprintf(w, "\nWARNING: Quickshell was built for another Qt version\n  %s\n", msg)
	if stale := staleLocalQs(); stale != "" {
		fmt.Fprintf(w, "\nNOTE: A stale local quickshell binary is shadowing the system package:\n      %s\n      Remove it: rm %s\n", stale, stale)
	}
	fmt.Fprint(w, "\nQuickshell uses Qt internals and asks to be rebuilt after every Qt update. Until then it can crash.\nFix: inir doctor --fix-abi\n\n")
}

// staleLocalQs is a Quickshell in ~/.local/bin or /usr/local/bin, of another version, shadowing the system's.
func staleLocalQs() string {
	path, err := exec.LookPath("qs")
	if err != nil {
		if path, err = exec.LookPath("quickshell"); err != nil {
			return ""
		}
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		real = path
	}
	home := os.Getenv("HOME")
	if !strings.HasPrefix(real, home+"/.local/bin/") && !strings.HasPrefix(real, "/usr/local/bin/") {
		return ""
	}
	system := ""
	for _, p := range []string{"/usr/bin/qs", "/usr/bin/quickshell"} {
		if executable(p) {
			system = p
			break
		}
	}
	if system == "" {
		return ""
	}
	local, sys := firstMatch(real, qsVersion), firstMatch(system, qsVersion)
	if local != "" && sys != "" && local != sys {
		return real
	}
	return ""
}

func mtime(stat func(string) (os.FileInfo, error), p string) int64 {
	if st, err := stat(p); err == nil {
		return st.ModTime().Unix()
	}
	return 0
}

// printable runs of 4 or more bytes, as strings(1) reads a binary
func eachString(path string, fn func(string) bool) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<16)
	var run bytes.Buffer
	for {
		b, err := r.ReadByte()
		if err == nil && (b == '\t' || (b >= 0x20 && b < 0x7f)) {
			run.WriteByte(b)
			continue
		}
		if run.Len() >= 4 && fn(run.String()) {
			return
		}
		run.Reset()
		if err != nil {
			return
		}
	}
}

func firstString(path string, re *regexp.Regexp) (found string) {
	eachString(path, func(s string) bool {
		if re.MatchString(s) {
			found = s
			return true
		}
		return false
	})
	return found
}

func firstMatch(path string, re *regexp.Regexp) (found string) {
	eachString(path, func(s string) bool {
		found = re.FindString(s)
		return found != ""
	})
	return found
}
