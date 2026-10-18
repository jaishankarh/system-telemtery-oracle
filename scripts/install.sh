#!/bin/sh
# Install sys-telemetry-app from GitHub Releases.
#   curl -fsSL https://github.com/jaishankarh/system-telemtery-oracle/releases/latest/download/install.sh | sudo sh
# Environment:
#   INSTALL_DIR              target directory (default /usr/local/bin)
#   SYS_TELEMETRY_VERSION    release tag, e.g. v0.1.0 (default: latest)
#   SYS_TELEMETRY_BASE_URL   URL or local directory holding the release files
#                            (overrides the GitHub location; used by tests)
#   SYS_TELEMETRY_UNAME_M    override of `uname -m` (used by tests)
set -eu

BIN=sys-telemetry-app
REPO=jaishankarh/system-telemtery-oracle
INSTALL_DIR=${INSTALL_DIR:-/usr/local/bin}

die() { echo "install: $*" >&2; exit 1; }

machine=${SYS_TELEMETRY_UNAME_M:-$(uname -m)}
case "$machine" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) die "unsupported architecture: $machine (need x86_64 or aarch64)" ;;
esac

if [ -n "${SYS_TELEMETRY_BASE_URL:-}" ]; then
  base=$SYS_TELEMETRY_BASE_URL
elif [ -n "${SYS_TELEMETRY_VERSION:-}" ]; then
  base=https://github.com/$REPO/releases/download/$SYS_TELEMETRY_VERSION
else
  base=https://github.com/$REPO/releases/latest/download
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

fetch() { # fetch <name> <dest>
  case "$base" in
    http://*|https://*)
      if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$base/$1" -o "$2"
      elif command -v wget >/dev/null 2>&1; then
        wget -q "$base/$1" -O "$2"
      else
        die "need curl or wget"
      fi ;;
    *) cp "${base#file://}/$1" "$2" ;;
  esac
}

asset=$BIN-linux-$arch
fetch "$asset" "$tmp/$asset" || die "download of $asset failed"
fetch SHA256SUMS "$tmp/SHA256SUMS" || die "download of SHA256SUMS failed"

want=$(awk -v f="$asset" '$2 == f { print $1 }' "$tmp/SHA256SUMS")
[ -n "$want" ] || die "no checksum listed for $asset"
have=$(sha256sum "$tmp/$asset" | awk '{ print $1 }')
[ "$want" = "$have" ] || die "checksum mismatch for $asset"

mkdir -p "$INSTALL_DIR"
chmod 755 "$tmp/$asset"
mv "$tmp/$asset" "$INSTALL_DIR/$BIN" 2>/dev/null || cp "$tmp/$asset" "$INSTALL_DIR/$BIN"
echo "Installed $INSTALL_DIR/$BIN ($arch)"
