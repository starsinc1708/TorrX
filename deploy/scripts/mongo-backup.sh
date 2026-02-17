#!/bin/bash
set -euo pipefail

# MongoDB backup script for torrentstream
# Usage: docker compose -f deploy/docker-compose.yml --profile backup run --rm mongo-backup
# Note: Ensure this file has executable permissions (chmod +x mongo-backup.sh)

MONGO_URI="${MONGO_URI:-mongodb://mongo:27017}"
DB_NAME="${DB_NAME:-torrentstream}"
BACKUP_DIR="${BACKUP_DIR:-/backups}"
RETENTION_DAYS="${RETENTION_DAYS:-7}"

TIMESTAMP=$(date +%Y%m%d_%H%M%S)
BACKUP_PATH="${BACKUP_DIR}/${TIMESTAMP}"

log() {
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*"
}

log "Starting MongoDB backup"
log "  URI:       ${MONGO_URI}"
log "  Database:  ${DB_NAME}"
log "  Output:    ${BACKUP_PATH}"
log "  Retention: ${RETENTION_DAYS} days"

mkdir -p "${BACKUP_PATH}"

if ! mongodump --uri="${MONGO_URI}" --db="${DB_NAME}" --out="${BACKUP_PATH}" --gzip; then
  log "ERROR: mongodump failed"
  rm -rf "${BACKUP_PATH}"
  exit 1
fi

log "Backup completed successfully"

# Prune backups older than RETENTION_DAYS
log "Pruning backups older than ${RETENTION_DAYS} days..."
PRUNED=0
for dir in "${BACKUP_DIR}"/[0-9]*_[0-9]*; do
  [ -d "${dir}" ] || continue
  if [ "$(find "${dir}" -maxdepth 0 -mtime +"${RETENTION_DAYS}" 2>/dev/null)" ]; then
    log "  Removing old backup: $(basename "${dir}")"
    rm -rf "${dir}"
    PRUNED=$((PRUNED + 1))
  fi
done
log "Pruned ${PRUNED} old backup(s)"

log "Done"
