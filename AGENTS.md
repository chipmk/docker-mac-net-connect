# Agents

## What this repo is

docker-mac-net-connect enables direct IP connectivity from macOS to Docker containers without port binding. It works by creating a WireGuard tunnel between the macOS host and the Docker Desktop Linux VM, then automatically managing routes for Docker networks.

## Architecture

Two Go binaries, one on each side of the tunnel:

1. **Host binary** (`main.go`) - runs on macOS as root
   - Creates a `utun` interface with a WireGuard server
   - Generates ephemeral WireGuard key pairs per run
   - Watches Docker events to add/remove routes for container subnets
   - Reconnects automatically if Docker Desktop restarts

2. **Setup container** (`client/main.go`) - ephemeral container inside Docker Desktop VM
   - Runs with `NET_ADMIN` + host networking
   - Creates a WireGuard client interface (`chip0`)
   - Configures iptables NAT/forwarding rules
   - Self-destructs after setup; the WireGuard interface persists in the VM

The tunnel peers use a fixed `10.33.33.0/24` subnet (`10.33.33.1` = host, `10.33.33.2` = VM).

## Project layout

```
main.go                  # Host binary entry point (darwin-only build tag)
version/version.go       # Build-time version + setup image vars (set via ldflags)
networkmanager/           # macOS routing table management (ifconfig, route)
client/                   # Setup container (separate Go module)
  main.go                # VM-side WireGuard + iptables setup
  Dockerfile             # Multi-stage build (golang -> debian)
scripts/e2e-test.sh      # Smoke test - spins up nginx, curls its container IP
```

## Build and run

Requires: Go 1.26+, Docker Desktop, macOS, root privileges.

```
make run            # Build docker image + run host binary with sudo
make build          # Build both docker image and Go binary
make build-go       # Compile macOS binary only
make build-docker   # Build setup container image only
make vet            # go vet ./...
make lint           # golangci-lint run ./...
make e2e            # Run smoke test (scripts/e2e-test.sh)
```

The host binary has two modules - root `go.mod` and `client/go.mod`. They are independent.

## Commit style

- One-line commit messages
- Conventional-ish prefixes: `fix:`, `chore:`, `feat:`
- No Claude attribution

## Code style

- Go with minimal abstractions - the codebase is small and intentionally flat
- No unit tests - testing is integration/e2e only due to the low-level OS/networking nature
- darwin build tag on host code (`//go:build darwin`)
- The client module targets Linux (runs inside Docker VM)
