#!/usr/bin/env bash
# Builds MeetMD.app — a self-contained menu-bar bundle that embeds the bridge
# and the audio helper. The .app gives a stable identity for macOS permissions
# (Screen Recording, Microphone, Automation), which a bare binary / LaunchAgent
# cannot get. See issues #3/#4.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
APP="$ROOT/menubar/MeetMD.app"
MACOS="$APP/Contents/MacOS"
RES="$APP/Contents/Resources"
BUNDLE_ID="com.tbdc.meetmd"
VERSION="0.1.0"

# whisper.cpp source + models (instalados no pré-requisito do README)
WHISPER_SRC="${WHISPER_SRC:-$HOME/.meetmd/tools/whisper.cpp}"
MODELS_DIR="${MODELS_DIR:-$HOME/.meetmd/models}"
WHISPER_MODEL="${WHISPER_MODEL:-ggml-small.bin}"
VAD_MODEL="ggml-silero-v5.1.2.bin"

# URL de download de cada modelo (sem array associativo — compat. bash 3.2 do macOS)
model_url() {
	case "$1" in
	ggml-small.bin) echo "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-small.bin" ;;
	ggml-base.bin) echo "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml-base.bin" ;;
	ggml-silero-v5.1.2.bin) echo "https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v5.1.2.bin" ;;
	esac
}

echo "==> limpando $APP"
rm -rf "$APP"
mkdir -p "$MACOS"

SWIFT_TARGET="arm64-apple-macos13.0"

echo "==> compilando menu-bar (Swift, arm64)"
swiftc -O -target "$SWIFT_TARGET" "$ROOT/menubar/MeetMDBar.swift" -o "$MACOS/MeetMD" -framework Cocoa

echo "==> compilando helper de áudio (Swift, arm64 / ScreenCaptureKit)"
swiftc -O -target "$SWIFT_TARGET" "$ROOT/spike/macos-audio/SystemAudioRecorder.swift" -o "$MACOS/system-audio-recorder"

echo "==> compilando bridge (Go, arm64)"
# arm64 nativo (sem Rosetta), alinhado ao resto do bundle. Go >= 1.24 já emite
# LC_UUID com o linker interno, então não precisa mais do linkmode externo (#7).
# Nome "meetmd-bridge" (não "meetmd"): o filesystem do macOS é case-insensitive e
# "meetmd" colidiria com o executável principal "MeetMD".
( cd "$ROOT/bridge" && GOOS=darwin GOARCH=arm64 go build -o "$MACOS/meetmd-bridge" ./cmd/meetmd )

echo "==> whisper.cpp estático + Metal (binário único, autocontido)"
WHISPER_STATIC="$WHISPER_SRC/build-static"
if [ ! -x "$WHISPER_STATIC/bin/whisper-cli" ]; then
	if [ ! -d "$WHISPER_SRC" ]; then
		echo "    clonando whisper.cpp em $WHISPER_SRC"
		git clone --depth 1 https://github.com/ggerganov/whisper.cpp "$WHISPER_SRC" >/dev/null 2>&1
	fi
	echo "    (primeira vez — buildando, leva alguns minutos)"
	cmake -S "$WHISPER_SRC" -B "$WHISPER_STATIC" -DCMAKE_BUILD_TYPE=Release \
		-DBUILD_SHARED_LIBS=OFF -DGGML_METAL=ON -DGGML_METAL_EMBED_LIBRARY=ON \
		-DCMAKE_OSX_ARCHITECTURES=arm64 -DCMAKE_SYSTEM_PROCESSOR=arm64 \
		-DGGML_NATIVE=OFF -DWHISPER_BUILD_TESTS=OFF >/dev/null
	cmake --build "$WHISPER_STATIC" -j --target whisper-cli >/dev/null
fi
cp "$WHISPER_STATIC/bin/whisper-cli" "$MACOS/whisper-cli"

echo "==> modelos → Resources/models"
mkdir -p "$RES/models" "$MODELS_DIR"
for m in "$WHISPER_MODEL" "$VAD_MODEL"; do
	if [ ! -f "$MODELS_DIR/$m" ]; then
		echo "    baixando $m (uma vez)"
		curl -L --fail --progress-bar -o "$MODELS_DIR/$m" "$(model_url "$m")"
	fi
	cp "$MODELS_DIR/$m" "$RES/models/"
done

for b in MeetMD meetmd-bridge whisper-cli system-audio-recorder; do
	[ -x "$MACOS/$b" ] || { echo "ERRO: binário $b não foi gerado"; exit 1; }
done

echo "==> ícone do app (.icns, renderizado do mascote)"
ICONSET="$(mktemp -d)/MeetMD.iconset"
mkdir -p "$ICONSET"
gen_icon() { "$MACOS/MeetMD" --app-icon "$ICONSET/icon_$1.png" "$2"; }
gen_icon 16x16 16
gen_icon 16x16@2x 32
gen_icon 32x32 32
gen_icon 32x32@2x 64
gen_icon 128x128 128
gen_icon 128x128@2x 256
gen_icon 256x256 256
gen_icon 256x256@2x 512
gen_icon 512x512 512
gen_icon 512x512@2x 1024
iconutil -c icns "$ICONSET" -o "$RES/AppIcon.icns"
rm -rf "$(dirname "$ICONSET")"

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
	<key>CFBundleIconFile</key>        <string>AppIcon</string>
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

# Identidade de assinatura: usa o cert self-signed estável "MeetMD Dev" se existir
# (rode menubar/setup-dev-cert.sh uma vez) — as permissões TCC colam entre rebuilds.
# Sem o cert, cai pra ad-hoc: a identidade muda a cada rebuild e o macOS invalida
# as permissões (Gravação de Tela, Microfone, Automação), exigindo reconcessão.
DEV_KEYCHAIN="$HOME/Library/Keychains/meetmd-codesign.keychain-db"
DEV_IDENTITY="MeetMD Dev"
SIGN_ARGS=(--force --sign -)
if security find-identity -p codesigning "$DEV_KEYCHAIN" 2>/dev/null | grep -q "$DEV_IDENTITY"; then
	security unlock-keychain -p meetmd-dev "$DEV_KEYCHAIN" 2>/dev/null || true
	SIGN_ARGS=(--force --sign "$DEV_IDENTITY" --keychain "$DEV_KEYCHAIN")
	echo "==> assinando com '$DEV_IDENTITY' (identidade estável p/ TCC), de dentro pra fora"
else
	echo "==> assinatura ad-hoc, de dentro pra fora (rode setup-dev-cert.sh p/ permissões estáveis)"
fi
# Ordem inside-out: helpers primeiro, o .app por último (assina o principal e sela o bundle).
codesign "${SIGN_ARGS[@]}" "$MACOS/meetmd-bridge"
codesign "${SIGN_ARGS[@]}" "$MACOS/whisper-cli"
codesign "${SIGN_ARGS[@]}" "$MACOS/system-audio-recorder"
codesign "${SIGN_ARGS[@]}" "$APP"

echo "==> verificando"
codesign --verify --verbose "$APP" 2>&1 | sed 's/^/   /'
echo "OK → $APP"
echo "   Para usar: abra MeetMD.app (ou: open '$APP'). Conceda as permissões na 1ª gravação."
