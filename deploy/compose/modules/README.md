# Compose Modules

`deploy/compose/modules/` is the generated/selected compose module set used by `scripts/prepare-compose.sh`.

The base service is linked as:

- `00-goclaw.yml` -> `../docker-compose.yml`

Additional optional modules are selected from `deploy/compose/options/`.
