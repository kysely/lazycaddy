#!/bin/sh
set -eu

DEFAULT_REPO="kysely/lazycaddy"
DEFAULT_BINDIR="/usr/local/bin"

REPO="${REPO:-$DEFAULT_REPO}"
BINDIR="${BINDIR:-$DEFAULT_BINDIR}"
VERSION="${VERSION:-latest}"
NO_BREW="${NO_BREW:-0}"
BIN="lazycaddy"
TMPDIR=""

log() {
  printf '%s\n' "$*"
}

fail() {
  printf 'lazycaddy install: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<EOF
Install lazycaddy from GitHub Releases.

Usage:
  install.sh [options]

Options:
  --version <tag>   Release tag to install, for example v0.1.1 (default: latest)
  --bindir <dir>    Install directory (default: /usr/local/bin)
  --repo <repo>     GitHub repo in owner/name form (default: kysely/lazycaddy)
  --no-brew         Do not delegate to Homebrew on macOS
  -h, --help        Show this help

Environment variables:
  VERSION           Same as --version
  BINDIR            Same as --bindir
  REPO              Same as --repo
  NO_BREW=1         Same as --no-brew
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || fail "--version requires a value"
      VERSION="$2"
      shift 2
      ;;
    --bindir)
      [ "$#" -ge 2 ] || fail "--bindir requires a value"
      BINDIR="$2"
      shift 2
      ;;
    --repo)
      [ "$#" -ge 2 ] || fail "--repo requires a value"
      REPO="$2"
      shift 2
      ;;
    --no-brew)
      NO_BREW=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

need() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

cleanup() {
  if [ -n "$TMPDIR" ] && [ -d "$TMPDIR" ]; then
    rm -rf "$TMPDIR"
  fi
}
trap cleanup EXIT INT TERM

need uname
need tar
need grep
need sed
need install

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -qO- "$1"; }
else
  fail "missing required command: curl or wget"
fi

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) fail "unsupported OS: $os" ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) fail "unsupported architecture: $arch" ;;
esac

if [ "$os" = "darwin" ] \
  && [ "$NO_BREW" != "1" ] \
  && [ "$VERSION" = "latest" ] \
  && [ "$REPO" = "$DEFAULT_REPO" ] \
  && [ "$BINDIR" = "$DEFAULT_BINDIR" ] \
  && command -v brew >/dev/null 2>&1; then
  log "Homebrew detected; installing lazycaddy with Homebrew"
  brew install kysely/tap/lazycaddy
  log "Installed lazycaddy with Homebrew"
  exit 0
fi

if [ "$VERSION" = "latest" ]; then
  api_url="https://api.github.com/repos/$REPO/releases/latest"
else
  api_url="https://api.github.com/repos/$REPO/releases/tags/$VERSION"
fi

TMPDIR=$(mktemp -d)
release_json="$TMPDIR/release.json"
fetch "$api_url" > "$release_json" || fail "could not fetch release metadata from $api_url"

asset_url=$(grep '"browser_download_url"' "$release_json" \
  | grep "${os}_${arch}.*\.tar\.gz" \
  | sed -E 's/.*"browser_download_url": "([^"]+)".*/\1/' \
  | head -n 1)

[ -n "$asset_url" ] || fail "could not find ${os}_${arch} tarball in release assets"

checksums_url=$(grep '"browser_download_url"' "$release_json" \
  | grep 'checksums.txt' \
  | sed -E 's/.*"browser_download_url": "([^"]+)".*/\1/' \
  | head -n 1 || true)

archive="$TMPDIR/lazycaddy.tar.gz"
log "Downloading $asset_url"
fetch "$asset_url" > "$archive" || fail "download failed"

if [ -n "$checksums_url" ]; then
  checksums="$TMPDIR/checksums.txt"
  fetch "$checksums_url" > "$checksums" || fail "could not download checksums"
  archive_name=$(basename "$asset_url")
  expected=$(grep "  $archive_name\$" "$checksums" | sed 's/[[:space:]].*//' | head -n 1 || true)
  if [ -n "$expected" ]; then
    if command -v sha256sum >/dev/null 2>&1; then
      actual=$(sha256sum "$archive" | sed 's/[[:space:]].*//')
    elif command -v shasum >/dev/null 2>&1; then
      actual=$(shasum -a 256 "$archive" | sed 's/[[:space:]].*//')
    else
      actual=""
      log "Skipping checksum verification: sha256sum or shasum not found"
    fi
    if [ -n "$actual" ] && [ "$actual" != "$expected" ]; then
      fail "checksum mismatch"
    fi
    [ -z "$actual" ] || log "Checksum verified"
  fi
fi

tar -xzf "$archive" -C "$TMPDIR" || fail "could not extract archive"
[ -f "$TMPDIR/$BIN" ] || fail "archive did not contain $BIN"
chmod 755 "$TMPDIR/$BIN"

if mkdir -p "$BINDIR" 2>/dev/null && [ -w "$BINDIR" ]; then
  install -m 755 "$TMPDIR/$BIN" "$BINDIR/$BIN"
else
  if command -v sudo >/dev/null 2>&1; then
    sudo mkdir -p "$BINDIR"
    sudo install -m 755 "$TMPDIR/$BIN" "$BINDIR/$BIN"
  else
    fail "$BINDIR is not writable and sudo is not available; set BINDIR to a writable directory"
  fi
fi

log "Installed $BIN to $BINDIR/$BIN"
log "Run: $BIN --version"
