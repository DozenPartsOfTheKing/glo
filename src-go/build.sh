#!/bin/sh
# Пересборка exe (запускается с любой ОС, где есть Go).
set -e
cd "$(dirname "$0")"
GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui -s -w" \
  -o "../Glasspad.exe" .
echo "Готово: ../Glasspad.exe"
