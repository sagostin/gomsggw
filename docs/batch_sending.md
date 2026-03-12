# Batch Sending

Send messages in bulk via JSON arrays or CSV file uploads.

---

## Overview

The batch sending system enables high-volume message delivery with:

- **JSON or CSV input** — flexible payload formats
- **Template variables** — `{{name}}`, `{{code}}`, etc.
- **Throttled delivery** — configurable messages-per-second
- **Queue-on-limit** — rate-limited messages are queued for retry, not failed
- **Per-message IDs** — cancel individual messages by UUID
- **Batch cancellation** — cancel entire batch jobs in one call
- **Job tracking** — poll status or receive webhook callbacks
- **Limit pre-check** — verify limits before submitting
- **Per-message error reporting** — individual failure reasons
- **Pagination & filtering** — page through jobs and messages

Maximum batch size: **10,000 messages** per request.

---

## Quick Start

### JSON Batch

```bash
curl -X POST http://gateway:3000/messages/batch \
  -H "Authorization: Bearer gw_live_your_api_key_here" \
  -H "Content-Type: application/json" \
  -d '{
    "from": "+12505551234",
    "text_template": "Hi {{name}}, your code is {{code}}",
    "throttle_per_second": 30,
    "max_retry_mins": 60,
    "webhook_url": "https://myapp.com/batch-done",
    "messages": [
      {"to": "+14155551111", "variables": {"name": "Alice", "code": "A123"}},
      {"to": "+14155552222", "variables": {"name": "Bob", "code": "B456"}},
      {"to": "+14155553333", "text": "Custom message for Carol"}
    ]
  }'
```

### CSV Batch

```bash
curl -X POST http://gateway:3000/messages/batch \
  -H "Authorization: Bearer gw_live_your_api_key_here" \
  -F "from=+12505551234" \
  -F "text_template=Hi {{name}}, your appointment is at {{time}}" \
  -F "throttle_per_second=20" \
  -F "csv=@contacts.csv"
```

**Response** (202 Accepted):
```json
{
  "id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "status": "pending",
  "total_count": 3
}
```

---

## CSV Format

The CSV must have a header row. Column names are case-insensitive.

### Required Column (one of)

| Column Name | Aliases |
|-------------|---------|
| `to` | `phone`, `number`, `destination` |

### Optional Columns

| Column Name | Aliases | Description |
|-------------|---------|-------------|
| `text` | `message`, `body` | Per-row message text (overrides template) |
| *any other* | — | Available as `{{column_name}}` template variable |

### Example CSV

```csv
to,name,code
+14155551111,Alice,A123
+14155552222,Bob,B456
+14155553333,Carol,C789
```

---

## Template Variables

Use `{{variable_name}}` placeholders in `text_template`. Variables are populated from:
- JSON: the `variables` map on each message
- CSV: any column that isn't `to` or `text`

If a message has its own `text` field, it takes precedence over the template.

---

## Limit Pre-Check

Before submitting a batch, verify that your limits can handle it:

```bash
curl -X POST http://gateway:3000/messages/batch/check \
  -H "Authorization: Bearer gw_live_your_api_key_here" \
  -H "Content-Type: application/json" \
  -d '{"from": "+12505551234", "message_count": 500, "msg_type": "sms"}'
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

If `allowed` is `false`, the `reason` field explains which limit would be exceeded.

---

## Job Tracking

### Poll Status

```bash
curl http://gateway:3000/messages/batch/f47ac10b-58cc-4372-a567-0e02b2c3d479 \
  -H "Authorization: Bearer gw_live_your_api_key_here"
```

**Response**:
```json
{
  "id": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
  "status": "completed",
  "total_count": 3,
  "sent_count": 2,
  "failed_count": 1,
  "from_number": "+12505551234",
  "throttle_per_second": 30,
  "created_at": "2026-03-07T06:00:00Z",
  "completed_at": "2026-03-07T06:00:05Z",
  "errors": [
    {
      "index": 2,
      "to": "+14155553333",
      "error": "SMS daily limit exceeded (500/500)",
      "code": 429
    }
  ]
}
```

### List Recent Jobs

```bash
curl "http://gateway:3000/messages/batch?page=1&per_page=20&status=completed" \
  -H "Authorization: Bearer gw_live_your_api_key_here"
```

Supports pagination and filtering:

| Parameter | Description |
|-----------|-------------|
| `page` | Page number (default: 1) |
| `per_page` | Results per page (default: 50, max: 100) |
| `status` | Filter by job status |
| `from` | Filter by from-number |
| `since` | Start date (`2026-03-01` or RFC3339) |
| `until` | End date |

Response includes `X-Total-Count`, `X-Page`, `X-Per-Page` headers.

### List Messages in a Job

```bash
curl "http://gateway:3000/messages/batch/f47ac10b-.../messages?page=1&per_page=100&status=queued" \
  -H "Authorization: Bearer gw_live_your_api_key_here"
