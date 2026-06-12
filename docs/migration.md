# Database Migration Guide

Migration steps for updating from encrypted usernames to plaintext usernames.

---

## Overview

**Breaking Change**: Client usernames are now stored in plaintext instead of encrypted.

This requires a full database rebuild because:
1. Existing usernames are AES-256 encrypted
2. The decryption key is only available at runtime
3. GORM auto-migration cannot handle this data transformation

> [!TIP]
> The repo ships with ready-to-run migration helpers in the `migration/` directory (`migrate_clients.go` and `migrate_carriers.go`). They handle decryption, re-encryption, and dry-run previews. **Use those instead of writing your own script.** The steps below are a high-level overview; the canonical instructions are in [`migration/README.md`](../migration/README.md).

---

## Migration Steps

### 1. Backup First

```bash
# Stop the gateway so the data is stable
docker-compose down

# Full backup (clients, carriers, message records)
docker-compose exec postgres pg_dump -U smsgw -d smsgw > backup_before_migration.sql
```

### 2. Run the Bundled Migration Helpers

Set both the old and new encryption keys in your shell or `.env`:

```bash
export OLD_ENCRYPTION_KEY=""           # the original key (often blank — see migration/README.md)
export ENCRYPTION_KEY="your-new-32-char-key"
export POSTGRES_HOST=localhost
export POSTGRES_USER=smsgw
export POSTGRES_PASSWORD=...
export POSTGRES_DB=smsgw
```

Then run from the `migration/` directory:

```bash
cd migration

# Dry run first — shows what would change without modifying data
go run migrate_clients.go -dry-run
go run migrate_carriers.go -dry-run

# Apply
go run migrate_clients.go
go run migrate_carriers.go
```

The scripts decrypt `username` columns with `OLD_ENCRYPTION_KEY`, re-encrypt the `password` column with `ENCRYPTION_KEY`, and persist usernames as plaintext.

### 3. Apply the SQL Schema Migration

```bash
psql -U smsgw -d smsgw -f migration/migrate.sql
```

This adds new columns (`tag`, `group`, `direction`, etc.) and creates `client_settings` and `number_settings` tables.

### 4. Restart the Gateway

GORM's `AutoMigrate` will pick up any remaining schema differences on startup:

```bash
docker-compose up -d
```

### 5. Re-Add Clients and Carriers via the API

> If you prefer to start fresh, drop the database, restart, and re-add everything through the CLI/API. Existing credentials are encrypted at rest, so once a backup is taken you can rebuild without data loss.

```bash
cd scripts
export MSGGW_BASE_URL=http://localhost:3000
export MSGGW_API_KEY=your-admin-api-key
python main.py
# 2) Add carrier
# 5) Create client
# 8) Add numbers to client
```

### 6. Verify

```bash
# Check clients are loaded
curl -u admin:API_KEY http://localhost:3000/stats

# Test SMPP bind (for legacy clients)
# Test REST API auth (for web clients)
```

---

## Rollback

If something goes wrong, restore from the backup:

```bash
docker-compose down
dropdb -U smsgw smsgw
createdb -U smsgw smsgw
psql -U smsgw -d smsgw < backup_before_migration.sql
```

---

## Checklist

- [ ] Export a full backup
- [ ] Run `migrate_clients.go` and `migrate_carriers.go` with `-dry-run` and review
- [ ] Apply both migrations
- [ ] Apply `migration/migrate.sql`
- [ ] Restart the gateway
- [ ] Verify `/stats` shows expected clients and carriers
- [ ] Test a single SMPP bind and one REST send
