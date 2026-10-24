#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
# Builds the installer package "21-Remote-<version>.pkg" (macOS only).
#
#   dist/21 Remote.app               -> /Applications
#   dist/plugins/Components/*.component -> /Library/Audio/Plug-Ins/Components
#   dist/plugins/VST3/*.vst3            -> /Library/Audio/Plug-Ins/VST3
#   plugins/jsfx/*                      -> /Library/Application Support/21 Remote/JSFX
#                                          (JS effects and the "21 Remote Bridge" script for REAPER)
#                                          and copied to the user's JS effects folder
#
# Each part is a choice in the installer ("Customize").
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION=${VERSION:-$(cat VERSION)}
OUT=dist
WORK=$(mktemp -d)
PKGS="$WORK/pkgs"
mkdir -p "$PKGS"

# component package; bundles are installed where we say, never "relocated"
component() { # id root install-location [scripts]
  local id=$1 root=$2 loc=$3 scripts=${4:-}
  local plist="$WORK/$id.plist"
  pkgbuild --analyze --root "$root" "$plist" >/dev/null
  local n
  n=$(python3 - "$plist" <<'PY'
import plistlib, sys
p = sys.argv[1]
items = plistlib.load(open(p, 'rb'))
for it in items:
    it['BundleIsRelocatable'] = False
    it['BundleOverwriteAction'] = 'upgrade'
plistlib.dump(items, open(p, 'wb'))
print(len(items))
PY
)
  local args=(--root "$root" --identifier "$id" --version "$VERSION" --install-location "$loc")
  [ "$n" -gt 0 ] && args+=(--component-plist "$plist")
  [ -n "$scripts" ] && args+=(--scripts "$scripts")
  pkgbuild "${args[@]}" "$PKGS/$id.pkg" >/dev/null
}

# --- payloads
mkdir -p "$WORK/app" "$WORK/au" "$WORK/vst3" "$WORK/jsfx"
cp -R "$OUT/21 Remote.app" "$WORK/app/"
cp -R "$OUT"/plugins/Components/*.component "$WORK/au/"
cp -R "$OUT"/plugins/VST3/*.vst3 "$WORK/vst3/"
cp plugins/jsfx/*.jsfx plugins/jsfx/*.jsfx-inc plugins/jsfx/*.lua "$WORK/jsfx/"

# --- scripts
mkdir -p "$WORK/scripts-au" "$WORK/scripts-vst3" "$WORK/scripts-jsfx"
common='
USER_NAME=$(stat -f%Su /dev/console 2>/dev/null || true)
USER_HOME=""
if [ -n "$USER_NAME" ] && [ "$USER_NAME" != root ]; then
  USER_HOME=$(dscl . -read "/Users/$USER_NAME" NFSHomeDirectory 2>/dev/null | awk "{print \$2}")
fi
'
cat > "$WORK/scripts-au/postinstall" <<EOF
#!/bin/bash
$common
# copies installed by hand earlier would show up twice in the DAW
if [ -n "\$USER_HOME" ]; then
  rm -rf "\$USER_HOME/Library/Audio/Plug-Ins/Components/21 Channel.component" \\
         "\$USER_HOME/Library/Audio/Plug-Ins/Components/21 Overview.component"
fi
# let macOS rescan Audio Units
killall -9 AudioComponentRegistrar 2>/dev/null || true
exit 0
EOF
cat > "$WORK/scripts-vst3/postinstall" <<EOF
#!/bin/bash
$common
if [ -n "\$USER_HOME" ]; then
  rm -rf "\$USER_HOME/Library/Audio/Plug-Ins/VST3/21 Channel.vst3" \\
         "\$USER_HOME/Library/Audio/Plug-Ins/VST3/21 Overview.vst3"
fi
exit 0
EOF
cat > "$WORK/scripts-jsfx/postinstall" <<EOF
#!/bin/bash
$common
# JS effects are read from the user's own effects folder
SRC="/Library/Application Support/21 Remote/JSFX"
if [ -n "\$USER_HOME" ]; then
  DEST="\$USER_HOME/Library/Application Support/REAPER/Effects/21 Remote"
  mkdir -p "\$DEST"
  cp -f "\$SRC"/*.jsfx "\$SRC"/*.jsfx-inc "\$DEST"/
  # the bridge between the JS plug-ins and the app starts together with REAPER
  RS="\$USER_HOME/Library/Application Support/REAPER/Scripts"
  mkdir -p "\$RS/21 Remote"
  cp -f "\$SRC/21 Remote Bridge.lua" "\$RS/21 Remote/"
  if ! grep -q "21 Remote Bridge.lua" "\$RS/__startup.lua" 2>/dev/null; then
    printf '%s\\n' 'pcall(dofile, reaper.GetResourcePath() .. "/Scripts/21 Remote/21 Remote Bridge.lua") -- 21 Remote' >> "\$RS/__startup.lua"
  fi
  R="\$USER_HOME/Library/Application Support/REAPER"
  chown "\$USER_NAME" "\$R" "\$R/Effects" "\$RS" "\$RS/__startup.lua" 2>/dev/null || true
  chown -R "\$USER_NAME" "\$DEST" "\$RS/21 Remote" 2>/dev/null || true
fi
exit 0
EOF
chmod 755 "$WORK"/scripts-*/postinstall

