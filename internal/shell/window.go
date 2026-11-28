package shell

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Instances lists the live qs processes that run the QML file at path, a shell.qml or one of the windows beside it.
// Quickshell indexes instances by the md5 of the path it was given (by-path/<md5> -> by-shell/<dir> holding the
// instance ids) and each process by its pid (by-pid/<pid> -> by-id/<id>); a link left by a process that died, or
// whose pid went to another program, is not counted.
func Instances(path string) []int {
	run := filepath.Join(runtimeDir(), "quickshell")
	sum := md5.Sum([]byte(path))
	byShell, err := filepath.EvalSymlinks(filepath.Join(run, "by-path", hex.EncodeToString(sum[:])))
	if err != nil {
		return nil
	}
	entries, err := os.ReadDir(byShell)
	if err != nil {
		return nil
	}
	ids := make(map[string]bool, len(entries))
	for _, e := range entries {
		ids[e.Name()] = true
	}
	links, _ := filepath.Glob(filepath.Join(run, "by-pid", "*"))
	var pids []int
	for _, link := range links {
		target, err := os.Readlink(link)
		if err != nil || !ids[filepath.Base(target)] {
			continue
		}
		pid, err := strconv.Atoi(filepath.Base(link))
		if err != nil {
			continue
		}
		if _, ok := Exe(strconv.Itoa(pid)); ok {
			pids = append(pids, pid)
		}
	}
	return pids
}

// Environ is the environment a process started with.
func Environ(pid int) ([]string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil {
		return nil, err
	}
	var env []string
	for _, kv := range strings.Split(string(data), "\x00") {
		if strings.Contains(kv, "=") {
			env = append(env, kv)
		}
	}
	return env, nil
}
