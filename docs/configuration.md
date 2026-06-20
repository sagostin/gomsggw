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

### POSTGRES_TIMEZONE

**Default**: `America/Vancouver`

IANA timezone applied to the GORM DSN. Used so that `TIMESTAMPTZ` columns and the limit-period calculations line up with the gateway's intended local time. Set this to match `TZ` on the host if you change the OS timezone.

```bash
POSTGRES_TIMEZONE=America/Vancouver
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

### TRUSTED_PROXIES

**Default**: `10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7`

Comma-separated list of CIDR ranges whose `X-Forwarded-For` headers are trusted for resolving the client IP of incoming requests. Override this if you front the gateway with a proxy on a non-private network.

```bash
TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
```

---

## Server Identity

### SERVER_ID

**Default**: (empty)

Identifier embedded in log lines and message records (`server_id` column) so multi-instance deployments can attribute traffic to a specific node. If left blank the field is recorded as an empty string.

```bash
SERVER_ID=gateway-east-1
```

### SERVER_ADDRESS

**Default**: (empty)

Public base URL of the gateway, used to build absolute `/media/{token}` URLs that get sent to carriers for outbound MMS. If unset, carriers receive relative URLs and media delivery will fail.

```bash
SERVER_ADDRESS=https://sms.example.com
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
**Status**: ⚠️ Not yet implemented — the Go code does not currently read this variable. The SMPP server listens in plaintext on `SMPP_LISTEN` only. Documented here for forward compatibility.

Enable TLS for SMPP connections.

```bash
SMPP_TLS_ENABLED=true
```

### SMPP_TLS_CERT

**Required if TLS enabled**  
**Status**: ⚠️ Not yet implemented (see `SMPP_TLS_ENABLED`).

Path to TLS certificate file.

```bash
SMPP_TLS_CERT=/etc/ssl/certs/smpp.crt
```

### SMPP_TLS_KEY

**Required if TLS enabled**  
**Status**: ⚠️ Not yet implemented (see `SMPP_TLS_ENABLED`).

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
**Status**: ⚠️ Not yet implemented — the Go code does not currently read this variable. The MM4 server uses `MM4_ORIGINATOR_SYSTEM` and `MM4_MSG_ID_HOST` (see [Deployment](deployment.md)) for its SMTP greetings and message IDs.

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
**Status**: ⚠️ Not yet implemented — the Go code only reads `LOG_LEVEL` (debug/info/warn/error) and emits structured logrus output. Format switching is not wired up.

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

### LOKI_JOB

**Default**: `gomsggw`

Loki `job` label attached to every pushed log stream. Use a distinct value per gateway instance when shipping to a shared Loki to make multi-instance filtering easier.

```bash
LOKI_JOB=gomsggw
```

### LOKI_USERNAME / LOKI_PASSWORD

**Default**: (empty)

Optional Basic Auth credentials for Loki. Most local Loki installations leave these unset.

```bash
LOKI_USERNAME=
LOKI_PASSWORD=
```

---

## Proxy / Debug

### HAPROXY_PROXY_PROTOCOL

**Default**: `false`

When `true`, the SMPP and MM4 servers honour the HAProxy PROXY protocol on their listening sockets. Enable this if you front the gateway with HAProxy and want the real client IP in ACL checks (SMPP bind) and MM4 session tracking.

```bash
HAPROXY_PROXY_PROTOCOL=true
```

### DEBUG

**Default**: `false`

When `true`, the gateway starts a Go `net/http/pprof` server on `PPROF_LISTEN` for runtime profiling. Should be left off in production unless you are actively capturing profiles.

```bash
DEBUG=false
```

### PPROF_LISTEN

**Default**: `0.0.0.0:42666`

Listen address for the pprof HTTP server (only used when `DEBUG=true`).

```bash
PPROF_LISTEN=0.0.0.0:42666
```

---

## MMS Transcoding

### MMS_MAX_SIZE

**Default**: `614400` (600 KB)  
**Status**: ⚠️ Not yet implemented — the transcoder currently uses hard-coded targets and the `u2takey/ffmpeg-go` Go bindings, which shell out via PATH. This variable is documented for forward compatibility and will become the configurable size cap once the transcoder is refactored to read it.

Target maximum size for MMS content in bytes.

```bash
MMS_MAX_SIZE=614400
```

### MMS_IMAGE_QUALITY

**Default**: `85`  
**Status**: ⚠️ Not yet implemented (see `MMS_MAX_SIZE`).

Initial JPEG quality for image transcoding (1-100).

```bash
MMS_IMAGE_QUALITY=85
```

### MMS_MIN_IMAGE_QUALITY

**Default**: `50`  
**Status**: ⚠️ Not yet implemented (see `MMS_MAX_SIZE`).

Minimum JPEG quality before giving up on size reduction.

```bash
MMS_MIN_IMAGE_QUALITY=50
```

### FFMPEG_PATH

