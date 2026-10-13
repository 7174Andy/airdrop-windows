#!/bin/sh
# Builds airdrop.app for macOS: the app binary plus Info.plist and icon, ad-hoc signed.
# A bundle is what gets the app native notifications and its own name and icon in
# Login Items. Usage: app/bundle.sh [output dir, default .]
set -eu
out="$(cd "${1:-.}" && pwd)/airdrop.app" # resolved before the cd below
cd "$(dirname "$0")"
rm -rf "$out"
mkdir -p "$out/Contents/MacOS" "$out/Contents/Resources"
go build -o "$out/Contents/MacOS/airdrop" .
cp icons/icon.icns "$out/Contents/Resources/"
cat > "$out/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>airdrop</string>
	<key>CFBundleIdentifier</key><string>dev.airdrop-windows.app</string>
	<key>CFBundleExecutable</key><string>airdrop</string>
	<key>CFBundleIconFile</key><string>icon</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>CFBundleShortVersionString</key><string>0.4</string>
	<key>CFBundleVersion</key><string>4</string>
	<key>LSMinimumSystemVersion</key><string>11.0</string>
	<key>LSUIElement</key><true/>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST
codesign --force --sign - "$out"
echo "built $out"
