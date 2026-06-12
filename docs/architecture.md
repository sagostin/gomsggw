# GOMSGGW Architecture

Comprehensive system design and component documentation for the GOMSGGW messaging gateway.

## Table of Contents

1. [System Overview](#system-overview)
2. [Core Architecture](#core-architecture)
3. [Component Details](#component-details)
4. [Data Flow](#data-flow)
5. [Concurrency Model](#concurrency-model)
6. [Client Types](#client-types)

---

## System Overview

`gomsggw` is a high-throughput, multi-protocol message gateway designed to bridge traditional telecom protocols (SMPP/MM4) with modern web technologies. It serves as a central hub for:

- **Routing**: Directing messages between clients and carriers
- **Transcoding**: Normalizing MMS content for carrier compatibility
- **Rate Limiting**: Enforcing usage quotas
- **Protocol Translation**: Converting between SMPP, MM4, and HTTP

```mermaid
graph TB
    subgraph "Clients"
        LC1[Legacy PBX<br/>SMPP/MM4]
        LC2[Legacy System<br/>SMPP/MM4]
        WC1[Web App<br/>REST API]
        WC2[Web Service<br/>REST API]
        EA1[External App<br/>API Key Auth]
    end
    
    subgraph "GOMSGGW"
        direction TB
        SMPP[SMPP Server<br/>Port 9550]
        MM4[MM4 Server<br/>Port 2566]
        WEB[Web Server<br/>Port 3000]
        
        ROUTER[Unified Router]
        TRANS[Media Transcoder]
        CONVO[ConvoManager]
        BATCH[Batch Processor]
        
        SMPP --> ROUTER
        MM4 --> ROUTER
        WEB --> ROUTER
        BATCH --> ROUTER
        ROUTER <--> TRANS
        SMPP <--> CONVO
    end
    
    subgraph "Carriers"
        TEL[Telnyx API]
        TWI[Twilio API]
        OVP[OneVoicePlus API]
    end
    
    LC1 --> SMPP
    LC2 --> MM4
    WC1 --> WEB
    WC2 --> WEB
    EA1 --> WEB
    WEB --> BATCH
    
    ROUTER --> TEL
    ROUTER --> TWI
    TEL --> WEB
    TWI --> WEB
```

---

## Core Architecture

### Hub-and-Spoke Model

The gateway follows a hub-and-spoke architecture:

- **Hub (Router)**: Central message dispatcher that receives all traffic
- **Spokes (Servers)**: Protocol-specific ingress/egress points

```mermaid
graph LR
    subgraph "Ingress Spokes"
        I1[SMPP Server]
        I2[MM4 Server]
        I3[Web API]
    end
    
    subgraph "Hub"
        R[Router]
        Q1[ClientMsgChan]
        Q2[CarrierMsgChan]
    end
    
    subgraph "Egress Spokes"
        E1[SMPP Client]
        E2[MM4 Client]
        E3[Carrier APIs]
        E4[Webhooks]
    end
    
    I1 --> Q1
    I2 --> Q1
    I3 --> Q1
    
    Q1 --> R
    Q2 --> R
    
    R --> E1
    R --> E2
    R --> E3
    R --> E4
    
    E3 --> Q2
```

### Message Flow

Every message follows this general path:

1. **Ingestion**: Received by protocol-specific server
2. **Normalization**: Numbers converted to E.164, metadata extracted
3. **Queuing**: Placed in appropriate channel (Client or Carrier)
4. **Routing**: Router determines destination and path
5. **Processing**: Transcoding applied if needed (MMS)
6. **Dispatch**: Sent to destination via appropriate egress

---

## Component Details

### Router (`router.go`)

The brain of the system. Runs a continuous `UnifiedRouter` loop.

**Responsibilities:**
- Monitor `ClientMsgChan` and `CarrierMsgChan`
- Normalize phone numbers to E.164 format
- Lookup senders and receivers via in-memory maps
- Apply routing rules (internal vs external)
- Enforce usage limits
- Dispatch to appropriate egress
- Trigger SMPP failover when the primary client session is offline

**Key Data Structures:**
```go
type Router struct {
    gateway        *Gateway
    Routes         []*Route
    ClientMsgChan  chan MsgQueueItem  // From clients
    CarrierMsgChan chan MsgQueueItem  // From carriers
}
```

**Routing Logic:**
```mermaid
flowchart TD
    MSG[Incoming Message]
    NORM[Normalize Numbers]
    LOOKUP{Destination<br/>is Client?}
    INTERNAL[Route to Client]
    EXTERNAL[Route to Carrier]
    
    MSG --> NORM
    NORM --> LOOKUP
    LOOKUP -->|Yes| INTERNAL
    LOOKUP -->|No| EXTERNAL
```

### SMS Subsystem (`sms_server.go`)

Handles SMPP protocol connections for SMS traffic.

**Protocol**: SMPP v3.4  
**Transport**: Persistent TCP connections  
**Concurrency**: One goroutine per session

**Key Features:**
- `bind_transceiver` for bidirectional connections
- PDU (Protocol Data Unit) parsing and generation
- User Data Header (UDH) handling for long messages
- Delivery receipt management

**Session Lifecycle:**
```mermaid
sequenceDiagram
    participant C as Client
    participant S as SMPP Server
    participant R as Router
    
    C->>S: TCP Connect
    C->>S: bind_transceiver PDU
    S->>S: Authenticate
    S->>C: bind_transceiver_resp
    
    loop Message Exchange
        C->>S: submit_sm (outbound)
        S->>R: Queue message
        S->>C: submit_sm_resp
        
        R->>S: Inbound message
        S->>C: deliver_sm
        C->>S: deliver_sm_resp
    end
    
    C->>S: unbind
    S->>C: unbind_resp
```

### MMS Subsystem (`mms_server.go`)

Handles MM4 protocol (SMTP-based) for MMS traffic.

**Protocol**: MM4 (SMTP)  
**Transport**: SMTP connections  
**Format**: MIME Multipart

**Key Features:**
- SMTP handshake handling
- MIME parsing for multimedia content
- Client identification by IP address
- Session state tracking via `MM4ClientState`

**MM4 Flow:**
```mermaid
sequenceDiagram
    participant C as MM4 Client
    participant S as MM4 Server
    participant T as Transcoder
    participant R as Router
    
    C->>S: EHLO/HELO
    S->>C: 250 OK
    C->>S: MAIL FROM
    S->>C: 250 OK
    C->>S: RCPT TO
    S->>C: 250 OK
    C->>S: DATA
    C->>S: MIME Content
    C->>S: .
    S->>T: Process Media
    T->>S: Transcoded Media
    S->>R: Queue Message
    S->>C: 250 OK
```

### Web Subsystem (`web_server.go`)

HTTP API server for management, carrier webhooks, and web clients.

**Framework**: Iris (high-performance Go web framework)  
**Authentication**: Basic Auth / API Keys

**Endpoint Categories:**
1. **Health & Monitoring**: `/health`, `/stats`
2. **Management**: `/clients`, `/carriers`, `/reload`
3. **API Key Management**: `/clients/{id}/api-keys` (admin auth)
4. **Carrier Webhooks**: `/inbound/{carrier}`
5. **Web Client API**: `/messages/send`, `/messages/usage`
6. **Batch Sending**: `/messages/batch` (client or API key auth)
7. **Media Serving**: `/media/{token}` (UUID-based access tokens for security)

### Transcoder (`mms_transcode.go`)

Media normalization pipeline for MMS content.

**Capabilities:**
- Image: Resize, compress, format conversion
- Video: Transcode to H.264, size optimization
- Audio: Convert to MP3/AMR

For detailed transcoding documentation, see [Transcoding](./transcoding.md).

### ConvoManager (`convo.go`)

Coordinates SMPP message ordering and delivery-receipt correlation.

**Purpose**: Prevent in-flight messages from overtaking each other on a single SMPP session, and track carrier acknowledgements.

**Mechanism:**
- Track pending `submit_sm` operations by conversation ID
- Ensure responses correlate to the correct request
- Maintain sequence numbers
- `HandleCarrierAck` is invoked from the Telnyx inbound webhook (`message.sent` events) to clear pending entries

---

### Failover Subsystem (`clients.go`, `gateway.go`)

Allows an admin to declare that a legacy client's outbound traffic should be re-routed to a different client when the primary's SMPP session is offline. The router calls `resolveFailoverSession` whenever it needs an active session for a client.

**Data model** (`ClientFailover`):
- `primary_client_id`, `fallback_client_id`, `priority` (ascending), `enabled`

**Resolution flow:**
1. Router looks up the primary client's session
2. If offline, walk the failover list in `priority` order
3. Return the first fallback client with an active SMPP session
4. Log the failover activation; if all failovers are offline, the message is queued for retry

**Admin endpoints**: `/clients/{id}/failovers[/{failover_id}]` (CRUD) and `/clients/{id}/smpp-status` for live diagnostics.

---

### API Key Subsystem (`api_keys.go`)

Tenant-scoped API keys for external applications. Keys are:
- Generated as `gw_live_` + 64 hex chars (32 random bytes)
- **SHA-256 hashed** before storage — the raw key is only returned on creation
- Cached in an in-memory map for O(1) lookup on every request
- Optionally scoped to specific client numbers and permission scopes (`send`, `batch`, `usage`)

**Auth path**: A request with `Authorization: Bearer gw_live_...` is hashed, looked up in the in-memory map, then validated for scope and rate limit. Revoked and expired keys are rejected immediately.

See [API Keys](./api_keys.md) for setup details.

---

### Batch Subsystem (`batch.go`, `batch_routes.go`)

Asynchronous high-volume message delivery with queue-on-limit semantics.

**Lifecycle** (`BatchJob` + `BatchMessageItem`):
1. `POST /messages/batch` accepts a JSON array or CSV with template variables
2. Job is persisted and queued; an initial pass sends messages in order, honouring `throttle_per_second`
3. Messages that hit a rate limit are marked `queued`, not `failed`
4. A retry loop re-checks queued messages every 30 seconds against current limits
5. After `max_retry_mins` (default 60), remaining queued messages are marked `failed`
6. Final `webhook_url` callback fires when the job completes; individual messages can be cancelled by UUID mid-flight

See [Batch Sending](./batch_sending.md) for full details.

---

## Data Flow

### Outbound Message (Client → Carrier)

```mermaid
sequenceDiagram
    participant C as Client
    participant I as Ingress Server
    participant Q as ClientMsgChan
    participant R as Router
    participant L as Limit Check
    participant T as Transcoder
    participant CR as Carrier API
    
    C->>I: Send Message
    I->>I: Parse & Normalize
    I->>Q: MsgQueueItem
    Q->>R: Dequeue
    R->>R: Identify Sender/Receiver
    R->>L: Check Limits
    
    alt Limit Exceeded
        L-->>C: Reject (429/Error)
    else Under Limit
        L->>R: Continue
        
        alt MMS Content
            R->>T: Transcode Media
            T->>R: Processed Media
        end
        
        R->>CR: API Request
        CR-->>R: Response
        R-->>C: Confirmation
    end
```

### Inbound Message (Carrier → Client)

```mermaid
sequenceDiagram
    participant CR as Carrier
    participant W as Webhook Handler
    participant Q as CarrierMsgChan
    participant R as Router
    participant E as Egress
    participant C as Client
    
    CR->>W: Webhook POST
    W->>W: Parse Payload
    W->>Q: MsgQueueItem
    Q->>R: Dequeue
    R->>R: Lookup Destination
    
    alt Web Client
        R->>E: Dispatch Webhook
        E->>C: HTTP POST
    else Legacy Client
        alt SMPP Client
            R->>E: deliver_sm
            E->>C: SMPP PDU
        else MM4 Client
            R->>E: SMTP Message
            E->>C: MIME Email
        end
    end
```

---

## Concurrency Model

### Goroutine Architecture

```mermaid
graph TB
    subgraph "Main Thread"
        MAIN["main()"]
    end
    
    subgraph "Server Goroutines"
        SMPP_MAIN[SMPP Server Loop]
        MM4_MAIN[MM4 Server Loop]
        WEB_MAIN[Web Server]
        ROUTER_MAIN[UnifiedRouter Loop]
        PROM[Prometheus Server]
    end
    
    subgraph "Per-Connection Goroutines"
        SMPP_SESS1[SMPP Session 1]
        SMPP_SESS2[SMPP Session 2]
        MM4_CONN1[MM4 Connection 1]
    end
    
    MAIN --> SMPP_MAIN
    MAIN --> MM4_MAIN
    MAIN --> WEB_MAIN
    MAIN --> ROUTER_MAIN
    MAIN --> PROM
    
    SMPP_MAIN --> SMPP_SESS1
    SMPP_MAIN --> SMPP_SESS2
    MM4_MAIN --> MM4_CONN1
```

### Channel Communication

| Channel | Direction | Purpose |
|---------|-----------|---------|
| `ClientMsgChan` | Client → Router | Outbound messages |
| `CarrierMsgChan` | Carrier → Router | Inbound messages |
| `MessageAckStatus` | Router → Server | Delivery confirmations |

### Thread Safety

- **In-memory maps**: Protected by `sync.RWMutex`
- **Database access**: Serialized through GORM
- **Channel operations**: Inherently thread-safe

---

## Client Types

GOMSGGW supports two primary client architectures:

### Legacy Clients

Traditional telecom equipment using standard protocols.

| Protocol | Use Case |
|----------|----------|
| SMPP | SMS sending/receiving |
| MM4 | MMS sending/receiving |

**Characteristics:**
- Persistent TCP connections
- Protocol-mandated message limits (160 chars SMS)
- Mandatory transcoding for MMS
- Automatic message splitting for long SMS

See [Legacy Clients](./legacy_clients.md) for details.

### Web Clients

Modern API-based integrations.

| Protocol | Use Case |
|----------|----------|
| REST API | Sending messages |
| Webhooks | Receiving messages |

**Characteristics:**
- HTTP-based, stateless connections
- Configurable message handling
- Optional transcoding bypass
- Rich JSON payloads

See [Web Clients](./web_clients.md) for details.

---

## Related Documentation

- [Data Models](./data_models.md) - Database schema
- [API Reference](./api_reference.md) - REST endpoints
- [API Keys](./api_keys.md) - Tenant API key management
- [Batch Sending](./batch_sending.md) - Bulk message delivery
- [Legacy Clients](./legacy_clients.md) - SMPP/MM4 details
- [Web Clients](./web_clients.md) - HTTP API details
- [Transcoding](./transcoding.md) - Media processing
- [Usage Limits](./usage_limits.md) - Rate limiting
- [Deployment](./deployment.md) - Operations guide
