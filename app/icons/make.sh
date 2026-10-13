#!/bin/sh
# Regenerates the icon files from icon.svg and tray.svg; run it after editing either.
# Needs rsvg-convert (brew install librsvg) and macOS iconutil. The outputs are committed,
# so builds don't need these tools.
set -eu
cd "$(dirname "$0")"
iconset=$(mktemp -d)/icon.iconset
mkdir -p "$iconset"
for s in 16 32 128 256 512; do
	rsvg-convert -w $s -h $s icon.svg -o "$iconset/icon_${s}x${s}.png"
	rsvg-convert -w $((s * 2)) -h $((s * 2)) icon.svg -o "$iconset/icon_${s}x${s}@2x.png"
done
iconutil -c icns "$iconset" -o icon.icns                  # macOS .app bundle
rsvg-convert -w 44 -h 44 tray.svg -o tray-mac.png      # menu bar template image (22pt @2x)
rsvg-convert -w 64 -h 64 icon.svg -o tray.png          # Windows tray
# Windows .exe icon + manifest, linked in automatically because the .syso sits in package main.
go run github.com/tc-hib/go-winres@v0.3.3 simply --icon "$iconset/icon_256x256.png" --manifest gui --arch amd64,arm64 --out ../rsrc
