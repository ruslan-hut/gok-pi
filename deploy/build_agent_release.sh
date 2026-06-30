#!/usr/bin/env bash
#
# Helper script to build the agent binary (`gok`) and emit a VERSION manifest
# containing the SHA-256 of the compiled artifact. Optionally, the script can
# invoke an upload helper that publishes the binary and manifest to a download
# location.

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
  cat <<EOF
Usage: $(basename "$0") [flags]

Flags:
  -o, --output DIR         Output directory for build artifacts (default: \$ROOT_DIR/dist/agent)
      --binary-name NAME   Agent binary filename to generate (default: gok)
      --updater-name NAME  Updater binary filename to generate (default: agentupdater)
      --goos GOOS          GOOS for cross compilation (default: linux)
      --goarch GOARCH      GOARCH for cross compilation (default: arm64)
      --upload CMD         Optional shell command to run after build; receives agent binary
                           path as \$1, VERSION path as \$2, and updater binary path as \$3.
  -h, --help               Show this help message.

Environment:
  CGO_ENABLED            Defaults to 0 for reproducible static builds.

Examples:
  ./deploy/build_agent_release.sh -o /tmp/release
  ./deploy/build_agent_release.sh --upload 'scp "\$1" "\$2" user@host:/srv/downloads/'
EOF
}

OUTPUT_DIR="$ROOT_DIR/dist/agent"
BINARY_NAME="gok"
UPDATER_NAME="agentupdater"
GOOS="${GOOS:-linux}"
GOARCH="${GOARCH:-arm64}"
UPLOAD_CMD=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    -o|--output)
      OUTPUT_DIR="$2"
      shift 2
      ;;
    --binary-name)
      BINARY_NAME="$2"
      shift 2
      ;;
    --updater-name)
      UPDATER_NAME="$2"
      shift 2
      ;;
    --goos)
      GOOS="$2"
      shift 2
      ;;
    --goarch)
      GOARCH="$2"
      shift 2
      ;;
    --upload)
      UPLOAD_CMD="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

mkdir -p "$OUTPUT_DIR"

CGO_ENABLED="${CGO_ENABLED:-0}"
export CGO_ENABLED

OUTPUT_BIN="${OUTPUT_DIR}/${BINARY_NAME}"
VERSION_FILE="${OUTPUT_DIR}/VERSION"
UPDATER_BIN="${OUTPUT_DIR}/${UPDATER_NAME}"

AGENT_VERSION="$(git -C "$ROOT_DIR" describe --tags --always --dirty 2>/dev/null || echo dev)"
echo "[build] agent version: ${AGENT_VERSION}"

build_binary() {
  local target="$1"
  local pkg="$2"
  local ldflags="$3"
  echo "[build] GOOS=${GOOS} GOARCH=${GOARCH} -> ${target}"
  GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath -ldflags "$ldflags" -o "$target" "$pkg"
  if [[ ! -x "$target" ]]; then
    chmod +x "$target"
  fi
}

build_binary "$OUTPUT_BIN" "$ROOT_DIR/cmd/gok" \
  "-s -w -X gok-pi/internal/remote/wsclient.version=${AGENT_VERSION}"
build_binary "$UPDATER_BIN" "$ROOT_DIR/cmd/agentupdater" "-s -w"

compute_sha256() {
  local target="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$target" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$target" | awk '{print $1}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$target" | awk '{print $NF}'
  else
    echo "Unable to locate a SHA-256 utility (sha256sum, shasum, openssl)." >&2
    exit 1
  fi
}

HASH="$(compute_sha256 "$OUTPUT_BIN")"
printf '%s\n' "$HASH" > "$VERSION_FILE"

echo "[build] VERSION -> ${VERSION_FILE}"

if [[ -n "$UPLOAD_CMD" ]]; then
  echo "[upload] ${UPLOAD_CMD}"
  # shellcheck disable=SC2086
  eval "$UPLOAD_CMD" '"$OUTPUT_BIN"' '"$VERSION_FILE"' '"$UPDATER_BIN"'
fi

echo "[done] Agent binary hash: $HASH"
echo "[done] Updater binary: ${UPDATER_BIN}"

