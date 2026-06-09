#!/usr/bin/env bash
# Builds MeetMD.app — a self-contained menu-bar bundle that embeds the bridge
# and the audio helper. The .app gives a stable identity for macOS permissions
# (Screen Recording, Microphone, Automation), which a bare binary / LaunchAgent
# cannot get. See issues #3/#4.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP="$ROOT/menubar/MeetMD.app"
MACOS="$APP/Contents/MacOS"
BUNDLE_ID="com.tbdc.meetmd"
VERSION="0.1.0"

echo "==> limpando $APP"
rm -rf "$APP"
mkdir -p "$MACOS"

SWIFT_TARGET="arm64-apple-macos13.0"

echo "==> compilando menu-bar (Swift, arm64)"
swiftc -O -target "$SWIFT_TARGET" "$ROOT/menubar/MeetMDBar.swift" -o "$MACOS/MeetMD" -framework Cocoa

echo "==> compilando helper de áudio (Swift, arm64 / ScreenCaptureKit)"
swiftc -O -target "$SWIFT_TARGET" "$ROOT/spike/macos-audio/SystemAudioRecorder.swift" -o "$MACOS/system-audio-recorder"

echo "==> compilando bridge (Go)"
# external linkmode: stamp LC_UUID (Go 1.21 + macOS recente). No-op em Go >= 1.22.
# Nota: bridge sai x86_64 (Go amd64/Rosetta); vira arm64 ao migrar o toolchain (#7).
# Nome "meetmd-bridge" (não "meetmd"): o filesystem do macOS é case-insensitive e
# "meetmd" colidiria com o executável principal "MeetMD".
( cd "$ROOT/bridge" && go build -ldflags=-linkmode=external -o "$MACOS/meetmd-bridge" ./cmd/meetmd )

for b in MeetMD meetmd-bridge system-audio-recorder; do
	[ -x "$MACOS/$b" ] || { echo "ERRO: binário $b não foi gerado"; exit 1; }
done

echo "==> Info.plist"
cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key>            <string>MeetMD</string>
	<key>CFBundleDisplayName</key>     <string>MeetMD</string>
	<key>CFBundleIdentifier</key>      <string>${BUNDLE_ID}</string>
	<key>CFBundleExecutable</key>      <string>MeetMD</string>
	<key>CFBundleVersion</key>         <string>${VERSION}</string>
	<key>CFBundleShortVersionString</key><string>${VERSION}</string>
	<key>CFBundlePackageType</key>     <string>APPL</string>
	<key>LSMinimumSystemVersion</key>  <string>13.0</string>
	<key>LSUIElement</key>             <true/>
	<key>NSMicrophoneUsageDescription</key>
	<string>MeetMD captura o áudio da reunião (sua voz) para transcrever localmente.</string>
	<key>NSAppleEventsUsageDescription</key>
	<string>MeetMD detecta reuniões do Google Meet abertas no Safari.</string>
</dict>
</plist>
PLIST

echo "==> assinatura ad-hoc, de dentro pra fora (identidade estável p/ TCC)"
codesign --force --sign - "$MACOS/meetmd-bridge"
codesign --force --sign - "$MACOS/system-audio-recorder"
codesign --force --sign - "$APP" # assina o executável principal e sela o bundle

echo "==> verificando"
codesign --verify --verbose "$APP" 2>&1 | sed 's/^/   /'
echo "OK → $APP"
echo "   Para usar: abra MeetMD.app (ou: open '$APP'). Conceda as permissões na 1ª gravação."
