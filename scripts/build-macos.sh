#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
[[ "$(uname -s)" == Darwin ]] || { echo "Building Samuel.app requires macOS and Xcode Command Line Tools." >&2; exit 1; }
version="${1:-1.2.5}"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "Expected a numeric version such as 1.2.5." >&2; exit 1; }
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
iconutil -c icns "$work/Samuel.iconset" -o "$bundle/Contents/Resources/Samuel.icns"
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
  lipo "$binary" -verify_arch arm64 x86_64
done
codesign --force --sign - "$bundle/Contents/Helpers/SamuelData"
codesign --force --sign - "$bundle"
codesign --verify --deep --strict "$bundle"

archive="Samuel-$version-macos-universal.zip"
ditto -c -k --sequesterRsrc --keepParent "$bundle" "$work/$archive"
cp "$work/$archive" "dist/$archive"
printf 'Built %s\n' "dist/$archive"
