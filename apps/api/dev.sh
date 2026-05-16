#!/usr/bin/env bash
# Process runner: starts all pre-built Go services concurrently.
# Called by air after a successful build. Air handles file watching and rebuild.

cd "$(dirname "$0")" || exit 1

# Colors
BLUE='\033[0;34m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
MAGENTA='\033[0;35m'
CYAN='\033[0;36m'
GRAY='\033[0;90m'
NC='\033[0m'

# Tracked binary PIDs only (not the label subshells).
pids=()

cleanup() {
    # Clear traps first to avoid re-entry if kill propagates a signal.
    trap - SIGINT SIGTERM
    for pid in "${pids[@]}"; do
        kill "$pid" 2>/dev/null
    done
    wait 2>/dev/null
}
# Only INT/TERM — not EXIT. An EXIT trap combined with killing children
# would race with air's process-group handling and could kill air itself.
trap cleanup SIGINT SIGTERM

label() {
    local color=$1 name=$2
    while IFS= read -r line; do
        # Use %b for colors, %s for all dynamic content — prevents printf
        # from interpreting stray % in log lines (e.g. "100%", "%s").
        printf '%b%s %b|%b %s\n' "$color" "$name" "$GRAY" "$NC" "$line"
    done
}

start() {
    local color=$1 name=$2 bin=$3
    if [[ ! -x $bin ]]; then
        printf '%b%s %b|%b binary not found or not executable: %s\n' \
            "$color" "$name" "$GRAY" "$NC" "$bin" >&2
        return 1
    fi
    # Process substitution (not pipe) so $! captures the binary's PID,
    # not the label subshell's. This lets us kill and wait on the right thing.
    "$bin" > >(label "$color" "$name") 2>&1 &
    pids+=("$!")
}

printf '\n'
printf '%boauth%b      :9277\n'    "$BLUE"    "$NC"
printf '%bgalgame%b    :9280\n'    "$GREEN"   "$NC"
printf '%bartifact%b   :9279\n'    "$YELLOW"  "$NC"
printf '%bmoderation%b :9281\n'    "$MAGENTA" "$NC"
printf '%bimage%b      :9278\n'    "$CYAN"    "$NC"
printf '\n'

start "$BLUE"    "oauth     " ./tmp/oauth      || { cleanup; exit 1; }
start "$GREEN"   "galgame   " ./tmp/galgame    || { cleanup; exit 1; }
start "$YELLOW"  "artifact  " ./tmp/artifact   || { cleanup; exit 1; }
start "$MAGENTA" "moderation" ./tmp/moderation || { cleanup; exit 1; }
start "$CYAN"    "image     " ./tmp/image      || { cleanup; exit 1; }

# Fail fast: if any single service exits, tear the rest down so air rebuilds.
# Restricting to $pids means we don't react to the label subshell finishing
# by itself (it exits on EOF after the binary does, which is the normal order).
wait -n "${pids[@]}"
cleanup
exit 1