**Default**: `/usr/bin/ffmpeg`  
**Status**: ⚠️ Not yet implemented — the Go code uses the `u2takey/ffmpeg-go` library, which resolves `ffmpeg` from `$PATH`. The Dockerfile installs the Alpine `ffmpeg` package, so a custom path is not currently configurable.

Path to FFmpeg binary for video/audio transcoding.

```bash
FFMPEG_PATH=/usr/local/bin/ffmpeg
```

### TRANSCODER_WORKERS

**Default**: `4`  
**Status**: ⚠️ Not yet implemented — the transcoder runs synchronously in the message-processing path today. Worker pooling is on the roadmap.

Number of concurrent transcoding workers.

```bash
TRANSCODER_WORKERS=8
```

---

## Media Storage

Media files (MMS content) are stored in PostgreSQL using the `MediaFile` table, **not** on the filesystem. Files are automatically cleaned up after 7 days.

### TRANSCODE_TEMP_PATH

**Default**: `./transcode`

Temporary directory for MMS media transcoding operations.

```bash
TRANSCODE_TEMP_PATH=/tmp/gomsggw/transcode
```

> [!NOTE]
> The media retention period is currently hardcoded at 7 days in `media_storage.go`.

---

## Rate Limiting

### DEFAULT_SMS_PERIOD

**Default**: `daily`  
**Status**: ⚠️ Not yet implemented — period is currently set per-client via `ClientSettings`; there is no global default applied at startup.

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

## Auto-Reply

Per-number automatic reply for inbound messages. When enabled on a number, an
inbound to that number (from a carrier OR from another client) gets an automatic
reply back to the original sender and the inbound itself is suppressed from
normal client delivery.

### AUTO_REPLY_ENABLED

**Default**: `false`  
**Values**: `true` | `false`

Global master switch for the auto-reply feature. When `false`, the feature is
fully off regardless of per-number settings. When `true`, individual numbers
opt in via `NumberSettings.auto_reply_enabled` (see [Data Models](data_models.md)
or `PUT /numbers/{id}/auto-reply`).

```bash
AUTO_REPLY_ENABLED=true
```

### AUTO_REPLY_DEFAULT_MESSAGE

**Default**: `""` (empty — feature is effectively disabled per-number if no
per-number message is set)

Fallback reply body used when a number has `auto_reply_enabled=true` but no
`auto_reply_message` of its own.

```bash
AUTO_REPLY_DEFAULT_MESSAGE=This number does not accept text messages. Please call us instead.
```

See [Number Management](number_management.md) for the end-to-end "we don't
accept texts" setup walkthrough.

---

## Carrier Settings

### TELNYX_API_BASE

**Default**: `https://api.telnyx.com/v2`  
**Status**: ⚠️ Not yet implemented — the base URL is hard-coded in `carrier_telnyx.go`. This variable is documented for forward compatibility.

Base URL for Telnyx API.

```bash
TELNYX_API_BASE=https://api.telnyx.com/v2
```

### TWILIO_API_BASE

**Default**: `https://api.twilio.com/2010-04-01`  
**Status**: ⚠️ Not yet implemented — the base URL is hard-coded in `carrier_twilio.go`. This variable is documented for forward compatibility.

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
POSTGRES_TIMEZONE=America/Vancouver

# Security
ENCRYPTION_KEY=abcdefghijklmnopqrstuvwxyz123456
API_KEY=admin_api_key_here

# Services
WEB_LISTEN=0.0.0.0:3000
SMPP_LISTEN=0.0.0.0:9550
MM4_LISTEN=0.0.0.0:2566
SERVER_ID=gateway1
SERVER_ADDRESS=http://your-gateway.example.com:3000

# Proxy / pprof
HAPROXY_PROXY_PROTOCOL=false
# TRUSTED_PROXIES=10.0.0.0/8,172.16.0.0/12,192.168.0.0/16
DEBUG=false
PPROF_LISTEN=0.0.0.0:42666

# Prometheus
PROMETHEUS_LISTEN=0.0.0.0:2550
PROMETHEUS_PATH=/metrics

# Logging
LOG_LEVEL=info
# LOG_FORMAT=json
LOKI_ENABLED=false
LOKI_URL=http://loki:3100/loki/api/v1/push
LOKI_USERNAME=
LOKI_PASSWORD=
LOKI_JOB=gomsggw

# Transcoding (some not yet implemented — see notes above)
TRANSCODE_TEMP_PATH=/tmp/gomsggw/transcode
# MMS_MAX_SIZE=614400
# MMS_IMAGE_QUALITY=85
# TRANSCODER_WORKERS=4
```

---

## Runtime Reload

Some configurations can be reloaded at runtime via the API:

### Reload Clients and Numbers

```bash
POST /clients/reload
```

Reloads all clients and numbers from the database.

### Reload Carriers

```bash
POST /carriers/reload
```

Reloads carrier configurations.

> [!NOTE]
> Core server settings (ports, database connection, encryption key) require a full restart to take effect.
