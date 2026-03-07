# API Keys

Tenant-scoped API keys for external application integration with GOMSGGW.

---

## Overview

API keys provide a secure way for external applications to authenticate with the
gateway without using client credentials directly. Keys are:

- **Hashed at rest** (SHA-256) — the raw key is only returned once on creation
- **Scoped to specific numbers** (optional) — restrict which numbers an app can send from
- **Scoped by permission** — `send`, `batch`, `usage`
- **Rate-limited independently** — or inherit client limits

**Format**: `gw_live_<64 hex chars>` (total ~72 characters)

---

## Creating API Keys

```bash
# Create an API key for client ID 1
curl -X POST http://gateway:3000/clients/1/api-keys \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "CSV Import App",
    "scopes": "send,batch,usage",
    "rate_limit": 0,
    "expires_in_days": 90,
    "allowed_number_ids": [1, 3]
  }'
```

**Response** (201 Created):
```json
{
  "key": "gw_live_a1b2c3d4e5f6...",
  "id": 1,
  "name": "CSV Import App",
  "key_prefix": "gw_live_a1b2c3d4",
  "scopes": "send,batch,usage",
  "rate_limit": 0,
  "active": true,
  "expires_at": "2026-06-05T22:00:00Z",
  "allowed_numbers": [
    {"id": 1, "number_id": 1, "number": "12505551234"},
    {"id": 2, "number_id": 3, "number": "12505555678"}
  ],
  "created_at": "2026-03-07T06:00:00Z"
}
```

> [!CAUTION]
> The raw `key` value is **only returned once**. Store it securely — it cannot be retrieved again.

---

## Using API Keys

Authenticate with the `Authorization: Bearer` header:

```bash
curl -X POST http://gateway:3000/messages/send \
  -H "Authorization: Bearer gw_live_a1b2c3d4e5f6..." \
  -H "Content-Type: application/json" \
  -d '{
    "from": "+12505551234",
    "to": "+14155559876",
    "text": "Hello via API key!"
  }'
```

API keys work with all client-authenticated endpoints:
- `POST /messages/send` — requires `send` scope
- `POST /messages/batch` — requires `batch` scope
- `GET /messages/usage` — requires `usage` scope
- `GET /messages/batch/{id}` — requires `batch` scope

---

## Number Scoping

API keys can optionally be restricted to specific phone numbers:

| `allowed_number_ids` | Behavior |
|---------------------|----------|
| Empty `[]` | Key can send from **any** client number |
| `[1, 3]` | Key can **only** send from number IDs 1 and 3 |

If an API key tries to send from an unauthorized number, the gateway returns `403 Forbidden`.

---

## Managing API Keys

### List Keys

```bash
curl http://gateway:3000/clients/1/api-keys \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)"
```

### Revoke a Key

```bash
curl -X DELETE http://gateway:3000/clients/1/api-keys/5 \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)"
```

**Response**: `{"message": "API key revoked", "key_id": 5}`

---

## Scopes

| Scope | Permissions |
|-------|-------------|
| `send` | Send individual messages via `POST /messages/send` |
| `batch` | Submit and track batch jobs via `POST /messages/batch` |
| `usage` | Read usage statistics via `GET /messages/usage` |

Scopes are comma-separated in the `scopes` field: `"send,batch,usage"`.

---

## Key Lifecycle

| Field | Description |
|-------|-------------|
| `active` | Set to `false` on revoke |
| `expires_at` | Optional expiration timestamp |
| `last_used_at` | Updated on each successful authentication |
| `key_prefix` | First 16 chars for identification in logs |

---

## Security

- Keys are **SHA-256 hashed** before storage — the database never contains raw keys
- Key lookup uses an **in-memory hash map** for O(1) performance
- Revoked/expired keys are immediately rejected
- All API key operations are logged with client ID and key prefix
