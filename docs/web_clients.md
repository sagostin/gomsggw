# Web Client Integration

Documentation for integrating modern web applications with GOMSGGW.

---

## Overview

Web clients use REST APIs and webhooks instead of legacy protocols:
- **REST API** - Send messages via HTTP POST
- **Webhooks** - Receive inbound messages via HTTP callbacks

This is ideal for:
- Web applications
- Mobile app backends
- Modern PBX systems (Bicom PBXware, etc.)
- Integration platforms

---

## Quick Start

### 1. Create Web Client

```bash
curl -X POST http://gateway:3000/clients \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "username": "my_app",
    "password": "secure_api_key_here",
    "name": "My Web Application",
    "type": "web",
    "timezone": "America/Vancouver"
  }'

# Response: {"id": 1, "username": "my_app", ...}
```

### 2. Configure Settings (use client ID from response)

```bash
curl -X PUT http://gateway:3000/clients/1/settings \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "auth_method": "basic",
    "api_format": "generic",
    "default_webhook": "https://myapp.com/api/sms/inbound",
    "sms_daily_limit": 10000,
    "mms_daily_limit": 1000,
    "webhook_retries": 3,
    "webhook_timeout_secs": 10
  }'
```

### 3. Add Phone Numbers

```bash
curl -X POST http://gateway:3000/clients/1/numbers \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "number": "+1-250-555-1234",
    "carrier": "telnyx",
    "tag": "support",
    "group": "customer-service"
  }'
```

### 4. Send a Message

```bash
curl -X POST http://gateway:3000/messages/send \
  -H "Authorization: Basic $(echo -n 'my_app:secure_api_key_here' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "from": "+12505551234",
    "to": "+14155559876",
    "text": "Hello from my app!"
  }'
```

---

## Authentication

Web clients authenticate using HTTP Basic Auth or Bearer Token.

### Basic Auth (default)
```
Authorization: Basic <base64(username:password)>
```

### Bearer Token (Bicom format)
```
Authorization: Bearer <base64(username:password)>
```

The password acts as an API key - use a secure random string.

---

---

## Web Client Settings

| Setting | Type | Default | Description |
|---------|------|---------|-------------|
| `api_format` | string | generic | Webhook payload format: `generic`, `bicom`, `telnyx` |
| `disable_message_splitting` | bool | false | Deliver long messages as single payload (web→web only) |
| `webhook_retries` | int | 3 | Number of retry attempts for webhook delivery |
| `webhook_timeout_secs` | int | 10 | Webhook request timeout in seconds |
| `include_raw_segments` | bool | false | Include segment details in webhook payload |
| `default_webhook` | string | - | Fallback webhook URL if number doesn't have one |

### API Format Options

| Format | Auth Header | Payload Style | Use Case |
|--------|-------------|---------------|----------|
| `generic` | Basic | `{id, from, to, text, media}` | General web apps |
| `bicom` | Bearer | `{from, to, text, media_urls}` | Bicom PBXware |
| `telnyx` | Basic | Nested `{data: {payload}}` | Telnyx-compatible |

### Update Settings

```bash
curl -X PUT http://gateway:3000/clients/{id}/settings \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "api_format": "bicom",
    "webhook_retries": 5,
    "webhook_timeout_secs": 15
  }'
```

---

## Sending Messages

### Send SMS

```bash
POST /messages/send
Authorization: Basic <credentials>
Content-Type: application/json

{
  "from": "+12505551234",
  "to": "+14155559876",
  "text": "Hello!"
}
```

### Send MMS

```bash
POST /messages/send
Authorization: Basic <credentials>
Content-Type: application/json

{
  "from": "+12505551234",
  "to": "+14155559876",
  "text": "Check this out!",
  "media": [
    {
      "filename": "photo.jpg",
      "content_type": "image/jpeg",
      "content": "<base64-encoded-data>"
    }
  ]
}
```

### Response

**Success** (202 Accepted):
```json
{"status": "queued", "id": "msg-abc123"}
```

**Rate Limited** (429):
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

