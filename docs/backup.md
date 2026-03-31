# Backup & Restore Guide

This guide covers how to set up, automate, and restore backups for GOMSGGW.

---

## What Gets Backed Up

| Data | File Pattern | Format |
|------|-------------|--------|
| PostgreSQL database | `db_smsgw_YYYYMMDD_HHMMSS.sql.gz` | Gzip-compressed SQL dump |
| `.env` configuration | `env_YYYYMMDD_HHMMSS.enc` | Plain copy (or AES-256 encrypted if configured) |

---

## 1. Configuration

Add the following to your `.env` file:

```bash
# ----------------------
# Backup Settings
# ----------------------
BACKUP_LOCAL_DIR=/var/backups/gomsggw    # Where backups are stored
BACKUP_RETENTION_DAYS=7                  # Auto-delete backups older than N days (0 = keep forever)

# Optional: Encrypt .env backups with AES-256-CBC
# Leave empty for plain-text backups (default)
# BACKUP_ENCRYPT_KEY=your-32-character-key

# Optional: Upload backups to FTP server
# Leave empty to disable FTP upload
# BACKUP_FTP_HOST=ftp.example.com
# BACKUP_FTP_PORT=21
# BACKUP_FTP_USER=backup_user
# BACKUP_FTP_PASSWORD=secure_password
# BACKUP_FTP_DIR=/gomsggw
```

> [!TIP]
> All settings have sensible defaults. The minimum you need is a working PostgreSQL connection (which you already have if the gateway is running).

---

## 2. Running a Manual Backup

From the repository root:

```bash
# Full backup (database + .env)
./scripts/backup.sh

# Database only
./scripts/backup.sh --db-only

# .env file only
./scripts/backup.sh --env-only
```

Backup files are saved to `BACKUP_LOCAL_DIR` (default: `/var/backups/gomsggw`).

---

## 3. Automating Backups (Host Cron)

The simplest approach is to run the backup script via the host's cron scheduler.

### Step 1: Create the backup directory

```bash
sudo mkdir -p /var/backups/gomsggw
sudo chown $USER:$USER /var/backups/gomsggw
```

### Step 2: Add cron job

Edit your crontab:

```bash
crontab -e
```

Add this line for daily backups at 2am:

```
0 2 * * * /opt/gomsggw/scripts/backup.sh >> /var/log/backup.log 2>&1
```

To also upload to FTP, source the .env first:

```
0 2 * * * cd /opt/gomsggw && ./scripts/backup.sh >> /var/log/backup.log 2>&1
```

### Common Schedules

| Schedule | Cron Expression |
|----------|-----------------|
| Daily at 2am | `0 2 * * *` |
| Every 6 hours | `0 */6 * * *` |
| Every 12 hours | `0 0,12 * * *` |
| Weekly (Sunday 3am) | `0 3 * * 0` |

---

## 4. Restore Procedures

### Restore the Database

```bash
# Option A: Pipe directly from backup
gunzip -c backups/db_smsgw_20260315_020000.sql.gz | \
  docker exec -i gomsggw-db psql -U smsgw -d smsgw

# Option B: Extract first, then restore
gunzip backups/db_smsgw_20260315_020000.sql.gz
docker exec -i gomsggw-db psql -U smsgw -d smsgw < backups/db_smsgw_20260315_020000.sql
```

> [!IMPORTANT]
> This will overwrite the current database contents. Stop the gateway first to avoid conflicts:
> ```bash
> docker compose stop gomsggw
> # ... restore ...
> docker compose start gomsggw
> ```

### Restore the .env File

**If the backup is unencrypted (default):**

```bash
cp backups/env_20260315_020000.enc .env
chmod 600 .env
```

**If the backup was encrypted with `BACKUP_ENCRYPT_KEY`:**

```bash
openssl enc -aes-256-cbc -d -pbkdf2 \
  -in backups/env_20260315_020000.enc \
  -out .env \
  -pass pass:"your-encryption-key"
chmod 600 .env
```

### Full Disaster Recovery

1. Deploy a fresh instance (`git clone`, `cp sample.env .env`)
2. Restore `.env` from backup (see above)
3. Start postgres: `docker compose up -d postgres`
4. Wait for it to be healthy: `docker compose ps`
5. Restore the database (see above)
6. Start the gateway: `docker compose up -d gomsggw`
7. Verify: `curl http://localhost:3000/health`

---

## 5. FTP Offsite Uploads

When `BACKUP_FTP_HOST` and `BACKUP_FTP_USER` are set, every backup file is automatically uploaded after creation.

To test FTP connectivity:

```bash
lftp -u backup_user,password -p 21 ftp.example.com -e "ls; bye"
```

---

## 6. Troubleshooting

| Problem | Solution |
|---------|----------|
| `pg_dump: connection refused` | Set `POSTGRES_HOST=postgres` (not `localhost`) in Docker |
| `POSTGRES_PASSWORD is not set` | Ensure `.env` is loaded or vars are exported |
| Backup files not appearing | Check that the backup directory exists and is writable |
| FTP upload fails | Verify credentials and that remote directory exists |
| Cron not running | Check `sudo systemctl status cron` or `crontab -l` |
| Old backups not cleaned up | Verify `BACKUP_RETENTION_DAYS` is set and > 0 |
