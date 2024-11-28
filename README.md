# Zultys SMPP MM4

## Description
Zultys SMPP MM4 is a project that integrates SMPP and MM4 protocols for messaging services. It provides a robust solution for handling SMS and MMS messages through various carriers.

## Features
- SMPP server implementation
- MM4 message handling
- Integration with RabbitMQ
- Support for multiple carriers like Twilio and Telnyx

## Installation
To set up the project, ensure you have the necessary dependencies installed. You can use the provided `Dockerfile` and `docker-compose.yml` for containerized deployment.

### .env Configuration
The `.env` file is used to configure environment variables for the project. Below are the key configurations:

- **PostgreSQL Settings**
  - `POSTGRES_USER`: Username for PostgreSQL.
  - `POSTGRES_PASSWORD`: Password for PostgreSQL.
  - `POSTGRES_DB`: Database name for PostgreSQL.
  - `POSTGRES_HOST_AUTH_METHOD`: Authentication method for PostgreSQL.
  - `POSTGRES_TIMEZONE`: Timezone setting for PostgreSQL.

- **RabbitMQ Settings**
  - `RABBITMQ_DEFAULT_USER`: Default username for RabbitMQ.
  - `RABBITMQ_DEFAULT_PASS`: Default password for RabbitMQ.
  - `RABBITMQ_VHOST`: Virtual host for RabbitMQ.
  - `RABBITMQ_PORT`: Port for RabbitMQ.
  - `RABBITMQ_MANAGEMENT_PORT`: Management port for RabbitMQ.
  - `RABBITMQ_PROMETHEUS_PORT`: Prometheus metrics port for RabbitMQ.

- **Server Configuration**
  - `WEB_LISTEN`: Address and port for the web server.
  - `SERVER_ID`: Identifier for the server instance.
  - `SERVER_ADDRESS`: Public address for media URLs.
  - `MM4_ORIGINATOR_SYSTEM`: Originator system for MM4.
  - `MM4_LISTEN`: Address and port for MM4 server.
  - `SMPP_LISTEN`: Address and port for SMPP server.

### Docker Compose Configuration
The `docker-compose.yml` file defines the services and their configurations:

- **PostgreSQL Service**
  - Uses the `postgres:15-alpine` image.
  - Configured with environment variables from the `.env` file.
  - Data is persisted in `./postgres/data`.

- **RabbitMQ Service**
  - Uses the `rabbitmq:3.12-management-alpine` image.
  - Configured with environment variables from the `.env` file.
  - Configuration files are mounted from the `./rabbitmq` directory.

- **Messaging Gateway Services (msggw1 and msggw2)**
  - Use the `sms-mms-gw:latest` image.
  - Configured with environment variables from the `.env` file.
  - Expose ports for web, SMPP, and MM4 services.

- **HAProxy Service**
  - Uses the `haproxy:latest` image.
  - Configuration file is mounted from `./haproxy/haproxy.cfg`.
  - Exposes ports for HTTP, SMPP, and MM4 services.

## Usage
Run the project using the `build.sh` and `manage.sh` scripts. Configure the environment using the `sample.env` file.

## Configuration
- **RabbitMQ**: Configuration files are located in the `rabbitmq` directory.
- **HAProxy**: Configuration files are located in the `haproxy` directory.

## License
This project is licensed under the terms specified in the `LICENSE` file located in the `smpp` directory.