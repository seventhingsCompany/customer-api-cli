#!/bin/sh
# Install the seventhings CLI on Linux or macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/SeventhingsCompany/customer-api-cli/main/install.sh | sh
#   curl -fsSL .../install.sh | sh -s -- --version v1.2.3 --bin-dir ~/bin
#
# Options (or environment variables):
#   --version <tag>   SEVENTHINGS_VERSION      release tag, default: latest
#   --bin-dir <dir>   SEVENTHINGS_INSTALL_DIR  default: /usr/local/bin if writable, else ~/.local/bin
#   --no-man                                   do not install man pages
# SEVENTHINGS_DOWNLOAD_URL overrides the releases URL (mirrors, testing).
#
# The download is checked against checksums.txt. If cosign is installed, the
# Sigstore signature of checksums.txt is verified as well.
set -eu

REPO_URL="https://github.com/SeventhingsCompany/customer-api-cli"
BASE_URL="${SEVENTHINGS_DOWNLOAD_URL:-$REPO_URL/releases}"
VERSION="${SEVENTHINGS_VERSION:-latest}"
BIN_DIR="${SEVENTHINGS_INSTALL_DIR:-}"
INSTALL_MAN=1

say() { printf '%s\n' "$*" >&2; }
usage() {
	cat <<'USAGE'
Install the seventhings CLI.

Usage: install.sh [--version <tag>] [--bin-dir <dir>] [--no-man]

  --version <tag>   release to install (default: latest; env SEVENTHINGS_VERSION)
  --bin-dir <dir>   install directory (default: /usr/local/bin if writable,
                    else ~/.local/bin; env SEVENTHINGS_INSTALL_DIR)
  --no-man          do not install man pages
USAGE
}
fail() {
	say "error: $*"
	exit 1
}

while [ $# -gt 0 ]; do
	case "$1" in
	--version) [ $# -ge 2 ] || fail "--version needs a value"; VERSION="$2"; shift 2 ;;
	--version=*) VERSION="${1#*=}"; shift ;;
	--bin-dir) [ $# -ge 2 ] || fail "--bin-dir needs a value"; BIN_DIR="$2"; shift 2 ;;
	--bin-dir=*) BIN_DIR="${1#*=}"; shift ;;
	--no-man) INSTALL_MAN=0; shift ;;
	-h | --help) usage; exit 0 ;;
	*) fail "unknown option: $1" ;;
	esac
done

# --- platform ---------------------------------------------------------------
case "$(uname -s)" in
Linux) OS=linux ;;
Darwin) OS=darwin ;;
*) fail "unsupported OS $(uname -s); on Windows use install.ps1" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*) fail "unsupported architecture $(uname -m) (amd64 and arm64 are available)" ;;
esac

# --- tools ------------------------------------------------------------------
if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
else
	fail "curl or wget is required"
fi
if command -v sha256sum >/dev/null 2>&1; then
	sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
	sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
	fail "sha256sum or shasum is required to verify the download"
fi
command -v tar >/dev/null 2>&1 || fail "tar is required"

TMP="$(mktemp -d 2>/dev/null || mktemp -d -t seventhings)"
trap 'rm -rf "$TMP"' EXIT INT TERM

# --- resolve the release ----------------------------------------------------
if [ "$VERSION" = latest ]; then
	RELEASE_URL="$BASE_URL/latest/download"
else
	case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
	RELEASE_URL="$BASE_URL/download/$VERSION"
fi

say "Downloading checksums from $RELEASE_URL ..."
fetch "$RELEASE_URL/checksums.txt" "$TMP/checksums.txt" || fail "could not download $RELEASE_URL/checksums.txt (does the release exist?)"

