# GOMSGGW Database Migration Guide

This directory contains migration scripts to upgrade from the old database schema to the new schema.

## Schema Changes Summary

### Clients Table
- `username`: Now stored in **plaintext** (was encrypted)
- `type`: NEW - `'legacy'` or `'web'` (default: `'legacy'`)
- `timezone`: NEW - IANA timezone string (default: `'UTC'`)

### Client Numbers Table
- `tag`: NEW - Organizational tag
- `group`: NEW - Number grouping

### Carriers Table
- `profile_id`: NEW - Carrier-specific ID (e.g., Telnyx messaging_profile_id)

### Media Files Table
- `access_token`: NEW - UUID for secure access (replaces integer ID in URLs)

### Message Records Table
- `direction`, `from_client_type`, `to_client_type`, `delivery_method`: NEW tracking fields
- `encoding`, `total_segments`, `segment_index`: NEW SMS segment tracking
- `original_size_bytes`, `transcoded_size_bytes`, `media_count`, `transcoding_performed`: NEW MMS tracking

### New Tables
- `client_settings`: Per-client configuration (limits, webhooks, auth settings)
- `number_settings`: Per-number configuration (limit overrides)

---

## Migration Steps

### 1. Backup First!

```bash
# Create a full backup before migration
pg_dump -U smsgw smsgw > backup_before_migration.sql
```

### 2. Decrypt Usernames (if encrypted)

If your old database has encrypted client usernames, run the Go migration tool:

```bash
cd migration

# Install postgres driver
go get github.com/lib/pq

# Dry run first (shows what would change)
go run migrate_decrypt.go -key=YOUR_ENCRYPTION_KEY -dry-run

# Actually migrate
go run migrate_decrypt.go -key=YOUR_ENCRYPTION_KEY
```

The tool reads the `POSTGRES_*` environment variables or you can specify a DSN:

```bash
go run migrate_decrypt.go \
  -key=your-32-character-key-here \
  -dsn="host=localhost user=smsgw password=xxx dbname=smsgw sslmode=disable"
```

### 3. Run SQL Schema Migration

```bash
# Add new columns and create new tables
psql -U smsgw -d smsgw -f 01_add_columns.sql
```

### 4. Restart GOMSGGW

The application will auto-migrate any remaining schema differences via GORM's AutoMigrate.

```bash
docker-compose restart gomsggw
```

---

## Verify Migration

```sql
-- Check client types
SELECT id, username, type, timezone FROM clients;

-- Check new tables exist
SELECT COUNT(*) FROM client_settings;
SELECT COUNT(*) FROM number_settings;

-- Check media_files has access_token column
SELECT column_name FROM information_schema.columns 
WHERE table_name = 'media_files' AND column_name = 'access_token';
```

---

## Rollback

If something goes wrong, restore from your backup:

```bash
# Drop and recreate database
dropdb smsgw
createdb smsgw

# Restore backup
psql -U smsgw -d smsgw < backup_before_migration.sql
```

---

## Notes

- **Encrypted Usernames**: The old schema encrypted client usernames. The new schema stores them in plaintext for easier identification. The `migrate_decrypt.go` tool handles this conversion.

- **AutoMigrate**: GORM's AutoMigrate will create indexes and handle minor schema differences automatically on application startup.

- **Data Preservation**: All existing data (clients, numbers, carriers, messages) is preserved during migration.
