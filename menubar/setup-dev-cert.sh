#!/usr/bin/env bash
# Creates a self-signed code-signing certificate ("MeetMD Dev") in a dedicated
# keychain so the .app gets a STABLE signing identity. With ad-hoc signing the
# cdhash changes on every rebuild and macOS invalidates the TCC permissions
# (Screen Recording, Microphone, Automation) — forcing a re-grant each time.
# A stable cert keys the TCC designated requirement to the certificate, so the
# grants survive rebuilds. This is a LOCAL DEV cert (not for distribution — see
# issue #4 for Developer ID + notarization).
#
# Idempotent: re-running is a no-op once the identity exists.
set -euo pipefail

IDENTITY="MeetMD Dev"
KEYCHAIN="$HOME/Library/Keychains/meetmd-codesign.keychain-db"
KEYCHAIN_PW="meetmd-dev" # password for this throwaway keychain only

if security find-identity -v -p codesigning "$KEYCHAIN" 2>/dev/null | grep -q "$IDENTITY"; then
	echo "OK → identidade '$IDENTITY' já existe em $KEYCHAIN"
	exit 0
fi

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "==> gerando certificado self-signed (codeSigning)"
cat >"$TMP/cert.cnf" <<EOF
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no
[dn]
CN = $IDENTITY
[ext]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
EOF

openssl req -x509 -newkey rsa:2048 -nodes \
	-keyout "$TMP/key.pem" -out "$TMP/cert.pem" \
	-days 3650 -config "$TMP/cert.cnf" 2>/dev/null
# -legacy: PBE/MAC SHA1+3DES, compatível com o `security import` da Apple
# (OpenSSL 3.x usa AES-256/SHA256 por padrão, que o Keychain rejeita).
openssl pkcs12 -export -legacy -inkey "$TMP/key.pem" -in "$TMP/cert.pem" \
	-out "$TMP/cert.p12" -name "$IDENTITY" -passout pass:meetmd 2>/dev/null

echo "==> criando keychain dedicado $KEYCHAIN"
security delete-keychain "$KEYCHAIN" 2>/dev/null || true
security create-keychain -p "$KEYCHAIN_PW" "$KEYCHAIN"
security set-keychain-settings "$KEYCHAIN" # sem timeout de auto-lock
security unlock-keychain -p "$KEYCHAIN_PW" "$KEYCHAIN"

echo "==> importando cert e autorizando o codesign"
security import "$TMP/cert.p12" -k "$KEYCHAIN" -P meetmd -T /usr/bin/codesign -A
security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$KEYCHAIN_PW" "$KEYCHAIN" >/dev/null

echo "==> adicionando o keychain à search list (pro codesign achar a identidade)"
EXISTING="$(security list-keychains -d user | sed -e 's/^[[:space:]]*"//' -e 's/"$//')"
# shellcheck disable=SC2086
security list-keychains -d user -s "$KEYCHAIN" $EXISTING

echo "==> verificando"
security find-identity -v -p codesigning "$KEYCHAIN" | sed 's/^/   /'
echo "OK → assine com: codesign --sign '$IDENTITY'  (o build-app.sh detecta sozinho)"
