#!/bin/bash
###############################################################################
# sync-and-build.sh - Sync skillweave and build on z/OS
#
# Uses rsync for fast iteration, then runs a Go build remotely.
#
# Usage:
#   ./scripts/sync-and-build.sh
#   ./scripts/sync-and-build.sh --test
###############################################################################

set -euo pipefail

RUN_TEST=0

while [[ $# -gt 0 ]]; do
    case "$1" in
        --test|--check)
            RUN_TEST=1
            shift
            ;;
        --help|-h)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --test      Build and run go tests"
            echo "  --check     Alias for --test"
            echo "  --help      Show this help"
            echo ""
            echo "Environment overrides:"
            echo "  JUMP_USER   (default: itodorov)"
            echo "  JUMP_HOST   (default: rogi21.fyre.ibm.com)"
            echo "  ZOS_HOST    (default: zoscan2b.pok.stglabs.ibm.com)"
            echo "  ZOS_USER    (default: itodoro)"
            echo "  ZOS_DIR     (default: skillweave)"
            echo "  RSYNC_PATH  (default: /home/itodoro/zopen/usr/local/bin/rsync)"
            echo "  GO_BIN      (default: /home/itodoro/install_test/go1.25/bin)"
            echo "  GOMAXPROCS  (default: 1)"
            echo "  GOPROXY     (default: direct)"
            echo "  GOSUMDB     (default: off)"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

JUMP_USER="${JUMP_USER:-itodorov}"
JUMP_HOST="${JUMP_HOST:-rogi21.fyre.ibm.com}"
ZOS_HOST="${ZOS_HOST:-zoscan2b.pok.stglabs.ibm.com}"
ZOS_USER="${ZOS_USER:-itodoro}"
ZOS_DIR="${ZOS_DIR:-skillweave}"
RSYNC_PATH="${RSYNC_PATH:-/home/itodoro/zopen/usr/local/bin/rsync}"
GO_BIN="${GO_BIN:-/home/itodoro/install_test/go1.25/bin}"
GOMAXPROCS="${GOMAXPROCS:-1}"
GOPROXY="${GOPROXY:-direct}"
GOSUMDB="${GOSUMDB:-off}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

echo "Syncing to ${ZOS_USER}@${ZOS_HOST}:~/${ZOS_DIR} via ${JUMP_USER}@${JUMP_HOST}..."
echo ""

rsync -avz --progress \
    --rsync-path="${RSYNC_PATH}" \
    -e "ssh -J ${JUMP_USER}@${JUMP_HOST}" \
    --exclude '.git/' \
    --exclude '.DS_Store' \
    --exclude 'skillweave' \
    --exclude '*.log' \
    "${ROOT_DIR}/" "${ZOS_USER}@${ZOS_HOST}:~/${ZOS_DIR}/"

echo ""
echo "Files synced. Building on z/OS..."
echo ""

if [[ "${RUN_TEST}" -eq 1 ]]; then
    REMOTE_TEST_CMD='go test ./...'
    TEST_STATUS_MSG='Running tests (go test ./...)...'
else
    REMOTE_TEST_CMD=':'
    TEST_STATUS_MSG='Skipping tests'
fi

ssh -J "${JUMP_USER}@${JUMP_HOST}" "${ZOS_USER}@${ZOS_HOST}" <<ENDSSH
set -e

if [ -f /home/itodoro/zopen/etc/zopen-config ]; then
    set +u
    . /home/itodoro/zopen/etc/zopen-config
    set -u
fi

if [ -f ~/.profile ]; then
    . ~/.profile
fi

export PATH=/home/itodoro/zopen/usr/local/bin:/home/itodoro/zopen/usr/local/altbin:\$PATH
if [ -x "${GO_BIN}/go" ]; then
    export PATH="${GO_BIN}:\$PATH"
fi
export GOMAXPROCS="${GOMAXPROCS}"
export CGO_ENABLED=0
export GOPROXY="${GOPROXY}"
export GOSUMDB="${GOSUMDB}"

if ! command -v go >/dev/null 2>&1; then
    echo "ERROR: go is not in PATH on z/OS."
    echo "Checked GO_BIN=${GO_BIN} and zopen paths."
    exit 1
fi

cd ~/${ZOS_DIR}

echo "Go: \$(go version)"
echo "GOOS/GOARCH: \$(go env GOOS)/\$(go env GOARCH)"
echo "CGO_ENABLED: \${CGO_ENABLED}"
echo "GOPROXY: \${GOPROXY}"
echo "GOSUMDB: \${GOSUMDB}"
echo "Downloading module dependencies..."
if ! go mod download; then
    echo "ERROR: go mod download failed."
    exit 1
fi

echo "Building skillweave..."
if ! go build -buildvcs=false -o skillweave .; then
    echo "ERROR: go build failed."
    exit 1
fi

echo "${TEST_STATUS_MSG}"
if ! ${REMOTE_TEST_CMD}; then
    echo "ERROR: test command failed."
    exit 1
fi

echo ""
echo "Build complete."
ENDSSH

echo ""
if [[ "${RUN_TEST}" -eq 1 ]]; then
    echo "Sync, build, and tests completed."
else
    echo "Sync and build completed."
fi
