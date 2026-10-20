package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/snowarch/inir-cli/internal/doctor"
	"github.com/snowarch/inir-cli/internal/ui"
)

// runDoctor is `inir doctor [--check] [--json] [-y] [--only=N,M]`. It checks
// in Go and repairs through the bash doctor; --check changes nothing. The
// launcher keeps --fix-abi, --perf and -c.
func runDoctor(args []string) (int, bool) {
	opt := doctor.Options{Fix: true, Out: os.Stdout, Pretty: true}
	asJSON := false
	for _, a := range args {
		switch {
		case a == "--check":
			opt.Fix = false
		case a == "--json":
			asJSON = true
		case a == "--offline":
			opt.Offline = true
		case a == "-y" || a == "--yes" || a == "-n" || a == "--noninteractive" || a == "--non-interactive":
			opt.Yes = true
		case strings.HasPrefix(a, "--only="):
			for _, f := range strings.Split(strings.TrimPrefix(a, "--only="), ",") {
				n, err := strconv.Atoi(f)
				if err != nil || n < 1 || n > len(doctor.Steps) {
					ui.Log("inir doctor").Error("no such step", "step", f, "steps", fmt.Sprintf("1-%d", len(doctor.Steps)))
					return 2, true
				}
				opt.Only = append(opt.Only, n)
			}
		case a == "-h" || a == "--help":
			fmt.Print(`Usage: inir doctor [--check] [--json] [-y] [--offline] [--only=N,M]

Checks the install and fixes what it can.
  --check     only check; change nothing
  --json      print the report as JSON
  -y          no questions
  --only=N,M  just these steps
  --offline   do not fetch the repo's remote
  --fix-abi   rebuild Quickshell against the installed Qt
  --perf      snapshot of graphics and media at runtime
`)
			return 0, true
		default:
			return 0, false // --fix-abi, --perf, -c: the launcher's
		}
	}
	if asJSON {
		opt.Pretty = false
		opt.Out = os.Stderr
	}
	r := doctor.Run(doctor.NewEnv(), opt)
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(r)
	}
	if r.OK() {
		return 0, true
	}
	return 1, true
}
