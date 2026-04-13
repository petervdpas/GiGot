# GiGot

Git-backed server for [Formidable](https://github.com/petervdpas/Formidable). Provides optional server-centered storage while keeping Formidable local-first.

On first connect, clients receive a full clone of all templates and context. After that, everything works locally with incremental sync back to GiGot.

## Quick start

```bash
# Build
go build -o gigot .

# Generate default config
./gigot --init

# Run (loads gigot.json from working directory)
./gigot

# Run with explicit config
./gigot --config /path/to/gigot.json
```

## Configuration

GiGot is configured via a `gigot.json` file:

```json
{
  "server": {
    "host": "127.0.0.1",
    "port": 3417
  },
  "storage": {
    "repo_root": "./repos"
  },
  "auth": {
    "enabled": false,
    "type": "token"
  },
  "logging": {
    "level": "info"
  }
}
```

## API

| Endpoint | Description |
| --- | --- |
| `GET /` | Status page |
| `GET /api/health` | Health check |
| `GET /api/repos` | List repositories |
| `GET /api/repos/{name}` | Repository details |

## Project structure

```bash
GiGot/
├── main.go                      # Entry point
├── gigot.json                   # Server config (generated with --init)
├── cmd/gigot/root.go            # CLI bootstrap
├── internal/
│   ├── config/config.go         # JSON config loading
│   ├── server/server.go         # HTTP server and routes
│   ├── git/manager.go           # Bare repo management
│   └── auth/auth.go             # Authentication middleware
```
