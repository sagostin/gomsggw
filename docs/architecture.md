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
    end
    
    subgraph "GOMSGGW"
        direction TB
        SMPP[SMPP Server<br/>Port 9550]
        MM4[MM4 Server<br/>Port 2566]
        WEB[Web Server<br/>Port 3000]
        
        ROUTER[Unified Router]
        TRANS[Media Transcoder]
        CONVO[ConvoManager]
        
        SMPP --> ROUTER
        MM4 --> ROUTER
        WEB --> ROUTER
        ROUTER <--> TRANS
        SMPP <--> CONVO
    end
    
    subgraph "Carriers"
        TEL[Telnyx API]
        TWI[Twilio API]
    end
    
    LC1 --> SMPP
    LC2 --> MM4
    WC1 --> WEB
    WC2 --> WEB
    
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

**Key Data Structures:**
```go
type Router struct {
    gateway          *Gateway
    Routes           []*Route
    ClientMsgChan    chan MsgQueueItem  // From clients
    CarrierMsgChan   chan MsgQueueItem  // From carriers
    MessageAckStatus chan MsgQueueItem  // Delivery confirmations
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
3. **Carrier Webhooks**: `/inbound/{carrier}`
4. **Web Client API**: `/messages/send`
5. **Media Serving**: `/media/{token}` (UUID-based access tokens for security)

### Transcoder (`mms_transcode.go`)

Media normalization pipeline for MMS content.

**Capabilities:**
- Image: Resize, compress, format conversion
- Video: Transcode to H.264, size optimization
- Audio: Convert to MP3/AMR

For detailed transcoding documentation, see [Transcoding](./transcoding.md).

### ConvoManager (`convo.go`)

Ensures message ordering for SMPP sessions.

**Purpose**: Manage in-flight windows to prevent message overtaking on the wire.

**Mechanism:**
- Track pending `submit_sm` operations
- Ensure responses correlate to correct requests
- Maintain sequence numbers

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
- [Legacy Clients](./legacy_clients.md) - SMPP/MM4 details
- [Web Clients](./web_clients.md) - HTTP API details
- [Transcoding](./transcoding.md) - Media processing
- [Usage Limits](./usage_limits.md) - Rate limiting
- [Deployment](./deployment.md) - Operations guide
