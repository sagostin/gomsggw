#!/bin/bash
# ==============================================================================
# GOMSGGW Backup Script
# 
# Backs up:
# - PostgreSQL database
# - .env configuration file
#
# Configuration via environment variables (from .env or export):
# - POSTGRES_HOST, POSTGRES_USER, POSTGRES_PASSWORD, POSTGRES_DB
# - BACKUP_FTP_HOST, BACKUP_FTP_USER, BACKUP_FTP_PASSWORD, BACKUP_FTP_DIR
# - BACKUP_LOCAL_DIR (default: /var/backups/gomsggw)
# - BACKUP_RETENTION_DAYS (default: 7, set to 0 to disable cleanup)
#
# Usage:
#   ./backup.sh              # Run backup
#   ./backup.sh --env-only   # Backup .env file only
#   ./backup.sh --db-only    # Backup database only
# ==============================================================================

set -euo pipefail

# Load .env if it exists
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${ENV_FILE:-$SCRIPT_DIR/../.env}"

if [[ -f "$ENV_FILE" ]]; then
    echo "[INFO] Loading environment from: $ENV_FILE"
    set -a
    source "$ENV_FILE"
    set +a
fi

# === CONFIGURATION (from environment) ===

# PostgreSQL
PG_HOST="${POSTGRES_HOST:-localhost}"
PG_PORT="${POSTGRES_PORT:-5432}"
PG_USER="${POSTGRES_USER:-smsgw}"
PG_PASSWORD="${POSTGRES_PASSWORD:-}"
PG_DATABASE="${POSTGRES_DB:-smsgw}"

# Backup settings
BACKUP_DIR="${BACKUP_LOCAL_DIR:-/var/backups/gomsggw}"
RETENTION_DAYS="${BACKUP_RETENTION_DAYS:-7}"
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")

# FTP settings (optional)
FTP_HOST="${BACKUP_FTP_HOST:-}"
FTP_PORT="${BACKUP_FTP_PORT:-21}"
FTP_USER="${BACKUP_FTP_USER:-}"
FTP_PASSWORD="${BACKUP_FTP_PASSWORD:-}"
FTP_REMOTE_DIR="${BACKUP_FTP_DIR:-/gomsggw}"

# === FUNCTIONS ===

log_info() { echo "[INFO] $(date '+%Y-%m-%d %H:%M:%S') $*"; }
log_error() { echo "[ERROR] $(date '+%Y-%m-%d %H:%M:%S') $*" >&2; }
log_success() { echo "[SUCCESS] $(date '+%Y-%m-%d %H:%M:%S') $*"; }

backup_database() {
    local backup_file="$BACKUP_DIR/db_${PG_DATABASE}_${TIMESTAMP}.sql.gz"
    
    log_info "Backing up PostgreSQL database: $PG_DATABASE"
    
    export PGPASSWORD="$PG_PASSWORD"
    
    if pg_dump -h "$PG_HOST" -p "$PG_PORT" -U "$PG_USER" "$PG_DATABASE" | gzip > "$backup_file"; then
        log_success "Database backup created: $backup_file"
        echo "$backup_file"
    else
        log_error "Failed to backup database"
        return 1
    fi
}

backup_env_file() {
    if [[ ! -f "$ENV_FILE" ]]; then
        log_info "No .env file found, skipping"
        return 0
    fi
    
    local backup_file="$BACKUP_DIR/env_${TIMESTAMP}.enc"
    
    log_info "Backing up .env file (encrypted)"
    
    # Encrypt with openssl if ENCRYPTION_KEY is available
    if [[ -n "${ENCRYPTION_KEY:-}" ]]; then
        openssl enc -aes-256-cbc -salt -pbkdf2 \
            -in "$ENV_FILE" \
            -out "$backup_file" \
            -pass pass:"$ENCRYPTION_KEY"
        log_success "Encrypted .env backup created: $backup_file"
    else
        # Plain copy with restrictive permissions
        cp "$ENV_FILE" "$backup_file"
        chmod 600 "$backup_file"
        log_info ".env backup created (unencrypted): $backup_file"
    fi
    
    echo "$backup_file"
}

upload_to_ftp() {
    local file="$1"
    
    if [[ -z "$FTP_HOST" ]] || [[ -z "$FTP_USER" ]]; then
        log_info "FTP not configured, skipping upload"
        return 0
    fi
    
    log_info "Uploading to FTP: $FTP_HOST"
    
    if command -v lftp &>/dev/null; then
        lftp -u "$FTP_USER","$FTP_PASSWORD" -p "$FTP_PORT" "$FTP_HOST" <<EOF
mkdir -p $FTP_REMOTE_DIR
cd $FTP_REMOTE_DIR
put "$file"
bye
EOF
        log_success "Uploaded: $(basename "$file")"
    elif command -v curl &>/dev/null; then
        curl -T "$file" "ftp://$FTP_USER:$FTP_PASSWORD@$FTP_HOST:$FTP_PORT$FTP_REMOTE_DIR/"
        log_success "Uploaded: $(basename "$file")"
    else
        log_error "No FTP client available (lftp or curl required)"
        return 1
    fi
}

cleanup_old_backups() {
    if [[ "$RETENTION_DAYS" -le 0 ]]; then
        return 0
    fi
    
    log_info "Cleaning up backups older than $RETENTION_DAYS days"
    find "$BACKUP_DIR" -type f -name "*.gz" -mtime +"$RETENTION_DAYS" -delete 2>/dev/null || true
    find "$BACKUP_DIR" -type f -name "*.enc" -mtime +"$RETENTION_DAYS" -delete 2>/dev/null || true
}

# === MAIN ===

main() {
    local mode="${1:-all}"
    
    # Ensure backup directory exists
    mkdir -p "$BACKUP_DIR"
    chmod 700 "$BACKUP_DIR"
    
    log_info "Starting backup (mode: $mode)"
    
    local files_to_upload=()
    
    case "$mode" in
        --db-only)
            files_to_upload+=("$(backup_database)")
            ;;
        --env-only)
            files_to_upload+=("$(backup_env_file)")
            ;;
        *)
            files_to_upload+=("$(backup_database)")
            files_to_upload+=("$(backup_env_file)")
            ;;
    esac
    
    # Upload files
    for file in "${files_to_upload[@]}"; do
        if [[ -n "$file" ]] && [[ -f "$file" ]]; then
            upload_to_ftp "$file"
        fi
    done
    
    # Cleanup old backups
    cleanup_old_backups
    
    log_success "Backup completed!"
}

main "$@"
