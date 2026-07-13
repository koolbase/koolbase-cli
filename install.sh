#!/bin/sh
# Koolbase CLI installer — https://get.koolbase.com
#
#   curl -fsSL https://get.koolbase.com/install.sh | sh
#
# Detects OS/arch, downloads the latest release from the signed manifest,
# verifies its sha256, and installs to /usr/local/bin (override with
# KOOLBASE_INSTALL_DIR). POSIX sh — no bashisms.

set -eu

BASE_URL="https://get.koolbase.com"
INSTALL_DIR="${KOOLBASE_INSTALL_DIR:-/usr/local/bin}"

say() { printf '%s\n' "$*"; }
die() { printf 'koolbase install: %s\n' "$*" >&2; exit 1; }

# --- platform detection ------------------------------------------------------
OS=$(uname -s 2>/dev/null || echo unknown)
ARCH=$(uname -m 2>/dev/null || echo unknown)

case "$OS" in
  Darwin) OS=darwin ;;
  Linux)  OS=linux ;;
  MINGW*|MSYS*|CYGWIN*|Windows_NT)
    die "Windows: download the binary directly from ${BASE_URL}/latest.json (see docs.koolbase.com/cli)" ;;
  *) die "unsupported operating system: $OS" ;;
esac

case "$ARCH" in
  arm64|aarch64) ARCH=arm64 ;;
  x86_64|amd64)  ARCH=amd64 ;;
  *) die "unsupported architecture: $ARCH" ;;
esac

PLATFORM="${OS}-${ARCH}"

# --- fetch manifest ----------------------------------------------------------
command -v curl >/dev/null 2>&1 || die "curl is required"

MANIFEST=$(curl -fsSL "${BASE_URL}/latest.json") || die "could not fetch release manifest"

# Extract url + sha256 for our platform without requiring jq: latest.json is
# machine-generated with stable key ordering (url then sha256 per artifact).
URL=$(printf '%s' "$MANIFEST" | tr -d ' \n' | sed -n "s|.*\"${PLATFORM}\":{\"url\":\"\([^\"]*\)\".*|\1|p")
SHA=$(printf '%s' "$MANIFEST" | tr -d ' \n' | sed -n "s|.*\"${PLATFORM}\":{\"url\":\"[^\"]*\",\"sha256\":\"\([a-f0-9]*\)\".*|\1|p")
VERSION=$(printf '%s' "$MANIFEST" | tr -d ' \n' | sed -n 's|.*"version":"\([^"]*\)".*|\1|p')

[ -n "$URL" ] && [ -n "$SHA" ] || die "no artifact for platform ${PLATFORM} in manifest"

say "Installing Koolbase CLI ${VERSION} (${OS}/${ARCH})"

# --- download + verify -------------------------------------------------------
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

curl -fsSL "$URL" -o "${TMP}/koolbase" || die "download failed"

if command -v sha256sum >/dev/null 2>&1; then
  GOT=$(sha256sum "${TMP}/koolbase" | cut -d' ' -f1)
elif command -v shasum >/dev/null 2>&1; then
  GOT=$(shasum -a 256 "${TMP}/koolbase" | cut -d' ' -f1)
else
  die "need sha256sum or shasum to verify the download"
fi

[ "$GOT" = "$SHA" ] || die "checksum mismatch (expected ${SHA}, got ${GOT}) — aborting"

chmod +x "${TMP}/koolbase"

# --- install -----------------------------------------------------------------
if [ -d "$INSTALL_DIR" ] && [ -w "$INSTALL_DIR" ]; then
  mv "${TMP}/koolbase" "${INSTALL_DIR}/koolbase"
elif [ ! -d "$INSTALL_DIR" ] && mkdir -p "$INSTALL_DIR" 2>/dev/null; then
  mv "${TMP}/koolbase" "${INSTALL_DIR}/koolbase"
else
  say "Installing to ${INSTALL_DIR} requires sudo:"
  sudo mkdir -p "$INSTALL_DIR"
  sudo mv "${TMP}/koolbase" "${INSTALL_DIR}/koolbase"
fi

say ""
say "✓ $("${INSTALL_DIR}/koolbase" version)"
say ""
say "Get started:  koolbase login"
say "MCP setup:    https://docs.koolbase.com/mcp"
