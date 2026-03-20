#!/bin/bash
###############################################################################
# start-mcp-server.sh - Start skillweave on z/OS and forward the port
#
# Usage:
#   ./scripts/start-mcp-server.sh
#   ./scripts/start-mcp-server.sh --port 7377
###############################################################################

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

JUMP_USER="${JUMP_USER:-itodorov}"
JUMP_HOST="${JUMP_HOST:-rogi21.fyre.ibm.com}"
ZOS_HOST="${ZOS_HOST:-zoscan2b.pok.stglabs.ibm.com}"
ZOS_USER="${ZOS_USER:-itodoro}"
ZOS_DIR="${ZOS_DIR:-skillweave}"
PORT="${PORT:-7377}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --port) PORT="$2"; shift 2 ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

echo "Starting skillweave on ${ZOS_USER}@${ZOS_HOST}:${PORT} ..."
echo "Local MCP endpoint: http://localhost:${PORT}"
echo "Press Ctrl-C to stop."
echo ""

ssh -N \
    -o ExitOnForwardFailure=yes \
    -L "${PORT}:localhost:${PORT}" \
    -J "${JUMP_USER}@${JUMP_HOST}" \
    "${ZOS_USER}@${ZOS_HOST}" &

TUNNEL_PID=$!
trap "kill ${TUNNEL_PID} 2>/dev/null || true" EXIT INT TERM

"${SCRIPT_DIR}/ssh-zos.sh" --project \
    "./skillweave -http :${PORT}"
