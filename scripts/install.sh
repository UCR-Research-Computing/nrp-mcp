#!/bin/sh
# Install nrp-mcp from the latest GitHub release (Linux, macOS).
#   curl -fsSL https://raw.githubusercontent.com/UCR-Research-Computing/nrp-mcp/main/scripts/install.sh | sh
# Installs into ~/.local/bin (no admin rights), checks SHA256SUMS. Override with
# NRP_MCP_VERSION=v0.10.0, NRP_MCP_BIN=/some/dir, NRP_MCP_BASE=https://.../download/v0.10.0,
# or NRP_MCP_YES=1 (add ~/.local/bin to PATH without asking).
set -eu
REPO="UCR-Research-Computing/nrp-mcp"
BIN="${NRP_MCP_BIN:-$HOME/.local/bin}"
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in linux|darwin) ;; *) echo "unsupported OS: $os (on Windows, run scripts/install.ps1 in PowerShell)"; exit 1;; esac
arch="$(uname -m)"
case "$arch" in x86_64|amd64) arch=amd64;; arm64|aarch64) arch=arm64;; *) echo "unsupported CPU: $arch"; exit 1;; esac
ver="${NRP_MCP_VERSION:-}"
if [ -z "$ver" ]; then
  # Resolve via github.com redirect first (not rate-limited behind shared campus NAT).
  ver="$(curl -fsSI "https://github.com/$REPO/releases/latest" 2>/dev/null | tr -d '\r' | sed -n 's/^[Ll]ocation:.*\/tag\/\([^/?#]*\).*/\1/p' | tail -1)"
fi
if [ -z "$ver" ]; then
  ver="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)"
fi
[ -n "$ver" ] || { echo "could not find the latest release"; exit 1; }
name="nrp-mcp_${ver#v}_${os}_${arch}"
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
base="${NRP_MCP_BASE:-https://github.com/$REPO/releases/download/$ver}"
echo "Downloading nrp-mcp $ver for $os/$arch ..."
curl -fsSL -o "$tmp/$name.tar.gz" "$base/$name.tar.gz"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS"
want="$(grep -E " [ *]?$name\\.tar\\.gz\$" "$tmp/SHA256SUMS" | awk '{print $1}' | head -1)"
if command -v sha256sum >/dev/null 2>&1; then got="$(sha256sum "$tmp/$name.tar.gz" | awk '{print $1}')"; else got="$(shasum -a 256 "$tmp/$name.tar.gz" | awk '{print $1}')"; fi
[ -n "$want" ] && [ "$want" = "$got" ] || { echo "SHA256 mismatch; not installing"; exit 1; }
tar -C "$tmp" -xzf "$tmp/$name.tar.gz"
mkdir -p "$BIN"
rm -f "$BIN/nrp-mcp"
install -m 0755 "$tmp/$name/nrp-mcp" "$BIN/nrp-mcp"
if [ "$os" = darwin ] && command -v xattr >/dev/null 2>&1; then
  xattr -d com.apple.quarantine "$BIN/nrp-mcp" 2>/dev/null || true
fi
echo "Installed $BIN/nrp-mcp ($("$BIN/nrp-mcp" version))"

sh_name="$(basename "${SHELL:-}")"
if [ -z "$sh_name" ]; then
  if [ "$os" = darwin ]; then sh_name="zsh"; else sh_name="bash"; fi
fi
case "$sh_name" in
  zsh) profile="$HOME/.zprofile" ;;
  *)   profile="$HOME/.bashrc" ;;
esac

case ":$PATH:" in
  *":$BIN:"*) ;;
  *)
    ans="${NRP_MCP_YES:-}"
    if [ -z "$ans" ] && [ -r /dev/tty ]; then
      printf "Add %s to PATH in %s? [y/N] " "$BIN" "$profile" >/dev/tty
      read -r ans </dev/tty || ans=""
    fi
    case "$ans" in
      y|Y|yes|YES|1)
        line="export PATH=\"$BIN:\$PATH\""
        if [ ! -f "$profile" ] || ! grep -Fq "$BIN" "$profile" 2>/dev/null; then
          printf "\n%s\n" "$line" >> "$profile"
        fi
        echo "Added $BIN to PATH in $profile (open a new terminal, or run: source $profile)"
        ;;
      *)
        echo "To add $BIN to your PATH later: echo 'export PATH=\"$BIN:\$PATH\"' >> $profile"
        ;;
    esac
    ;;
esac

echo "Binary path: $BIN/nrp-mcp"
echo "Next: download your NRP config at https://nrp.ai/config, then run: $BIN/nrp-mcp setup"
