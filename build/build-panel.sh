#!/usr/bin/env bash
#
# Build an installable control-panel extension zip: the panel adapter
# (panels/<panel>/), the relical core built from the same commit, and the
# pinned restic release, with a SHA256SUMS that the extension's post-install
# checks before it installs either binary.
#
# Everything comes from `git archive` of one ref, never from the working
# tree, so only committed files ship and the core inside matches the
# extension. restic is extracted from the release archive cached by
# tools/fetch.sh (`make tools`) after checking it against
# tools/SHA256SUMS.pinned from the same ref.
#
# Usage:
#   build/build-panel.sh <panel> <version>
# Example:
#   build/build-panel.sh plesk 0.2.0   ->  dist/relical-0.2.0.zip and dist/relical.zip
#
# Environment:
#   BUILD_REF          the commit or tag to build (default: HEAD)
#   RELICAL_TOOLS_DIR  where tools/fetch.sh put its downloads (default: .tools
#                      in this checkout, or in the main checkout for a worktree)
#
set -euo pipefail

PANEL="${1:?usage: build-panel.sh <panel> <version>}"
VERSION="${2:?usage: build-panel.sh <panel> <version>}"
REF="${BUILD_REF:-HEAD}"

die() { echo "build-panel: $*" >&2; exit 1; }

[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([.-][A-Za-z0-9.]+)?$ ]] || die "version '$VERSION' is not like 0.2.0"
[[ "$PANEL" =~ ^[a-z0-9-]+$ ]] || die "panel '$PANEL' is not a plain name"
for tool in git go zip bunzip2 sha256sum tar; do
    command -v "$tool" >/dev/null 2>&1 || die "$tool is not installed"
done

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DIST="$ROOT/dist"

git -C "$ROOT" rev-parse --is-inside-work-tree >/dev/null 2>&1 \
    || die "$ROOT is not a git checkout; the package is built from 'git archive', not the working tree"
COMMIT="$(git -C "$ROOT" rev-parse --verify --quiet "$REF^{commit}")" || die "unknown ref '$REF'"

# .tools/ is not in git; a worktree finds it in the main checkout.
if [ -n "${RELICAL_TOOLS_DIR:-}" ]; then
    TOOLS="$RELICAL_TOOLS_DIR"
elif [ -d "$ROOT/.tools" ]; then
    TOOLS="$ROOT/.tools"
else
    COMMON="$(git -C "$ROOT" rev-parse --path-format=absolute --git-common-dir)"
    TOOLS="$(dirname "$COMMON")/.tools"
fi
[ -d "$TOOLS/dl" ] || die "no restic download in $TOOLS/dl; run 'make tools' first"

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

# 1) The committed panel, core and pinned checksums for the ref.
SRC="$STAGE/src"
mkdir -p "$SRC"
git -C "$ROOT" archive --format=tar "$COMMIT" "panels/$PANEL" core tools/SHA256SUMS.pinned | tar -x -C "$SRC" \
    || die "panels/$PANEL, core or tools/SHA256SUMS.pinned is missing in $REF"
PANEL_DIR="$SRC/panels/$PANEL"
[ -f "$PANEL_DIR/meta.xml" ] || die "panels/$PANEL/meta.xml is not in $REF"

ID="$(sed -n 's:[[:space:]]*<id>\(.*\)</id>.*:\1:p' "$PANEL_DIR/meta.xml" | head -n1)"
[[ "$ID" =~ ^[a-z0-9-]+$ ]] || die "could not read a plain <id> from panels/$PANEL/meta.xml"
META_VERSION="$(sed -n 's:[[:space:]]*<version>\(.*\)</version>.*:\1:p' "$PANEL_DIR/meta.xml" | head -n1)"
[ "$META_VERSION" = "$VERSION" ] \
    || die "meta.xml says version '$META_VERSION' but building '$VERSION'; bump meta.xml first"

# 2) The package layout. Tests, composer files and the phpunit config live
#    outside these four paths and are not shipped.
PKG="$STAGE/pkg"
mkdir -p "$PKG"
cp -R "$PANEL_DIR/meta.xml" "$PANEL_DIR/plib" "$PANEL_DIR/htdocs" "$PANEL_DIR/_meta" "$PKG/"
BIN="$PKG/plib/bin"
mkdir -p "$BIN"
rm -f "$BIN/.gitkeep" "$BIN/relical" "$BIN/restic" "$BIN/SHA256SUMS"

# 3) The core, from the archived tree.
( cd "$SRC/core" && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOFLAGS=-mod=readonly \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$BIN/relical" ./cmd/relical ) \
    || die "the core did not build"
"$BIN/relical" version >/dev/null 2>&1 || die "the built core does not run"

# 4) restic, from the release archive whose sha256 is pinned.
PIN_LINE="$(grep -E '^[0-9a-f]{64}  restic_[0-9.]+_linux_amd64\.bz2$' "$SRC/tools/SHA256SUMS.pinned" | head -n1)" \
    || die "tools/SHA256SUMS.pinned has no restic linux_amd64 line"
RESTIC_ARCHIVE="${PIN_LINE#*  }"
[ -f "$TOOLS/dl/$RESTIC_ARCHIVE" ] || die "$TOOLS/dl/$RESTIC_ARCHIVE is missing; run 'make tools' first"
( cd "$TOOLS/dl" && printf '%s\n' "$PIN_LINE" | sha256sum --quiet -c - ) \
    || die "$TOOLS/dl/$RESTIC_ARCHIVE does not match tools/SHA256SUMS.pinned"
bunzip2 -c "$TOOLS/dl/$RESTIC_ARCHIVE" > "$BIN/restic"
chmod 0755 "$BIN/relical" "$BIN/restic"
"$BIN/restic" version >/dev/null 2>&1 || die "the extracted restic does not run"

# 5) The checksums post-install verifies.
( cd "$BIN" && sha256sum relical restic > SHA256SUMS )

# 6) Package.
OUT="$DIST/$ID-$VERSION.zip"   # versioned, one per release
ALIAS="$DIST/$ID.zip"          # stable name for the latest build
mkdir -p "$DIST"
rm -f "$OUT" "$ALIAS"
( cd "$PKG" && zip -rqX "$OUT" meta.xml plib htdocs _meta )
cp "$OUT" "$ALIAS"

SIZE="$(stat -c %s "$OUT")"
echo "build-panel: built $OUT ($SIZE bytes, $(( (SIZE + 1048575) / 1048576 )) MiB) from $REF ($COMMIT)"
echo "build-panel: stable copy $ALIAS"
sed 's/^/build-panel:   /' "$BIN/SHA256SUMS"
