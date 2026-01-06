# Legacy Client Integration

Documentation for integrating legacy systems (SMPP/MM4) with GOMSGGW.

---

## Overview

Legacy clients connect via industry-standard protocols:
- **SMPP** (Short Message Peer-to-Peer) - For SMS
- **MM4** - For MMS multimedia messages

These are typically PBX systems, enterprise messaging platforms, or older telephony equipment that don't support modern REST APIs.

---

## Zultys MX Integration

Zultys MX PBX systems use SMPP for SMS messaging. Here's how to configure the integration:

### Gateway Configuration

Create a legacy client for the Zultys system:

```bash
curl -X POST http://gateway:3000/clients \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "username": "zultys_mx",
    "password": "smpp_password",
    "name": "Zultys MX PBX",
    "type": "legacy",
    "address": "192.168.1.100",
    "timezone": "America/Vancouver"
  }'

# Response: {"id": 1, "username": "zultys_mx", ...}
```

> **Address field**: Required for legacy clients. Supports IP (`192.168.1.100`) or hostname (`mx.zultys.local`). Used for SMPP ACL and MM4 delivery.

Configure limits (optional):

```bash
curl -X PUT http://gateway:3000/clients/1/settings \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "sms_daily_limit": 10000,
    "mms_daily_limit": 1000
  }'
```

Add phone numbers (use client ID from response):

```bash
curl -X POST http://gateway:3000/clients/1/numbers \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "number": "+12505551234",
    "carrier": "telnyx"
  }'
```

### Zultys MX Configuration

On the Zultys MX system, configure the SMPP connection:

| Setting | Value |
|---------|-------|
| SMPP Host | `gateway-ip` |
| SMPP Port | `9550` (or your configured port) |
| System ID | `zultys_mx` |
| Password | `smpp_password` |
| Bind Type | Transceiver |
| TON | 1 (International) |
| NPI | 1 (E.164) |

### Message Flow

```
┌─────────────┐         ┌──────────────┐         ┌─────────────┐
│  Zultys MX  │  SMPP   │   GOMSGGW    │  API    │   Carrier   │
│    PBX      │◄───────►│   Gateway    │◄───────►│  (Telnyx)   │
└─────────────┘         └──────────────┘         └─────────────┘
```

**Outbound (Zultys → Carrier)**:
1. Zultys sends `submit_sm` PDU via SMPP
2. Gateway receives and validates
3. Gateway routes to carrier via REST API
4. Gateway returns `submit_sm_resp`

**Inbound (Carrier → Zultys)**:
1. Carrier sends webhook to gateway
2. Gateway looks up destination number
3. Gateway sends `deliver_sm` PDU to Zultys
4. Zultys acknowledges with `deliver_sm_resp`

### Message Splitting

For Zultys (and all legacy clients), **message splitting is mandatory**:
- Messages over 160 chars (GSM-7) or 70 chars (UCS-2) are split
- UDH headers handle reassembly on the receiving end
- Gateway tracks segments via `TotalSegments` and `SegmentIndex`

---

## Bicom PBXware Integration

Bicom PBXware can integrate as either legacy (SMPP) or web client depending on your needs.

### Option 1: SMPP Integration (Legacy)

Same configuration as Zultys - create a legacy client and configure PBXware's SMPP settings.

### Option 2: REST API Integration (Web Client)

For modern deployments, use the REST API:

```bash
# Create web client
curl -X POST http://gateway:3000/clients \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "username": "bicom_pbx",
    "password": "secure_api_key",
    "name": "Bicom PBXware",
    "type": "web",
    "sms_limit": 50000
  }'

# Configure webhook for inbound messages
curl -X PUT http://gateway:3000/clients/bicom_pbx/settings \
  -H "Authorization: Basic $(echo -n 'admin:API_KEY' | base64)" \
  -H "Content-Type: application/json" \
  -d '{
    "default_webhook": "https://bicom-pbx.local/api/sms/inbound",
    "webhook_retries": 3
  }'
```

---

## Generic SMPP Client Setup

For any SMPP-compatible system:

### 1. Create Client

```json
{
  "username": "smpp_client",
  "password": "bind_password",
  "name": "Generic SMPP System",
  "type": "legacy"
}
```

### 2. SMPP Connection Parameters

| Parameter | Value |
|-----------|-------|
| Host | Gateway IP |
| Port | 9550 (default) |
| System ID | Client username |
| Password | Client password |
| System Type | (optional) |
| Bind Mode | Transceiver recommended |

### 3. PDU Support

| PDU Type | Direction | Description |
|----------|-----------|-------------|
| `bind_transceiver` | Client→GW | Authentication |
| `bind_transceiver_resp` | GW→Client | Auth response |
| `submit_sm` | Client→GW | Send message |
| `submit_sm_resp` | GW→Client | Send acknowledgement |
| `deliver_sm` | GW→Client | Receive message |
| `deliver_sm_resp` | Client→GW | Receive acknowledgement |
| `enquire_link` | Both | Keep-alive |
| `unbind` | Both | Disconnect |

---

## MM4 Integration (MMS)

For MMS support via MM4 protocol:

### Connection Settings

| Setting | Value |
|---------|-------|
| Host | Gateway IP |
| Port | 2566 (default) |
| Protocol | MM4/SMTP |

### Supported MM4 Message Types

- `MM4_forward.REQ` - Send MMS
- `MM4_forward.RES` - Send acknowledgement
- `MM4_delivery_report.REQ` - Delivery notification

---

## Troubleshooting

### SMPP Connection Issues

```bash
# Check if SMPP port is listening
nc -zv gateway-ip 9550

# Check gateway logs for bind attempts
grep "HandleBind" /var/log/gomsggw.log
```

### Common Errors

| Error | Cause | Solution |
|-------|-------|----------|
| `ESME_RINVSYSID` | Invalid username | Verify client username |
| `ESME_RINVPASWD` | Wrong password | Check password in DB |
| `ESME_RBINDFAIL` | Bind failed | Check client exists |

### Debug Logging

Enable debug logging to see PDU details:

```bash
export LOG_LEVEL=debug
```
