# Documentation Index

This directory keeps project documentation grouped by purpose. Keep runtime and API references in the numbered root docs, and put product-specific material under `product/`.

## Architecture

- [00 Architecture Overview](./00-architecture-overview.md)
- [25 Technical Architecture](./25-technical-architecture.zh-CN.md)
- [01 Agent Loop](./01-agent-loop.md)
- [03 Tools System](./03-tools-system.md)
- [06 Store Data Model](./06-store-data-model.md)
- [23 Multi-tenant Architecture](./23-multi-tenant-architecture.md)
- [24 Knowledge Vault](./24-knowledge-vault.md)

## API

- [18 HTTP API](./18-http-api.md)
- [19 WebSocket RPC](./19-websocket-rpc.md)
- [OpenAPI for Apifox](./openapi-apifox.yaml)

## Product

- [Closy](./product/closy/README.md)

## Operations

- [Packages and GitHub Releases](./packages-github.md)
- [TTS Provider Capabilities](./tts-provider-capabilities.md)
- [Agent Hooks](./agent-hooks.md)
- [Agent Identity Conventions](./agent-identity-conventions.md)

## Repository Layout

- `cmd/`: CLI entrypoints and gateway startup wiring.
- `internal/`: private Go packages for runtime, stores, HTTP, WebSocket, tools, memory, channels, and providers.
- `pkg/`: public Go packages.
- `ui/web/`: React management UI.
- `ui/desktop/`: desktop shell and frontend.
- `migrations/`: database migrations.
- `deploy/compose/`: Docker Compose base files, overlays, modules, and selectable options.
- `deploy/docker/`: Dockerfiles, entrypoint, and image dependency manifests.
- `deploy/options/`: deployment tuning snippets that are not part of the default compose set.
- `scripts/`: install, setup, and maintenance scripts.
- `skills/`: bundled skill packages.
- `tests/`: contract, integration, invariant, and scenario tests.

When reorganizing code directories, move one boundary at a time and update imports, Docker/compose paths, scripts, docs, and tests in the same change.
