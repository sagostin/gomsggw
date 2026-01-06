# GOMSGGW Messaging Gateway

A high-performance multi-protocol messaging gateway bridging **SMPP/MM4** (legacy clients) and **REST API/Webhooks** (web clients) with unified message routing, usage limits, and comprehensive logging.

---

## Quick Start

```bash
git clone https://github.com/sagostin/gomsggw.git
cd gomsggw
cp sample.env .env
# Edit .env with your settings
./build.sh
docker-compose up -d
```

---

## Features

### Multi-Protocol Support
| Protocol | Direction | Use Case |
|----------|-----------|----------|
| SMPP | Bidirectional | Zultys MX, legacy PBX |
| MM4 | Bidirectional | MMS via legacy |
| REST API | Outbound | Web apps, Bicom PBXware |
| Webhooks | Inbound | Web apps, Bicom PBXware |

### Client Types
| Feature | Legacy | Web |
|---------|--------|-----|
| Protocol | SMPP/MM4 | REST/Webhook |
| Auth | SMPP Bind | HTTP Basic/Bearer |
| Splitting | Always | Configurable |
| API Format | N/A | generic, bicom, telnyx |
| Use Case | Zultys MX | Bicom PBXware, Web Apps |

### Additional Features
- **Usage Limits** - Burst/daily/monthly quotas with timezone support
- **Number Organization** - Tags and groups
- **MMS Transcoding** - Automatic media optimization
- **Enhanced Logging** - Client types, delivery methods, segments
- **Global Retry Config** - Configurable via environment variables

---

## Integration Examples

### Zultys MX (SMPP/Legacy)

```bash
# Create legacy client (address required for ACL)
curl -X POST http://gateway:3000/clients \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -d '{"username":"zultys","password":"smpp_pass","type":"legacy","name":"Zultys MX","address":"192.168.1.100"}'

# Response: {"id": 1, "username": "zultys", ...}

# Configure limits
curl -X PUT http://gateway:3000/clients/1/settings \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -d '{"sms_daily_limit": 10000}'

# Add number (use client ID from response)
curl -X POST http://gateway:3000/clients/1/numbers \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -d '{"number":"+12505551234","carrier":"telnyx"}'
```

**Zultys SMPP Settings**: Host: `gateway-ip`, Port: `9550`, System ID: `zultys`

### Bicom PBXware (REST API)

```bash
# Create web client
curl -X POST http://gateway:3000/clients \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -d '{"username":"bicom","password":"api_key","type":"web","name":"Bicom PBXware","timezone":"America/Vancouver"}'

# Response: {"id": 2, "username": "bicom", ...}

# Configure settings and limits
curl -X PUT http://gateway:3000/clients/2/settings \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -d '{"api_format":"bicom","default_webhook":"https://bicom.local/smsservice/connector","sms_daily_limit":25000}'

# Send SMS (client auth with Bearer token for Bicom format)
curl -X POST http://gateway:3000/messages/send \
  -H "Authorization: Bearer $(echo -n 'bicom:api_key' | base64)" \
  -d '{"from":"+12505551234","to":"+14155559876","text":"Hello!"}'
```

---

## API Endpoints

### Admin Endpoints (API_KEY auth)

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/health` | Health check |
| GET | `/stats` | Connection stats |
| GET | `/clients` | List all clients |
| POST | `/clients` | Create client |
| DELETE | `/clients/{id}` | Delete a client |
| PATCH | `/clients/{id}/password` | Update client password |
| GET | `/clients/{id}/numbers` | List client numbers |
| POST | `/clients/{id}/numbers` | Add number to client |
| PUT | `/clients/{id}/numbers/{number_id}` | Update number properties |
| DELETE | `/clients/{id}/numbers/{number_id}` | Remove number |
| GET | `/clients/{id}/settings` | Get client settings |
| PUT | `/clients/{id}/settings` | Update client settings |
| GET | `/numbers/{id}/settings` | Get number settings |
| PUT | `/numbers/{id}/settings` | Update number settings |
| GET | `/carriers` | List carriers |
| POST | `/carriers` | Add carrier |
| POST | `/clients/reload` | Reload clients from DB |
| POST | `/carriers/reload` | Reload carriers |

### Web Client Endpoints (client auth)

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/messages/send` | Send SMS/MMS |
| GET | `/messages/usage` | Check quota |

---

## Configuration

### Required Environment Variables

```bash
# Security
ENCRYPTION_KEY=your-32-byte-key
API_KEY=admin-api-key

# Server Ports
WEB_LISTEN=0.0.0.0:3000
SMPP_LISTEN=0.0.0.0:9550
MM4_LISTEN=0.0.0.0:2566

# PostgreSQL
POSTGRES_HOST=postgres
POSTGRES_USER=smsgw
POSTGRES_PASSWORD=secret
POSTGRES_DB=smsgw
```

### Global Retry Configuration

```bash
WEBHOOK_RETRIES=3
WEBHOOK_TIMEOUT_SECS=10
SMPP_RETRIES=3
SMPP_TIMEOUT_SECS=30
MM4_RETRIES=3
MM4_TIMEOUT_SECS=60
NOTIFY_SENDER_ON_FAILURE=true
```

### Carriers

```bash
TELNYX_ENABLE=true
TELNYX_API_KEY=your-key

TWILIO_ENABLE=true
TWILIO_ACCOUNT_SID=your-sid
TWILIO_AUTH_TOKEN=your-token
```

---

## CLI Management Tool

```bash
cd scripts
pip install requests
export MSGGW_BASE_URL=http://localhost:3000
export MSGGW_API_KEY=your-api-key
python main.py
```

See [scripts/README.md](scripts/README.md) for usage.

---

## Documentation

| Document | Description |
|----------|-------------|
| [docs/api_reference.md](docs/api_reference.md) | Complete API reference |
| [docs/deployment.md](docs/deployment.md) | Docker setup & production |
| [docs/web_clients.md](docs/web_clients.md) | Web/Bicom integration |
| [docs/legacy_clients.md](docs/legacy_clients.md) | SMPP/Zultys integration |
| [docs/data_models.md](docs/data_models.md) | Database schemas |
| [docs/usage_limits.md](docs/usage_limits.md) | Quota management |
| [docs/configuration.md](docs/configuration.md) | All env variables |

---

## License

See `smpp/LICENSE` file.
