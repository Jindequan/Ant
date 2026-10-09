#!/bin/sh
# Builds Ant's icons from the SVG sources in assets/brand (needs
# rsvg-convert; iconutil on macOS for the .icns). Run from the repo root:
#   sh scripts/brand.sh
set -eu
cd "$(dirname "$0")/../assets/brand"

# the app icon, every size macOS asks for
rm -rf AppIcon.iconset && mkdir AppIcon.iconset
for spec in 16:16x16 32:16x16@2x 32:32x32 64:32x32@2x 128:128x128 256:128x128@2x 256:256x256 512:256x256@2x 512:512x512 1024:512x512@2x; do
  px=${spec%%:*}; name=${spec#*:}
  rsvg-convert -w "$px" -h "$px" app-icon.svg -o "AppIcon.iconset/icon_$name.png"
done
command -v iconutil >/dev/null && iconutil -c icns AppIcon.iconset -o Ant.icns

# the menu bar: a template image (black on transparent; macOS tints it)
mkdir -p menubar
sed 's/currentColor/#000000/' ant.svg > menubar/AntTemplate.svg
rsvg-convert -w 18 -h 18 menubar/AntTemplate.svg -o menubar/AntTemplate.png
rsvg-convert -w 36 -h 36 menubar/AntTemplate.svg -o menubar/AntTemplate@2x.png
echo "built AppIcon.iconset, Ant.icns, menubar/AntTemplate*.png"
