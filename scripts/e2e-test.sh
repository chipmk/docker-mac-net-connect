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

cleanup() {
  echo "Cleaning up..."
  docker rm -f "$CONTAINER_NAME" 2>/dev/null || true
}
trap cleanup EXIT

# Remove pre-existing container from a previous run
docker rm -f "$CONTAINER_NAME" 2>/dev/null || true

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
