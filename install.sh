#!/bin/bash
set -e

REPO="JonrGull/prflow"
BINARY="prflow"

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  darwin) OS="darwin" ;;
  linux) OS="linux" ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect architecture
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Install location - use ~/.local/bin for both (no sudo needed for updates)
INSTALL_DIR="$HOME/.local/bin"
mkdir -p "$INSTALL_DIR"

# Get latest version
echo "Fetching latest release..."
VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
if [ -z "$VERSION" ]; then
  echo "Failed to get latest version"
  exit 1
fi

# Download
ASSET="${BINARY}-${OS}-${ARCH}"
URL="https://github.com/$REPO/releases/download/$VERSION/$ASSET"
echo "Downloading $ASSET $VERSION..."

# A private directory, not a fixed name in /tmp that another user could
# create first.
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

curl -fsSL "$URL" -o "$TMP/$ASSET"
curl -fsSL "https://github.com/$REPO/releases/download/$VERSION/SHA256SUMS" -o "$TMP/SHA256SUMS"

# Verify before installing, as self-update does.
EXPECTED=$(grep " \*\?$ASSET\$" "$TMP/SHA256SUMS" | cut -d' ' -f1)
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL=$(sha256sum "$TMP/$ASSET" | cut -d' ' -f1)
else
  ACTUAL=$(shasum -a 256 "$TMP/$ASSET" | cut -d' ' -f1)
fi
if [ -z "$EXPECTED" ] || [ "$EXPECTED" != "$ACTUAL" ]; then
  echo "Checksum mismatch for $ASSET; not installing it"
  exit 1
fi

# Install
chmod +x "$TMP/$ASSET"
mv "$TMP/$ASSET" "$INSTALL_DIR/$BINARY"

echo "Installed $BINARY $VERSION to $INSTALL_DIR/$BINARY"

# Check PATH
if ! echo "$PATH" | grep -q "$INSTALL_DIR"; then
  echo ""
  echo "Add to your PATH:"
  echo "  export PATH=\"$INSTALL_DIR:\$PATH\""
fi
