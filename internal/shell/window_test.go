package shell

import (
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// layout writes Quickshell's runtime index for one instance "abc" of the QML file at path and returns the by-pid dir.
func layout(t *testing.T, path string) string {
	t.Helper()
	runtime := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	qs := filepath.Join(runtime, "quickshell")
	sum := md5.Sum([]byte(path))
	for _, d := range []string{"by-shell/w", "by-id/abc", "by-path", "by-pid"} {
		if err := os.MkdirAll(filepath.Join(qs, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	link(t, filepath.Join(qs, "by-id", "abc"), filepath.Join(qs, "by-shell", "w", "abc"))
	link(t, filepath.Join(qs, "by-shell", "w"), filepath.Join(qs, "by-path", hex.EncodeToString(sum[:])))
	return filepath.Join(qs, "by-pid")
}

func link(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Fatal(err)
	}
}

// fakeQS starts a process named qs (a copy of sleep) and returns its pid.
func fakeQS(t *testing.T) int {
	t.Helper()
	sleep, err := exec.LookPath("sleep")
	if err != nil {
		t.Skip("no sleep")
	}
	src, err := os.Open(sleep)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	bin := filepath.Join(t.TempDir(), "qs")
	dst, err := os.OpenFile(bin, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	dst.Close()
	cmd := exec.Command(bin, "30")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	// Until its exec the child still carries this test's name.
	for i := 0; i < 100; i++ {
		if _, ok := Exe(strconv.Itoa(cmd.Process.Pid)); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cmd.Process.Pid
}

func TestInstancesFindsALiveWindowByItsPath(t *testing.T) {
	byPid := layout(t, "/shell/settings.qml")
	pid := fakeQS(t)
	link(t, filepath.Join(filepath.Dir(byPid), "by-id", "abc"), filepath.Join(byPid, strconv.Itoa(pid)))
	if got := Instances("/shell/settings.qml"); len(got) != 1 || got[0] != pid {
		t.Fatalf("got %v, want [%d]", got, pid)
	}
	if got := Instances("/shell/shell.qml"); len(got) != 0 {
		t.Fatalf("another path counted: %v", got)
	}
}

func TestInstancesSkipsAPidThatIsNoQS(t *testing.T) {
	byPid := layout(t, "/shell/settings.qml")
	// This test's own pid is alive but runs no qs: a link left by a dead window whose pid went to another program.
	link(t, filepath.Join(filepath.Dir(byPid), "by-id", "abc"), filepath.Join(byPid, strconv.Itoa(os.Getpid())))
	if got := Instances("/shell/settings.qml"); len(got) != 0 {
		t.Fatalf("a process that is not qs counted: %v", got)
	}
}

func TestEnvironReadsTheStartEnvironment(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.Env = []string{"A=1", "B=two=2"}
	if err := cmd.Start(); err != nil {
		t.Skip("no sleep")
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	// Read before the child's exec, the environment is still empty.
	var env []string
	var err error
	for i := 0; i < 100 && len(env) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
		if env, err = Environ(cmd.Process.Pid); err != nil {
			t.Fatal(err)
		}
	}
	if len(env) != 2 || env[0] != "A=1" || env[1] != "B=two=2" {
		t.Fatalf("got %q", env)
	}
}
