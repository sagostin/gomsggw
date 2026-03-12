# API Reference

Complete reference for all GOMSGGW API endpoints.

---

## Authentication

### Admin Endpoints
Use Basic Auth with `admin:<API_KEY>` (env var `API_KEY`).

```bash
curl -H "Authorization: Basic $(echo -n 'admin:YOUR_API_KEY' | base64)" ...
```

### Web Client Endpoints

Authentication method depends on the client's `auth_method` setting:

| auth_method | Header Format |
|-------------|---------------|
| `basic` (default) | `Authorization: Basic <base64(username:password)>` |
| `bearer` | `Authorization: Bearer <base64(username:password)>` |

**Basic Auth Example:**
```bash
curl -H "Authorization: Basic $(echo -n 'client:password' | base64)" ...
```

**Bearer Token Example:**
```bash
curl -H "Authorization: Bearer $(echo -n 'client:password' | base64)" ...
```

### API Key Authentication

External applications can authenticate using tenant API keys:

```bash
curl -H "Authorization: Bearer gw_live_your_api_key_here" ...
```

API keys support number-level scoping and permission scopes (`send`, `batch`, `usage`). See [API Keys](api_keys.md) for setup details.

**Rate Limiting**: API keys with a non-zero `rate_limit` are enforced at the specified requests-per-minute. Exceeding the limit returns `429 Too Many Requests`.

---

## Health & Status

### GET /health
Health check endpoint (no auth required).

**Response**: `200 OK`

---

### GET /stats
Connection statistics (admin auth).

**Response**:
```json
{
  "smpp_connected_clients": 5,
  "smpp_clients": [
    {"username": "client1", "ip_address": "192.168.1.10", "last_seen": "2026-01-06T12:00:00Z"}
  ],
  "mm4_connected_clients": 2,
  "mm4_clients": [
    {"client_id": "mm4_001", "username": "client2", "active_sessions": 1}
  ]
}
```

---

## Carrier Management

### GET /carriers
List all carriers (admin auth).

**Response**:
```json
[
  {"id": 1, "name": "Telnyx", "type": "telnyx", "active": true}
]
```

---

### POST /carriers
Add a carrier (admin auth).

**Request**:
```json
{
  "name": "Telnyx Production",
  "type": "telnyx",
  "username": "api_key",
  "password": "api_secret",
  "profile_id": "40016c5f-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
}
```

> `profile_id` is optional. For Telnyx, this is the messaging_profile_id.

---

### POST /carriers/reload
Reload carriers from database (admin auth).

**Response**: `200 OK`

---

## Client Management

### GET /clients
List all clients (admin auth).

**Response**:
```json
[
  {
    "id": 1,
    "username": "web_client",
    "name": "My App",
    "type": "web",
    "timezone": "America/Vancouver",
    "settings": {
      "sms_daily_limit": 10000,
      "default_webhook": "https://app.com/webhook"
    },
    "numbers": [
      {"number": "12505551234", "carrier": "telnyx", "tag": "support"}
    ]
  }
]
```

---

### POST /clients
Create a client (admin auth).

**Request**:
```json
{
  "username": "my_web_client",
  "password": "secure_api_key",
  "name": "My Web Application",
  "type": "web",
  "timezone": "America/Vancouver",
  "log_privacy": false
}
```

> **Note**: Legacy clients require `address` (IP or hostname).

**Response**:
```json
{"id": 2, "username": "my_web_client", "type": "web"}
```

---

### PATCH /clients/{id}/password
Update client password (admin auth). Password is write-only and never returned.

**Request**:
```json
{
  "new_password": "new_secure_password"
}
```

**Response**:
```json
{"status": "Password updated successfully"}
```

---

### GET /clients/{id}/numbers
Get all numbers for a client (admin auth).

**Response**:
```json
[
  {
    "id": 1,
    "number": "12505551234",
    "carrier": "telnyx",
    "tag": "sales",
    "group": "west-coast",
    "webhook": "https://app.com/numbers/12505551234",
    "settings": {
      "sms_daily_limit": 1000
    }
  }
]
```

