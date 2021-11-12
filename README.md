# Docker Mac Net Connect

> Connect directly to Docker-for-Mac containers via IP address.

## Background

Accessing containers directly by IP (instead of port binding) can be useful and convenient.

## Problem

Docker-for-Mac works by running Linux in a VM and executing containers within that VM.

Containers are accessible by IP address from the Linux VM, but not from the macOS host.

## Solution

Create a network tunnel between your macOS host and the Docker Desktop Linux VM. The tunnel is implemented using WireGuard.

## Why WireGuard?

WireGuard is an extremely lightweight and fast VPN. It’s also built in to the Linux kernel, which means no background processes/containers are required to build the tunnel. It is the perfect tool for this application.

## Installation

This project just passed POC, so installation is manual. Homebrew package coming soon.

```bash
$ git clone https://github.com/chipmk/docker-mac-net-connect
$ cd docker-mac-net-connect && sudo go run .
```

## How does it work?

### macOS side

A lightweight customized WireGuard server runs on your macOS host and creates a virtual network interface (`utun`) that acts as the link between your Mac and the Docker Desktop Linux VM.

### Linux VM side

Since WireGuard is built into the Linux kernel, all we need to do is configure the VM with a virtual network interface that links to the macOS host. No background processes or containers are required.

How do we configure the VM? A one-time container is deployed with just enough privileges to configure the Linux host’s network interfaces (`—-cap-add=NET_ADMIN` + `-—net=host`).

The container creates the interface, configures WireGuard, then exits and is destroyed. The WireGuard interface continues working after the container is gone because it was created on the Linux host’s network namespace, not the container’s.

### Tying it together

The server on macOS monitors your Docker container networks and automatically adds their subnets to your macOS routing table (routing through the `utun` interface). Now you can connect to any container directly by it’s IP address from your macOS host. Eg.

```bash
# Run an nginx container
$ docker run --rm --name nginx -d nginx

# Get the internal IP for the container
$ docker inspect nginx --format '{{.NetworkSettings.IPAddress}}'
172.17.0.2

# Make an HTTP request directly to its IP
$ curl -I 172.17.0.2
HTTP/1.1 200 OK
Server: nginx/1.21.3
Date: Thu, 11 Nov 2021 21:00:37 GMT
Content-Type: text/html
Content-Length: 615
Last-Modified: Tue, 07 Sep 2021 15:21:03 GMT
Connection: keep-alive
ETag: "6137835f-267"
Accept-Ranges: bytes
```

## FAQ

### What happens if Docker Desktop restarts?

The server detects when the Docker daemon stops and automatically reconfigures the tunnel when it starts back up.

### Do you remove routes when Docker networks are removed?

Yes, the server watches the Docker daemon for both network creations and deletions and will add/remove routes accordingly.

### Will routes remain orphaned in the routing table if the server crashes?

No, routes are tied to the `utun` device created by the server. If the server dies, the `utun` interface will disappear along with its routes.

## License

MIT
