# inir-cli

The `inir` command for the [iNiR](https://github.com/snowarch/iNiR) shell, as one Go binary.

It is the front door: what it knows it does itself, and every other command goes to the shell's `scripts/inir`.

- **IPC calls** to a running shell (`inir <target> <function> [args]`), about 2.5 times
  faster than through the bash launcher (35 ms against 88 ms for `inir colorMode get`,
  2026-11-14, Ryzen 7 5700G).
- **The shell's windows**: `inir settings-window [--toggle]`, `inir welcome` and `inir kill-dialog` start
  on the running shell's own runtime and environment, with no bash in between (a toggle closes Settings in
  160 ms, 270 ms through the launcher); with no shell running the launcher starts them.
- **Tab completion** for bash, zsh and fish (`inir completions install`). The scripts are
  small: on every Tab they ask `inir __complete`, which knows the launcher's commands and
  flags (`internal/cli`) and reads the installed shell's IPC surface, so they follow the
  shell without being regenerated.
- **Doctor**: the 25 checks of setup's doctor, step for step and with the same verdicts.
  `inir doctor --check` changes nothing, `--json` prints the report. A plain `inir doctor`
  repairs through the bash doctor, one failing step at a time (`setup doctor --only=N`), so
  the repairs are the same code as before.
- **The hub**: `inir hub` finds, installs, updates and removes what people make, and holds
  the tools the hub repo runs on itself (`new`, `check`, `validate`, `audit`, `bumps`,
  `build`, `submit`). `inir hub check` is what a pull request must pass, run on the
  author's machine first.
- **Packages**: `inir deps plan [--distro D] [--no-audio ...] [--json]` reads the shell's
  `sdata/deps.toml` (what iNiR installs per distro and install group) and prints the packages,
  AUR packages and providers a full install would use; `inir deps install` runs that plan.
- **Install and update wizards**: at a terminal and without `-y`, `inir install` and
  `inir update` put a wizard in front of `setup`, which still does the work.
- **Compositors**: iNiR runs on Niri and on Umbriel. `inir install` asks where it should run;
  `inir compositor status` says what runs and what is installed, `add umbriel` sets iNiR up
  there (your Niri keybinds and keyboard layout come along, `sync` brings them again) and
  `remove umbriel` leaves Umbriel as it is. The doctor wires every compositor you have.


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

iNiR requires the binary: setup fails without it, and the shell runs it directly (`Directories.inir`).
`scripts/inir` looks for it at `$INIR_CLI`, `~/.local/share/inir/cli/inir`, `/usr/local/lib/inir/inir` and
`/usr/lib/inir/inir` and execs it; the binary execs the shell's launcher back (`INIR_BASH_LAUNCHER`) for what is
not ported yet. `inir ipc TARGET FUNCTION ARGS` is an IPC call that no command name shadows. The shell repo's
`docs/adr/0006-cli-binary-behind-the-bash-launcher.md` and `docs/adr/0007-the-inir-binary-is-required.md` have
the design.

It reads the shell's IPC surface from the shell repo's `scripts/lib/ipc.json`, written from the `///`
comment above each IPC handler.

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
