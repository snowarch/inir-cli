package activity

import (
	"bytes"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DefaultAfter is how long a command runs before the hook shows it.
const DefaultAfter = 10

// The shell starts the watcher in the background as the command starts and,
// at the next prompt, leaves the exit status where the watcher looks: no
// process the prompt waits for, no signal that could reach a reused pid.
var scripts = map[string]string{
	"bash": `# inir hook bash: commands that run longer than @AFTER@s get a live notification.
# INIR_HOOK_AFTER (seconds) and INIR_HOOK_IGNORE (more programs to leave alone) tune it.
__inir_hook_start() {
    [[ -n $__inir_hook_armed && -z $COMP_LINE ]] || return 0
    __inir_hook_armed=
    { '@INIR@' __hook watch --after @AFTER@ --shell $$ -- "$1" </dev/null >/dev/null 2>&1 & } 2>/dev/null
    __inir_hook_pid=$!
    disown $__inir_hook_pid 2>/dev/null
}
__inir_hook_end() {
    local s=$?
    if [[ -n $__inir_hook_pid ]]; then
        printf %s "$s" 2>/dev/null >"${XDG_RUNTIME_DIR:-/run/user/$UID}/inir-hook-$__inir_hook_pid"
        __inir_hook_pid=
    fi
    return $s
}
__inir_hook_arm() { __inir_hook_armed=1; }
if [[ ${preexec_functions+set} ]]; then
    preexec_functions+=(__inir_hook_start)
    precmd_functions=(__inir_hook_end "${precmd_functions[@]}" __inir_hook_arm)
else
    trap '__inir_hook_start "$BASH_COMMAND"' DEBUG
    PROMPT_COMMAND="__inir_hook_end${PROMPT_COMMAND:+; $PROMPT_COMMAND}; __inir_hook_arm"
fi
`,
	"zsh": `# inir hook zsh: commands that run longer than @AFTER@s get a live notification.
# INIR_HOOK_AFTER (seconds) and INIR_HOOK_IGNORE (more programs to leave alone) tune it.
__inir_hook_start() {
    setopt local_options no_monitor no_notify
    '@INIR@' __hook watch --after @AFTER@ --shell $$ -- "$1" </dev/null >/dev/null 2>&1 &!
    __inir_hook_pid=$!
}
__inir_hook_end() {
    local s=$?
    if [[ -n $__inir_hook_pid ]]; then
        print -rn -- "$s" 2>/dev/null >"${XDG_RUNTIME_DIR:-/run/user/$UID}/inir-hook-$__inir_hook_pid"
        __inir_hook_pid=
    fi
}
autoload -Uz add-zsh-hook
add-zsh-hook preexec __inir_hook_start
precmd_functions=(__inir_hook_end ${precmd_functions:#__inir_hook_end})
`,
	"fish": `# inir hook fish: commands that run longer than @AFTER@s get a live notification.
# INIR_HOOK_AFTER (seconds) and INIR_HOOK_IGNORE (more programs to leave alone) tune it.
function __inir_hook_start --on-event fish_preexec
    '@INIR@' __hook watch --after @AFTER@ --shell $fish_pid -- $argv[1] </dev/null >/dev/null 2>&1 &
    set -g __inir_hook_pid $last_pid
    disown $last_pid 2>/dev/null
end
function __inir_hook_end --on-event fish_postexec
    set -l s $status
    if set -q __inir_hook_pid[1]
        set -l run $XDG_RUNTIME_DIR
        set -q run[1]; or set run /run/user/(id -u)
        printf %s $s >$run/inir-hook-$__inir_hook_pid 2>/dev/null
        set -e __inir_hook_pid
    end
end
`,
}

// Shells the hook speaks.
var Shells = []string{"bash", "zsh", "fish"}

// Script is the hook for a shell, calling the binary at exe.
func Script(shell string, after int, exe string) (string, error) {
	s, ok := scripts[shell]
	if !ok {
		return "", fmt.Errorf("no hook for %q (bash, zsh or fish)", shell)
	}
	exe = strings.ReplaceAll(exe, "'", `'\''`)
	return strings.NewReplacer("@AFTER@", strconv.Itoa(max(after, 1)), "@INIR@", exe).Replace(s), nil
}

// Programs people sit in rather than wait for: a notification when they quit
// says nothing.
var interactive = strings.Fields(`vi vim nvim nano micro hx helix emacs kak less more most man info
	ssh mosh tmux zellij screen top htop btop btm atop nvtop watch fzf yazi ranger lf nnn mc mpv vlc
	python python3 ipython node bun deno irb ghci lua sqlite3 psql mysql gdb lldb claude codex aider`)

var wrappers = strings.Fields("sudo doas env time nice nohup command exec builtin noglob")

// Ignored says whether the hook leaves a command line alone.
func Ignored(line string) bool {
	words := strings.Fields(line)
	for len(words) > 0 && (slices.Contains(wrappers, words[0]) || strings.Contains(words[0], "=") ||
		strings.HasPrefix(words[0], "-")) {
		words = words[1:]
	}
	if len(words) == 0 {
		return true
	}
	name := filepath.Base(words[0])
	if name == "inir" && len(words) > 1 && words[1] == "run" {
		return true // it keeps its own notification
	}
	return slices.Contains(interactive, name) || slices.Contains(strings.Fields(os.Getenv("INIR_HOOK_IGNORE")), name)
}

type hookArgs struct {
	after, shell int
	line         string
}

func parseHook(args []string) (h hookArgs, ok bool) {
	h.after = DefaultAfter
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--" && i+1 < len(args):
			h.line = strings.Join(args[i+1:], " ")
			i = len(args)
		case a == "--after" && i+1 < len(args):
			i++
			h.after, _ = strconv.Atoi(args[i])
		case a == "--shell" && i+1 < len(args):
			i++
			h.shell, _ = strconv.Atoi(args[i])
		}
	}
	if v, err := strconv.Atoi(os.Getenv("INIR_HOOK_AFTER")); err == nil {
		h.after = v
	}
	h.after = max(h.after, 1)
	return h, h.shell > 0
}

