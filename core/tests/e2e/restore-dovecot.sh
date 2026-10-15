#!/usr/bin/env bash
# End to end: back up a Plesk-shaped Maildir, delete a message, restore into
# a new IMAP folder through a real Dovecot (Debian 12) in Docker.
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../../.." && pwd)
RESTIC=${RELICAL_RESTIC:-$ROOT/.tools/restic}
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
(cd "$ROOT/core" && CGO_ENABLED=0 go build -o "$WORK/relical" ./cmd/relical)
cp "$RESTIC" "$WORK/restic"
cp "$ROOT/core/tests/e2e/inside.sh" "$WORK/inside.sh"
# RELICAL_DOCKER_ARGS lets a host with no container egress add e.g. "--network host".
# shellcheck disable=SC2086
docker run --rm ${RELICAL_DOCKER_ARGS:-} -v "$WORK:/work:ro" debian:12 bash /work/inside.sh
