# Compose Options

`deploy/compose/options/` contains symlinks to optional Docker Compose overlays in `deploy/compose/`. The numeric prefixes define a stable ordering when options are selected by setup scripts.

Current options:

- `11-postgres.yml`
- `12-selfservice.yml`
- `13-upgrade.yml`
- `14-browser.yml`
- `15-otel.yml`
- `16-redis.yml`
- `17-sandbox.yml`
- `18-tailscale.yml`

The symlink targets intentionally stay beside the compose files so Makefile and setup scripts can build a stable `COMPOSE_FILE`.
