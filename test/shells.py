#!/usr/bin/env python3
"""Presses Tab in real bash, zsh and fish with the completion scripts loaded.

    test/shells.py <inir binary> <iNiR shell checkout>

Each shell runs in a pseudo-terminal with a throwaway HOME, the binary first
on PATH as `inir` and INIR_RUNTIME_DIR on the checkout, so completions read
its ipc.json. A case types a line, presses Tab (once or twice) and checks the
line it ends with or the candidates the shell lists.
"""
import os
import pty
import re
import select
import shutil
import subprocess
import sys
import tempfile
import time

binary, checkout = os.path.abspath(sys.argv[1]), os.path.abspath(sys.argv[2])
tmp = tempfile.mkdtemp(prefix="inir-shells.")
bindir = os.path.join(tmp, "bin")
os.makedirs(bindir)
shutil.copy(binary, os.path.join(bindir, "inir"))
env = {
    "PATH": bindir + ":/usr/bin:/bin",
    "HOME": os.path.join(tmp, "home"),
    "XDG_CONFIG_HOME": os.path.join(tmp, "config"),
    "XDG_DATA_HOME": os.path.join(tmp, "data"),
    "XDG_STATE_HOME": os.path.join(tmp, "state"),
    "XDG_CACHE_HOME": os.path.join(tmp, "cache"),
    "XDG_RUNTIME_DIR": os.path.join(tmp, "run"),
    "INIR_RUNTIME_DIR": checkout,
    "TERM": "dumb",
    "LANG": "C.UTF-8",
}
for d in ("HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_RUNTIME_DIR"):
    os.makedirs(env[d], exist_ok=True)


def script(shell):
    return subprocess.run([binary, "completions", shell], env=env, check=True,
                          capture_output=True, text=True).stdout


class Term:
    def __init__(self, argv):
        self.pid, self.fd = pty.fork()
        if self.pid == 0:
            os.execve(argv[0], argv, env)
        self.buf = b""

    def send(self, data):
        os.write(self.fd, data.encode())

    def read(self, until=None, timeout=5.0):
        end = time.time() + timeout
        while time.time() < end:
            r, _, _ = select.select([self.fd], [], [], 0.05)
            if r:
                try:
                    chunk = os.read(self.fd, 65536)
                except OSError:
                    break
                self.buf += chunk
                if until and re.search(until, self.buf.decode(errors="replace")):
                    break
            elif until is None and self.buf:
                break
        out, self.buf = self.buf.decode(errors="replace"), b""
        return re.sub(r"\x1b\[[0-9;?]*[a-zA-Z]|\x1b[=>]|\r", "", out)

    def close(self):
        try:
            os.kill(self.pid, 9)
            os.waitpid(self.pid, 0)
        except OSError:
            pass


def start(shell):
    path = os.path.join(tmp, "inir." + shell)
    with open(path, "w") as f:
        f.write(script(shell))
    if shell == "bash":
        t = Term(["/usr/bin/bash", "--norc", "--noprofile", "-i"])
        t.send(f"PS1='$ '; source {path}; bind -x '\"\\C-x\": printf \"[[%s]]\\n\" \"$READLINE_LINE\"'; "
               "bind 'set show-all-if-ambiguous off'; echo READY\n")
    elif shell == "zsh":
        fdir = os.path.join(tmp, "zfunc")
        os.makedirs(fdir, exist_ok=True)
        shutil.copy(path, os.path.join(fdir, "_inir"))
        t = Term(["/usr/bin/zsh", "-f", "-i"])
        t.send(f"PS1='$ '; fpath=({fdir} $fpath); autoload -Uz compinit; compinit -u -D; "
               "zstyle ':completion:*' list-prompt ''; zstyle ':completion:*:messages' format '%d'; LISTMAX=1000; "
               "show() {{ print -r -- \"[[$BUFFER]]\" }}; zle -N show; bindkey '^X' show; echo READY\n")
    t.read("READY\n", timeout=10)
    t.read(timeout=0.3)
    return t


failures = 0


