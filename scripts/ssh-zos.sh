#!/bin/bash
###############################################################################
# ssh-zos.sh - Quick SSH to z/OS system for skillweave
#
# Simple wrapper for SSH to z/OS through jump host.
# Automatically sets up PATH for Go and git/gh.
#
# Usage:
#   ./scripts/ssh-zos.sh
#   ./scripts/ssh-zos.sh "ls -l"
#   ./scripts/ssh-zos.sh --project "go version"
###############################################################################

set -euo pipefail

JUMP_USER="${JUMP_USER:-itodorov}"
JUMP_HOST="${JUMP_HOST:-rogi21.fyre.ibm.com}"
ZOS_HOST="${ZOS_HOST:-zoscan2b.pok.stglabs.ibm.com}"
ZOS_USER="${ZOS_USER:-itodoro}"
ZOS_DIR="${ZOS_DIR:-skillweave}"
GO_BIN="${GO_BIN:-/home/itodoro/install_test/go1.25/bin}"
EXTRA_PATH="${EXTRA_PATH:-}"

USE_PROJECT_DIR=0
if [[ "${1:-}" == "--project" ]]; then
    USE_PROJECT_DIR=1
    shift
fi

path_parts=("/home/itodoro/zopen/usr/local/altbin" "/home/itodoro/zopen/usr/local/bin" "${GO_BIN}")
if [[ -n "${EXTRA_PATH}" ]]; then
    path_parts=("${EXTRA_PATH}" "${path_parts[@]}")
fi
PATH_EXPORT="export PATH=\"$(IFS=:; echo "${path_parts[*]}"):\$PATH\""

BASE_ENV="${PATH_EXPORT}"

if [[ "${USE_PROJECT_DIR}" -eq 1 ]]; then
    REMOTE_PREFIX="${BASE_ENV} && cd ~/${ZOS_DIR}"
else
    REMOTE_PREFIX="${BASE_ENV}"
fi

echo "Connecting to ${ZOS_USER}@${ZOS_HOST} via ${JUMP_USER}@${JUMP_HOST}..."

if [[ $# -gt 0 ]]; then
    REMOTE_CMD="${REMOTE_PREFIX} && $*"
    ssh -J "${JUMP_USER}@${JUMP_HOST}" "${ZOS_USER}@${ZOS_HOST}" \
        "bash -lc $(printf '%q' "${REMOTE_CMD}")"
else
    REMOTE_CMD="${REMOTE_PREFIX} && exec \$SHELL -l"
    ssh -J "${JUMP_USER}@${JUMP_HOST}" "${ZOS_USER}@${ZOS_HOST}" \
        -t "bash -lc $(printf '%q' "${REMOTE_CMD}")"
fi
