# Database Migration Guide

Migration steps for updating from encrypted usernames to plaintext usernames.

---

## Overview

**Breaking Change**: Client usernames are now stored in plaintext instead of encrypted.

This requires a full database rebuild because:
1. Existing usernames are AES-256 encrypted
2. The decryption key is only available at runtime
3. GORM auto-migration cannot handle this data transformation

---

## Migration Steps

### 1. Export Current Data

```bash
# Stop the gateway
docker-compose down

# Export clients with their credentials (will need manual decryption)
docker-compose exec postgres pg_dump -U smsgw -t clients -t client_settings -t client_numbers -t number_settings smsgw > clients_backup.sql

# Export carriers (still encrypted, no changes needed)
docker-compose exec postgres pg_dump -U smsgw -t carriers smsgw > carriers_backup.sql

# Export message records
docker-compose exec postgres pg_dump -U smsgw -t msg_record_db_items smsgw > messages_backup.sql

# Full backup (optional)
docker-compose exec postgres pg_dump -U smsgw smsgw > full_backup.sql
```

### 2. Create Decryption Script

Create `scripts/migrate_usernames.go`:

```go
package main

import (
    "encoding/json"
    "fmt"
    "os"
)

// Copy the DecryptAES256 function from encryption.go

type OldClient struct {
    ID       uint   `json:"id"`
    Username string `json:"username"` // Encrypted
    Password string `json:"password"` // Encrypted
    Name     string `json:"name"`
    Address  string `json:"address"`
    Type     string `json:"type"`
    Timezone string `json:"timezone"`
}

type NewClient struct {
    ID       uint   `json:"id"`
    Username string `json:"username"` // Plaintext
    Password string `json:"password"` // Still encrypted
    Name     string `json:"name"`
    Address  string `json:"address"`
    Type     string `json:"type"`
    Timezone string `json:"timezone"`
}

func main() {
    key := os.Getenv("ENCRYPTION_KEY")
    if key == "" {
        fmt.Println("ENCRYPTION_KEY not set")
        os.Exit(1)
    }

    // Read old clients JSON
    // Decrypt usernames, keep passwords encrypted
    // Output new clients JSON with INSERT statements
}
```

### 3. Drop and Recreate Database

```bash
# Connect to postgres and drop/recreate
docker-compose exec postgres psql -U smsgw -c "DROP DATABASE smsgw;"
docker-compose exec postgres psql -U smsgw -c "CREATE DATABASE smsgw;"

# Start gateway to auto-create new schema
docker-compose up -d gomsggw
docker-compose logs -f gomsggw  # Wait for "Starting Web Server"
docker-compose down
```

### 4. Re-Add Data via API

**Recommended approach** - Use the CLI tool to re-add carriers and clients:

```bash
cd scripts
export MSGGW_BASE_URL=http://localhost:3000
export MSGGW_API_KEY=your-admin-api-key

# Start gateway
docker-compose up -d

# Add carriers
python main.py
# Choose: 2) Add carrier

# Add clients  
python main.py
# Choose: 5) Create client

# Add numbers
python main.py
# Choose: 8) Add numbers to client
```

### 5. Verify Migration

```bash
# Check clients are loaded
curl -u admin:API_KEY http://localhost:3000/stats

# Test SMPP bind (for legacy clients)
# Test REST API auth (for web clients)
```

---

## Alternative: Direct SQL Restore

If you have many clients, create transformed INSERT statements:

```sql
-- Example: Insert with plaintext username, encrypted password
INSERT INTO clients (username, password, name, address, type, timezone)
VALUES (
    'zultys_mx',  -- Plaintext username
    'encrypted_password_here',  -- Keep encrypted
    'Zultys MX',
    '192.168.1.100',
    'legacy',
    'UTC'
);
```

---

## Rollback

If you need to rollback:
1. Restore from `full_backup.sql`
2. Revert `clients.go` to encrypt usernames again
3. Rebuild and deploy old version

---

## Checklist

- [ ] Export all data (clients, carriers, numbers, messages)
- [ ] Stop all gateway instances
- [ ] Drop and recreate database
- [ ] Deploy new code version
- [ ] Re-add carriers via API
- [ ] Re-add clients via API (usernames now plaintext)
- [ ] Re-add phone numbers
- [ ] Verify SMPP/REST authentication works
- [ ] Optionally restore message history
