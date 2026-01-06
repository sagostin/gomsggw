# Configuration Reference

Complete reference for all GOMSGGW configuration options.

## Environment Variables

All configuration is done through environment variables. Create a `.env` file in the project root or set them in your deployment environment.

---

## Required Variables

These variables **must** be set for the gateway to start.

### ENCRYPTION_KEY

**Required**: Yes  
**Type**: String (32 bytes recommended)

Encryption key used for encrypting sensitive data at rest (credentials stored in database).

```bash
ENCRYPTION_KEY=your-32-byte-encryption-key-here
```

> [!CAUTION]
> If you lose this key, you will not be able to decrypt existing credentials. Back it up securely.

---

## Database Configuration

### POSTGRES_HOST

**Default**: `localhost`

PostgreSQL server hostname.

```bash
POSTGRES_HOST=postgres.example.com
```

### POSTGRES_PORT

**Default**: `5432`

PostgreSQL server port.

```bash
POSTGRES_PORT=5432
```

### POSTGRES_USER

**Default**: `gomsggw`

Database username.

```bash
POSTGRES_USER=gomsggw
```

### POSTGRES_PASSWORD

**Required**: Yes

Database password.

```bash
POSTGRES_PASSWORD=secure_database_password
```

### POSTGRES_DB

**Default**: `gomsggw`

Database name.

```bash
POSTGRES_DB=gomsggw
```

### POSTGRES_SSLMODE

**Default**: `disable`

PostgreSQL SSL mode. Options: `disable`, `require`, `verify-ca`, `verify-full`.

```bash
POSTGRES_SSLMODE=require
```

---

## Web Server

### WEB_LISTEN

**Default**: `0.0.0.0:3000`

Address and port for the HTTP API server.

```bash
WEB_LISTEN=0.0.0.0:3000
```

### API_KEY

**Required**: Yes

API key for admin endpoint authentication.

```bash
API_KEY=your-admin-api-key
```

---

## SMPP Server

### SMPP_LISTEN

**Default**: `0.0.0.0:9550`

Address and port for the SMPP server.

```bash
SMPP_LISTEN=0.0.0.0:9550
```

### SMPP_TLS_ENABLED

**Default**: `false`

Enable TLS for SMPP connections.

```bash
SMPP_TLS_ENABLED=true
```

### SMPP_TLS_CERT

**Required if TLS enabled**

Path to TLS certificate file.

```bash
SMPP_TLS_CERT=/etc/ssl/certs/smpp.crt
```

### SMPP_TLS_KEY

**Required if TLS enabled**

Path to TLS private key file.

```bash
SMPP_TLS_KEY=/etc/ssl/private/smpp.key
```

---

## MM4 Server (MMS)

### MM4_LISTEN

**Default**: `0.0.0.0:2566`

Address and port for the MM4 (SMTP) server.

```bash
MM4_LISTEN=0.0.0.0:2566
```

### MM4_DOMAIN

**Default**: System hostname

Domain name for MM4 SMTP greetings.

```bash
MM4_DOMAIN=mms.example.com
```

---

## Prometheus Metrics

### PROMETHEUS_LISTEN

**Default**: `0.0.0.0:2550`

Address and port for Prometheus metrics endpoint.

```bash
PROMETHEUS_LISTEN=0.0.0.0:2550
```

### PROMETHEUS_PATH

**Default**: `/metrics`

URL path for metrics endpoint.

```bash
PROMETHEUS_PATH=/metrics
```

---

## Logging

### LOG_LEVEL

**Default**: `info`

Minimum log level. Options: `debug`, `info`, `warn`, `error`.

```bash
LOG_LEVEL=debug
```

### LOG_FORMAT

**Default**: `json`

Log output format. Options: `json`, `text`.

```bash
LOG_FORMAT=json
```

### LOKI_ENABLED

**Default**: `false`

Enable Loki log shipping.

```bash
LOKI_ENABLED=true
```

### LOKI_URL

**Required if Loki enabled**

Loki push endpoint URL.

```bash
LOKI_URL=http://loki:3100/loki/api/v1/push
```

---

## MMS Transcoding

### MMS_MAX_SIZE

**Default**: `614400` (600 KB)

Target maximum size for MMS content in bytes.

```bash
MMS_MAX_SIZE=614400
```

### MMS_IMAGE_QUALITY

**Default**: `85`

Initial JPEG quality for image transcoding (1-100).

```bash
MMS_IMAGE_QUALITY=85
```

### MMS_MIN_IMAGE_QUALITY

**Default**: `50`

Minimum JPEG quality before giving up on size reduction.

