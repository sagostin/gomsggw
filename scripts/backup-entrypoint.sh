#!/bin/bash
# Backup container entrypoint
set -e

# Setup cron job from environment
SCHEDULE="${BACKUP_SCHEDULE:-0 2 * * *}"

echo "[INFO] Setting up backup schedule: $SCHEDULE"

# Write cron job (with all env vars passed through)
printenv | grep -E '^(POSTGRES_|BACKUP_|ENCRYPTION_|FTP_)' > /etc/environment

cat > /etc/crontabs/root <<EOF
# GOMSGGW Backup Schedule
$SCHEDULE /app/backup.sh >> /var/log/backup.log 2>&1
EOF

# Create log file
touch /var/log/backup.log

echo "[INFO] Starting crond..."
echo "[INFO] Backup will run on schedule: $SCHEDULE"
echo "[INFO] Run manually: docker exec gomsggw-backup /app/backup.sh"

# Run crond in foreground
exec crond -f -l 2
