# Setup & Interaction Guide

Complete guide for deploying GOMSGGW and interacting with its API key, batch sending, and messaging features.

---

## Prerequisites

- **Go 1.21+** compiled gateway binary (or Docker image)
- **PostgreSQL** database
- **Python 3.9+** for the CLI management tool
- Required environment variables (see [Configuration](configuration.md))

---

## Initial Setup

### 1. Environment Variables

```bash
# Database
export POSTGRES_HOST=localhost
export POSTGRES_PORT=5432
export POSTGRES_USER=gateway
export POSTGRES_PASSWORD=your_db_password
export POSTGRES_DB=gomsggw

# Security
export ENCRYPTION_KEY=your-32-byte-encryption-key

# Web Server
export WEB_LISTEN=0.0.0.0:3000

# Optional: Logging
export LOKI_ENABLED=true
export LOKI_URL=http://loki:3100
```

### 2. Start the Gateway

```bash
# Direct
./gomsggw

# Docker
docker compose up -d
```

The gateway automatically runs database migrations on startup, creating all required tables including `batch_jobs`, `batch_message_items`, `tenant_api_keys`, etc.

### 3. CLI Management Tool

```bash
cd scripts/
pip install -r requirements.txt

# Set admin credentials
export MSGGW_BASE_URL=http://localhost:3000
export MSGGW_API_KEY=your_admin_api_key

# Launch
python main.py
```

---

## Setup Workflow

### Step 1: Create a Carrier

```
> 2  (Add carrier)
Carrier Name: telnyx_prod
Choose type: 1 (telnyx)
API Key: your_telnyx_api_key
```

### Step 2: Create a Client

```
> 5  (Create client)
Username: acme_app
Type: 2 (web)
Display Name: Acme Corp
```

Save the generated password — it's shown only once.

### Step 3: Add Numbers

```
> 9  (Add numbers)
Client: acme_app
Carrier: telnyx_prod
Numbers: 12505551234, 12505555678
```

### Step 4: Create API Keys

```
> b  (Create API key)
Client: acme_app
Key Name: Production App
Scopes: send,batch,usage
Expires: 90 days
Number restriction: (blank for all)
```

> **⚠️ Save the raw key immediately** — it's displayed only once and stored hashed.

### Step 5: Reload

```
> r  (Reload all)
```

---

## Interacting with the API

### Authentication Methods

| Method | Format | Use Case |
|--------|--------|----------|
| Basic Auth | `Authorization: Basic base64(user:pass)` | Client credentials |
| API Key | `Authorization: Bearer gw_live_...` | External app integration |
| Admin Key | `Authorization: Basic base64(apikey:KEY)` | CLI/admin operations |

### Send a Single Message

```bash
curl -X POST http://gateway:3000/messages/send \
  -H "Authorization: Bearer gw_live_a1b2c3d4..." \
  -H "Content-Type: application/json" \
  -d '{
    "from": "+12505551234",
    "to": "+14155559876",
    "text": "Hello from the gateway!"
  }'
```

### Submit a Batch Job

```bash
curl -X POST http://gateway:3000/messages/batch \
  -H "Authorization: Bearer gw_live_a1b2c3d4..." \
  -H "Content-Type: application/json" \
  -d '{
    "from": "+12505551234",
    "text_template": "Hi {{name}}, your code is {{code}}",
    "throttle_per_second": 30,
    "max_retry_mins": 60,
    "webhook_url": "https://myapp.com/batch-done",
    "messages": [
      {"to": "+14155551111", "variables": {"name": "Alice", "code": "A123"}},
      {"to": "+14155552222", "variables": {"name": "Bob", "code": "B456"}}
    ]
  }'
```

### Check Batch Status

```bash
curl http://gateway:3000/messages/batch/{job_id} \
  -H "Authorization: Bearer gw_live_a1b2c3d4..."
```

### List Messages in a Batch

```bash
# All messages
curl http://gateway:3000/messages/batch/{job_id}/messages \
  -H "Authorization: Bearer gw_live_a1b2c3d4..."

# Filter by status
curl "http://gateway:3000/messages/batch/{job_id}/messages?status=queued" \
  -H "Authorization: Bearer gw_live_a1b2c3d4..."
```

### Cancel a Queued Message

```bash
curl -X DELETE http://gateway:3000/messages/batch/{job_id}/messages/{msg_id} \
  -H "Authorization: Bearer gw_live_a1b2c3d4..."
```

Returns `200` if cancelled, `409` if already sent/failed.

### Check Usage

```bash
curl http://gateway:3000/messages/usage \
  -H "Authorization: Bearer gw_live_a1b2c3d4..."
```

---

## API Key Management (Admin)

Admin endpoints require admin authentication (Basic Auth with the admin API key).

### Create Key

```bash
curl -X POST http://gateway:3000/clients/{id}/api-keys \
  -H "Authorization: Basic $(echo -n 'apikey:ADMIN_KEY' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Batch Worker",
    "scopes": "batch,usage",
    "expires_in_days": 90,
    "allowed_number_ids": [1, 3]
  }'
```

### List Keys

```bash
curl http://gateway:3000/clients/{id}/api-keys \
  -H "Authorization: Basic $(echo -n 'apikey:ADMIN_KEY' | base64)"
```

### Revoke Key

```bash
curl -X DELETE http://gateway:3000/clients/{id}/api-keys/{key_id} \
  -H "Authorization: Basic $(echo -n 'apikey:ADMIN_KEY' | base64)"
```

---

## CLI Tool Menu Reference

```
📡 Carriers:        📋 Clients:          📞 Numbers:
  1) List carriers    3) List clients      8) List numbers
  2) Add carrier      4) Show details      9) Add numbers
                      5) Create client
                      6) Update settings
                      7) Change password

🔑 API Keys:        📦 Batch Jobs:       ⚙️ Admin:
  a) List keys        d) List jobs         r) Reload all
  b) Create key       e) Job detail        q) Quick flow
  c) Revoke key
```

---

## Scope Reference

| Scope | Endpoints Permitted |
|-------|-------------------|
| `send` | `POST /messages/send`, `POST /messages` |
| `batch` | `POST /messages/batch`, `GET /messages/batch/*` |
| `usage` | `GET /messages/usage` |

Keys without a required scope receive `403 Forbidden`.

---

## Batch Message Flow

```
Submit Job → Process Messages → Retry Queued → Finalize
     │              │                │             │
     │         ┌────┴────┐      ┌────┴────┐   ┌───┴───┐
     │         │  sent   │      │  sent   │   │ count │
     │         │  queued │      │ failed  │   │ stats │
     │         │  failed │      │cancelled│   │webhook│
     │         └─────────┘      └─────────┘   └───────┘
     │
     └── Each message gets a UUID for tracking/cancellation
```

**Status transitions:**
- `pending` → `sent` / `queued` / `failed` / `cancelled`
- `queued` → `sent` / `failed` / `cancelled`