```bash
MMS_MIN_IMAGE_QUALITY=50
```

### FFMPEG_PATH

**Default**: `/usr/bin/ffmpeg`

Path to FFmpeg binary for video/audio transcoding.

```bash
FFMPEG_PATH=/usr/local/bin/ffmpeg
```

### TRANSCODER_WORKERS

**Default**: `4`

Number of concurrent transcoding workers.

```bash
TRANSCODER_WORKERS=8
```

---

## Media Storage

### MEDIA_STORAGE_PATH

**Default**: `/tmp/gomsggw/media`

Path for temporary and cached media files.

```bash
MEDIA_STORAGE_PATH=/var/lib/gomsggw/media
```

### MEDIA_RETENTION_HOURS

**Default**: `24`

Hours to retain media files before cleanup.

```bash
MEDIA_RETENTION_HOURS=48
```

---

## Rate Limiting

### DEFAULT_SMS_PERIOD

**Default**: `daily`

Default period for SMS limit counting. Options: `hourly`, `daily`, `monthly`.

```bash
DEFAULT_SMS_PERIOD=daily
```

---

## Global Retry Configuration

These settings control retry behavior for all message delivery attempts.

### WEBHOOK_RETRIES

**Default**: `3`

Number of webhook delivery retry attempts for web clients.

```bash
WEBHOOK_RETRIES=3
```

### WEBHOOK_TIMEOUT_SECS

**Default**: `10`

Webhook request timeout in seconds.

```bash
WEBHOOK_TIMEOUT_SECS=10
```

### WEBHOOK_RETRY_DELAY_SECS

**Default**: `5`

Delay between webhook retry attempts in seconds.

```bash
WEBHOOK_RETRY_DELAY_SECS=5
```

### SMPP_RETRIES

**Default**: `3`

Number of SMPP delivery retry attempts for legacy clients.

```bash
SMPP_RETRIES=3
```

### SMPP_TIMEOUT_SECS

**Default**: `30`

SMPP operation timeout in seconds.

```bash
SMPP_TIMEOUT_SECS=30
```

### MM4_RETRIES

**Default**: `3`

Number of MM4 (MMS) delivery retry attempts.

```bash
MM4_RETRIES=3
```

### MM4_TIMEOUT_SECS

**Default**: `60`

MM4 operation timeout in seconds (longer default due to MMS size).

```bash
MM4_TIMEOUT_SECS=60
```

### NOTIFY_SENDER_ON_FAILURE

**Default**: `true`

Send error notification back to sender when message delivery fails.

```bash
NOTIFY_SENDER_ON_FAILURE=true
```

---

## Carrier Settings

### TELNYX_API_BASE

**Default**: `https://api.telnyx.com/v2`

Base URL for Telnyx API.

```bash
TELNYX_API_BASE=https://api.telnyx.com/v2
```

### TWILIO_API_BASE

**Default**: `https://api.twilio.com/2010-04-01`

Base URL for Twilio API.

```bash
TWILIO_API_BASE=https://api.twilio.com/2010-04-01
```

---

## Sample Configuration

Complete `.env` example:

```bash
# Database
POSTGRES_HOST=postgres
POSTGRES_PORT=5432
POSTGRES_USER=gomsggw
POSTGRES_PASSWORD=super_secure_password_123
POSTGRES_DB=gomsggw
POSTGRES_SSLMODE=disable

# Security
ENCRYPTION_KEY=abcdefghijklmnopqrstuvwxyz123456
API_KEY=admin_api_key_here

# Services
WEB_LISTEN=0.0.0.0:3000
SMPP_LISTEN=0.0.0.0:9550
MM4_LISTEN=0.0.0.0:2566

# Prometheus
PROMETHEUS_LISTEN=0.0.0.0:2550
PROMETHEUS_PATH=/metrics

# Logging
LOG_LEVEL=info
LOG_FORMAT=json
LOKI_ENABLED=false

# Transcoding
MMS_MAX_SIZE=614400
MMS_IMAGE_QUALITY=85
TRANSCODER_WORKERS=4

# Media
MEDIA_STORAGE_PATH=/tmp/gomsggw/media
MEDIA_RETENTION_HOURS=24
```

---

## Runtime Reload

Some configurations can be reloaded at runtime via the API:

### Reload Clients and Numbers

```bash
POST /reload
```

Reloads all clients and numbers from the database.

### Reload Carriers

```bash
POST /carriers/reload
```

Reloads carrier configurations.

> [!NOTE]
> Core server settings (ports, database connection, encryption key) require a full restart to take effect.
