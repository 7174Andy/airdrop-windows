#!/bin/sh
# Dev build of the CLI and the desktop app into dist/: the Mac ones natively (the app as
# airdrop.app), the Windows ones cross-compiled. Usage: ./build.sh [mac|windows]
set -eu
cd "$(dirname "$0")"
target=${1:-all}
case $target in mac | windows | all) ;; *) echo "usage: $0 [mac|windows]" >&2; exit 2 ;; esac
mkdir -p dist
if [ "$target" != windows ]; then
	go build -o dist/airdrop ./cmd/airdrop
	CGO_LDFLAGS=-Wl,-w app/bundle.sh dist # -w: hush ld's harmless macOS-version warnings from Wails
fi
if [ "$target" != mac ]; then
	GOOS=windows GOARCH=amd64 go build -o dist/airdrop.exe ./cmd/airdrop
	GOOS=windows GOARCH=amd64 go build -ldflags=-H=windowsgui -o dist/airdrop-app.exe ./app
fi
ls -1 dist
