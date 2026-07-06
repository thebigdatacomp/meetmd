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
VERSION="0.2.1"

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
# Rendering runs the GUI binary, which needs a window-server session. Keep it
# non-fatal: a missing icon is cosmetic (Finder falls back to a generic one) and
# must not abort the whole build (e.g. headless/CI). Cleans up its temp dir.
generate_app_icon() {
	local tmp iconset s
	tmp="$(mktemp -d)"
	iconset="$tmp/MeetMD.iconset"
	mkdir -p "$iconset"
	for s in 16x16:16 16x16@2x:32 32x32:32 32x32@2x:64 128x128:128 \
		128x128@2x:256 256x256:256 256x256@2x:512 512x512:512 512x512@2x:1024; do
		"$MACOS/MeetMD" --app-icon "$iconset/icon_${s%%:*}.png" "${s##*:}" || { rm -rf "$tmp"; return 1; }
	done
	iconutil -c icns "$iconset" -o "$RES/AppIcon.icns" || { rm -rf "$tmp"; return 1; }
	rm -rf "$tmp"
}
if generate_app_icon; then
	echo "    AppIcon.icns gerado"
else
	echo "    AVISO: ícone do app não gerado (sem sessão gráfica?) — bundle usa ícone genérico"
fi

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

ENT="$ROOT/menubar/entitlements"
# Pin the signing identity via DEVELOPER_ID (hash or name), else pick the first
# Developer ID Application identity in the keychain.
DEVID="${DEVELOPER_ID:-$(security find-identity -v -p codesigning | awk '/Developer ID Application/ {print $2; exit}')}"

if [ -n "${RELEASE:-}" ]; then
	if [ -z "$DEVID" ]; then
		echo "ERRO: RELEASE=1 mas nenhum 'Developer ID Application' no keychain." >&2
		echo "      Instale o cert (ou aponte DEVELOPER_ID=<hash>), ou rode sem RELEASE p/ build de dev." >&2
		exit 1
	fi
	# RELEASE: Developer ID + hardened runtime + secure timestamp + entitlements,
	# inside-out. This is the distributable, notarizable signature.
	echo "==> assinatura RELEASE (Developer ID + hardened runtime + entitlements)"
	sign_rel() { codesign --force --options runtime --timestamp --sign "$DEVID" --entitlements "$2" "$1"; }
	sign_rel "$MACOS/whisper-cli" "$ENT/whisper.plist"
	sign_rel "$MACOS/system-audio-recorder" "$ENT/app.plist"
	sign_rel "$MACOS/meetmd-bridge" "$ENT/app.plist"
	sign_rel "$APP" "$ENT/app.plist" # main executable + bundle seal
else
	# DEV: stable self-signed cert "MeetMD Dev" if present (run setup-dev-cert.sh),
	# else ad-hoc. No hardened runtime/timestamp — fast local iteration only.
	DEV_KEYCHAIN="$HOME/Library/Keychains/meetmd-codesign.keychain-db"
	DEV_IDENTITY="MeetMD Dev"
	SIGN_ARGS=(--force --sign -)
	if security find-identity -p codesigning "$DEV_KEYCHAIN" 2>/dev/null | grep -q "$DEV_IDENTITY"; then
		security unlock-keychain -p meetmd-dev "$DEV_KEYCHAIN" 2>/dev/null || true
		SIGN_ARGS=(--force --sign "$DEV_IDENTITY" --keychain "$DEV_KEYCHAIN")
		echo "==> assinatura dev: '$DEV_IDENTITY' (identidade estável p/ TCC), de dentro pra fora"
	else
		echo "==> assinatura ad-hoc, de dentro pra fora (RELEASE=1 p/ Developer ID; setup-dev-cert.sh p/ TCC estável)"
	fi
	codesign "${SIGN_ARGS[@]}" "$MACOS/meetmd-bridge"
	codesign "${SIGN_ARGS[@]}" "$MACOS/whisper-cli"
	codesign "${SIGN_ARGS[@]}" "$MACOS/system-audio-recorder"
	codesign "${SIGN_ARGS[@]}" "$APP"
fi

echo "==> verificando assinatura"
codesign --verify --strict --verbose "$APP" 2>&1 | sed 's/^/   /'

# Temp dirs for notarization/dmg staging — cleaned up even if a step aborts.
NWORK=""
STAGE=""
trap 'rm -rf "$NWORK" "$STAGE" 2>/dev/null || true' EXIT

# Notarização + staple (RELEASE, quando há perfil de credencial do notarytool).
if [ -n "${RELEASE:-}" ] && [ -n "${NOTARY_PROFILE:-}" ]; then
	echo "==> notarizando via perfil '$NOTARY_PROFILE' (pode levar alguns minutos)"
	NWORK="$(mktemp -d)"
	ditto -c -k --keepParent "$APP" "$NWORK/MeetMD.zip"
	if ! xcrun notarytool submit "$NWORK/MeetMD.zip" --keychain-profile "$NOTARY_PROFILE" --wait; then
		echo "ERRO: notarização não foi aceita. Veja o motivo com:" >&2
		echo "      xcrun notarytool log <submission-id> --keychain-profile $NOTARY_PROFILE" >&2
		exit 1
	fi
	xcrun stapler staple "$APP"
	echo "==> validando notarização"
	xcrun stapler validate "$APP" && spctl -a -vvv -t install "$APP" 2>&1 | sed 's/^/   /'
fi

# Empacota um .dmg de arrastar-pra-Aplicativos (RELEASE).
if [ -n "${RELEASE:-}" ]; then
	echo "==> empacotando .dmg"
	DMG="$ROOT/menubar/MeetMD.dmg"
	rm -f "$DMG"
	STAGE="$(mktemp -d)"
	cp -R "$APP" "$STAGE/"
	ln -s /Applications "$STAGE/Applications"
	hdiutil create -volname MeetMD -srcfolder "$STAGE" -ov -format UDZO "$DMG" >/dev/null
	# Notarize+staple the .dmg itself so a downloaded (quarantined) dmg opens
	# cleanly — the app inside is already stapled for the extracted-app case.
	if [ -n "${NOTARY_PROFILE:-}" ]; then
		echo "==> notarizando o .dmg"
		if ! xcrun notarytool submit "$DMG" --keychain-profile "$NOTARY_PROFILE" --wait; then
			echo "ERRO: notarização do .dmg falhou (veja 'xcrun notarytool log')." >&2
			exit 1
		fi
		xcrun stapler staple "$DMG" && xcrun stapler validate "$DMG"
	else
		echo "   AVISO: .dmg NÃO notarizado (sem NOTARY_PROFILE) — Gatekeeper bloqueará em outros Macs."
	fi
	echo "   .dmg → $DMG"
fi

# Refresh the LaunchServices icon cache for the rebuilt bundle. Without this, a
# rebuild at the same path/bundle-id keeps the stale (often generic) icon in
# NSApp.applicationIconImage and the Dock/switcher until macOS catches up.
LSREGISTER="/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister"
[ -x "$LSREGISTER" ] && "$LSREGISTER" -f "$APP" || true

echo "OK → $APP"
echo "   Dev: abra MeetMD.app. Release: RELEASE=1 NOTARY_PROFILE=meetmd-notary ./menubar/build-app.sh"