# The checksum list names the archive, which carries the version number.
ARCHIVE="$(awk -v s="_${OS}_${ARCH}.tar.gz" 'index($2, s) == length($2) - length(s) + 1 && $2 ~ /^seventhings_/ { print $2; exit }' "$TMP/checksums.txt")"
[ -n "$ARCHIVE" ] || fail "no archive for ${OS}/${ARCH} in this release"
EXPECTED="$(awk -v f="$ARCHIVE" '$2 == f { print $1; exit }' "$TMP/checksums.txt")"
RELEASE_VERSION="${ARCHIVE#seventhings_}"
RELEASE_VERSION="${RELEASE_VERSION%_"${OS}"_"${ARCH}".tar.gz}"

# --- signature (optional) ---------------------------------------------------
if command -v cosign >/dev/null 2>&1; then
	say "Verifying the Sigstore signature of checksums.txt ..."
	fetch "$RELEASE_URL/checksums.txt.sigstore.json" "$TMP/checksums.txt.sigstore.json" ||
		fail "could not download the signature bundle"
	cosign verify-blob "$TMP/checksums.txt" \
		--bundle "$TMP/checksums.txt.sigstore.json" \
		--certificate-identity-regexp '^https://github.com/SeventhingsCompany/customer-api-cli/' \
		--certificate-oidc-issuer https://token.actions.githubusercontent.com >/dev/null 2>&1 ||
		fail "signature verification of checksums.txt failed"
else
	say "cosign not found; skipping signature verification (checksums are still verified)"
fi

# --- download and verify ----------------------------------------------------
say "Downloading seventhings $RELEASE_VERSION for ${OS}/${ARCH} ..."
fetch "$RELEASE_URL/$ARCHIVE" "$TMP/$ARCHIVE" || fail "could not download $ARCHIVE"
ACTUAL="$(sha256 "$TMP/$ARCHIVE")"
[ "$ACTUAL" = "$EXPECTED" ] || fail "checksum mismatch for $ARCHIVE (expected $EXPECTED, got $ACTUAL)"

mkdir -p "$TMP/x"
tar -xzf "$TMP/$ARCHIVE" -C "$TMP/x"
[ -f "$TMP/x/seventhings" ] || fail "archive does not contain the seventhings binary"

# --- install ----------------------------------------------------------------
if [ -z "$BIN_DIR" ]; then
	if [ -w /usr/local/bin ]; then
		BIN_DIR=/usr/local/bin
	else
		BIN_DIR="$HOME/.local/bin"
	fi
fi
case "$BIN_DIR" in "~"/*) BIN_DIR="$HOME/${BIN_DIR#"~/"}" ;; esac
mkdir -p "$BIN_DIR" 2>/dev/null || fail "cannot create $BIN_DIR (re-run with sudo, or use --bin-dir)"
[ -w "$BIN_DIR" ] || fail "$BIN_DIR is not writable (re-run with sudo, or use --bin-dir)"

# Copy then rename, so a running seventhings is never half-overwritten.
cp "$TMP/x/seventhings" "$BIN_DIR/.seventhings.new"
chmod 0755 "$BIN_DIR/.seventhings.new"
mv -f "$BIN_DIR/.seventhings.new" "$BIN_DIR/seventhings"
say "Installed $BIN_DIR/seventhings"

if [ "$INSTALL_MAN" = 1 ] && [ -d "$TMP/x/man" ]; then
	MAN_DIR="$(dirname "$BIN_DIR")/share/man/man1"
	if mkdir -p "$MAN_DIR" 2>/dev/null && [ -w "$MAN_DIR" ]; then
		cp "$TMP/x/man/"*.1.gz "$MAN_DIR/"
		say "Installed man pages to $MAN_DIR"
	fi
fi

case ":$PATH:" in
*":$BIN_DIR:"*) ;;
*) say "Note: $BIN_DIR is not on your PATH. Add it, e.g.: export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac

say ""
say "Shell completion: add one of these to your shell profile"
say "  bash: source <(seventhings completion bash)"
say "  zsh:  source <(seventhings completion zsh)"
say "  fish: seventhings completion fish | source"
say "Get started: seventhings auth login --url https://<tenant>.seventhings.com"
