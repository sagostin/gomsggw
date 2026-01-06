# GOMSGGW Manager CLI

Interactive CLI tool for managing carriers, clients, and phone numbers.

## Setup

```bash
cd scripts
pip install requests
```

## Configuration

```bash
export MSGGW_BASE_URL="http://your-gateway:3000"
export MSGGW_API_KEY="your-admin-api-key"
```

## Usage

```bash
python main.py
```

## Menu

```
� Carriers:
  1) List carriers
  2) Add carrier

📋 Clients:
  3) List clients
  4) Show client details
  5) Create client
  6) Update client settings

📞 Numbers:
  7) List client numbers
  8) Add numbers to client

⚙️ Admin:
  9) Reload all
  q) Quick flow
```

## Examples

### Add Telnyx Carrier

```
> 2
Carrier Name: telnyx_prod
Carrier Type: 1 (telnyx)
API Key: KEY123...
```

### Create Zultys Client (Legacy)

```
> 5
Username: zultys_mx
Password: [auto-generated]
Display Name: Zultys MX PBX
Client Type: 1 (legacy)
```

### Create Bicom Client (Web)

```
> 5
Username: bicom_pbx
Client Type: 2 (web)

> 6
Client: bicom_pbx
API Format: 2 (bicom)
Default Webhook: https://bicom/smsservice/connector
```

### Add Numbers

```
> 8
Client: zultys_mx
Carrier: telnyx_prod
Numbers: 12505551234, 12505551235
```

## Number Formatting

- Input: `+1-250-555-1234`, `250-555-1234`, `12505551234`
- Output: `12505551234` (11 digits)
