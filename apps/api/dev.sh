#!/usr/bin/env bash
# Process runner: starts all pre-built Go services concurrently
# Called by air after successful build. Air handles file watching and rebuild.

cd "$(dirname "$0")"

cleanup() {
    kill 0 2>/dev/null
    wait 2>/dev/null
    exit 0
}
trap cleanup SIGINT SIGTERM EXIT

# Colors
BLUE='\033[0;34m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
MAGENTA='\033[0;35m'
GRAY='\033[0;90m'
NC='\033[0m'

label() {
    local color=$1 name=$2
    while IFS= read -r line; do
        printf "${color}%s ${GRAY}|${NC} %s\n" "$name" "$line"
    done
}

printf "\n"
printf "${BLUE}oauth${NC}      :9277\n"
printf "${GREEN}galgame${NC}    :9280\n"
printf "${YELLOW}artifact${NC}   :9279\n"
printf "${MAGENTA}moderation${NC} :9281\n"
printf "\n"

./tmp/oauth      2>&1 | label "$BLUE"    "oauth     " &
./tmp/galgame    2>&1 | label "$GREEN"   "galgame   " &
./tmp/artifact   2>&1 | label "$YELLOW"  "artifact  " &
./tmp/moderation 2>&1 | label "$MAGENTA" "moderation" &

wait
