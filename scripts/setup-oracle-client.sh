#!/bin/bash
# Downloads Oracle Instant Client and places it in lib/oracle/
# Supports macOS (ARM64/x86_64) and Linux (x86_64/aarch64)

set -e

LIB_DIR="$(cd "$(dirname "$0")/.." && pwd)/lib/oracle"
VERSION="23.7.0.25.01"

detect_platform() {
    local os=$(uname -s | tr '[:upper:]' '[:lower:]')
    local arch=$(uname -m)

    case "$os" in
        darwin)
            case "$arch" in
                arm64)  echo "macos-arm64" ;;
                x86_64) echo "macos-x64" ;;
                *)      echo "unsupported: $os/$arch" && exit 1 ;;
            esac
            ;;
        linux)
            case "$arch" in
                x86_64)  echo "linux-x64" ;;
                aarch64) echo "linux-aarch64" ;;
                *)       echo "unsupported: $os/$arch" && exit 1 ;;
            esac
            ;;
        *)
            echo "unsupported OS: $os" && exit 1
            ;;
    esac
}

PLATFORM=$(detect_platform)
echo "Platform: $PLATFORM"

if [ -d "$LIB_DIR" ] && ls "$LIB_DIR"/libclntsh* 1>/dev/null 2>&1; then
    echo "Oracle Instant Client already exists at $LIB_DIR"
    exit 0
fi

mkdir -p "$LIB_DIR"

echo "Downloading Oracle Instant Client for $PLATFORM..."

case "$PLATFORM" in
    macos-arm64)
        # Oracle provides DMG for macOS — use Homebrew instead
        echo ""
        echo "For macOS, install via Homebrew:"
        echo ""
        echo "  brew tap instantclienttap/instantclient"
        echo "  brew install instantclient-basic"
        echo ""
        echo "Then create symlinks:"
        echo "  BREW_PREFIX=\$(brew --prefix)/lib"
        echo "  ln -sf \$BREW_PREFIX/libclntsh.dylib $LIB_DIR/"
        echo "  ln -sf \$BREW_PREFIX/libnnz*.dylib $LIB_DIR/"
        echo "  ln -sf \$BREW_PREFIX/liboramysql*.dylib $LIB_DIR/"
        echo ""
        echo "Or download manually from:"
        echo "  https://www.oracle.com/database/technologies/instant-client/macos-arm64-downloads.html"
        echo "  Extract the DMG and copy .dylib files to $LIB_DIR/"
        echo ""

        # Try Homebrew automatically
        if command -v brew &>/dev/null; then
            echo "Attempting Homebrew install..."
            brew tap instantclienttap/instantclient 2>/dev/null || true
            brew install instantclient-basic 2>/dev/null || {
                echo "Homebrew install failed. Please install manually."
                exit 1
            }
            BREW_LIB="$(brew --prefix)/lib"
            for f in "$BREW_LIB"/libclntsh* "$BREW_LIB"/libnnz* "$BREW_LIB"/liboramysql* "$BREW_LIB"/libociei* "$BREW_LIB"/libons*; do
                [ -f "$f" ] && ln -sf "$f" "$LIB_DIR/" && echo "  Linked: $(basename $f)"
            done
            echo "Done."
        fi
        ;;

    macos-x64)
        echo "Same as ARM64 — use Homebrew or manual download."
        echo "  brew tap instantclienttap/instantclient"
        echo "  brew install instantclient-basic"
        ;;

    linux-x64)
        URL="https://download.oracle.com/otn_software/linux/instantclient/2370000/instantclient-basic-linux.x64-${VERSION}dbru.zip"
        echo "Downloading from $URL"
        TMP=$(mktemp -d)
        curl -fSL "$URL" -o "$TMP/oci.zip"
        unzip -q "$TMP/oci.zip" -d "$TMP"
        cp "$TMP"/instantclient_*/lib*.so* "$LIB_DIR/" 2>/dev/null || cp "$TMP"/instantclient_*/*.so* "$LIB_DIR/"
        rm -rf "$TMP"
        echo "Installed to $LIB_DIR"
        ;;

    linux-aarch64)
        URL="https://download.oracle.com/otn_software/linux/instantclient/2370000/instantclient-basic-linux.arm64-${VERSION}dbru.zip"
        echo "Downloading from $URL"
        TMP=$(mktemp -d)
        curl -fSL "$URL" -o "$TMP/oci.zip"
        unzip -q "$TMP/oci.zip" -d "$TMP"
        cp "$TMP"/instantclient_*/lib*.so* "$LIB_DIR/" 2>/dev/null || cp "$TMP"/instantclient_*/*.so* "$LIB_DIR/"
        rm -rf "$TMP"
        echo "Installed to $LIB_DIR"
        ;;
esac

echo ""
echo "Library path: $LIB_DIR"
echo "Set before running: export DYLD_LIBRARY_PATH=$LIB_DIR  (macOS)"
echo "                 or: export LD_LIBRARY_PATH=$LIB_DIR    (Linux)"
