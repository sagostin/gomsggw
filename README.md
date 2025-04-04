# Gomsggw Messaging Gateway

Gomsggw is a high-performance messaging gateway that integrates SMPP and MM4 protocols. It is designed to handle SMS and MMS messages by bridging communications between various carriers (such as Twilio and Telnyx) and client applications. The gateway leverages web APIs, Prometheus metrics, and robust logging to provide a reliable, scalable, and observable messaging solution.

## Contents

- [Overview](#overview)
- [Features](#features)
- [Architecture](#architecture)
- [Installation & Setup](#installation--setup)
- [Configuration](#configuration)
  - [Environment Variables](#environment-variables)
- [API Documentation](#api-documentation)
  - [Authentication](#authentication)
  - [Endpoints](#endpoints)
- [Building & Running](#building--running)
- [Troubleshooting & Logging](#troubleshooting--logging)
- [Contributing](#contributing)
- [License](#license)

## Overview

Gomsggw integrates multiple messaging protocols in one unified gateway service. It supports:

- **SMPP Server:** For SMS messaging. The gateway accepts SMPP connections from clients and forwards messages to carriers.
- **MM4 Server:** For MMS messaging. The MM4 protocol handles multimedia messages.
- **Web API:** A RESTful API (built with the Iris framework) for administrative tasks such as managing carriers, clients, retrieving statistics, health checks, and media file retrieval.
- **Metrics:** Prometheus metrics endpoints provide visibility into system performance and client connections.
- **Logging:** Uses Logrus for structured logging with support for external logging systems (e.g., Loki).

## Features

- **Multi-Protocol Messaging:** Supports both SMS (via SMPP) and MMS (via MM4) messaging.
- **Carrier & Client Management:** RESTful endpoints for adding and managing carriers and clients.
- **Robust Logging & Monitoring:** Detailed logs and Prometheus metrics for system observability.
- **Docker Ready:** Comes with a Dockerfile and Docker Compose configuration for containerized deployments.
- **Scalability:** Designed to support multiple gateway instances behind load balancers.

## Architecture

The system is composed of several interconnected components:

1. **Main Application (main.go):** Loads environment configuration, initializes components, and starts the web server, SMPP server, and MM4 server concurrently.
2. **Web Server (web_server.go):** Exposes RESTful endpoints for health checks, API management (carriers, clients), media retrieval, and inbound message processing.
3. **Routers (router.go, router_carrier.go, router_client.go):** Handle message routing between carriers and clients.
4. **SMPP & MM4 Servers:** Handle protocol-specific communications. The SMPP server manages SMS sessions and message delivery, while the MM4 server handles multimedia messages.
5. **Metrics & Logging:** Exposes Prometheus metrics and logs detailed events using Logrus.

## Installation & Setup

### Prerequisites

- [Go](https://golang.org/) (check the `go.mod` file for the required version).
- [Docker](https://www.docker.com/) (for containerized deployments).
- [PostgreSQL](https://www.postgresql.org/) (can be run via Docker Compose).

### Clone the Repository

```bash
git clone https://github.com/sagostin/gomsggw.git
cd gomsggw
```

### Environment Configuration

Copy the sample environment file and modify it according to your deployment settings:

```bash
cp sample.env .env
```

Key environment variables:

- **ENCRYPTION_KEY**: (Required) Encryption key used for sensitive data.
- **WEB_LISTEN**: Address and port for the web API server (default: `0.0.0.0:3000`).
- **SMPP_LISTEN**: Listening address for the SMPP server (e.g., `0.0.0.0:9550`).
- **MM4_LISTEN**: Listening address for the MM4 server (e.g., `0.0.0.0:2566`).
- **API_KEY**: API key used for Basic Authentication on protected endpoints.
- **PostgreSQL Settings**: Variables beginning with `POSTGRES_`.
- **Prometheus Settings**: `PROMETHEUS_LISTEN` and `PROMETHEUS_PATH` for exposing metrics.

### Docker Compose Setup

A pre-configured `docker-compose.yml` is provided to spin up necessary services (PostgreSQL and multiple gateway instances). Review and adjust the configuration as needed before deployment.

## API Documentation

### Authentication

Most API endpoints (except for public health checks) require Basic Authentication. Use the following guidelines:

- **Header**: `Authorization: Basic <credentials>`
- **Credentials Format**: Base64 encoded string in the format `username:API_KEY`.

Example using cURL:

```bash
curl -H "Authorization: Basic $(echo -n 'user:YOUR_API_KEY' | base64)" http://localhost:3000/stats
```

### Endpoints

#### Health Check

- **GET /health**

  Returns HTTP 200 to indicate the server is up.

#### Statistics

- **GET /stats** (protected)

  Provides a JSON response with statistics on connected SMPP and MM4 clients. Sample response:

  ```json
  {
    "smpp_connected_clients": 5,
    "smpp_clients": [
      { "username": "client1", "ip_address": "192.168.1.10", "last_seen": "2025-04-04T12:34:56Z" }
    ],
    "mm4_connected_clients": 3,
    "mm4_clients": [
      { "client_id": "mm4_001", "connected_at": "2025-04-04T12:35:00Z" }
    ]
  }
  ```

#### Carrier Management

- **POST /carriers** (protected)

  Add a new carrier. The JSON body should include (at minimum):

  - `name`: Carrier name
  - `type`: Carrier type (e.g., Twilio, Telnyx)
  - `username`: Account username
  - `password`: Account password

  **Example Request**:

  ```json
  {
    "name": "TwilioCarrier",
    "type": "twilio",
    "username": "twilio_user",
    "password": "secret"
  }
  ```

- **GET /carriers** (protected)

  Retrieve a list of all configured carriers.

- **POST /carriers/reload** (protected)

  Reload carriers from persistent storage (e.g., database).

#### Client Management

- **POST /clients** (protected)

  Add a new client. The JSON payload should include required client fields such as `username` and `password`.

  **Example Request**:

  ```json
  {
    "username": "client1",
    "password": "client_secret",
    "name": "Client One",
    "address": "http://client.example.com",
    "log_privacy": "public"
  }
  ```

#### Inbound Messaging

- **POST /inbound/{carrier}**

  Endpoint to receive inbound messages from carriers. The `{carrier}` parameter should specify the carrier identifier. The payload will be processed by the gateway and routed appropriately.

#### Media Retrieval

- **GET /media/{id}**

  Retrieve media files associated with MMS messages. The `{id}` path parameter corresponds to the media identifier.

## Building & Running

### Local Development (Without Docker)

1. Ensure you have set up the environment variables by copying and modifying `.env`.
2. Build and run the application:

   ```bash
go build -o msggw
./msggw
   ```

### Docker Build

Use the provided `build.sh` script to build the Docker image:

```bash
./build.sh
```

### Docker Compose

Start the services with Docker Compose:

```bash
docker-compose up --build
```

This will launch the messaging gateway and PostgreSQL services. Make sure to adjust the configuration in `docker-compose.yml` (and your environment variables) as needed.

## Troubleshooting & Logging

- **Logs:** The gateway uses Logrus for structured logging. Logs are sent to STDOUT; ensure your logging aggregator (if used) is configured correctly.
- **Metrics:** Access Prometheus metrics at the endpoint defined by `PROMETHEUS_LISTEN` and `PROMETHEUS_PATH` (e.g., http://localhost:2550/metrics).
- **Authentication Errors:** Ensure the API key provided in the `Authorization` header matches the `API_KEY` environment variable.
- **Environment Variable Issues:** The gateway will exit if essential variables (like `ENCRYPTION_KEY`) are missing. Double-check your `.env` file.

## Contributing

Contributions are welcome! Please fork the repository and submit pull requests. When making changes:

- Follow the coding style and structure already present in the codebase.
- Write tests for significant functionality where applicable.
- Update documentation as needed.

## License

This project is licensed under the terms specified in the `LICENSE` file located in the `smpp` directory.
