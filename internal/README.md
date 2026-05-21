# Internal Packages

`internal/` contains private Go packages used by the GoClaw binary. Keep package moves conservative because import paths under this tree are referenced by CLI startup code, HTTP handlers, WebSocket methods, tests, and Docker builds.

## Main Boundaries

- `agent/`: Agent loop, media handling, prompt/context assembly, memory flush, run orchestration.
- `pipeline/`: Stage-based Agent execution pipeline.
- `gateway/`: WebSocket server, RPC router, and gateway-level connection handling.
- `gateway/methods/`: WebSocket RPC method implementations.
- `http/`: REST/SSE/OpenAI-compatible HTTP handlers.
- `providers/`: LLM provider registry and provider implementations.
- `tools/`: Built-in Agent tools, media tools, memory tools, TTS tools, filesystem tools.
- `store/`: Store interfaces and backend-specific implementations.
- `memory/`, `knowledgegraph/`, `vault/`: Long-term memory, graph extraction, and knowledge vault features.
- `channels/`: External channel integrations.
- `audio/`, `tts/`, `media/`: Audio, speech, and media support.
- `config/`, `permissions/`, `security/`, `crypto/`: Configuration, authorization, and security utilities.
- `tracing/`, `eventbus/`, `bus/`: Observability and event delivery.

## Move Rule

Move only one boundary at a time. Any package move must update imports, tests, docs, generated OpenAPI examples when relevant, and Docker/script references in the same change.