// Hook runs `inir __hook watch`, what the shell snippets start.
func Hook(args []string) int {
	if len(args) == 0 || args[0] != "watch" {
		return 2
	}
	if h, ok := parseHook(args[1:]); ok {
		watch(h)
	}
	return 0
}

// statusFile is where the shell leaves the exit status for watcher pid.
func statusFile(pid int) string {
	run := os.Getenv("XDG_RUNTIME_DIR")
	if run == "" {
		run = fmt.Sprintf("/run/user/%d", os.Getuid())
	}
	return filepath.Join(run, "inir-hook-"+strconv.Itoa(pid))
}

// watch waits out the threshold, then keeps the notification of a command
// the shell is running until the shell leaves its exit status, or goes away.
func watch(h hookArgs) {
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	start := time.Now()
	file := statusFile(os.Getpid())
	ended := func() bool { _, err := os.Stat(file); return err == nil }
	status := func() int {
		b, _ := os.ReadFile(file)
		os.Remove(file)
		n, err := strconv.Atoi(strings.TrimSpace(string(b)))
		if err != nil {
			return 0
		}
		return n
	}
	threshold := time.NewTimer(time.Duration(h.after) * time.Second)
	poll := time.NewTicker(200 * time.Millisecond)
	defer poll.Stop()
	for waiting := true; waiting; {
		select {
		case <-sigs:
			return
		case <-poll.C:
			if ended() {
				status()
				return
			}
			if !alive(h.shell) {
				return
			}
		case <-threshold.C:
			waiting = Ignored(h.line)
		}
	}
	l := newLive(Title(h.line))
	l.update(time.Since(start), -1, "")
	events := l.events()
	stopped := false
	for {
		select {
		case <-poll.C:
			if ended() {
				l.finish(time.Since(start), status(), stopped, "")
				return
			}
			if !alive(h.shell) {
				l.dismiss()
				return
			}
			l.update(time.Since(start), -1, "")
		case <-sigs:
			l.dismiss()
			return
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if l.handle(ev) {
				stopped = interruptForeground(h.shell)
			}
		}
	}
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

// interruptForeground sends Ctrl-C's signal to the job the shell is waiting
// on (the foreground process group of its terminal).
func interruptForeground(shell int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", shell))
	if err != nil {
		return false
	}
	i := bytes.LastIndexByte(b, ')')
	if i < 0 || i+2 > len(b) {
		return false
	}
	f := strings.Fields(string(b[i+2:])) // state ppid pgrp session tty tpgid
	if len(f) < 6 {
		return false
	}
	pgrp, _ := strconv.Atoi(f[2])
	tpgid, _ := strconv.Atoi(f[5])
	if tpgid <= 0 || tpgid == pgrp {
		return false
	}
	return syscall.Kill(-tpgid, syscall.SIGINT) == nil
}
