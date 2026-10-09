#!/bin/sh
# Builds bin/Ant.app: the menu bar app (app/macos/AntApp.swift) with Ant's
# server inside, its icon, and the menu bar ant. Needs Go and the Xcode
# command line tools (swiftc). Run from the repo root:
#   sh scripts/build-app.sh
set -eu
cd "$(dirname "$0")/.."
APP=bin/Ant.app
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

(cd server && go build -o "../$APP/Contents/Resources/ant-server" ./cmd/ant-server)
swiftc -O -o "$APP/Contents/MacOS/Ant" app/macos/AntApp.swift -framework AppKit
cp assets/brand/Ant.icns "$APP/Contents/Resources/Ant.icns"
cp assets/brand/menubar/AntTemplate.png assets/brand/menubar/AntTemplate@2x.png "$APP/Contents/Resources/"

VERSION=$(git describe --always --dirty 2>/dev/null || echo dev)
cat > "$APP/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key><string>Ant</string>
  <key>CFBundleDisplayName</key><string>Ant</string>
  <key>CFBundleIdentifier</key><string>local.ant.app</string>
  <key>CFBundleExecutable</key><string>Ant</string>
  <key>CFBundleIconFile</key><string>Ant</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>0.1</string>
  <key>CFBundleVersion</key><string>$VERSION</string>
  <key>LSMinimumSystemVersion</key><string>13.0</string>
  <key>LSUIElement</key><true/>
  <key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
EOF
# an ad-hoc signature: macOS remembers the app's Keychain permissions
codesign --force --deep --sign - "$APP" >/dev/null
echo "built $APP"
