package activity

import (
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/snowarch/inir-cli/internal/notify"
)

func TestMeter(t *testing.T) {
	m := NewMeter()
	for _, chunk := range []string{"Downloading\n", "\r\x1b[32m 1", "2.5%\x1b[0m [##   ]", "\r 47% [####  ] 3 MiB/s", "\r 100% done\n", "build ok\n"} {
		m.Write([]byte(chunk))
	}
	if p, l := m.State(); p != 100 || l != "build ok" {
		t.Fatalf("got %d %q", p, l)
	}
	m = NewMeter()
	m.Write([]byte("disk at 450% of quota\n"))
	if p, _ := m.State(); p != -1 {
		t.Fatalf("450%% is not a progress: %d", p)
	}
	m.Write([]byte("(3/7) 12"))
	m.Write([]byte("% installing\n"))
	if p, _ := m.State(); p != 12 {
		t.Fatalf("split number: %d", p)
	}
}

func TestTitleAndIgnored(t *testing.T) {
	if got := Title("/usr/bin/paru   -Syu --noconfirm"); got != "paru -Syu --noconfirm" {
		t.Errorf("title %q", got)
	}
	t.Setenv("INIR_HOOK_IGNORE", "cava")
	for line, want := range map[string]bool{
		"sudo nvim /etc/fstab": true, "FOO=1 make -j8": false, "inir run -- make": true, "cava": true,
		"env -i ssh host": true, "paru -Syu": false, "  ": true, "./build.sh": false,
	} {
		if Ignored(line) != want {
			t.Errorf("Ignored(%q) = %v", line, !want)
		}
	}
}

func TestScriptsParse(t *testing.T) {
	for _, sh := range Shells {
		s, err := Script(sh, 30, "/opt/it's/inir")
		if err != nil || !strings.Contains(s, "--after 30") || !strings.Contains(s, `'/opt/it'\''s/inir'`) {
			t.Fatalf("%s: %v\n%s", sh, err, s)
		}
		bin, err := exec.LookPath(sh)
		if err != nil {
			continue
		}
		if out, err := exec.Command(bin, "-n", "-c", s).CombinedOutput(); err != nil {
			t.Errorf("%s -n: %v %s", sh, err, out)
		}
	}
	if _, err := Script("tcsh", 10, "inir"); err == nil {
		t.Error("tcsh should fail")
	}
}

// A command that ends before the watcher is even up.
func TestHookWatchEndsOnFileAlone(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	os.WriteFile(statusFile(os.Getpid()), []byte("0"), 0o600)
	done := make(chan struct{})
	go func() { watch(hookArgs{after: 1, shell: os.Getpid(), line: "nvim"}); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("watcher stayed")
	}
}

func fake(t *testing.T) *notify.Fake {
	f, err := notify.StartFake()
	if err != nil {
		t.Skip("no dbus-daemon:", err)
	}
	t.Cleanup(f.Stop)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", f.Address)
	return f
}

func TestRunUpdatesThenEnds(t *testing.T) {
	f := fake(t)
	code := Run([]string{"sh", "-c", "echo 10%; sleep 1.4; echo 50% compiling; sleep 1; echo 'error: boom'; exit 3"}, "")
	if code != 3 {
		t.Fatalf("exit %d", code)
	}
	notes := f.Notes()
	if len(notes) < 2 {
		t.Fatalf("notes %+v", notes)
	}
	live, last := notes[len(notes)-2], notes[len(notes)-1]
	if live.Hints["value"] != int32(50) || live.Hints["urgency"] != notify.Low || live.Timeout != 0 ||
		!strings.HasPrefix(live.Body, "50% · 0:0") || live.Replaces == 0 || !strings.HasPrefix(live.Summary, "sh -c echo 10%; sleep") {
		t.Errorf("live note %+v", live)
	}
	if !strings.HasPrefix(last.Body, "Failed with exit 3 after 0:02\nerror: boom") || last.Icon != "dialog-error" ||
		last.Replaces != live.Replaces || last.Hints["value"] != nil {
		t.Errorf("final note %+v", last)
	}
}

func TestRunStopButton(t *testing.T) {
	f := fake(t)
	go func() {
		for i := 0; i < 40; i++ {
			time.Sleep(100 * time.Millisecond)
			if n := f.Notes(); len(n) > 0 {
				f.Invoke(1, "cancel")
				return
			}
		}
	}()
	start := time.Now()
	code := Run([]string{"sleep", "30"}, "long sleep")
	notes := f.Notes()
	if code != 128+int(syscall.SIGTERM) || time.Since(start) > 5*time.Second {
		t.Fatalf("exit %d after %v", code, time.Since(start))
	}
	if last := notes[len(notes)-1]; last.Summary != "long sleep" || !strings.HasPrefix(last.Body, "Stopped after 0:01") {
		t.Errorf("final %+v", last)
	}
}

func TestHookWatch(t *testing.T) {
	f := fake(t)
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	done := make(chan struct{})
	go func() { watch(hookArgs{after: 1, shell: os.Getpid(), line: "paru -Syu"}); close(done) }()
	time.Sleep(2300 * time.Millisecond)
	os.WriteFile(statusFile(os.Getpid()), []byte("2"), 0o600)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not end")
	}
	notes := f.Notes()
	if len(notes) < 2 || notes[0].Summary != "paru -Syu" || notes[0].Body != "0:01" ||
		!strings.HasPrefix(notes[len(notes)-1].Body, "Failed with exit 2 after 0:02") {
		t.Fatalf("notes %+v", notes)
	}
}
