# Command Entrypoints

`cmd/` contains Cobra commands and gateway startup wiring.

## Keep Here

- CLI command definitions.
- Gateway bootstrap and dependency wiring.
- Setup, migration, backup, restore, provider, channel, skill, and tenant commands.
- Thin adapters that connect configuration to `internal/` packages.

## Keep Out

- Reusable runtime logic should live under `internal/`.
- Store-specific query logic should live under `internal/store/*`.
- Frontend or product UI logic should live under `ui/`.

When a command file grows large, prefer extracting reusable behavior into `internal/` while keeping the command surface in `cmd/`.
