# lazycaddy

A lazygit-style Bun/OpenTUI TUI for inspecting the local Caddy instance.

lazycaddy treats the running Caddy Admin API config as the source of truth, then uses the discovered Caddyfile for source correlation and config context.

## Install

```bash
bun install
```

## Run

```bash
bun dev
# or
bun run src/index.ts
```

lazycaddy discovers the Admin API endpoint in this order:

1. `--admin-url` / `--admin` CLI override
2. `CADDY_ADMIN_API` / `CADDY_ADMIN_URL` environment override
3. `systemd` `caddy.service`: inspect the running process / `ExecStart`, read `--config`, and parse the Caddyfile/JSON `admin` setting
4. running `caddy` process fallback via `pgrep`
5. Caddy's default endpoint: `http://localhost:2019/config/`

Override the Admin API URL with either:

```bash
CADDY_ADMIN_API=http://localhost:2019 bun dev
bun dev --admin-url http://localhost:2019
```

## UI model

lazycaddy uses one global column and opens on **Services**.

- **Services**: source/host list from the active runtime config
- **Logs**: selected service access-log request table
- **Request detail**: selected access request details
- **Config**: selected service Caddyfile block + active runtime summary
- **System**: Caddy service status, startup config, validation, Admin API status, discovery notes, and service logs

The top line is only an orientation breadcrumb on detail pages, for example:

```txt
dev.example.org › logs
dev.example.org › logs › 500 GET /api/users
dev.example.org › config
caddy › system
```

The bottom status bar contains actions and keeps the global Admin API status dot visible on the right.

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
↓/j        scroll config overlay down
↑/k        scroll config overlay up
←/esc/h/c  close Config

System
↓/j        scroll system overlay down
↑/k        scroll system overlay up
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
