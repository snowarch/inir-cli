#!/usr/bin/env bash
# Same output and exit code as the bash launcher, for every IPC target, also
# when the launcher hands over to the binary.
#   test/parity.sh <inir shell checkout> <inir binary>
# Runs with a throwaway HOME and runtime dir, so no shell is running and
# nothing reaches the real session. Only calls that never start the shell.
set -euo pipefail

shell_repo="$(cd "$1" && pwd)"
go_inir="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
bash_inir="$shell_repo/scripts/inir"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
export HOME="$tmp/home" XDG_CONFIG_HOME="$tmp/config" XDG_STATE_HOME="$tmp/state" \
       XDG_CACHE_HOME="$tmp/cache" XDG_DATA_HOME="$tmp/data" XDG_RUNTIME_DIR="$tmp/run" \
       INIR_RUNTIME_DIR="$shell_repo"
unset WAYLAND_DISPLAY NIRI_SOCKET DBUS_SESSION_BUS_ADDRESS
mkdir -p "$HOME" "$XDG_RUNTIME_DIR"

calls=(--help help version "no-such-target foo" completions "completions nosuch" "completions install nosuch")
while IFS= read -r target; do
  calls+=("$target" "$target --help" "$target noSuchFunction")
done < <(python3 -c 'import json,sys; [print(t["name"]) for t in json.load(open(sys.argv[1]))["targets"]]' \
           "$shell_repo/scripts/lib/ipc.json")
while IFS= read -r alias; do
  calls+=("$alias --help")
done < <(python3 -c 'import json,sys; [print(a) for a in json.load(open(sys.argv[1]))["aliases"]]' \
           "$shell_repo/scripts/lib/ipc.json")

# Each call three ways: the bash launcher alone, the binary, and the bash
# launcher with the binary installed (it hands over, the binary hands back).
failures=0
for call in "${calls[@]}"; do
  read -r -a argv <<<"$call"
  set +e
  expected="$(INIR_NO_CLI=1 "$bash_inir" "${argv[@]}" 2>&1)"; expected_rc=$?
  actual="$("$go_inir" "${argv[@]}" 2>&1)"; actual_rc=$?
  front="$(INIR_CLI="$go_inir" "$bash_inir" "${argv[@]}" 2>&1)"; front_rc=$?
  set -e
  if [[ "$front" != "$actual" || $front_rc != "$actual_rc" ]]; then
    printf 'DIFF through scripts/inir: inir %s (binary rc=%s, front door rc=%s)\n' "$call" "$actual_rc" "$front_rc"
    diff <(printf '%s\n' "$actual") <(printf '%s\n' "$front") | head -6
    failures=$((failures + 1))
  fi
  if [[ "$expected" != "$actual" || $expected_rc != "$actual_rc" ]]; then
    printf 'DIFF: inir %s (bash rc=%s, go rc=%s)\n' "$call" "$expected_rc" "$actual_rc"
    diff <(printf '%s\n' "$expected") <(printf '%s\n' "$actual") | head -6
    failures=$((failures + 1))
  fi
done
printf '%d calls, %d differ\n' "${#calls[@]}" "$failures"
((failures == 0))
