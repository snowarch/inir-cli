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

It also plans installs: `inir deps plan [--distro D] [--no-audio ...] [--json]` reads the
shell's `sdata/deps.toml` (what iNiR installs per distro and install group) and prints the
packages, AUR packages and providers a full install would use. Running that plan, and the
rest of install and update, moves here next.


## Long commands and your look

- **`inir run -- CMD`** runs CMD right there (through a pty, so it still draws its own
  progress bars) and keeps one notification about it: the last percentage it printed as the
  standard `value` hint, or the time so far, plus a Stop button; then how it ended. Plain
  `inir run` still starts the shell.
- **`inir hook bash|zsh|fish`** prints a snippet for your rc file (`eval "$(inir hook bash)"`,
  `inir hook fish | source`): anything that runs longer than 10 seconds gets the same
  notification. Editors, pagers, ssh and friends are left alone (`INIR_HOOK_IGNORE` adds more,
  `INIR_HOOK_AFTER` changes the seconds). Stop sends Ctrl-C to the job.
- Both speak the notification spec over D-Bus directly (no notify-send, no D-Bus library), so
  any notification server shows them.
- **`inir look export [FILE]`** writes everything that makes your desktop look like yours
  (family, iRiS Design, colors, appearance, bar, dock, pieces, widgets, wallpaper path and a
  link when it came from Wallhaven) as what differs from the shell's defaults. Your pinned
  apps, paths, screens and accounts stay out.
- **`inir look import FILE|URL`** applies one (`--dry-run` lists every setting it would
  change); keys outside the look or of the wrong type are refused. Each import takes a
  snapshot first: **`inir look history`** lists them, **`inir look restore N`** goes back,
  byte for byte.

## Where it sits

iNiR is a few pieces, each with one job: [iNiR](https://github.com/snowarch/iNiR) is the shell, in QML;
[qs-rs](https://github.com/snowarch/qs-rs) runs it; [inir-accel](https://github.com/snowarch/inir-accel) does what the
shell runs in the background; this is the command you type; [inir-hub](https://github.com/snowarch/inir-hub) holds
the families, themes and widgets you install, served at [inir.space/hub](https://inir.space/hub).

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

## License

GPL-3.0, like iNiR, whose `scripts/inir` this started from (`LICENSE`).
