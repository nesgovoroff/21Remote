#!/bin/sh
# SPDX-License-Identifier: AGPL-3.0-or-later
# Builds "21 Remote.app" for macOS 10.10-10.15 (Intel) without Xcode.
# Works on Linux or macOS with: Go 1.12 (the last Go that runs on macOS 10.10;
# set GO=/path/to/go1.12/bin/go), clang, ld64.lld, python3 (+Pillow for the
# icon) and esbuild (or npx).
set -eu
cd "$(dirname "$0")/.."
VERSION=${VERSION:-$(cat VERSION)}
BUILD=${BUILD:-$VERSION}
OUT=dist
APP="$OUT/21 Remote.app"
TMP=$(mktemp -d)
GO=${GO:-go}
MINOS=10.10
LD64=$(command -v ld64.lld || command -v ld64.lld-18 || true)
ON_MAC=$([ "$(uname)" = Darwin ] && echo 1 || echo 0)

rm -rf "$APP"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

case "$("$GO" version)" in
  *go1.12*) ;;
  *) echo "build.sh: this branch needs Go 1.12 (GO=$GO is $("$GO" version)); newer Go does not run on macOS 10.10" >&2; exit 1 ;;
esac

echo "• user interface for old WebKit"
python3 macos/legacyweb.py

echo "• engine (Go 1.12, no cgo)"
GOFLAGS= GO111MODULE=on GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 "$GO" build -ldflags "-s -w -X main.version=$VERSION -X main.updateRepo=${UPDATE_REPO:-}" \
  -o "$APP/Contents/MacOS/remote21-core" ./cmd/remote21-core

echo "• window shell (C + Objective-C runtime)"
for a in x86_64; do
  for src in shell midi; do
    clang -target $a-apple-macos$MINOS -ffreestanding -fno-stack-protector -O2 -Wall -Wno-unused-parameter -c macos/$src.c -o "$TMP/$src-$a.o"
  done
  if [ "$ON_MAC" = 1 ]; then
    # on a Mac: link against the real SDK frameworks
    clang -target $a-apple-macos$MINOS -o "$TMP/shell-$a" "$TMP/shell-$a.o" "$TMP/midi-$a.o" \
      -framework AppKit -framework WebKit -framework CoreMIDI -framework CoreFoundation -framework Foundation -lobjc
  else
    # elsewhere: link against the hand-written .tbd stubs
    "$LD64" -arch $a -platform_version macos $MINOS 10.15 -o "$TMP/shell-$a" "$TMP/shell-$a.o" "$TMP/midi-$a.o" \
      macos/tbd/libSystem.tbd macos/tbd/libobjc.tbd macos/tbd/Foundation.tbd macos/tbd/AppKit.tbd macos/tbd/WebKit.tbd \
      macos/tbd/CoreMIDI.tbd macos/tbd/CoreFoundation.tbd
  fi
done
cp "$TMP/shell-x86_64" "$APP/Contents/MacOS/21 Remote"
chmod 755 "$APP/Contents/MacOS/"*

echo "• icon"
python3 - "$APP/Contents/Resources/AppIcon.icns" <<'EOF'
import io, struct, sys
from PIL import Image
src = Image.open('macos/icon/icon_1024.png').convert('RGBA')
def png(sz):
    b = io.BytesIO(); src.resize((sz, sz), Image.LANCZOS).save(b, 'PNG'); return b.getvalue()
entries = [(b'ic10', 1024), (b'ic14', 1024), (b'ic09', 512), (b'ic13', 512), (b'ic08', 256), (b'ic07', 128), (b'ic12', 64), (b'ic11', 32)]
body = b''.join(t + struct.pack('>I', 8 + len(d)) + d for t, d in ((t, png(s)) for t, s in entries))
open(sys.argv[1], 'wb').write(b'icns' + struct.pack('>I', 8 + len(body)) + body)
EOF

cp macos/icon/StatusIcon.png "$APP/Contents/Resources/StatusIcon.png"

cat > "$APP/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key><string>en</string>
  <key>CFBundleExecutable</key><string>21 Remote</string>
  <key>CFBundleIconFile</key><string>AppIcon</string>
  <key>CFBundleIdentifier</key><string>local.remote21.app</string>
  <key>CFBundleInfoDictionaryVersion</key><string>6.0</string>
  <key>CFBundleName</key><string>21 Remote</string>
  <key>CFBundleDisplayName</key><string>21 Remote</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>$VERSION</string>
  <key>CFBundleVersion</key><string>$BUILD</string>
  <key>LSMinimumSystemVersion</key><string>10.10</string>
  <key>LSApplicationCategoryType</key><string>public.app-category.music</string>
  <key>NSHighResolutionCapable</key><true/>
  <key>NSPrincipalClass</key><string>NSApplication</string>
  <key>NSSupportsAutomaticGraphicsSwitching</key><true/>
  <key>NSAppTransportSecurity</key>
  <dict>
    <key>NSAllowsLocalNetworking</key><true/>
    <key>NSAllowsArbitraryLoads</key><true/>
    <key>NSAllowsArbitraryLoadsInWebContent</key><true/>
  </dict>
  <key>NSLocalNetworkUsageDescription</key><string>21 Remote talks to the D21m frame through a serial device server on your network.</string>
  <key>NSAppleEventsUsageDescription</key><string>Used to show file dialogs.</string>
  <key>NSHumanReadableCopyright</key><string>21 Remote $VERSION</string>
</dict>
</plist>
EOF
printf 'APPL????' > "$APP/Contents/PkgInfo"

if [ "$ON_MAC" = 1 ]; then
  echo "• sign (ad-hoc, whole bundle)"
  codesign --force --deep --sign - "$APP"
  codesign --verify --deep --strict "$APP"
fi

echo "• zip"
python3 - "$OUT" <<'EOF'
import os, sys, zipfile, stat
out = sys.argv[1]
z = zipfile.ZipFile(os.path.join(out, '21-Remote-macOS.zip'), 'w', zipfile.ZIP_DEFLATED)
root = os.path.join(out, '21 Remote.app')
for d, dirs, files in os.walk(root):
    dirs.sort()
    rel = os.path.relpath(d, out)
    zi = zipfile.ZipInfo(rel + '/'); zi.external_attr = (0o40755 << 16) | 0x10; z.writestr(zi, b'')
    for f in sorted(files):
        p = os.path.join(d, f)
        zi = zipfile.ZipInfo(os.path.relpath(p, out))
        mode = 0o755 if os.access(p, os.X_OK) else 0o644
        zi.external_attr = ((stat.S_IFREG | mode) << 16)
        zi.compress_type = zipfile.ZIP_DEFLATED
        z.writestr(zi, open(p, 'rb').read())
z.close()
EOF
rm -rf "$TMP"
echo "done: $APP and $OUT/21-Remote-macOS.zip"
