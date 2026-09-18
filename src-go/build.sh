#!/bin/sh
# Rebuild the exe (runs on any OS that has Go).
set -e
cd "$(dirname "$0")"
GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui -s -w" \
  -o "../Glo.exe" .
echo "Done: ../Glo.exe"