> The API uses comprehensive limit checking (burst/daily/monthly for SMS/MMS) with timezone-aware resets.

---

## Receiving Messages (Webhooks)

Inbound messages are delivered to your webhook as HTTP POST requests.

### Webhook Resolution

The gateway looks for webhooks in this order:
1. Number-specific `webhook` field
2. Client `default_webhook` setting
3. Error if neither configured

### Webhook Payload

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

### MMS Payload

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

The gateway sends your credentials in the Authorization header:
```
Authorization: Basic <base64(username:password)>
```

Verify this in your webhook handler for security.

### Expected Response

Return `2xx` for success. Any other status triggers retries.

---

## Usage Limits

### Check Current Usage

```bash
curl http://gateway:3000/messages/usage \
  -H "Authorization: Basic $(echo -n 'my_app:api_key' | base64)"
```

**Response**:
```json
{
  "client": {
    "username": "my_app",
    "type": "web",
    "sms": {
      "burst":   {"current_usage": 5,  "limit": 50,   "remaining": 45},
      "daily":   {"current_usage": 150, "limit": 10000, "remaining": 9850},
      "monthly": {"current_usage": 4500, "limit": 100000, "remaining": 95500}
    },
    "mms": {
      "burst":   {"current_usage": 0, "limit": 10,   "remaining": 10},
      "daily":   {"current_usage": 25, "limit": 1000, "remaining": 975},
      "monthly": {"current_usage": 500, "limit": 10000, "remaining": 9500}
    }
  },
  "numbers": [
    {
      "number": "12505551234",
      "direction": "outbound",
      "sms": {"current_usage": 50, "limit": 500},
      "mms": {"current_usage": 10, "limit": 100},
      "limit_both": false,
      "tag": "support"
    }
  ],
  "timezone": "America/Vancouver",
  "reset_times": {
    "burst":   "2026-01-07T12:01:00Z",
    "daily":   "2026-01-07T08:00:00Z",
    "monthly": "2026-02-01T08:00:00Z"
  },
  "timestamp": "2026-01-06T12:00:00Z"
}
```

### Limit Hierarchy

1. **Number-level limit** - Checked first, if configured
2. **Client-level limit** - Checked second, aggregates all numbers
3. **Periods checked** - Burst (per-minute), Daily, Monthly
4. **Direction** - Outbound by default; set `limit_both: true` to count both

---

## Bicom PBXware Integration

Bicom PBXware can use the web client API for modern SMS integration.

### Setup

1. Create web client for PBXware
2. Configure webhook to PBXware's SMS API endpoint
3. Add phone numbers with carrier routing
4. Configure PBXware to send via gateway REST API

### Example Webhook Handler (PBXware)

Your PBXware webhook endpoint should:
1. Verify the Authorization header
2. Parse the JSON payload
3. Route to appropriate extension/user
4. Return 200 OK

---

## Message Flow

```
┌─────────────────────────────────────────────────────────────────┐
│                        OUTBOUND                                  │
│                                                                  │
│  ┌───────────┐  REST API  ┌──────────┐  API    ┌─────────────┐  │
│  │  Web App  │───────────►│  GOMSGGW │────────►│   Carrier   │  │
│  └───────────┘            └──────────┘         └─────────────┘  │
│                                                                  │
├─────────────────────────────────────────────────────────────────┤
│                        INBOUND                                   │
│                                                                  │
│  ┌───────────┐  Webhook   ┌──────────┐  Webhook ┌─────────────┐ │
│  │  Web App  │◄───────────│  GOMSGGW │◄─────────│   Carrier   │ │
│  └───────────┘            └──────────┘          └─────────────┘ │
└─────────────────────────────────────────────────────────────────┘
```

---

## Best Practices

1. **Use HTTPS** for webhook URLs
2. **Verify** Authorization header on webhooks
3. **Monitor** usage with `/messages/usage`
4. **Use tags/groups** to organize numbers
5. **Set per-number limits** for important numbers
6. **Configure retries** appropriately for your use case
