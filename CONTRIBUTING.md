# Contributing

## Prerequisites

- macOS (the host binary only compiles on darwin)
- Go 1.26+
- Docker Desktop
- Make

## Architecture

The project has two components:

1. **Host binary** (root `main.go`) - a macOS WireGuard server that creates a tunnel to the Docker Desktop Linux VM and manages routes for Docker networks
2. **Setup container** (`client/`) - a Linux container that runs briefly inside the Docker VM to configure the WireGuard client side

These are separate Go modules with their own `go.mod` files. The host binary builds the setup container image and runs it in Docker to configure the VM side of the tunnel.

## Development

```bash
# Build everything (Docker image + Go binary)
make build

# Run locally (builds Docker image, then runs Go binary)
# Note: requires sudo for TUN interface creation
sudo make run

# Run checks
make vet
make lint    # requires golangci-lint: brew install golangci-lint
```

## Testing

Automated testing is limited since the binary requires root permissions and direct interaction with macOS networking and Docker Desktop. CI runs build verification, `go vet`, and `golangci-lint`.

### E2E smoke test

A smoke test script is provided that verifies container connectivity. It assumes the server is already running:

```bash
# Terminal 1: start the server
make build
sudo ./docker-mac-net-connect

# Terminal 2: run the smoke test
./scripts/e2e-test.sh
```

The script starts an nginx container, attempts to reach it by IP from macOS, and reports pass/fail.

### Manual testing checklist

Before releasing, verify:

1. `make build` succeeds
2. `sudo ./docker-mac-net-connect` starts and creates the tunnel
3. `./scripts/e2e-test.sh` passes
4. Stop and restart Docker Desktop - verify the server reconnects automatically
5. For Homebrew releases: `brew upgrade chipmk/tap/docker-mac-net-connect` and `sudo brew services restart chipmk/tap/docker-mac-net-connect`

## Releases

Releases are automated via GitHub Actions. To create a release:

1. Ensure `main` is in a releasable state and all checks pass
2. Tag the commit:
   ```bash
   git tag v0.X.Y
   git push origin v0.X.Y
   ```
3. The release workflow automatically:
   - Builds and pushes the multi-arch Docker setup image to GHCR
   - Builds darwin/amd64 and darwin/arm64 binaries
   - Creates a GitHub release with the binaries and changelog
   - Updates the Homebrew formula in [chipmk/homebrew-tap](https://github.com/chipmk/homebrew-tap) (version + sha256)
