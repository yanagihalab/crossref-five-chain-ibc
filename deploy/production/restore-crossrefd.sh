#!/usr/bin/env bash
set -euo pipefail

ARCHIVE="${1:?usage: restore-crossrefd.sh <backup.tar.zst>}"
HOME_DIR="${HOME_DIR:-/var/lib/crossrefd}"
PARENT_DIR="$(dirname "${HOME_DIR}")"
TMP_DIR="$(mktemp -d "${PARENT_DIR}/.restore-crossrefd.XXXXXX")"

cleanup() {
  rm -rf "${TMP_DIR}"
}
trap cleanup EXIT

if [ -e "${HOME_DIR}" ] && [ -n "$(find "${HOME_DIR}" -mindepth 1 -maxdepth 1 2>/dev/null)" ]; then
  echo "refusing to restore into non-empty HOME_DIR=${HOME_DIR}" >&2
  exit 1
fi

if [ -f "${ARCHIVE}.sha256" ]; then
  sha256sum -c "${ARCHIVE}.sha256"
fi

mkdir -p "${PARENT_DIR}"
zstd -dc "${ARCHIVE}" | tar -C "${TMP_DIR}" -xf -

RESTORED_ROOTS="$(find "${TMP_DIR}" -mindepth 1 -maxdepth 1 -type d | wc -l | tr -d ' ')"
if [ "${RESTORED_ROOTS}" != "1" ]; then
  echo "backup archive must contain exactly one top-level home directory" >&2
  exit 1
fi

RESTORED_ROOT="$(find "${TMP_DIR}" -mindepth 1 -maxdepth 1 -type d | head -n 1)"
if [ -e "${HOME_DIR}" ]; then
  rmdir "${HOME_DIR}"
fi
mv "${RESTORED_ROOT}" "${HOME_DIR}"
printf 'restored=%s\n' "${HOME_DIR}"
