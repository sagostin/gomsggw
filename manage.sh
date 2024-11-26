#!/bin/bash

# Script: manage.sh
# Description: Adds carriers, clients, and numbers to msggw1 and reloads both msggw1 and msggw2 containers.
# Usage: ./manage.sh

set -e  # Exit immediately if a command exits with a non-zero status

# Configuration
API_KEY=""  # Replace with your actual API key
CONTAINER1="msggw1"
CONTAINER2="msggw2"
BASE_URL="http://localhost:3000"

# JSON Payloads
CLIENT_DATA='{
"username": "clientuser",
"password": "clientpassword",
"address": "123 Main St",
"name": "Client Name",
"log_privacy": true
}'

NUMBER_DATA='{
"number": "1234567890",
"carrier": "twilio"
}'

CARRIER_DATA='{
"name": "twilio",
"type": "twilio",
"username": "twilio_user",
"password": "twilio_pass"
}'

# Function to execute curl inside a container
exec_curl() {
local container=$1
local method=$2
local url=$3
local data=$4

docker exec "$container" curl -s -X "$method" "$url" \
-u "apikey:$API_KEY" \
-H "Content-Type: application/json" \
-d "$data"
}

# Function to execute reload on a container
reload_container() {
local container=$1

echo "Reloading data on $container..."

# Reload clients
docker exec "$container" curl -s -X POST "$BASE_URL/clients/reload" \
-u "apikey:$API_KEY" \
-H "Content-Type: application/json" \
-d '{}'

# Reload carriers
docker exec "$container" curl -s -X POST "$BASE_URL/carriers/reload" \
-u "apikey:$API_KEY" \
-H "Content-Type: application/json" \
-d '{}'

echo "Reload completed on $container."
}

# Check if containers are running
for container in "$CONTAINER1" "$CONTAINER2"; do
if ! docker ps --format '{{.Names}}' | grep -w "$container" > /dev/null; then
echo "Error: Container '$container' is not running."
exit 1
fi
done

# Add a new carrier to msggw1
echo "Adding a new carrier to $CONTAINER1..."
ADD_CARRIER_RESPONSE=$(exec_curl "$CONTAINER1" "POST" "$BASE_URL/carriers" "$CARRIER_DATA")

# Check if the carrier was added successfully
if echo "$ADD_CARRIER_RESPONSE" | jq -e '.id' > /dev/null; then
echo "Carrier added successfully."
else
echo "Error adding carrier:"
echo "$ADD_CARRIER_RESPONSE"
exit 1
fi

# Add a new client to msggw1
echo "Adding a new client to $CONTAINER1..."
ADD_CLIENT_RESPONSE=$(exec_curl "$CONTAINER1" "POST" "$BASE_URL/clients" "$CLIENT_DATA")

# Extract the client ID using jq
CLIENT_ID=$(echo "$ADD_CLIENT_RESPONSE" | jq -r '.id')

if [[ -z "$CLIENT_ID" || "$CLIENT_ID" == "null" ]]; then
echo "Error: Failed to retrieve client ID from response."
echo "Response: $ADD_CLIENT_RESPONSE"
exit 1
fi

echo "Client added with ID: $CLIENT_ID"

# Add a new number to the client
echo "Adding a new number to client ID $CLIENT_ID on $CONTAINER1..."
# Inject ClientID into NUMBER_DATA
NUMBER_DATA_WITH_ID=$(echo "$NUMBER_DATA" | jq --arg cid "$CLIENT_ID" '.ClientID = ($cid | tonumber)')
exec_curl "$CONTAINER1" "POST" "$BASE_URL/clients/$CLIENT_ID/numbers" "$NUMBER_DATA_WITH_ID"

echo "Number added to client ID $CLIENT_ID."

# Reload data on both msggw1 and msggw2
for container in "$CONTAINER1" "$CONTAINER2"; do
reload_container "$container"
done

echo "All operations completed successfully."