# GOMSGGW Documentation

Complete documentation for the GOMSGGW messaging gateway.

---

## Quick Links

| Document | Description |
|----------|-------------|
| [API Reference](api_reference.md) | All REST API endpoints |
| [Web Clients](web_clients.md) | REST API & webhook integration |
| [Legacy Clients](legacy_clients.md) | SMPP/MM4 integration (Zultys, Bicom) |
| [Data Models](data_models.md) | Database entities and schemas |
| [Usage Limits](usage_limits.md) | Quota management |
| [Number Management](number_management.md) | Phone number configuration |
| [Configuration](configuration.md) | Environment variables |
| [Deployment](deployment.md) | Docker and production setup |
| [Architecture](architecture.md) | System design |
| [Transcoding](transcoding.md) | MMS media processing |

---

## Getting Started

### For Web Applications
→ [Web Clients Guide](web_clients.md)

### For Zultys / Legacy PBX
→ [Legacy Clients Guide](legacy_clients.md)

### For Administrators
→ [API Reference](api_reference.md)
→ [Configuration](configuration.md)
→ [Deployment](deployment.md)

---

## Integration Examples

### Zultys MX PBX (SMPP)
```bash
# Create legacy client
curl -X POST http://gateway:3000/clients \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -d '{"username":"zultys","password":"smpp_pass","type":"legacy","address":"192.168.1.100"}'
# Response: {"id": 1, ...}

# Add number (use ID from response)
curl -X POST http://gateway:3000/clients/1/numbers \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -d '{"number":"+12505551234","carrier":"telnyx"}'
```

### Bicom PBXware (REST API)
```bash
# Create web client
curl -X POST http://gateway:3000/clients \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -d '{"username":"bicom","password":"api_key","type":"web"}'
# Response: {"id": 2, ...}

# Configure webhook (use ID from response)
curl -X PUT http://gateway:3000/clients/2/settings \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -d '{"api_format":"bicom","default_webhook":"https://bicom/smsservice/connector"}'
```

### Send SMS (Web Client)
```bash
curl -X POST http://gateway:3000/messages/send \
  -H "Authorization: Basic $(echo -n 'client:password' | base64)" \
  -d '{"from":"+12505551234","to":"+14155559876","text":"Hello!"}'
```
