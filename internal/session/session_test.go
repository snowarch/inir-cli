package session

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

func TestCksumMatchesPOSIX(t *testing.T) {
	// cksum(1): "" -> 4294967295, "123456789" -> 930766865
	if Cksum(nil) != 4294967295 || Cksum([]byte("123456789")) != 930766865 {
		t.Fatalf("cksum %d %d", Cksum(nil), Cksum([]byte("123456789")))
	}
}

func TestPrepareDefaultsAndOverrides(t *testing.T) {
	home := t.TempDir()
	root = t.TempDir()
	defer func() { root = "" }()
	os.MkdirAll(filepath.Join(root, "usr/share/drirc.d"), 0o755)
	os.WriteFile(filepath.Join(root, "usr/share/drirc.d/00-mesa.conf"), nil, 0o644)
	niri := filepath.Join(home, ".config/niri")
	os.MkdirAll(niri, 0o755)
	os.WriteFile(filepath.Join(niri, "config.kdl"), []byte("environment {\n  QT_QPA_PLATFORM \"wayland\"\n  QT_QPA_PLATFORM \"wayland;xcb\"\n}\n"), 0o644)
	cfgDir := filepath.Join(home, "shell")
	os.MkdirAll(filepath.Join(cfgDir, "scripts"), 0o755)
	os.WriteFile(filepath.Join(cfgDir, "scripts/inir"), nil, 0o755)
	e := Env{"HOME": home, "XDG_RUNTIME_DIR": home, "PATH": "/x", "MALLOC_CONF": "mine", "INIR_CLI_DELEGATED": "1"}
	Prepare(e, "/nonexistent/qs", cfgDir, "/bin/inir", "/l/inir", NewSystemd(home))
	want := map[string]string{
		"QSG_ATLAS_WIDTH": "1024", "MALLOC_CONF": "mine", "QT_QPA_PLATFORM": "wayland;xcb",
		"INIR_CLI": "/bin/inir", "INIR_BASH_LAUNCHER": filepath.Join(cfgDir, "scripts/inir"),
		"DRIRC_CONFIGDIR": filepath.Join(home, "inir/drirc.d"), "ELECTRON_OZONE_PLATFORM_HINT": "auto",
		"XDG_DATA_DIRS": "/usr/local/share:/usr/share",
	}
	for k, v := range want {
		if e[k] != v {
			t.Errorf("%s = %q, want %q", k, e[k], v)
		}
	}
	if e.Has("INIR_CLI_DELEGATED") {
		t.Error("the hand-over marker reached the shell")
	}
	conf, _ := os.ReadFile(filepath.Join(home, "inir/drirc.d/99-inir.conf"))
	if !strings.Contains(string(conf), `executable="inir-shell"`) {
		t.Error("drirc profile misses inir-shell")
	}
}

func TestGPUPolicyPinsTheIGPU(t *testing.T) {
	home := t.TempDir()
	root = t.TempDir()
	defer func() { root = "" }()
	os.MkdirAll(filepath.Join(home, ".config/inir"), 0o755)
	os.WriteFile(filepath.Join(home, ".config/inir/config.json"), []byte(`{"resources":{"monitorGpu":false}}`), 0o644)
	for card, vga := range map[string]string{"card0": "1", "card1": "0"} {
		dev := filepath.Join(root, "sys/devices/pci0000:00/0000:00:08.1/0000:0"+vga+":00.0")
		os.MkdirAll(dev, 0o755)
		os.WriteFile(filepath.Join(dev, "boot_vga"), []byte(vga+"\n"), 0o644)
		os.MkdirAll(filepath.Join(root, "sys/class/drm", card), 0o755)
		os.Symlink(dev, filepath.Join(root, "sys/class/drm", card, "device"))
	}
	e := Env{"HOME": home, "VDPAU_DRIVER": "va_gl"}
	gpuPolicy(e)
	if e["MESA_VK_DEVICE_SELECT"] != "pci-0000:01:00.0" || e["VDPAU_DRIVER"] != "va_gl" ||
		strings.Contains(e["INIR_SHELL_GPU_POLICY_VARS"], "VDPAU") || !strings.Contains(e["INIR_SHELL_GPU_POLICY_VARS"], "VK_LOADER_DRIVERS_DISABLE") {
		t.Fatalf("%v", e)
	}
}

func TestCleanupKillsOnlyTheServicesOwn(t *testing.T) {
	proc := t.TempDir()
	add := func(pid int, comm, cmd, cgroup string, ppid int) {
		d := filepath.Join(proc, strconv.Itoa(pid))
		os.MkdirAll(d, 0o755)
		os.WriteFile(filepath.Join(d, "comm"), []byte(comm+"\n"), 0o644)
		os.WriteFile(filepath.Join(d, "cmdline"), []byte(strings.ReplaceAll(cmd, " ", "\x00")), 0o644)
		os.WriteFile(filepath.Join(d, "cgroup"), []byte("0::/user.slice/"+cgroup+"\n"), 0o644)
		os.WriteFile(filepath.Join(d, "stat"), []byte(strconv.Itoa(pid)+" ("+comm+") S "+strconv.Itoa(ppid)+" 0"), 0o644)
	}
	add(9001, "qs", "qs -p /shell", "app.slice/inir.service", 1)               // orphaned shell: killed
	add(9002, "qs", "qs -p /other", "app.slice/foot.scope", 1)                 // another Quickshell: kept
	add(9003, "swayidle", "swayidle -w timeout", "app.slice/inir.service", 1)  // orphaned helper: killed
	add(9004, "swayidle", "swayidle -w timeout", "app.slice/session.scope", 1) // the person's: kept
	run := t.TempDir()
	os.MkdirAll(filepath.Join(run, "quickshell/by-id/dead"), 0o755)
	os.MkdirAll(filepath.Join(run, "quickshell/by-pid"), 0o755)
	os.Symlink(filepath.Join(run, "quickshell/by-id/dead"), filepath.Join(run, "quickshell/by-pid/999999"))
	var killed []int
	o := Orphans{RuntimeDir: run, ProcRoot: proc, Systemd: NewSystemd(run), Kill: func(pid int, sig syscall.Signal) error {
		if sig == 0 {
			if pid == 999999 {
				return syscall.ESRCH
			}
			return nil
		}
		killed = append(killed, pid)
		return nil
	}}
	// no user manager here: the runit rules, where a helper is the shell's only by its path
	o.Roots = []string{"/shell"}
	o.Cleanup()
	if len(killed) != 1 || killed[0] != 9001 {
		t.Fatalf("killed %v, want the orphaned shell only (runit: swayidle names no shell path)", killed)
	}
	if _, err := os.Lstat(filepath.Join(run, "quickshell/by-id/dead")); err == nil {
		t.Error("orphaned instance kept")
	}
}
