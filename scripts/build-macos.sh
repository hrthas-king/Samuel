#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
[[ "$(uname -s)" == Darwin ]] || { echo "Building Samuel.app requires macOS and Xcode Command Line Tools." >&2; exit 1; }
version="${1:-$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' macos/Info.plist)}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "Expected a numeric version such as 1.2.7." >&2; exit 1; }
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
bundle="$work/Samuel.app"
mkdir -p "$bundle/Contents/"{MacOS,Helpers,Resources} "$work/Samuel.iconset" dist
cp macos/Info.plist "$bundle/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleShortVersionString $version" "$bundle/Contents/Info.plist"
/usr/libexec/PlistBuddy -c "Set :CFBundleVersion $version" "$bundle/Contents/Info.plist"
plutil -lint "$bundle/Contents/Info.plist"

for size in 16 32 128 256 512; do
  sips -z "$size" "$size" assets/icon.png --out "$work/Samuel.iconset/icon_${size}x${size}.png" >/dev/null
  sips -z "$((2*size))" "$((2*size))" assets/icon.png --out "$work/Samuel.iconset/icon_${size}x${size}@2x.png" >/dev/null
done
icon="$bundle/Contents/Resources/Samuel.icns"
if ! iconutil -c icns "$work/Samuel.iconset" -o "$icon"; then
  # Keep the same PNG representations when iconutil cannot create the container.
  python3 - "$work/Samuel.iconset" "$icon" <<'SAMUEL_ICNS'
from pathlib import Path
import struct
import sys

iconset, output = map(Path, sys.argv[1:])
representations = (
    (b"icp4", "16x16", 16), (b"ic11", "16x16@2x", 32),
    (b"icp5", "32x32", 32), (b"ic12", "32x32@2x", 64),
    (b"ic07", "128x128", 128), (b"ic13", "128x128@2x", 256),
    (b"ic08", "256x256", 256), (b"ic14", "256x256@2x", 512),
    (b"ic09", "512x512", 512), (b"ic10", "512x512@2x", 1024),
)
elements = []
for kind, name, side in representations:
    png = (iconset / f"icon_{name}.png").read_bytes()
    png_header = b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"
    if not png.startswith(png_header) or len(png) < 24 or struct.unpack_from(">II", png, 16) != (side, side):
        raise SystemExit(f"Invalid icon representation: {name}")
    elements.append(struct.pack(">4sI", kind, len(png) + 8) + png)
body = b"".join(elements)
output.write_bytes(struct.pack(">4sI", b"icns", len(body) + 8) + body)
SAMUEL_ICNS
fi
cp LICENSE "$bundle/Contents/Resources/LICENSE.txt"

sdk="$(xcrun --sdk macosx --show-sdk-path)"
for arch in arm64 x86_64; do
  goarch="$arch"
  if [[ "$arch" == x86_64 ]]; then goarch=amd64; fi
  GOOS=darwin GOARCH="$goarch" CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=$version" -o "$work/SamuelData-$arch" ./cmd/samuel-data
  xcrun swiftc -swift-version 5 -parse-as-library -O -sdk "$sdk" \
    -target "$arch-apple-macos13.0" macos/*.swift -o "$work/Samuel-$arch"
done
lipo -create "$work/Samuel-arm64" "$work/Samuel-x86_64" -output "$bundle/Contents/MacOS/Samuel"
lipo -create "$work/SamuelData-arm64" "$work/SamuelData-x86_64" -output "$bundle/Contents/Helpers/SamuelData"
for binary in "$bundle/Contents/MacOS/Samuel" "$bundle/Contents/Helpers/SamuelData"; do
  read -r -a architectures <<< "$(lipo -archs "$binary")"
  case "${architectures[*]}" in
    "arm64 x86_64"|"x86_64 arm64") ;;
    *) printf 'Expected arm64 and x86_64 in %s, found %s\n' "$binary" "${architectures[*]}" >&2; exit 1 ;;
  esac
done
codesign --force --sign - "$bundle/Contents/Helpers/SamuelData"
codesign --force --sign - "$bundle"
codesign --verify --deep --strict "$bundle"

archive="Samuel-$version-macos-universal.zip"
ditto -c -k --sequesterRsrc --keepParent "$bundle" "$work/$archive"
cp "$work/$archive" "dist/$archive"
printf 'Built %s\n' "dist/$archive"
