# inir-cli

The `inir` command for the [iNiR](https://github.com/snowarch/iNiR) shell, as one Go binary.

Today it makes IPC calls to a running shell itself (`inir <target> <function> [args]`, about
25 ms against 90 ms through the bash launcher) and hands every other command to the shell's
`scripts/inir`. Install, update, doctor and the helpers move here next.

It reads the shell's IPC surface from `scripts/lib/ipc.json`, which the shell repo generates.

```sh
go build ./cmd/inir
go test ./...
test/parity.sh <path to an iNiR checkout> ./inir   # same output as the bash launcher
```
