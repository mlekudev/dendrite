#!/bin/bash
# make-dist.sh — build the reproducible distribution tarball.
# Run from the dendrite repo root.
#
# Usage:
#   scripts/make-dist.sh
#
# Produces: dendrite-benchmark-v1.tar.gz
set -euo pipefail

VERSION="v1"
NAME="dendrite-benchmark-${VERSION}"
STAGING="/tmp/${NAME}"

echo "=== building distribution: ${NAME} ==="

# Clean staging area.
rm -rf "${STAGING}"
mkdir -p "${STAGING}"

# ── Source code ──
echo "copying source..."
cp go.mod go.sum "${STAGING}/"
cp -r cmd/benchmark cmd/recognise cmd/sentry "${STAGING}/"
mkdir -p "${STAGING}/cmd"
mv "${STAGING}/benchmark" "${STAGING}/recognise" "${STAGING}/sentry" "${STAGING}/cmd/"
cp -r pkg "${STAGING}/"

# ── Trained lattices ──
echo "copying trained lattices..."
cp -r .recognise_db "${STAGING}/"
cp -r .troll_db "${STAGING}/"

# ── Corpus data ──
echo "copying corpus data..."
if [ -d dist/corpus ]; then
    cp -r dist/corpus "${STAGING}/"
else
    echo "warning: dist/corpus not found. Run sample scripts first." >&2
fi

if [ -f dist/raid-sample.csv ]; then
    cp dist/raid-sample.csv "${STAGING}/"
else
    echo "warning: dist/raid-sample.csv not found. Run sample-raid.go first." >&2
fi

# ── Scripts and docs ──
cp dist/README.md "${STAGING}/README.md"
cp dist/run-benchmark.sh "${STAGING}/run-benchmark.sh"
chmod +x "${STAGING}/run-benchmark.sh"

# ── Pre-built binaries ──
echo "building static binaries..."
for PAIR in "linux:amd64" "linux:arm64" "darwin:amd64" "darwin:arm64"; do
    OS="${PAIR%%:*}"
    ARCH="${PAIR##*:}"
    OUT="${STAGING}/bin/benchmark-${OS}-${ARCH}"
    echo "  ${OS}/${ARCH}..."
    CGO_ENABLED=0 GOOS="${OS}" GOARCH="${ARCH}" go build -o "${OUT}" ./cmd/benchmark
done

# ── Create tarball ──
echo "creating tarball..."
tar -czf "${NAME}.tar.gz" -C /tmp "${NAME}"

SIZE=$(du -sh "${NAME}.tar.gz" | cut -f1)
echo "=== done: ${NAME}.tar.gz (${SIZE}) ==="

# Cleanup staging.
rm -rf "${STAGING}"
