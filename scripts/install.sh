#!/bin/sh
# Install nrp-mcp from the latest GitHub release (Linux, macOS).
#   curl -fsSL https://raw.githubusercontent.com/UCR-Research-Computing/nrp-mcp/main/scripts/install.sh | sh
# Installs into ~/.local/bin (no admin rights), checks SHA256SUMS. Override with
# NRP_MCP_VERSION=v0.6.0 or NRP_MCP_BIN=/some/dir.
set -eu
REPO="UCR-Research-Computing/nrp-mcp"
BIN="${NRP_MCP_BIN:-$HOME/.local/bin}"
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in linux|darwin) ;; *) echo "unsupported OS: $os (on Windows, download the zip from the Releases page)"; exit 1;; esac
arch="$(uname -m)"
case "$arch" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) echo "unsupported CPU: $arch"; exit 1;; esac
ver="${NRP_MCP_VERSION:-}"
if [ -z "$ver" ]; then
  ver="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)"
fi
[ -n "$ver" ] || { echo "could not find the latest release"; exit 1; }
name="nrp-mcp_${ver#v}_${os}_${arch}"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
base="https://github.com/$REPO/releases/download/$ver"
echo "Downloading nrp-mcp $ver for $os/$arch ..."
curl -fsSL -o "$tmp/$name.tar.gz" "$base/$name.tar.gz"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS"
want="$(grep " $name.tar.gz\$" "$tmp/SHA256SUMS" | cut -d' ' -f1)"
if command -v sha256sum >/dev/null 2>&1; then got="$(sha256sum "$tmp/$name.tar.gz" | cut -d' ' -f1)"; else got="$(shasum -a 256 "$tmp/$name.tar.gz" | cut -d' ' -f1)"; fi
[ -n "$want" ] && [ "$want" = "$got" ] || { echo "SHA256 mismatch; not installing"; exit 1; }
tar -C "$tmp" -xzf "$tmp/$name.tar.gz"
mkdir -p "$BIN"
install -m 0755 "$tmp/$name/nrp-mcp" "$BIN/nrp-mcp"
echo "Installed $BIN/nrp-mcp ($("$BIN/nrp-mcp" version))"
case ":$PATH:" in *":$BIN:"*) ;; *) echo "Add $BIN to your PATH, or run it by full path.";; esac
echo "Next: download your NRP config at https://nrp.ai/config, then run: nrp-mcp setup"
