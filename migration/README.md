# GOMSGGW Database Migration Guide

This directory contains migration scripts to upgrade from the old database schema to the new schema.

## Schema Changes Summary

### Clients Table
- `username`: Now stored in **plaintext** (was encrypted)
- `password`: Encrypted with `ENCRYPTION_KEY`
- `type`: NEW - `'legacy'` or `'web'` (default: `'legacy'`)
- `timezone`: NEW - IANA timezone string (default: `'UTC'`)

### Carriers Table
- `username`: Now stored in **plaintext** (was encrypted)
- `password`: Encrypted with `ENCRYPTION_KEY`
- `profile_id`: NEW - Carrier-specific ID (e.g., Telnyx messaging_profile_id)

### Client Numbers Table
- `tag`: NEW - Organizational tag
- `group`: NEW - Number grouping

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

### 2. Re-key Encrypted Data

The old database may have been encrypted with a blank key (due to a missing initialization). The migration scripts will:
- Decrypt data using `OLD_ENCRYPTION_KEY` (the original key, which may be blank)
- Store **usernames as plaintext**
- Re-encrypt **passwords** with the new `ENCRYPTION_KEY`

#### Environment Variables

Set these in your `.env` file or export them:

```bash
# The key that was originally used (may be blank if ENCRYPTION_KEY wasn't initialized)
OLD_ENCRYPTION_KEY=""

# The new key to use going forward
ENCRYPTION_KEY="your-new-secure-32-char-key-here"

# Database connection (if not using defaults)
POSTGRES_HOST=localhost
POSTGRES_PORT=5432
POSTGRES_USER=smsgw
POSTGRES_PASSWORD=your-db-password
POSTGRES_DB=smsgw
POSTGRES_SSLMODE=disable
```

#### Run Client Migration

```bash
cd migration

# Install postgres driver (if needed)
go get github.com/lib/pq
go get github.com/joho/godotenv

# Dry run first (shows what would change without modifying data)
go run migrate_clients.go -dry-run

# Apply changes
go run migrate_clients.go
```

#### Run Carrier Migration

```bash
# Dry run first
go run migrate_carriers.go -dry-run

# Apply changes
go run migrate_carriers.go
```

### 3. Run SQL Schema Migration (if needed)

```bash
# Add new columns and create new tables
psql -U smsgw -d smsgw -f migrate.sql
```

### 4. Restart GOMSGGW

The application will auto-migrate any remaining schema differences via GORM's AutoMigrate.

```bash
docker-compose restart gomsggw
```

---

## Verify Migration

```sql
-- Check client usernames are plaintext
SELECT id, username, name FROM clients;

-- Check carrier usernames are plaintext
SELECT id, name, username FROM carriers;

-- Verify new tables exist
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

- **Blank Encryption Key Bug**: The original `gateway.go` did not initialize `EncryptionKey` from the environment variable, causing data to be encrypted with a blank key. This has been fixed.

- **Username vs Password Storage**:
  - **Clients**: Username is plaintext, password is encrypted
  - **Carriers**: Username is plaintext (e.g., API keys, Account SIDs), password is encrypted (e.g., Auth Tokens)

- **Separate Migration Scripts**: 
  - `migrate_clients.go` - Migrates client table
  - `migrate_carriers.go` - Migrates carriers table
  
  Run them independently with `-dry-run` to preview changes first.

- **AutoMigrate**: GORM's AutoMigrate will create indexes and handle minor schema differences automatically on application startup.

- **Data Preservation**: All existing data (clients, numbers, carriers, messages) is preserved during migration.
