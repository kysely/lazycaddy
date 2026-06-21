# lazycaddy

A fast terminal dashboard for inspecting and troubleshooting your local Caddy server.

lazycaddy gives you a live, service-oriented view of the Caddy instance that is actually running. It reads the active Caddy Admin API config, discovers your sites, follows their access logs, shows request details, checks upstream health, and correlates runtime config back to your Caddyfile when available.

## What it does

- **Browse Caddy services** discovered from the active runtime config
- **Inspect access logs** per site/source with status, method, path, duration, and upstream details
- **Drill into requests** to see headers, timing, TLS, client, upstream, and error context
- **Jump to config** for the selected service, including Caddyfile source context when discoverable
- **Check system state**: Admin API reachability, Caddy service status, startup config, validation, discovery notes, and service logs
- **Filter noisy logs** by slow requests, warnings/errors, and time window
- **Run locally with no agent**: lazycaddy talks to your existing Caddy instance and exits cleanly

## Install

### Homebrew

```bash
brew tap kysely/tap
brew install lazycaddy
```

### Linux/macOS install script

```bash
curl -fsSL https://raw.githubusercontent.com/kysely/lazycaddy/main/install.sh | sh
```

On macOS, the install script delegates to Homebrew when `brew` is available. Use `--no-brew` to force a direct GitHub Release install.

Install a specific version or directory:

```bash
curl -fsSL https://raw.githubusercontent.com/kysely/lazycaddy/main/install.sh | sh -s -- --version v0.1.1 --bindir ~/.local/bin
```

### Go

```bash
go install github.com/kysely/lazycaddy/cmd/lazycaddy@latest
```

### Debian/Ubuntu package

Download the `.deb` for your release and architecture from GitHub Releases, then install it:

```bash
curl -LO https://github.com/kysely/lazycaddy/releases/download/v0.1.1/lazycaddy_0.1.1_linux_amd64.deb
sudo apt install ./lazycaddy_0.1.1_linux_amd64.deb
```

Replace `v0.1.1`, `0.1.1`, and `amd64` with the release and architecture you want.

## Quick start

```bash
lazycaddy
```

If your Caddy Admin API is not at the default endpoint, pass it explicitly:

```bash
lazycaddy --admin-url http://localhost:2019
```

or:

```bash
CADDY_ADMIN_API=http://localhost:2019 lazycaddy
```

## Requirements

- A local Caddy instance with the Admin API enabled, or an explicit Admin API URL
- Access to the configured access logs if you want request tables
- Linux/systemd for full service discovery and `journalctl` integration
- Go 1.22+ only if building from source

## UI overview

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

## Admin API discovery

lazycaddy treats the running Caddy Admin API config as the source of truth. It discovers the Admin API endpoint in this order:

1. `--admin-url` / `--admin` CLI override
2. `CADDY_ADMIN_API` / `CADDY_ADMIN_URL` environment override
3. `systemd` `caddy.service`: inspect the running process / `ExecStart`, read `--config`, and parse the Caddyfile/JSON `admin` setting
4. running `caddy` process fallback via `pgrep`
5. Caddy's default endpoint: `http://localhost:2019`

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

## Theme detection

lazycaddy uses terminal background detection for automatic light/dark colors. If your terminal or tmux does not report its background correctly, force a theme with:

```bash
lazycaddy --theme light
lazycaddy --theme dark
LAZYCADDY_THEME=light lazycaddy
```

Accepted values are `auto`, `light`, and `dark`.

## Build from source

Run locally from a checkout:

```bash
go run ./cmd/lazycaddy
```

Build a local binary:

```bash
go build ./cmd/lazycaddy
./lazycaddy
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

## Development

Project layout:

```txt
cmd/lazycaddy/       CLI entrypoint
internal/app/        UI-independent application state and caches
internal/caddy/      Admin API, discovery, config extraction, Caddyfile correlation, health checks
internal/logs/       access/service log parsing and readers
internal/ui/         Bubble Tea model, views, key handling, refresh commands
```

## Release

Releases are built with GoReleaser from Git tags:

```bash
git tag v0.1.1
git push origin v0.1.1
```

The release workflow builds:

- Linux and macOS tarballs for amd64 and arm64
- Debian `.deb` packages for apt-based installs
- checksums
- a Homebrew formula in `kysely/homebrew-tap`

For Homebrew publishing, create a GitHub repository named `kysely/homebrew-tap` and add a `HOMEBREW_TAP_GITHUB_TOKEN` repository secret with permission to push to that tap.