```

Supports pagination (`?page=1&per_page=100`, max: 500) and status filter (`?status=queued`).

Returns all individual message items with their IDs, statuses, and error details.
Response includes `X-Total-Count`, `X-Page`, `X-Per-Page` headers.

### Cancel a Message

Cancel a specific message that is still `pending` or `queued`:

```bash
curl -X DELETE http://gateway:3000/messages/batch/f47ac10b-.../messages/msg-uuid-here \
  -H "Authorization: Bearer gw_live_your_api_key_here"
```

**Response** (200):
```json
{"message": "Message cancelled", "message_id": "msg-uuid", "status": "cancelled"}
```

**Response** (409 — already sent/failed):
```json
{"error": "Message cannot be cancelled", "status": "sent", "detail": "Message is already 'sent'"}
```

### Cancel Entire Batch Job

Cancel all pending/queued messages in a batch at once:

```bash
curl -X POST http://gateway:3000/messages/batch/f47ac10b-.../cancel \
  -H "Authorization: Bearer gw_live_your_api_key_here"
```

**Response** (200):
```json
{
  "message": "Batch job cancelled",
  "job_id": "f47ac10b-...",
  "status": "cancelled",
  "cancelled_count": 42,
  "sent_count": 8
}
```

**Response** (409 — job already finished):
```json
{"error": "Batch job cannot be cancelled", "status": "completed", "detail": "Job is already 'completed'"}
```

### Webhook Callback

If `webhook_url` is set, the gateway POSTs the full job status to that URL when the batch completes:

```
POST https://myapp.com/batch-done
Content-Type: application/json

{
  "id": "f47ac10b-...",
  "status": "completed",
  "total_count": 3,
  "sent_count": 2,
  "failed_count": 1,
  "errors": [...]
}
```

---

## Rate Limiting & Queuing

Batch messages go through the existing `CheckMessageLimits` system. When a message **hits a limit**:

| Old Behavior | New Behavior |
|---|---|
| Message permanently failed (429) | Message **queued** for retry |

### How it works

1. **Initial pass** — messages are sent in order. If a limit is hit, the message gets `queued` status
2. **Retry loop** — every 30 seconds, queued messages are re-checked against limits
3. **Limit resets** — when daily/monthly limits reset, queued messages are sent
4. **Max retry** — after `max_retry_mins` (default: 60 minutes), remaining queued messages are marked `failed`

### Per-Message Status Values

| Status | Description |
|--------|-------------|
| `pending` | Waiting to be processed |
| `sent` | Successfully queued to carrier |
| `queued` | Rate limited, waiting for retry |
| `failed` | Permanent failure (bad number, template error, retry expired) |
| `cancelled` | Cancelled by user via DELETE endpoint |

---

## Error Codes

Per-message errors include an HTTP-style code:

| Code | Meaning |
|------|---------|
| 400 | Invalid destination number or missing text |
| 403 | Number not authorized for this API key |
| 429 | Rate limit exceeded |

## Job Status Values

| Status | Description |
|--------|-------------|
| `pending` | Job created, processing not started |
| `processing` | Actively sending messages |
| `partially_queued` | Initial pass done, some messages queued for retry |
| `completed` | All messages resolved (sent/failed/cancelled) |
| `cancelled` | Entire job cancelled by user (via `POST /cancel`) |
| `failed` | All messages failed |

---

## Throttling

The `throttle_per_second` parameter controls delivery speed:

| Value | Behavior |
|-------|----------|
| `0` or omitted | Default: 30 messages/second |
| `1-100` | Custom rate |

---

## Retry Configuration

| Parameter | Default | Description |
|-----------|---------|-------------|
| `max_retry_mins` | 60 | Maximum minutes to retry queued messages |

Set `max_retry_mins: 0` to use the default (60 minutes). The retry loop checks queued messages every 30 seconds.

---

## Authentication

Batch messages are also subject to the client's burst/daily/monthly limits. Rate-limited messages are recorded as individual errors in the batch job.

---

## Authentication

Batch endpoints accept both authentication methods:

| Method | Header |
|--------|--------|
| API Key | `Authorization: Bearer gw_live_...` (requires `batch` scope) |
| Client credentials | `Authorization: Basic <base64(username:password)>` |

See [API Keys](./api_keys.md) for API key setup.
