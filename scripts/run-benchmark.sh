#!/bin/bash
# run-benchmark.sh — download data, build, and run the full benchmark.
# One command. Requires: Go 1.24+, curl.
set -euo pipefail

cd "$(dirname "$0")/.."
BASE="https://mleku.dev/pics/dendrite"

# Download trained lattices if missing.
if [ ! -d .recognise_db ]; then
  echo "downloading trained lattices (276K)..."
  curl -sL "$BASE/dendrite-lattices.tar.gz" | tar xz
fi

# Download corpus and RAID data if missing.
if [ ! -d corpus ]; then
  echo "downloading corpus + RAID data (70MB)..."
  curl -sL "$BASE/dendrite-corpus.tar.gz" | tar xz
fi

# Build.
echo "building benchmark..."
go build -o benchmark ./cmd/benchmark

# Run.
echo "running benchmark..."
echo
./benchmark \
  -memory .recognise_db \
  -troll-memory .troll_db \
  -corpus corpus/gutenberg \
  -claude-corpus corpus/claude \
  -raid raid-sample.csv \
  -raid-samples 100 \
  -max 500 \
  -summary
