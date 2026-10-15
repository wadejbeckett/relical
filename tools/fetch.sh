#!/usr/bin/env bash
# Fetch the pinned restic and rest-server release binaries into .tools/,
# verified against tools/SHA256SUMS.pinned.
set -euo pipefail
cd "$(dirname "$0")/.."
RESTIC_VERSION=0.19.1
REST_SERVER_VERSION=0.14.0
DL=.tools/dl
mkdir -p "$DL"
RESTIC_FILE="restic_${RESTIC_VERSION}_linux_amd64.bz2"
REST_FILE="rest-server_${REST_SERVER_VERSION}_linux_amd64.tar.gz"
curl -fsSL --proto '=https' -o "$DL/$RESTIC_FILE" \
  "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/${RESTIC_FILE}"
curl -fsSL --proto '=https' -o "$DL/$REST_FILE" \
  "https://github.com/restic/rest-server/releases/download/v${REST_SERVER_VERSION}/${REST_FILE}"
(cd "$DL" && sha256sum -c ../../tools/SHA256SUMS.pinned)
bunzip2 -c "$DL/$RESTIC_FILE" > .tools/restic
chmod 755 .tools/restic
tar -xzf "$DL/$REST_FILE" -C "$DL"
install -m 755 "$DL/rest-server_${REST_SERVER_VERSION}_linux_amd64/rest-server" .tools/rest-server
.tools/restic version
.tools/rest-server --version
