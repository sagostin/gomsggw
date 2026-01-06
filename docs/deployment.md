# Deployment Guide

Complete guide for deploying GOMSGGW.

---

## Prerequisites

- Docker & Docker Compose
- PostgreSQL (or use included container)
- Carrier API credentials (Telnyx, Twilio, etc.)

---

## Quick Start

```bash
# Clone
git clone https://github.com/sagostin/gomsggw.git
cd gomsggw

# Configure
cp sample.env .env
# Edit .env with your settings

# Build & Run
./build.sh
docker-compose up -d
```

---

## Environment Configuration

### Required Variables

```bash
# Encryption (REQUIRED - generate a 32-char random string)
ENCRYPTION_KEY=your-32-character-encryption-key

# Admin API Key
API_KEY=your-admin-api-key

# PostgreSQL
POSTGRES_HOST=postgres
POSTGRES_USER=smsgw
POSTGRES_PASSWORD=your-secure-password
POSTGRES_DB=smsgw
```

### Server Ports

```bash
# Web API
WEB_LISTEN=0.0.0.0:3000

# SMPP (for Zultys, legacy clients)
SMPP_LISTEN=0.0.0.0:9550

# MM4 (for MMS)
MM4_LISTEN=0.0.0.0:2566

# Prometheus metrics
PROMETHEUS_LISTEN=:2550
PROMETHEUS_PATH=/metrics
```

### Carrier Configuration

```bash
# Telnyx
TELNYX_ENABLE=true
TELNYX_API_KEY=your-telnyx-api-key

# Twilio
TWILIO_ENABLE=true
TWILIO_ACCOUNT_SID=your-account-sid
TWILIO_AUTH_TOKEN=your-auth-token
```

### Global Retry Settings

```bash
# Webhook (web clients)
WEBHOOK_RETRIES=3
WEBHOOK_TIMEOUT_SECS=10
WEBHOOK_RETRY_DELAY_SECS=5

# SMPP (SMS)
SMPP_RETRIES=3
SMPP_TIMEOUT_SECS=30

# MM4 (MMS)
MM4_RETRIES=3
MM4_TIMEOUT_SECS=60

# Send error messages back to sender on failure
NOTIFY_SENDER_ON_FAILURE=true
```

### MMS Configuration

```bash
# Media transcoding temp directory
TRANSCODE_TEMP_PATH=./transcode

# Public URL for media files (used by carriers)
SERVER_ADDRESS=http://your-public-ip:3000

# MM4 settings
MM4_ORIGINATOR_SYSTEM=system@your-domain.com
MM4_MSG_ID_HOST=your-domain.com
MM4_DEBUG=false
```

### Logging

```bash
# Loki (optional)
LOKI_URL=http://loki:3100/loki/api/v1/push
LOKI_USERNAME=
LOKI_PASSWORD=

# Debug mode
DEBUG=false
```

---

## Docker Compose

### docker-compose.yml

```yaml
version: "3.8"
services:
  postgres:
    image: postgres:17
    container_name: gomsggw-db
    env_file:
      - .env
    volumes:
      - ./postgres_data:/var/lib/postgresql/data
    restart: always

  gomsggw:
    image: gomsggw:latest
    container_name: gomsggw
    env_file:
      - .env
    restart: always
    depends_on:
      - postgres
    ports:
      - "3000:3000"   # Web API
      - "9550:9550"   # SMPP
      - "2566:2566"   # MM4
    environment:
      - SERVER_ID=gomsggw1
```

### Build & Run

```bash
# Build image
./build.sh

# Start services
docker-compose up -d

# View logs
docker-compose logs -f gomsggw

# Stop
docker-compose down
```

---

## Initial Setup

### 1. Start Services

```bash
docker-compose up -d
```

### 2. Add Carriers

Using the CLI tool:

```bash
cd scripts
pip install requests
export MSGGW_BASE_URL=http://localhost:3000
export MSGGW_API_KEY=your-admin-api-key
python main.py
# Choose: 2) Add carrier
```

Or via API:

```bash
curl -X POST http://localhost:3000/carriers \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -d '{"name":"telnyx","type":"telnyx","username":"api_key","password":""}'
```

### 3. Create Clients

**Zultys (Legacy/SMPP):**

```bash
# Using CLI tool
python main.py
# Choose: 5) Create client
# Type: 1 (legacy)
```

**Bicom (Web/REST):**

```bash
# Using CLI tool
python main.py
# Choose: 5) Create client
# Type: 2 (web)
# Then: 6) Update client settings
# Set API Format: 2 (bicom)
```

### 4. Add Phone Numbers

```bash
python main.py
# Choose: 8) Add numbers to client
# Enter client username
# Enter carrier name
# Enter numbers (comma-separated)
```

### 5. Reload Configuration

```bash
python main.py
# Choose: 9) Reload all
```

---

## Zultys Configuration

On the Zultys MX system:

| Setting | Value |
|---------|-------|
| SMPP Host | `gateway-ip` |
| SMPP Port | `9550` |
| System ID | Client username from gateway |
| Password | Client password from gateway |
| Bind Type | Transceiver |

---

## Bicom Configuration

On Bicom PBXware:

1. Create web client in gateway with `api_format: bicom`
2. Configure PBXware SMS Connector:
   - **Webhook URL**: `http://gateway:3000/messages/send`
   - **Auth Token**: `base64(username:password)`
   - **Inbound URL**: Your PBXware /smsservice/connector endpoint

---

## Health Checks

```bash
# API health
curl http://localhost:3000/health

# Connection stats
curl -u admin:API_KEY http://localhost:3000/stats

# Prometheus metrics
curl http://localhost:2550/metrics
```

---

## Troubleshooting

### SMPP Connection Issues

```bash
# Check SMPP is listening
nc -zv gateway-ip 9550

# Check logs for bind attempts
docker-compose logs gomsggw | grep HandleBind
```

### Webhook Failures

```bash
# Check logs for webhook errors
docker-compose logs gomsggw | grep webhook
```

### Database Issues

```bash
# Check PostgreSQL connection
docker-compose exec postgres psql -U smsgw -c "SELECT 1"
```

---

## Production Considerations

1. **Use HTTPS** - Put behind nginx/traefik with TLS
2. **Firewall** - Restrict SMPP/MM4 to known IPs
3. **Backups** - Regular PostgreSQL backups
4. **Monitoring** - Export Prometheus metrics to Grafana
5. **Log aggregation** - Send logs to Loki/ELK
