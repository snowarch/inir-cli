# inir-cli

The `inir` command for the [iNiR](https://github.com/snowarch/iNiR) shell, as one Go binary.

Today it makes IPC calls to a running shell itself (`inir <target> <function> [args]`, about
25 ms against 90 ms through the bash launcher), answers tab completion for bash, zsh and fish
(`inir completions install`), and hands every other command to the shell's `scripts/inir`.
Install, update, doctor and the helpers move here next.

Completion scripts are small: on every Tab they ask `inir __complete`, which knows the
launcher's commands and flags (`internal/cli`) and reads the installed shell's IPC surface, so
they follow the shell without being regenerated.

It reads the shell's IPC surface from `scripts/lib/ipc.json`, which the shell repo generates.

```sh
go build ./cmd/inir
go test ./...
test/parity.sh <path to an iNiR checkout> ./inir   # same output as the bash launcher
test/shells.py ./inir <path to an iNiR checkout>   # Tab in real bash, zsh and fish
INIR_SHELL_REPO=<checkout> go test ./internal/cli  # command table matches scripts/inir
```
