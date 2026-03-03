#!/bin/bash
input=$(cat)

cwd=$(echo "$input" | jq -r '.cwd')
pct=$(echo "$input" | jq -r '.context_window.used_percentage // 0' | cut -d. -f1)

# color by fill level: green <50, yellow 50-79, red 80+
if [ "$pct" -ge 80 ] 2>/dev/null; then
    c='\033[31m'
elif [ "$pct" -ge 50 ] 2>/dev/null; then
    c='\033[33m'
else
    c='\033[32m'
fi
r='\033[0m'

# 10-char bar
filled=$((pct / 10))
empty=$((10 - filled))
bar=""
[ "$filled" -gt 0 ] && bar=$(printf "%${filled}s" | tr ' ' '#')
[ "$empty" -gt 0 ] && bar="${bar}$(printf "%${empty}s" | tr ' ' '-')"

printf "\033[01;32m$(whoami)@$(hostname -s)\033[00m:\033[01;34m%s\033[00m ${c}${bar}${r} ${pct}%%" "$cwd"