---

### POST /clients/{id}/numbers
Add a number to a client (admin auth).

**Request**:
```json
{
  "number": "+1-250-555-1234",
  "carrier": "telnyx",
  "tag": "support",
  "group": "customer-service",
  "webhook": "https://app.com/inbound"
}
```

> Numbers are automatically normalized to E.164 format (`12505551234`).

---

### GET /clients/{id}/settings
Get client settings (admin auth). Works for all client types.

**Response**:
```json
{
  "id": 1,
  "client_id": 1,
  "auth_method": "basic",
  "api_format": "generic",
  "disable_message_splitting": false,
  "webhook_retries": 3,
  "webhook_timeout_secs": 10,
  "include_raw_segments": false,
  "default_webhook": "https://app.com/webhook",
  "sms_burst_limit": 0,
  "sms_daily_limit": 10000,
  "sms_monthly_limit": 0,
  "mms_burst_limit": 0,
  "mms_daily_limit": 1000,
  "mms_monthly_limit": 0,
  "limit_both": false
}
```

---

### PUT /clients/{id}/settings
Update client settings (admin auth). Partial updates supported.

**Request**:
```json
{
  "auth_method": "bearer",
  "api_format": "bicom",
  "sms_daily_limit": 25000,
  "mms_daily_limit": 5000,
  "sms_burst_limit": 100,
  "limit_both": false,
  "default_webhook": "https://app.com/new-webhook"
}
```

**auth_method options**: `basic` (default), `bearer`
**api_format options**: `generic` (default), `bicom`

**Response**:
```json
{
  "message": "Settings updated",
  "settings": {...}
}
```

---

## Web Client Messaging

### POST /messages/send
Send SMS or MMS (client auth).

**SMS Request**:
```json
{
  "from": "+12505551234",
  "to": "+14155559876",
  "text": "Hello from GOMSGGW!"
}
```

**MMS Request**:
```json
{
  "from": "+12505551234",
  "to": "+14155559876",
  "text": "Check out this image",
  "media": [
    {
      "filename": "photo.jpg",
      "content_type": "image/jpeg",
      "content": "<base64-encoded-data>"
    }
  ]
}
```

### Format-Specific Payloads

The expected request format depends on the client's `api_format` setting.

#### Generic Format (default)
```json
{
  "client_id": 2,
  "from": "+12505551234",
  "to": "+14155559876",
  "text": "Hello!",
  "media": [{"filename": "image.jpg", "content": "base64...", "content_type": "image/jpeg"}]
}
```
> `client_id` is optional but validated against auth if provided.

#### Bicom Format
```json
{
  "from": "+12505551234",
  "to": "+14155559876",
  "text": "Hello!",
  "media_urls": ["https://example.com/image.jpg"]
}
```

**Success Response** (202 Accepted):
```json
{"status": "queued", "id": "abc123-def456"}
```

**Rate Limit Response** (429 Too Many Requests):
```json
{
  "error": "rate_limit_exceeded",
  "message": "SMS daily limit exceeded (500/500)",
  "limit_type": "daily_sms_number",
  "period": "daily",
  "number": "12505551234",
  "current_usage": 500,
  "limit": 500
}
```

> Limit enforcement includes burst (per-minute), daily, and monthly periods for both SMS and MMS, with timezone-aware resets.

---

### GET /messages/usage
Check current usage and limits (client auth).

**Response**:
```json
{
  "client": {
    "username": "web_client",
    "type": "web",
    "sms": {
      "burst":   {"current_usage": 5,  "limit": 10,   "remaining": 5},
      "daily":   {"current_usage": 150, "limit": 1000, "remaining": 850},
      "monthly": {"current_usage": 3500, "limit": 10000, "remaining": 6500}
    },
    "mms": {
      "burst":   {"current_usage": 0, "limit": 5,   "remaining": 5},
      "daily":   {"current_usage": 10, "limit": 100, "remaining": 90},
      "monthly": {"current_usage": 200, "limit": 1000, "remaining": 800}
    }
  },
  "numbers": [
    {
      "number": "12505551234",
      "direction": "outbound",
      "sms": {"current_usage": 50, "limit": 500},
      "mms": {"current_usage": 5, "limit": 50},
      "limit_both": false,
      "tag": "support",
      "group": "customer-service"
    }
  ],
  "timezone": "America/Vancouver",
  "reset_times": {
    "burst":   "2026-01-06T22:29:00Z",
    "daily":   "2026-01-07T08:00:00Z",
    "monthly": "2026-02-01T08:00:00Z"
  },
  "timestamp": "2026-01-06T22:28:04Z"
}
```

