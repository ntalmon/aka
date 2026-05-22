#!/usr/bin/env sh
set -e

REPO="ntalmon/aka-cli"
BINARY="aka"

# Detect OS
case "$(uname -s)" in
  Darwin) OS="darwin" ;;
  Linux)  OS="linux"  ;;
  *)
    echo "Unsupported OS: $(uname -s)" >&2
    exit 1
    ;;
esac

# Detect architecture
case "$(uname -m)" in
  x86_64)         ARCH="amd64"  ;;
  arm64|aarch64)  ARCH="arm64"  ;;
  *)
    echo "Unsupported architecture: $(uname -m)" >&2
    exit 1
    ;;
esac

# Resolve latest version if not pinned
if [ -z "$AKA_VERSION" ]; then
  AKA_VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | sed 's/.*"tag_name": "\(.*\)".*/\1/')"
fi

if [ -z "$AKA_VERSION" ]; then
  echo "Could not determine latest version. Set AKA_VERSION to pin a release." >&2
  exit 1
fi

ARCHIVE="${BINARY}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${AKA_VERSION}/${ARCHIVE}"
CHECKSUM_URL="https://github.com/${REPO}/releases/download/${AKA_VERSION}/checksums.txt"

echo "Installing aka ${AKA_VERSION} (${OS}/${ARCH})..."

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

curl -fsSL "$URL" -o "${TMP}/${ARCHIVE}"
curl -fsSL "$CHECKSUM_URL" -o "${TMP}/checksums.txt"

# Verify checksum
cd "$TMP"
if command -v sha256sum >/dev/null 2>&1; then
  grep "${ARCHIVE}" checksums.txt | sha256sum --check --status
elif command -v shasum >/dev/null 2>&1; then
  grep "${ARCHIVE}" checksums.txt | shasum -a 256 --check --status
else
  echo "Error: sha256sum/shasum not found — cannot verify download integrity" >&2
  echo "Install coreutils (Linux) or use macOS built-in shasum, then retry." >&2
  exit 1
fi
cd - >/dev/null

tar -xzf "${TMP}/${ARCHIVE}" -C "$TMP"

# Choose install directory
if [ -w "/usr/local/bin" ]; then
  INSTALL_DIR="/usr/local/bin"
elif [ -d "$HOME/.local/bin" ]; then
  INSTALL_DIR="$HOME/.local/bin"
else
  mkdir -p "$HOME/.local/bin"
  INSTALL_DIR="$HOME/.local/bin"
fi

install -m 755 "${TMP}/${BINARY}" "${INSTALL_DIR}/${BINARY}"

echo "Installed: ${INSTALL_DIR}/${BINARY}"
echo ""

# PATH hint if needed
case ":${PATH}:" in
  *":${INSTALL_DIR}:"*) ;;
  *)
    echo "Note: add ${INSTALL_DIR} to your PATH:"
    echo "  export PATH=\"\$PATH:${INSTALL_DIR}\""
    echo ""
    ;;
esac

echo "Run 'aka init' to get started."
