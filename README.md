# lazycaddy

A fast Go TUI for inspecting and troubleshooting the local Caddy instance.

lazycaddy treats the running Caddy Admin API config as the source of truth, then uses the discovered Caddyfile for source correlation and config context when available.

## Requirements

- Go 1.22+
- Local Caddy instance with Admin API enabled, or an explicit Admin API URL
- Linux/systemd for full service discovery and `journalctl` integration

## Install

Install the latest tagged release with Go:

```bash
go install github.com/kysely/lazycaddy/cmd/lazycaddy@latest
```

Or download a prebuilt binary from GitHub Releases once releases are published.

## Build and run

Run locally from a checkout:

```bash
go run ./cmd/lazycaddy
```

Build a local binary:

```bash
go build ./cmd/lazycaddy
./lazycaddy
```

Run tests:

```bash
go test ./...
```

Make shortcuts are also available. The simplest local run command is:

```bash
make
```

Other useful commands:

```bash
make run
make build
make test
make vet
make check
```

## Release

Releases are built with GoReleaser from Git tags. To publish a release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow builds Linux and macOS binaries for amd64 and arm64 and uploads checksums.

## Admin API discovery

lazycaddy discovers the Admin API endpoint in this order:

1. `--admin-url` / `--admin` CLI override
2. `CADDY_ADMIN_API` / `CADDY_ADMIN_URL` environment override
3. `systemd` `caddy.service`: inspect the running process / `ExecStart`, read `--config`, and parse the Caddyfile/JSON `admin` setting
4. running `caddy` process fallback via `pgrep`
5. Caddy's default endpoint: `http://localhost:2019`

Override the Admin API URL with either:

```bash
CADDY_ADMIN_API=http://localhost:2019 go run ./cmd/lazycaddy
go run ./cmd/lazycaddy --admin-url http://localhost:2019
```

## Theme detection

lazycaddy uses terminal background detection for automatic light/dark colors. If your terminal or tmux does not report its background correctly, force a theme with:

```bash
go run ./cmd/lazycaddy --theme light
go run ./cmd/lazycaddy --theme dark
LAZYCADDY_THEME=light go run ./cmd/lazycaddy
```

Accepted values are `auto`, `light`, and `dark`.

## UI model

lazycaddy opens on **Services** and provides:

- **Services**: source/host list from the active runtime config
- **Logs**: selected service access-log request table
- **Request detail**: selected access request details
- **Config**: selected service Caddyfile block + active runtime summary
- **System**: Caddy service status, startup config, validation, Admin API status, discovery notes, and service logs
- **Help**: keybinding reference

## Keys

```txt
Global
?          show/return from help
S          open/return from System
r          refresh active config, logs, and upstream health
q          quit
←/esc/h    go back from Logs/System/Config/request detail/help

Services
↓/j        next service
↑/k        previous service
→/enter/l  open selected service Logs
c          open Config for selected service

Logs
↓/j        next request
↑/k        previous request
→/enter/l  open selected request detail
←/esc/h    back to Services
s          toggle slow request filter
e          toggle error/warning request filter
d/w/a      time window: day/week/all
c          open Config for selected service

Request detail
↓/j        scroll detail down
↑/k        scroll detail up
←/esc/h    back to Logs
c          open Config for selected service

Config
↓/j        scroll config down
↑/k        scroll config up
←/esc/h/c  close Config

System
↓/j        scroll system down
↑/k        scroll system up
e          toggle error/warning service-log filter
v          validate discovered Caddy config
←/esc/h/S  return
```

## Access logs

Access logs are shown per source/host. Request counts and request tables require access logging to be configured and readable for that source.

For file-based Caddyfile access logs, add a block like this inside a site block:

```caddyfile
log {
  output file /var/log/caddy/example.com.access.log
  format console
}
```

Console/stdout/stderr access logs are also parsed from `caddy.service` logs when configured in the active Caddy config.

## Development

Project layout:

```txt
cmd/lazycaddy/       CLI entrypoint
internal/app/        UI-independent application state and caches
internal/caddy/      Admin API, discovery, config extraction, Caddyfile correlation, health checks
internal/logs/       access/service log parsing and readers
internal/ui/         Bubble Tea model, views, key handling, refresh commands
```
