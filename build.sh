#!/usr/bin/env sh
# Cross-compiles Counterbuild.exe for 64-bit Windows.
set -e
cd "$(dirname "$0")"
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-H=windowsgui -s -w" -o Counterbuild.exe .
echo "Built Counterbuild.exe"
