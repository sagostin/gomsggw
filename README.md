# GOMSGGW Messaging Gateway

A high-performance multi-protocol messaging gateway bridging **SMPP/MM4** (legacy clients) and **REST API/Webhooks** (web clients) with unified message routing, usage limits, and comprehensive logging.

---

## Quick Start

```bash
git clone https://github.com/sagostin/gomsggw.git
cd gomsggw
cp sample.env .env
# Edit .env with your settings (see Configuration below)
./build.sh
docker-compose up -d
```

> If you run the binary directly (e.g. `./gomsggw` outside Docker), the Go binary auto-loads `.env` from the working directory via `godotenv`. Set the variables however you like (systemd unit, k8s ConfigMap, etc.) and skip the `.env` file.

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
| Message Splitting | Always | Configurable |
| API Format | N/A | generic, bicom, telnyx |
| Use Case | Zultys MX | Bicom PBXware, Web Apps |

### Additional Features

- **Usage Limits** - Burst/daily/monthly quotas (SMS & MMS) with timezone support
- **Number Organization** - Tags and groups for multi-tenant deployments
- **MMS Transcoding** - Automatic media optimization for carrier limits
- **Enhanced Logging** - Client types, delivery methods, segment tracking
- **Global Retry Config** - Configurable retries for webhooks, SMPP, MM4

---

## Integration Examples

### Zultys MX (SMPP/Legacy)

```bash
# Create legacy client (address required for IP ACL)
curl -X POST http://gateway:3000/clients \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -H "Content-Type: application/json" \
  -d '{"username":"zultys","password":"smpp_pass","type":"legacy","name":"Zultys MX","address":"192.168.1.100"}'

# Add number
curl -X POST http://gateway:3000/clients/1/numbers \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -H "Content-Type: application/json" \
  -d '{"number":"+12505551234","carrier":"telnyx"}'
```

**Zultys SMPP Settings**: Host: `gateway-ip`, Port: `9550`, System ID: `zultys`, Bind: Transceiver

### Bicom PBXware (REST API)

```bash
# Create web client
curl -X POST http://gateway:3000/clients \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -H "Content-Type: application/json" \
  -d '{"username":"bicom","password":"api_key","type":"web","name":"Bicom PBXware","timezone":"America/Vancouver"}'

# Configure Bicom format and webhook
curl -X PUT http://gateway:3000/clients/2/settings \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -H "Content-Type: application/json" \
  -d '{"api_format":"bicom","default_webhook":"https://bicom.local/smsservice/connector"}'

# Send SMS
curl -X POST http://gateway:3000/messages/send \
  -H "Authorization: Bearer $(echo -n 'bicom:api_key' | base64)" \
  -H "Content-Type: application/json" \
  -d '{"from":"+12505551234","to":"+14155559876","text":"Hello!"}'
```

---

## API Endpoints

> The full request/response shapes live in [docs/api_reference.md](docs/api_reference.md). The tables below are an at-a-glance index of every route the gateway exposes.