def check(name, ok, detail=""):
    global failures
    if not ok:
        failures += 1
        print(f"FAIL {name}\n  {detail.strip()[:600]}")
    else:
        print(f"ok   {name}")


def line_after_tab(t, typed):
    """Types, presses Tab once, prints the line and clears it."""
    t.send(typed + "\t")
    time.sleep(0.4)
    t.send("\x18")
    out = t.read(r"\[\[.*\]\]", timeout=5)
    t.send("\x15")  # kill the line
    t.read(timeout=0.3)
    m = re.findall(r"\[\[(.*)\]\]", out)
    return m[-1] if m else None


def list_after_tabs(t, typed):
    """Types, presses Tab twice and returns what the shell listed."""
    t.send(typed + "\t")
    time.sleep(0.4)
    t.send("\t")
    out = t.read(timeout=1.5)
    t.send("\x03")
    t.read(timeout=0.5)
    return out


for shell in ("bash", "zsh"):
    t = start(shell)
    got = line_after_tab(t, "inir hu")
    check(f"{shell}: command completes with a space", got == "inir hub ", repr(got))
    got = line_after_tab(t, "inir iris theme appl")
    check(f"{shell}: a value ending in ':' keeps the cursor on it", got == "inir iris theme apply:", repr(got))
    got = line_after_tab(t, "inir iris theme apply:")
    check(f"{shell}: Tab after 'apply:' leaves it alone", got == "inir iris theme apply:", repr(got))
    got = line_after_tab(t, "inir alt-switcher op")
    check(f"{shell}: kebab alias resolves to its functions", got in ("inir alt-switcher open", "inir alt-switcher open"), repr(got))
    got = line_after_tab(t, "inir bind --exa")
    check(f"{shell}: bind flags", got == "inir bind --examples ", repr(got))
    got = line_after_tab(t, "inir bind Mod+O hu")
    check(f"{shell}: after the keys of a bind, any call", got == "inir bind Mod+O hub ", repr(got))
    got = line_after_tab(t, "inir run -c /us")
    check(f"{shell}: -c completes directories", got == "inir run -c /usr/", repr(got))
    got = line_after_tab(t, "inir ipc setting")
    check(f"{shell}: under ipc, targets a command shadows", got == "inir ipc settings", repr(got))
    out = list_after_tabs(t, "inir altSwitcher opens ")
    check(f"{shell}: function values listed", all(v in out for v in ("inir", "niri", "status")), out)
    out = list_after_tabs(t, "inir hub ")
    check(f"{shell}: hub lists client verbs and IPC functions", "sync" in out and "openItem" in out, out)
    if shell == "zsh":
        out = list_after_tabs(t, "inir doctor --")
        check("zsh: flags listed with their text", "--fix-abi" in out and "Rebuild Quickshell" in out, out)
        out = list_after_tabs(t, "inir bind ")
        check("zsh: bind with nothing typed says what to type", "keys, e.g. Mod+O" in out, out)
    t.close()


def fish(line):
    return subprocess.run(["/usr/bin/fish", "--no-config", "-c",
                           f"source {os.path.join(tmp, 'inir.fish')}; complete -C {line!r}"],
                          env=env, capture_output=True, text=True).stdout


with open(os.path.join(tmp, "inir.fish"), "w") as f:
    f.write(script("fish"))
out = fish("inir hu")
check("fish: command with its text", "hub\tCommunity hub: widgets, themes, web apps" in out, out)
out = fish("inir iris theme ap")
check("fish: values of a function", "apply:" in out.split("\n"), out)
out = fish("inir alt-switcher ")
check("fish: alias functions", "opens\t" in out, out)
out = fish("inir bind Mod+O iris ")
check("fish: after the keys of a bind, the call", "settings\t" in out, out)
out = fish("inir run -c /us")
check("fish: -c completes directories", "/usr/" in [l.split("\t")[0] for l in out.split("\n")], out)
out = fish("inir hub list --kind ")
check("fish: finite flag values", set(out.split()) >= {"widget", "theme", "iris-theme", "webapp"}, out)

shutil.rmtree(tmp, ignore_errors=True)
print(f"{failures} failed")
sys.exit(1 if failures else 0)
