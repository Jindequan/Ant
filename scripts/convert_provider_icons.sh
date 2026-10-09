#!/bin/sh
# convert_icons.sh <downloads dir> <work dir> <out dir>: each provider's icon
# to a 64 px PNG (trimmed, 56 px content, transparent padding).
set -u
DL=$1; WORK=$2; OUT=$3
mkdir -p "$WORK" "$OUT"
for dir in "$DL"/*/; do
  id=$(basename "$dir")
  f=$(ls "$dir"icon.* 2>/dev/null | head -1)
  [ -n "$f" ] || continue
  raw="$WORK/$id-raw.png"
  case "$f" in
    *.svg) rsvg-convert -w 256 -h 256 --keep-aspect-ratio "$f" -o "$raw" ;;
    *.ico)
      best=$(magick identify -format "%p %w\n" "$f" 2>/dev/null | sort -k2 -n | tail -1 | cut -d' ' -f1)
      magick "${f}[${best:-0}]" "$raw" ;;
    *) magick "${f}[0]" "$raw" ;;
  esac || { echo "$id: could not decode"; continue; }
  magick "$raw" -background none -trim +repage -resize 56x56 -gravity center -extent 64x64 -strip "PNG32:$OUT/$id.png"
  echo "$id $(magick identify -format '%wx%h' "$raw")"
done