### Admin Endpoints (API_KEY auth)

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/health` | Health check (no auth) |
| GET | `/stats` | Connection stats |
| GET | `/clients` | List all clients |
| POST | `/clients` | Create client |
| DELETE | `/clients/{id}` | Delete a client |
| PATCH | `/clients/{id}/password` | Update client password |
| GET | `/clients/{id}/numbers` | List client numbers |
| POST | `/clients/{id}/numbers` | Add number to client |
| PUT | `/clients/{id}/numbers/{number_id}` | Update number |
| DELETE | `/clients/{id}/numbers/{number_id}` | Remove number |
| GET | `/clients/{id}/settings` | Get client settings |
| PUT | `/clients/{id}/settings` | Update client settings |
| GET | `/clients/{id}/failovers` | List client failovers |
| POST | `/clients/{id}/failovers` | Add failover |
| PUT | `/clients/{id}/failovers/{failover_id}` | Update failover |
| DELETE | `/clients/{id}/failovers/{failover_id}` | Remove failover |
| GET | `/clients/{id}/smpp-status` | SMPP session status + failovers |
| POST | `/clients/{id}/api-keys` | Create tenant API key |
| GET | `/clients/{id}/api-keys` | List tenant API keys |
| DELETE | `/clients/{id}/api-keys/{key_id}` | Revoke tenant API key |
| GET | `/numbers/{id}/settings` | Get number settings |
| PUT | `/numbers/{id}/settings` | Update number settings |
| GET | `/carriers` | List carriers |
| POST | `/carriers` | Add carrier |
| POST | `/clients/reload` | Reload clients from DB |
| POST | `/carriers/reload` | Reload carriers |
| POST | `/inbound/{carrier}` | Carrier inbound webhook (Telnyx/Twilio/OVP) |

### Web Client Endpoints (client auth or API key)

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/messages/send` | Send SMS/MMS |
| POST | `/messages` | Alias of `/messages/send` (Bicom compatibility) |
| GET | `/messages/usage` | Check quota usage |
| GET | `/messages/history` | Paginated message history |
| POST | `/messages/batch` | Submit a batch job |
| POST | `/messages/batch/check` | Pre-check batch against limits |
| GET | `/messages/batch` | List recent batch jobs |
| GET | `/messages/batch/{id}` | Get batch job status |
| GET | `/messages/batch/{id}/messages` | List messages in a batch |
| POST | `/messages/batch/{id}/cancel` | Cancel a batch job |
| DELETE | `/messages/batch/{id}/messages/{msg_id}` | Cancel a single batch message |

### Media (public)

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/media/{token}` | Retrieve MMS media by UUID access token |

---

## Configuration

### Required Environment Variables

```bash
# Security (REQUIRED)
ENCRYPTION_KEY=your-32-character-key-here
API_KEY=your-admin-api-key

# Server Ports
WEB_LISTEN=0.0.0.0:3000
SMPP_LISTEN=0.0.0.0:9550
MM4_LISTEN=0.0.0.0:2566

# PostgreSQL
POSTGRES_HOST=postgres
POSTGRES_PORT=5432
POSTGRES_USER=smsgw
POSTGRES_PASSWORD=your-secure-password
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

See [sample.env](sample.env) for all available options.

---

## CLI Management Tool

```bash
cd scripts
pip install requests
export MSGGW_BASE_URL=http://localhost:3000
export MSGGW_API_KEY=your-api-key
python main.py
```

Interactive menu for managing carriers, clients, and numbers. See [scripts/README.md](scripts/README.md).

---

## Testing

Unit tests cover the pure-function surface of the gateway (encryption, rate-limit resolution, SMPP conversation ordering, batch template rendering, CSV parsing, MMS URL helpers, GSM-7 validation, etc.). They run with no external dependencies — no PostgreSQL or network required.

```bash
make test            # quick run with -race
make test-verbose    # see every subtest
```

The `Makefile` runs the root package only; `migration/` has a pre-existing duplicate-`main` build conflict and `scripts/` contains an unrelated main, so both are excluded.

---

## Documentation

| Document | Description |
|----------|-------------|
| [Architecture](docs/architecture.md) | System design and message flow |
| [API Reference](docs/api_reference.md) | Complete endpoint documentation |
| [Deployment](docs/deployment.md) | Docker setup & production config |
| [Configuration](docs/configuration.md) | All environment variables |
| [Data Models](docs/data_models.md) | Database schemas |
| [Web Clients](docs/web_clients.md) | REST API / Bicom integration |
| [Legacy Clients](docs/legacy_clients.md) | SMPP / Zultys integration |
| [Usage Limits](docs/usage_limits.md) | Quota management |
| [Number Management](docs/number_management.md) | Tags, groups, per-number settings |
| [Transcoding](docs/transcoding.md) | MMS media optimization |
| [Migration](docs/migration.md) | Database migration guide |

---

## Security

- **Passwords encrypted** at rest using AES-256
- **Admin endpoints** protected by `API_KEY`
- **Client endpoints** use Basic/Bearer auth
- **SMPP ACL** validates source IP for legacy clients
- Usernames stored in plaintext (used as lookup key)

---

## License

See `LICENSE.md` file.
