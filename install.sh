#!/bin/sh
# Install the amarit binary from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/sudiptadeb/amarit/main/install.sh | sh
#
# AMARIT_VERSION=v0.6.0 pins a release (default: the latest).
# AMARIT_INSTALL_DIR=/some/bin picks the directory (default: /usr/local/bin
# when writable, else ~/.local/bin).
set -eu

REPO=sudiptadeb/amarit
VERSION=${AMARIT_VERSION:-latest}

say() { printf 'amarit: %s\n' "$*" >&2; }
die() { say "$*"; exit 1; }

case $(uname -s) in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) die "unsupported OS $(uname -s); build from source: go install github.com/$REPO/cmd/amarit@latest" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported architecture $(uname -m); build from source: go install github.com/$REPO/cmd/amarit@latest" ;;
esac
# Under Rosetta uname reports x86_64; prefer the native binary.
if [ "$os" = darwin ] && [ "$arch" = amd64 ] && [ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
	arch=arm64
fi

if [ "$VERSION" = latest ]; then
	base="https://github.com/$REPO/releases/latest/download"
else
	base="https://github.com/$REPO/releases/download/$VERSION"
fi
asset="amarit-$os-$arch"

if [ -n "${AMARIT_INSTALL_DIR:-}" ]; then
	dir=$AMARIT_INSTALL_DIR
elif [ -w /usr/local/bin ]; then
	dir=/usr/local/bin
else
	dir="$HOME/.local/bin"
fi
mkdir -p "$dir"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -qO "$2" "$1"; }
else
	die "need curl or wget"
fi

say "downloading $asset ($VERSION)"
fetch "$base/$asset" "$tmp/$asset" || die "download failed: $base/$asset"
fetch "$base/SHA256SUMS" "$tmp/SHA256SUMS" || die "download failed: $base/SHA256SUMS"

want=$(awk -v f="$asset" '$2 == f || $2 == "*" f {print $1}' "$tmp/SHA256SUMS")
[ -n "$want" ] || die "$asset is not listed in SHA256SUMS"
if command -v sha256sum >/dev/null 2>&1; then
	got=$(sha256sum "$tmp/$asset" | awk '{print $1}')
else
	got=$(shasum -a 256 "$tmp/$asset" | awk '{print $1}')
fi
[ "$want" = "$got" ] || die "checksum mismatch for $asset"

chmod 755 "$tmp/$asset"
mv -f "$tmp/$asset" "$dir/amarit"
say "installed $dir/amarit: $("$dir/amarit" version 2>&1 | head -n 1)"

case ":$PATH:" in
*":$dir:"*) ;;
*) say "$dir is not on your PATH; add: export PATH=\"$dir:\$PATH\"" ;;
esac
