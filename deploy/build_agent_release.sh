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
  -o, --output DIR       Output directory for build artifacts (default: \$ROOT_DIR/dist/agent)
      --binary-name NAME Binary filename to generate (default: gok)
      --goos GOOS        GOOS for cross compilation (default: linux)
      --goarch GOARCH    GOARCH for cross compilation (default: arm64)
      --upload CMD       Optional shell command to run after build; receives binary
                         path as \$1 and VERSION path as \$2.
  -h, --help             Show this help message.

Environment:
  CGO_ENABLED            Defaults to 0 for reproducible static builds.

Examples:
  ./deploy/build_agent_release.sh -o /tmp/release
  ./deploy/build_agent_release.sh --upload 'scp "\$1" "\$2" user@host:/srv/downloads/'
EOF
}

OUTPUT_DIR="$ROOT_DIR/dist/agent"
BINARY_NAME="gok"
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

echo "[build] GOOS=${GOOS} GOARCH=${GOARCH} -> ${OUTPUT_BIN}"
GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath -ldflags "-s -w" -o "$OUTPUT_BIN" "$ROOT_DIR/cmd/gok"

if [[ ! -x "$OUTPUT_BIN" ]]; then
  chmod +x "$OUTPUT_BIN"
fi

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
  eval "$UPLOAD_CMD" '"$OUTPUT_BIN"' '"$VERSION_FILE"'
fi

echo "[done] Binary hash: $HASH"

