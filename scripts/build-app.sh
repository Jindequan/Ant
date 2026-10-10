#!/bin/sh
# Local build: sh scripts/build-app.sh
# Universal signed release: ANT_UNIVERSAL=1 ANT_VERSION=0.1.0 ANT_BUILD=1
#   ANT_APP_PATH=bin/release/Ant.app ANT_SIGN_IDENTITY="Developer ID Application: …"
#   sh scripts/build-app.sh
set -eu
cd "$(dirname "$0")/.."
APP=${ANT_APP_PATH:-bin/Ant.app}
VERSION=${ANT_VERSION:-0.1.0}
BUILD=${ANT_BUILD:-1}
MIN_OS=${ANT_MIN_OS:-13.0}
SIGN_IDENTITY=${ANT_SIGN_IDENTITY:--}
case "$APP" in bin/*.app) ;; *) echo "ANT_APP_PATH must be under bin/ and end in .app" >&2; exit 1;; esac
case "$VERSION:$BUILD:$MIN_OS" in *[!0-9.:]*) echo "Versions must be numeric" >&2; exit 1;; esac
WORK=$(mktemp -d "${TMPDIR:-/tmp}/ant-build.XXXXXX")
trap 'rm -rf "$WORK"' EXIT HUP INT TERM
rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"
if [ "${ANT_UNIVERSAL:-0}" = 1 ]; then ARCHS="arm64 x86_64"; else ARCHS=$(uname -m); fi
for ARCH in $ARCHS; do
  case "$ARCH" in arm64) GO_ARCH=arm64;; x86_64) GO_ARCH=amd64;; *) echo "Unsupported architecture: $ARCH" >&2; exit 1;; esac
  (cd server && CGO_ENABLED=0 GOOS=darwin GOARCH="$GO_ARCH" go build -trimpath -o "$WORK/server-$ARCH" ./cmd/ant-server)
  xcrun swiftc -O -swift-version 5 -target "$ARCH-apple-macosx$MIN_OS" -parse-as-library \
    -o "$WORK/app-$ARCH" app/macos/PanelTokens.swift app/macos/AntApp.swift -framework AppKit -framework SwiftUI
done
if [ "${ANT_UNIVERSAL:-0}" = 1 ]; then
  lipo -create "$WORK/app-arm64" "$WORK/app-x86_64" -output "$APP/Contents/MacOS/Ant"
  lipo -create "$WORK/server-arm64" "$WORK/server-x86_64" -output "$APP/Contents/Resources/ant-server"
else
  cp "$WORK/app-$ARCH" "$APP/Contents/MacOS/Ant"
  cp "$WORK/server-$ARCH" "$APP/Contents/Resources/ant-server"
fi
cp assets/brand/Ant.icns "$APP/Contents/Resources/Ant.icns"
cp assets/brand/menubar/AntTemplate.png assets/brand/menubar/AntTemplate@2x.png "$APP/Contents/Resources/"
mkdir -p "$APP/Contents/Resources/logos"
cp server/internal/webui/static/logos/*.png "$APP/Contents/Resources/logos/"
cat > "$APP/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleName</key><string>Ant</string>
<key>CFBundleDisplayName</key><string>Ant</string>
<key>CFBundleIdentifier</key><string>local.ant.app</string>
<key>CFBundleExecutable</key><string>Ant</string>
<key>CFBundleIconFile</key><string>Ant</string>
<key>CFBundlePackageType</key><string>APPL</string>
<key>CFBundleShortVersionString</key><string>$VERSION</string>
<key>CFBundleVersion</key><string>$BUILD</string>
<key>LSMinimumSystemVersion</key><string>$MIN_OS</string>
<key>LSUIElement</key><true/>
<key>NSHighResolutionCapable</key><true/>
</dict></plist>
EOF
if [ "$SIGN_IDENTITY" = - ]; then
  codesign --force --sign - "$APP/Contents/Resources/ant-server"
  codesign --force --sign - "$APP"
else
  codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" "$APP/Contents/Resources/ant-server"
  codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" "$APP"
fi
codesign --verify --deep --strict "$APP"
echo "built $APP ($VERSION, macOS $MIN_OS+)"
