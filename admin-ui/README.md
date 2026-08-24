# GOMSGGW Admin Control Panel

Browser-based admin portal for the GOMSGGW gateway — a replacement for the
`scripts/main.py` CLI. Built with Vue 3 + Vite + TypeScript + Tailwind CSS.

## Features

- **Connect screen** — enter the gateway API URL and the admin master key
  (`API_KEY`). The key is stored in `sessionStorage` only (cleared when the
  tab closes) and sent as HTTP Basic auth (`apikey:<key>`).
- **Dashboard** — client/carrier counts, live SMPP & MM4 sessions, one-click
  reload of clients + carriers.
- **Clients** — searchable list, create (legacy/web, auto-password generator),
  delete, change password.
- **Client detail**
  - *Numbers* — list, bulk add with E.164/NANP normalization and per-number
    progress, edit carrier/tag/group/webhook, delete, per-number **and**
    bulk (multi-select) auto-reply configuration.
  - *Settings* — API format, auth method, webhook config, SMS/MMS usage limits.
  - *API Keys* — list, create (scopes, rate limit, expiry, number scoping;
    raw key shown once), revoke.
  - *Failover* — list with online status, add/edit/remove, priority editing.
  - *SMPP Status* — primary session + failover online states.
- **Carriers** — list, create (telnyx/twilio/bandwidth/plivo), reload.

## Local development

```bash
npm install
npm run dev        # http://localhost:5173/ui/
```

The Vite dev server proxies `/clients`, `/carriers`, `/numbers`, and `/stats`
to the gateway at `http://localhost:3000` (override with `GATEWAY_URL`), so
the app and API share an origin and no CORS is needed:

```bash
GATEWAY_URL=http://gateway.internal:3000 npm run dev
```

On the connect screen, leave the **Gateway API URL** blank to use the
same-origin proxy, or enter a full URL (e.g. `https://msggw1.example.com`) to
talk to a gateway directly. Direct cross-origin connections require the
gateway to run with CORS enabled — it is by default
(`CORS_ALLOWED_ORIGINS=*`, see `docs/configuration.md`).

## Deployment (Docker)

**Alongside the gateway (simplest):** the root `docker-compose.yml` already
includes this service, so one command brings up DB + gateway + panel:

```bash
docker compose up -d --build        # from the repo root
# → http://<host>:8080/ui/
```

**Standalone** (this compose file attaches to the external `gomsggw-network`
created by the root stack):

```bash
docker compose -f admin-ui/docker-compose.yml up -d --build
```

**Behind Caddy with TLS:** see [../Caddyfile.example](../Caddyfile.example) —
either point Caddy at this container (`reverse_proxy admin-ui:80`), or serve
the static `dist/` build directly with no container at all.

The image is multi-stage: Node builds the SPA, then nginx serves it under
`/ui/` and reverse-proxies the admin API paths to the gateway. Set
`GATEWAY_URL` (default `http://gomsggw:3000`) to point at your gateway
container or host.

## Build

```bash
npm run build      # type-check + production build → dist/
npm run preview    # serve the production build locally
```
