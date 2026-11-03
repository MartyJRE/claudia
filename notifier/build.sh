#!/usr/bin/env bash
# Builds Claudia.app (the native notification helper) into ~/Applications, or the given path.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
app="${1:-$HOME/Applications/Claudia.app}"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

swift build -c release --package-path "$here"
bin="$(swift build -c release --package-path "$here" --show-bin-path)"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp "$bin/claudia-notify" "$app/Contents/MacOS/claudia-notify"

"$bin/claudia-icon" "$work/icon.png"
iconset="$work/claudia.iconset"
mkdir "$iconset"
for s in 16 32 128 256 512; do
  sips -z $s $s "$work/icon.png" --out "$iconset/icon_${s}x${s}.png" >/dev/null
  sips -z $((s * 2)) $((s * 2)) "$work/icon.png" --out "$iconset/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$iconset" -o "$app/Contents/Resources/claudia.icns"

cat > "$app/Contents/Info.plist" <<'EOF'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleIdentifier</key><string>dev.martinprazak.claudia</string>
  <key>CFBundleName</key><string>claudia</string>
  <key>CFBundleDisplayName</key><string>claudia</string>
  <key>CFBundleExecutable</key><string>claudia-notify</string>
  <key>CFBundleIconFile</key><string>claudia</string>
  <key>CFBundlePackageType</key><string>APPL</string>
  <key>CFBundleShortVersionString</key><string>1.0</string>
  <key>CFBundleVersion</key><string>1</string>
  <key>LSUIElement</key><true/>
  <key>LSMinimumSystemVersion</key><string>14.0</string>
</dict>
</plist>
EOF

codesign --force --deep --sign - "$app"
/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -f "$app"
echo "built $app"
