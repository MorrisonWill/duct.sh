# duct.sh

Expose local servers to the internet via SSH.

```
ssh -R 0:localhost:3000 duct.sh
```

## Features

- **Live request log** — Watch requests stream in real-time from your terminal.
- **Inspect traffic** — View full request and response headers and bodies.
- **Replay requests** — Re-send any captured request with a single keystroke.
- **Export to curl** — Copy any request as a curl command to your clipboard.

## Self-hosting

```bash
go build -o duct ./cmd/duct
./duct
```

### Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `SSH_HOST` | `0.0.0.0` | SSH server bind address |
| `SSH_PORT` | `2222` | SSH server port |
| `HTTP_PORT` | `8080` | HTTP proxy port |
| `BASE_DOMAIN` | `localhost` | Domain for tunnel URLs |
| `HOST_KEY_PATH` | `./host_key` | Path to SSH host key |
| `USE_HTTPS` | `false` | Use HTTPS in generated URLs |
