#!/bin/bash
# sample-gutenberg.sh — select N representative Gutenberg texts.
# Uses deterministic selection: sort by filename, take every Kth file.
# This gives a spread across the full alphabetical range of the corpus.
#
# Usage:
#   scripts/sample-gutenberg.sh CORPUS_DIR OUTPUT_DIR [N]
#
# Example:
#   scripts/sample-gutenberg.sh gutenberg_dammit/corpus/gutenberg-dammit-files dist/corpus/gutenberg 500

set -euo pipefail

CORPUS="${1:?usage: sample-gutenberg.sh CORPUS_DIR OUTPUT_DIR [N]}"
OUTPUT="${2:?usage: sample-gutenberg.sh CORPUS_DIR OUTPUT_DIR [N]}"
N="${3:-500}"

# Collect all .txt files, sorted.
mapfile -t FILES < <(find "$CORPUS" -name '*.txt' -size +200c | sort)

TOTAL=${#FILES[@]}
if [ "$TOTAL" -eq 0 ]; then
    echo "error: no .txt files found in $CORPUS" >&2
    exit 1
fi

# Take every Kth file for even spread.
K=$(( TOTAL / N ))
if [ "$K" -lt 1 ]; then K=1; fi

mkdir -p "$OUTPUT"

COPIED=0
for (( i=0; i < TOTAL && COPIED < N; i += K )); do
    cp "${FILES[$i]}" "$OUTPUT/"
    COPIED=$((COPIED + 1))
done

echo "sampled $COPIED files from $TOTAL (every ${K}th file)" >&2
du -sh "$OUTPUT" >&2