> The `direction` field indicates outbound-only counting by default. Set `limit_both: true` on number settings to count both directions.

---

### GET /messages/history
Retrieve message history for the authenticated client (client or API key auth with `usage` scope).

**Query Parameters**:

| Parameter | Description |
|-----------|-------------|
| `from` | Filter by sender number |
| `to` | Filter by destination number |
| `type` | `sms` or `mms` |
| `direction` | `inbound` or `outbound` |
| `since` | Start date (`2026-03-01` or RFC3339) |
| `until` | End date (`2026-03-31` or RFC3339) |
| `page` | Page number (default: 1) |
| `per_page` | Results per page (default: 50, max: 200) |

**Response**:
```json
{
  "messages": [
    {
      "id": 1234,
      "client_id": 1,
      "from_number": "12505551234",
      "to_number": "+14155559876",
      "type": "sms",
      "direction": "outbound",
      "delivery_method": "carrier_api",
      "received_timestamp": "2026-03-07T06:00:00Z",
      "log_id": "abc123-def456"
    }
  ],
  "total_count": 150,
  "page": 1,
  "per_page": 50
}
```

**Response Headers**: `X-Total-Count`, `X-Page`, `X-Per-Page`

---

## API Key Management

Admin endpoints for managing tenant API keys. See [API Keys](api_keys.md) for full details.

### POST /clients/{id}/api-keys
Create an API key (admin auth).

**Request**:
```json
{
  "name": "CSV Import App",
  "scopes": "send,batch,usage",
  "rate_limit": 0,
  "expires_in_days": 90,
  "allowed_number_ids": [1, 3]
}
```

**Response** (201): Returns the raw key (shown once only), key ID, prefix, and allowed numbers.

---

### GET /clients/{id}/api-keys
List API keys for a client (admin auth).

---

### DELETE /clients/{id}/api-keys/{key_id}
Revoke an API key (admin auth).

---

## Batch Sending

Send messages in bulk. See [Batch Sending](batch_sending.md) for full details.

### POST /messages/batch
Submit a batch job (client or API key auth with `batch` scope).

Accepts JSON or `multipart/form-data` with CSV file.

**JSON Request**:
```json
{
  "from": "+12505551234",
  "text_template": "Hi {{name}}, your code is {{code}}",
  "throttle_per_second": 30,
  "webhook_url": "https://myapp.com/batch-done",
  "messages": [
    {"to": "+14155551111", "variables": {"name": "Alice", "code": "A123"}}
  ]
}
```

**Response** (202 Accepted):
```json
{"id": "uuid", "status": "pending", "total_count": 1}
```

---

### POST /messages/batch/check
Pre-check limits before submitting a batch (client or API key auth with `batch` scope).

**Request**:
```json
{
  "from": "+12505551234",
  "message_count": 500,
  "msg_type": "sms"
}
```

**Response**:
```json
{
  "allowed": true,
  "message_count": 500,
  "msg_type": "sms",
  "from": "12505551234",
  "limits": {
    "burst":   {"current_usage": 5, "limit": 100, "remaining": 95},
    "daily":   {"current_usage": 150, "limit": 1000, "remaining": 850},
    "monthly": {"current_usage": 3500, "limit": 10000, "remaining": 6500}
  },
  "number_limit": {
    "number": "12505551234",
    "current_usage": 50,
    "limit": 500,
    "remaining": 450
  }
}
```

---

### GET /messages/batch/{id}
Get batch job status (client or API key auth with `batch` scope).

