package shell

import (
	"crypto/md5"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// fakeInstance lays out Quickshell's runtime index for a shell at dir and
// returns the path where its ipc.sock belongs.
func fakeInstance(t *testing.T, runtime, dir string) string {
	t.Helper()
	sum := md5.Sum([]byte(filepath.Join(dir, "shell.qml")))
	byShell := filepath.Join(runtime, "quickshell", "by-shell", "inir")
	byID := filepath.Join(runtime, "quickshell", "by-id", "abc")
	for _, d := range []string{byShell, byID, filepath.Join(runtime, "quickshell", "by-path")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(byID, filepath.Join(byShell, "abc")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(runtime, "quickshell", "by-path", hex.EncodeToString(sum[:]))
	if err := os.Symlink(byShell, link); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(byID, "ipc.sock")
}

func installedShell(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "shell.qml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	return real
}

func TestRunningNeedsALiveSocket(t *testing.T) {
	runtime := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	dir := installedShell(t)
	if Running(dir) {
		t.Fatal("running without any instance")
	}

	sock := fakeInstance(t, runtime, dir)
	listener, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	if !Running(dir) {
		t.Fatal("live socket not seen")
	}

	// A crashed instance leaves its socket file behind with nobody listening.
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	listener.Close()
	if _, err := os.Stat(sock); err != nil {
		t.Fatalf("socket file should stay: %v", err)
	}
	if Running(dir) {
		t.Fatal("dead socket counted as running")
	}
}

func TestInstalledFollowsSymlinks(t *testing.T) {
	dir := installedShell(t)
	link := filepath.Join(t.TempDir(), "inir")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	real, ok := Installed(link)
	if !ok || real != dir {
		t.Fatalf("Installed(link) = %q, %v; want %q", real, ok, dir)
	}
	if _, ok := Installed(t.TempDir()); ok {
		t.Fatal("directory without shell.qml counted as installed")
	}
}
