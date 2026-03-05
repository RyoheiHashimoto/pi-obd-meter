#!/bin/bash
# pi-obd-meter deploy script
# Usage: ./scripts/deploy.sh [version]
# Example: ./scripts/deploy.sh v0.1.0
#   No args = latest release

set -euo pipefail

REPO="YOUR_USER/pi-obd-meter"  # ← GitHub ユーザー名に書き換え
INSTALL_DIR="/opt/pi-obd-meter"
SERVICE_NAME="pi-obd-meter"

# Get version
if [ -n "${1:-}" ]; then
  VERSION="$1"
else
  echo "Fetching latest release..."
  VERSION=$(curl -s "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
  if [ -z "$VERSION" ]; then
    echo "Error: Could not determine latest version"
    exit 1
  fi
fi

echo "Deploying ${VERSION}..."

# Download
URL="https://github.com/${REPO}/releases/download/${VERSION}/pi-obd-meter-${VERSION}-arm64.tar.gz"
TMPDIR=$(mktemp -d)
echo "Downloading ${URL}..."
curl -fsSL "$URL" -o "${TMPDIR}/release.tar.gz"

# Extract
echo "Extracting..."
tar xzf "${TMPDIR}/release.tar.gz" -C "${TMPDIR}"

# Stop service
echo "Stopping service..."
sudo systemctl stop "${SERVICE_NAME}" 2>/dev/null || true

# Install
echo "Installing to ${INSTALL_DIR}..."
mkdir -p "${INSTALL_DIR}"
cp "${TMPDIR}/pi-obd-meter/pi-obd-meter" "${INSTALL_DIR}/pi-obd-meter"
cp "${TMPDIR}/pi-obd-meter/pi-obd-scanner" "${INSTALL_DIR}/pi-obd-scanner" 2>/dev/null || true
chmod +x "${INSTALL_DIR}/pi-obd-meter"
chmod +x "${INSTALL_DIR}/pi-obd-scanner" 2>/dev/null || true
cp -r "${TMPDIR}/pi-obd-meter/web/" "${INSTALL_DIR}/web/"
cp -r "${TMPDIR}/pi-obd-meter/configs/" "${INSTALL_DIR}/configs/" 2>/dev/null || true

# Cleanup
rm -rf "${TMPDIR}"

# Restart service
echo "Starting service..."
sudo systemctl start "${SERVICE_NAME}"

echo "Deployed ${VERSION} successfully!"
echo "Check status: sudo systemctl status ${SERVICE_NAME}"