---

### GET /messages/batch
List recent batch jobs (client or API key auth with `batch` scope).

**Query Parameters**:

| Parameter | Description |
|-----------|-------------|
| `status` | Filter by job status (`pending`, `processing`, `completed`, etc.) |
| `from` | Filter by from-number |
| `since` | Start date (`2026-03-01` or RFC3339) |
| `until` | End date |
| `page` | Page number (default: 1) |
| `per_page` | Results per page (default: 50, max: 100) |

**Response Headers**: `X-Total-Count`, `X-Page`, `X-Per-Page`

---

### GET /messages/batch/{id}/messages
List all messages in a batch job with per-message statuses. Requires `batch` scope.

**Query Parameters**: `?status=queued`, `?page=1&per_page=100` (max: 500)

**Response Headers**: `X-Total-Count`, `X-Page`, `X-Per-Page`

---

### POST /messages/batch/{id}/cancel
Cancel an entire batch job (client or API key auth with `batch` scope).

Cancels all `pending` and `queued` messages. Returns `409` if the job is already completed/failed/cancelled.

**Response** (200):
```json
{
  "message": "Batch job cancelled",
  "job_id": "uuid",
  "status": "cancelled",
  "cancelled_count": 42,
  "sent_count": 8
}
```

---

### DELETE /messages/batch/{id}/messages/{msg_id}
Cancel a `pending` or `queued` message. Requires `batch` scope. Returns `409` if already sent/failed.

---

## Carrier Webhooks

### POST /inbound/{carrier}
Receive inbound messages from carriers.

Each carrier has its own payload format. The gateway normalizes and routes them.

---

### GET /media/{token}
Retrieve MMS media files using UUID access token.

**Security:**
- Media files are accessed via unguessable UUID tokens, not sequential IDs
- All access attempts are logged with client IP and User-Agent for audit trails
- Tokens expire after 7 days along with the media content

**Example URL:**
```
GET /media/a1b2c3d4-e5f6-7890-abcd-ef1234567890
```

**Response**: Binary file with appropriate Content-Type header.

**Error Responses:**
- `400` - Missing or invalid access token
- `404` - Media file not found or expired

---

## Webhook Delivery (Outbound to Web Clients)

When messages are received for web clients, they're delivered via HTTP POST to the configured webhook.

### Webhook Resolution Order
1. Number-specific `webhook` field
2. Client `default_webhook` setting
3. Error if neither configured

### Inbound SMS Payload
```json
{
  "id": "msg-abc123",
  "from": "+14155559876",
  "to": "+12505551234",
  "text": "Hello!",
  "type": "sms",
  "timestamp": "2026-01-06T12:00:00Z",
  "tag": "support",
  "group": "customer-service"
}
```

### Inbound MMS Payload
```json
{
  "id": "msg-def456",
  "from": "+14155559876",
  "to": "+12505551234",
  "text": "Check this out!",
  "type": "mms",
  "timestamp": "2026-01-06T12:00:00Z",
  "media": [
    {
      "filename": "image.jpg",
      "content_type": "image/jpeg",
      "base64": "<base64-encoded-data>"
    }
  ]
}
```

### Webhook Authentication
Webhooks include Basic Auth header using client credentials:
```
Authorization: Basic <base64(username:password)>
```

### Expected Response
- `2xx` - Success, message delivered
- `4xx/5xx` - Failure, will retry based on `webhook_retries` setting

---

## Error Responses

### 400 Bad Request
```json
{"error": "Invalid request body"}
```

### 401 Unauthorized
```json
{"error": "Unauthorized"}
```

### 404 Not Found
```json
{"error": "Client not found"}
```

### 429 Too Many Requests
```json
{
  "error": "rate_limit_exceeded",
  "message": "SMS daily limit exceeded (1000/1000)",
  "limit_type": "daily_sms_client",
  "period": "daily",
  "number": "12505551234",
  "current_usage": 1000,
  "limit": 1000
}
```

### 500 Internal Server Error
```json
{"error": "Internal server error"}
```
