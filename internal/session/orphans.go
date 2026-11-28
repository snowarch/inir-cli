package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Orphans is what a cleanup looks at: Quickshell's runtime index, the processes, and who supervises the shell.
type Orphans struct {
	RuntimeDir string   // XDG_RUNTIME_DIR, which holds quickshell/
	ProcRoot   string   // /proc
	Roots      []string // where the shell can be installed; under runit a helper is the shell's by its path
	Systemd    *Systemd
	ServiceDir string // runit's service folder for inir
	Kill       func(pid int, sig syscall.Signal) error
}

var (
	helperPattern = regexp.MustCompile(`keyboard_lock_state_daemon\.py|inir-accel keyboard-lock-daemon|nmcli monitor|^(/[^ ]*/)?swayidle -w|^(/[^ ]*/)?udevadm monitor --udev --subsystem-match=usb/usb_device`)
	thumbPattern  = regexp.MustCompile(`scripts/thumbnails/(thumbgen-venv\.sh|thumbgen\.py|generate-thumbnails-magick\.sh)|inir-accel thumbgen`)
	runitPID      = regexp.MustCompile(`\(pid ([0-9]+)\)`)
)

// Cleanup removes what a shell that died left: stale entries in Quickshell's runtime index, and the shell's own
// processes and helpers still running in inir.service (KillMode=process stops only the main one, so apps launched
// from the shell live on). A Quickshell or helper that is not the service's is never touched.
func (o Orphans) Cleanup() {
	o.pruneIndex()
	runit := !o.Systemd.Usable()
	main := 0
	if !runit {
		main = o.Systemd.MainPID("inir.service")
	} else if sv, err := exec.LookPath("sv"); err == nil {
		out, _ := exec.Command(sv, "status", o.ServiceDir).Output()
		if m := runitPID.FindSubmatch(out); m != nil {
			main, _ = strconv.Atoi(string(m[1]))
		}
	}
	inService := func(pid int) bool { return strings.Contains(o.read(pid, "cgroup"), "/inir.service") }
	self := os.Getpid()
	for _, pid := range o.pids() {
		if pid == self || pid == main {
			continue
		}
		comm := strings.TrimSpace(o.read(pid, "comm"))
		cmd := o.cmdline(pid)
		switch {
		case comm == "quickshell" || comm == "qs" || comm == "inir-shell":
			if inService(pid) {
				_ = o.Kill(pid, syscall.SIGTERM)
			}
		case helperPattern.MatchString(cmd):
			ppid := o.ppid(pid)
			if main > 0 && ppid == main {
				continue
			}
			// Runit reparents the helpers to pid 1, so their paths say whose they are.
			if runit && (ppid != 1 || !o.ownedHelper(cmd)) || !runit && !inService(pid) {
				continue
			}
			_ = o.Kill(pid, syscall.SIGTERM)
		}
	}
	// Thumbnails run as a process pool that can outlive the shell; only iNiR's own commands, in its service.
	var thumbs []int
	for _, pid := range o.pids() {
		if pid != self && thumbPattern.MatchString(o.cmdline(pid)) && (runit || inService(pid)) {
			thumbs = append(thumbs, pid)
			_ = o.Kill(pid, syscall.SIGTERM)
		}
	}
	if len(thumbs) > 0 {
		time.Sleep(200 * time.Millisecond)
		for _, pid := range thumbs {
			if o.Kill(pid, 0) == nil {
				_ = o.Kill(pid, syscall.SIGKILL)
			}
		}
	}
}

func (o Orphans) ownedHelper(cmd string) bool {
	for _, r := range o.Roots {
		if strings.Contains(cmd, r+"/scripts/daemon/keyboard_lock_state_daemon.py") || strings.Contains(cmd, "inir-accel keyboard-lock-daemon") {
			return true
		}
		if (strings.HasPrefix(cmd, "swayidle -w ") || strings.Contains(cmd, "/swayidle -w ")) && strings.Contains(cmd, r+"/scripts/inir") {
			return true
		}
	}
	return false
}

// pruneIndex drops by-pid links of dead processes, by-id instances no live pid points to, and dangling by-shell
// entries (and their folders once empty).
func (o Orphans) pruneIndex() {
	run := filepath.Join(o.RuntimeDir, "quickshell")
	links, _ := filepath.Glob(filepath.Join(run, "by-pid", "*"))
	live := map[string]bool{}
	for _, link := range links {
		pid, err := strconv.Atoi(filepath.Base(link))
		if err == nil && o.Kill(pid, 0) == syscall.ESRCH {
			_ = os.RemoveAll(link)
			continue
		}
		if t, err := filepath.EvalSymlinks(link); err == nil {
			live[t] = true
		}
	}
	ids, _ := filepath.Glob(filepath.Join(run, "by-id", "*"))
	for _, id := range ids {
		real, err := filepath.EvalSymlinks(id)
		if err == nil && isDir(real) && !live[real] {
			_ = os.RemoveAll(id)
		}
	}
	shells, _ := filepath.Glob(filepath.Join(run, "by-shell", "*"))
	for _, dir := range shells {
		if !isDir(dir) {
			continue
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			p := filepath.Join(dir, e.Name())
			if _, err := os.Stat(p); err != nil && e.Type()&os.ModeSymlink != 0 {
				_ = os.Remove(p)
			}
		}
		_ = syscall.Rmdir(dir)
	}
}

func (o Orphans) pids() []int {
	entries, _ := os.ReadDir(o.ProcRoot)
	var pids []int
	for _, e := range entries {
		if pid, err := strconv.Atoi(e.Name()); err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	return pids
}

func (o Orphans) read(pid int, name string) string {
	b, _ := os.ReadFile(filepath.Join(o.ProcRoot, strconv.Itoa(pid), name))
	return string(b)
}

// cmdline as pgrep -f matches it: the arguments joined by spaces.
func (o Orphans) cmdline(pid int) string {
	return strings.TrimRight(strings.ReplaceAll(o.read(pid, "cmdline"), "\x00", " "), " ")
}

// ppid is the fourth field of /proc/PID/stat, after the command name in parentheses.
func (o Orphans) ppid(pid int) int {
	stat := o.read(pid, "stat")
	i := strings.LastIndexByte(stat, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(stat[i+1:])
	if len(f) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(f[1])
	return n
}
