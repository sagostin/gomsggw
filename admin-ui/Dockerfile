# Build stage
FROM node:22-alpine AS builder

WORKDIR /app

COPY package.json package-lock.json ./
RUN npm ci

COPY . .
RUN npm run build

# Serve stage: static SPA + reverse proxy for the gateway admin API
FROM nginx:1.27-alpine

# The official nginx entrypoint runs envsubst on /etc/nginx/templates/*.template
# using only the container's environment variables (GATEWAY_URL).
COPY nginx.conf /etc/nginx/templates/default.conf.template
COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 80
