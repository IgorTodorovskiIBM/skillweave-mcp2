#!/bin/bash
###############################################################################
# bootstrap-mcp-config.sh - Set up git/gh on z/OS and print a paste-ready MCP
# configuration block for skillweave.
#
# Prerequisites on z/OS: go, git, gh (GitHub CLI)
#
# Usage:
#   ./scripts/bootstrap-mcp-config.sh
#   ./scripts/bootstrap-mcp-config.sh --print-only
#   ./scripts/bootstrap-mcp-config.sh --install-only
#   ./scripts/bootstrap-mcp-config.sh --name my-skillweave
###############################################################################

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

MCP_NAME="${MCP_NAME:-skillweave}"
REMOTE_PROJECT_DIR="${REMOTE_PROJECT_DIR:-/home/itodoro/skillweave}"
GO_BIN="${GO_BIN:-/home/itodoro/install_test/go1.25/bin}"
CACHE_DIR="${CACHE_DIR:-/home/itodoro/.skillweave}"
DO_INSTALL=1
DO_PRINT=1
MODE="${MODE:-auto}"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --name)
            MCP_NAME="$2"
            shift 2
            ;;
        --remote-project-dir)
            REMOTE_PROJECT_DIR="$2"
            shift 2
            ;;
        --cache-dir)
            CACHE_DIR="$2"
            shift 2
            ;;
        --local)
            MODE="local"
            shift
            ;;
        --ssh|--remote)
            MODE="ssh"
            shift
            ;;
        --print-only)
            DO_INSTALL=0
            shift
            ;;
        --install-only)
            DO_PRINT=0
            shift
            ;;
        --help|-h)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "Options:"
            echo "  --name NAME                 MCP config key (default: ${MCP_NAME})"
            echo "  --remote-project-dir PATH   Remote project dir"
            echo "  --cache-dir PATH            Remote cache dir for repos/ledger"
            echo "  --local                     Run install locally on z/OS USS"
            echo "  --ssh                       Force install through ssh-zos.sh"
            echo "  --print-only                Skip remote install; only print JSON"
            echo "  --install-only              Run install; skip JSON output"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

is_local_zos() {
    local os_name
    os_name="$(uname -s 2>/dev/null || true)"
    [[ "${os_name}" =~ ^(OS/390|z/OS)$ ]]
}

run_install_cmd() {
    local install_cmd="$1"
    if [[ "${MODE}" == "local" ]] || [[ "${MODE}" == "auto" && is_local_zos ]]; then
        (
            cd "${REMOTE_PROJECT_DIR}"
            eval "${install_cmd}"
        )
    else
        env GO_BIN="${GO_BIN}" \
            "${SCRIPT_DIR}/ssh-zos.sh" --project "${install_cmd}"
    fi
}

if [[ "${DO_INSTALL}" -eq 1 ]]; then
    echo "Verifying prerequisites on z/OS (go, git, gh)..."
    REMOTE_CMD="set -e; \
echo \"Go: \$(go version)\"; \
echo \"Git: \$(git --version)\"; \
if command -v gh >/dev/null 2>&1; then echo \"gh: \$(gh --version | head -1)\"; else echo 'WARNING: gh CLI not found - PR creation will not work'; fi; \
echo 'Prerequisites check complete.'"
    run_install_cmd "${REMOTE_CMD}"
fi

if [[ "${DO_PRINT}" -eq 1 ]]; then
    PATH_VALUE="${GO_BIN}:/home/itodoro/zopen/usr/local/bin:/home/itodoro/zopen/usr/local/altbin:/bin:/usr/bin"
    cat <<EOF
"${MCP_NAME}": {
  "command": "${REMOTE_PROJECT_DIR}/skillweave",
  "env": {
    "PATH": "${PATH_VALUE}",
    "HOME": "/home/itodoro"
  },
  "args": [
    "-cache-dir",
    "${CACHE_DIR}"
  ]
}
EOF
fi
