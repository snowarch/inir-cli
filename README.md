# inir-cli

The `inir` command for the [iNiR](https://github.com/snowarch/iNiR) shell, as one Go binary.

It does three things itself and hands every other command to the shell's `scripts/inir`:

- **IPC calls** to a running shell (`inir <target> <function> [args]`), about three times
  faster than through the bash launcher.
- **Tab completion** for bash, zsh and fish (`inir completions install`). The scripts are
  small: on every Tab they ask `inir __complete`, which knows the launcher's commands and
  flags (`internal/cli`) and reads the installed shell's IPC surface, so they follow the
  shell without being regenerated.
- **Doctor**: the 25 checks of setup's doctor, step for step and with the same verdicts.
  `inir doctor --check` changes nothing, `--json` prints the report. A plain `inir doctor`
  repairs through the bash doctor, one failing step at a time (`setup doctor --only=N`), so
  the repairs are the same code as before.

Install, update and the helpers move here next.

## Where it sits

The binary goes next to the bash launcher, not in place of it: `scripts/inir` looks for it at
`$INIR_CLI`, `~/.local/share/inir/cli/inir`, `/usr/local/lib/inir/inir` and
`/usr/lib/inir/inir`, execs it, and the binary execs the same launcher back for what it does
not do. Without the binary, or with `INIR_NO_CLI=1`, nothing changes. The shell repo's
`docs/adr/0006-cli-binary-behind-the-bash-launcher.md` has the whole design.

It reads the shell's IPC surface from `scripts/lib/ipc.json`, which the shell repo generates.

## Tests

```sh
go build ./cmd/inir
go test ./...
test/parity.sh <iNiR checkout> ./inir                # same output as the bash launcher, three ways
test/shells.py ./inir <iNiR checkout>                # Tab in real bash, zsh and fish
INIR_SHELL_REPO=<checkout> go test ./internal/cli    # command table matches scripts/inir
```

The doctor is held to the bash one by the shell repo's install bench:
`dev/lifecycle/run.sh --cli ./inir --scenario update DISTRO` runs `inir doctor --check` before
and after the bash doctor and fails on any step where they disagree.