# --- component packages
component local.remote21.pkg.app "$WORK/app" /Applications
component local.remote21.pkg.au "$WORK/au" /Library/Audio/Plug-Ins/Components "$WORK/scripts-au"
component local.remote21.pkg.vst3 "$WORK/vst3" /Library/Audio/Plug-Ins/VST3 "$WORK/scripts-vst3"
component local.remote21.pkg.jsfx "$WORK/jsfx" "/Library/Application Support/21 Remote/JSFX" "$WORK/scripts-jsfx"

# --- installer texts
mkdir -p "$WORK/res"
cat > "$WORK/res/welcome.html" <<EOF
<html><body style="font-family: -apple-system, sans-serif; font-size: 13px">
<h2>21 Remote $VERSION</h2>
<p>Remote control for D21m frames: the 21 Remote app and the DAW plug-ins.</p>
<ul>
<li><b>21 Remote</b> app, in Applications</li>
<li><b>Audio Unit</b> and <b>VST3</b> plug-ins 21 Channel and 21 Overview, for all users</li>
<li><b>JS plug-ins</b> 21 Channel and 21 Overview</li>
</ul>
<p>Click <b>Customize</b> on the Installation Type page to leave out parts.
Quit your DAW before installing, and start it again afterwards so it finds the new plug-ins.</p>
</body></html>
EOF
cat > "$WORK/res/conclusion.html" <<EOF
<html><body style="font-family: -apple-system, sans-serif; font-size: 13px">
<h2>21 Remote is installed</h2>
<p>Open <b>21 Remote</b> from Applications and choose the connection to the rack (CONNECT).
When you close its window, the app keeps running with an icon in the menu bar.</p>
<p>The plug-ins start the app by themselves when a DAW project uses them.</p>
</body></html>
EOF

cat > "$WORK/distribution.xml" <<EOF
<?xml version="1.0" encoding="utf-8"?>
<installer-gui-script minSpecVersion="2">
  <title>21 Remote $VERSION</title>
  <welcome file="welcome.html" mime-type="text/html"/>
  <conclusion file="conclusion.html" mime-type="text/html"/>
  <options customize="allow" require-scripts="false" hostArchitectures="arm64,x86_64" rootVolumeOnly="true"/>
  <domains enable_anywhere="false" enable_currentUserHome="false" enable_localSystem="true"/>
  <volume-check><allowed-os-versions><os-version min="11.0"/></allowed-os-versions></volume-check>
  <choices-outline>
    <line choice="app"/>
    <line choice="au"/>
    <line choice="vst3"/>
    <line choice="jsfx"/>
  </choices-outline>
  <choice id="app" title="21 Remote app" description="The app that talks to the rack. The plug-ins need it.">
    <pkg-ref id="local.remote21.pkg.app"/>
  </choice>
  <choice id="au" title="Audio Unit plug-ins" description="21 Channel and 21 Overview as Audio Units.">
    <pkg-ref id="local.remote21.pkg.au"/>
  </choice>
  <choice id="vst3" title="VST3 plug-ins" description="21 Channel and 21 Overview as VST3.">
    <pkg-ref id="local.remote21.pkg.vst3"/>
  </choice>
  <choice id="jsfx" title="JS plug-ins" description="21 Channel and 21 Overview as JS effects, and the 21 Remote Bridge script that connects them with the app (it starts together with REAPER).">
    <pkg-ref id="local.remote21.pkg.jsfx"/>
  </choice>
  <pkg-ref id="local.remote21.pkg.app" version="$VERSION" onConclusion="none">local.remote21.pkg.app.pkg</pkg-ref>
  <pkg-ref id="local.remote21.pkg.au" version="$VERSION" onConclusion="none">local.remote21.pkg.au.pkg</pkg-ref>
  <pkg-ref id="local.remote21.pkg.vst3" version="$VERSION" onConclusion="none">local.remote21.pkg.vst3.pkg</pkg-ref>
  <pkg-ref id="local.remote21.pkg.jsfx" version="$VERSION" onConclusion="none">local.remote21.pkg.jsfx.pkg</pkg-ref>
</installer-gui-script>
EOF

productbuild --distribution "$WORK/distribution.xml" --resources "$WORK/res" --package-path "$PKGS" \
  "$OUT/21-Remote-$VERSION.pkg"
rm -rf "$WORK"
echo "done: $OUT/21-Remote-$VERSION.pkg"
