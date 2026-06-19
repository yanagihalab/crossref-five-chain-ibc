#!/usr/bin/env bash
set -euo pipefail

HOME_DIR="${HOME_DIR:-/var/lib/crossrefd}"
BACKUP_DIR="${BACKUP_DIR:-/var/backups/crossrefd}"
CHAIN_ID="${CHAIN_ID:?set CHAIN_ID}"
STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
OUT="${BACKUP_DIR}/${CHAIN_ID}-${STAMP}.tar.zst"

mkdir -p "${BACKUP_DIR}"
tar --exclude 'data/snapshots' -C "$(dirname "${HOME_DIR}")" -cf - "$(basename "${HOME_DIR}")" | zstd -19 -T0 -o "${OUT}"
sha256sum "${OUT}" >"${OUT}.sha256"
printf 'backup=%s\nsha256=%s.sha256\n' "${OUT}" "${OUT}"
