#!/bin/bash
#
# E2E smoke test for docker-mac-net-connect.
# Assumes the server is already running (sudo ./docker-mac-net-connect).
#
# Usage: ./scripts/e2e-test.sh

set -euo pipefail

if ! docker info >/dev/null 2>&1; then
  echo "FAIL: Docker is not running"
  exit 1
fi

CONTAINER_NAME="docker-mac-net-connect-e2e-test"
INTERNAL_NETWORK_NAME="docker-mac-net-connect-e2e-internal"
INTERNAL_SUBNET="172.30.99.0/24"
INTERNAL_CONTAINER_IP="172.30.99.2"

cleanup() {
  echo "Cleaning up..."
  docker rm -f "$CONTAINER_NAME" 2>/dev/null || true
  docker rm -f "${CONTAINER_NAME}-internal" 2>/dev/null || true
  docker network rm "$INTERNAL_NETWORK_NAME" 2>/dev/null || true
}
trap cleanup EXIT
cleanup

# --- Test 1: Default bridge network ---

echo "=== Test 1: Default bridge network ==="

echo "Starting test container..."
docker run -d --name "$CONTAINER_NAME" nginx:alpine >/dev/null

CONTAINER_IP=$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$CONTAINER_NAME")

if [ -z "$CONTAINER_IP" ]; then
  echo "FAIL: Could not get container IP"
  exit 1
fi

echo "Container IP: $CONTAINER_IP"
echo "Waiting for nginx to start..."
sleep 2

echo "Attempting to reach container..."
if curl -sf --connect-timeout 5 "http://$CONTAINER_IP" >/dev/null; then
  echo "PASS: Successfully reached container at $CONTAINER_IP"
else
  echo "FAIL: Could not reach container at $CONTAINER_IP"
  echo ""
  echo "Is the server running? Start it with: sudo ./docker-mac-net-connect"
  exit 1
fi

# --- Test 2: Internal network ---

echo ""
echo "=== Test 2: Internal Docker network ==="

echo "Creating internal network ($INTERNAL_SUBNET)..."
docker network create --internal --subnet "$INTERNAL_SUBNET" "$INTERNAL_NETWORK_NAME" >/dev/null

echo "Starting container on internal network..."
docker run -d --name "${CONTAINER_NAME}-internal" \
  --network "$INTERNAL_NETWORK_NAME" \
  --ip "$INTERNAL_CONTAINER_IP" \
  nginx:alpine >/dev/null

echo "Container IP: $INTERNAL_CONTAINER_IP"
echo "Waiting for nginx to start..."
sleep 2

echo "Attempting to reach container on internal network..."
if curl -sf --connect-timeout 5 "http://$INTERNAL_CONTAINER_IP" >/dev/null; then
  echo "PASS: Successfully reached internal container at $INTERNAL_CONTAINER_IP"
else
  echo "FAIL: Could not reach internal container at $INTERNAL_CONTAINER_IP"
  exit 1
fi
