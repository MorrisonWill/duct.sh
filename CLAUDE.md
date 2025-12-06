# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Duct is an SSH tunneling service (similar to ngrok) written in Go. It exposes local servers to the internet via SSH remote port forwarding. Users connect with `ssh -R 0:localhost:PORT duct.sh` and receive a public URL to access their local service.

## Build & Run Commands

```bash
# Build the binary
go build -o duct ./cmd/duct

# Run the server
./duct

# Run with hot reload (if air is installed)
air
```

## Configuration

All configuration is via environment variables (see `internal/config/config.go`):

- `SSH_HOST` - SSH bind address (default: 0.0.0.0)
- `SSH_PORT` - SSH port (default: 2222)
- `HTTP_PORT` - HTTP proxy port (default: 8080)
- `BASE_DOMAIN` - Domain for tunnel URLs (default: localhost)
- `HOST_KEY_PATH` - Path to SSH host key (default: ./host_key)
- `USE_HTTPS` - Enable HTTPS URLs (default: false)

## Architecture

The server runs two concurrent services:

1. **SSH Server** (`internal/sshd/`) - Built on charmbracelet/wish, handles SSH connections and remote port forwarding requests. When a client connects with `-R`, it creates a tunnel and launches an interactive TUI session.

2. **HTTP Proxy** (`internal/proxy/`) - Receives HTTP requests on `subdomain.BASE_DOMAIN`, looks up the corresponding tunnel, and forwards traffic through the SSH connection to the client's local server.

### Key Components

- **Tunnel Manager** (`internal/tunnel/manager.go`) - Tracks active tunnels by subdomain and ID. Opens `forwarded-tcpip` channels to send HTTP traffic back through SSH connections.

- **Event Hub** (`internal/events/hub.go`) - Pub/sub system that delivers request events to tunnel TUI sessions. Each tunnel subscribes to receive live request logs.

- **TUI** (`internal/tui/model.go`) - Bubbletea-based terminal UI shown to SSH clients. Displays the public URL and live request stream.

### Request Flow

1. Client connects: `ssh -R 0:localhost:3000 duct.sh`
2. SSH server handles `tcpip-forward` request, creates tunnel with unique subdomain
3. TUI session starts, displays public URL (e.g., `https://abc12345.duct.sh`)
4. HTTP request arrives at `abc12345.duct.sh`
5. Proxy extracts subdomain, looks up tunnel, opens SSH channel
6. Request is forwarded through SSH to client's local port
7. Response flows back, event is published to TUI
