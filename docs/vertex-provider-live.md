# Vertex Provider and Live WebSocket

This document covers the local setup for GoClaw's native `vertex` provider and the Vertex Gemini Live WebSocket bridge.

## What Is Implemented

- Chat provider type: `vertex`
- Text/vision endpoint: Vertex AI Gemini `generateContent`
- Streaming endpoint: Vertex AI Gemini `streamGenerateContent`
- Live WebSocket bridge:
  - `GET /v1/vertex/live/ws`
  - `GET /v1/closy/live/ws`

The `/v1/closy/live/ws` route is an alias for the C-side Mochi/Closy realtime voice entry.

## Authentication

Use one of these methods.

### Option A: Access Token

```bash
export GOCLAW_VERTEX_ACCESS_TOKEN="$(gcloud auth print-access-token)"
```

For DB-configured `vertex` providers, the provider `api_key` field can also hold a Google OAuth access token.

### Option B: Service Account File

```bash
export GOOGLE_APPLICATION_CREDENTIALS="/absolute/path/to/service-account.json"
```

Equivalent env names are also supported:

```bash
export GOCLAW_VERTEX_SERVICE_ACCOUNT_FILE="/absolute/path/to/service-account.json"
export VERTEX_SERVICE_ACCOUNT_FILE="/absolute/path/to/service-account.json"
```

### Option C: Service Account JSON

```bash
export GOOGLE_APPLICATION_CREDENTIALS_JSON='{"type":"service_account",...}'
```

Equivalent env names are also supported:

```bash
export GOCLAW_VERTEX_SERVICE_ACCOUNT_JSON='{"type":"service_account",...}'
export VERTEX_SERVICE_ACCOUNT_JSON='{"type":"service_account",...}'
```

The credential must have access to Vertex AI in the configured project. The Vertex AI API must be enabled.

## Shared Vertex Configuration

```bash
export GOCLAW_VERTEX_PROJECT_ID="your-gcp-project-id"
export GOCLAW_VERTEX_LOCATION="us-central1"
```

Alternative env names:

```bash
export VERTEX_PROJECT_ID="your-gcp-project-id"
export GOOGLE_CLOUD_PROJECT="your-gcp-project-id"
export GCLOUD_PROJECT="your-gcp-project-id"

export VERTEX_LOCATION="us-central1"
export GOOGLE_CLOUD_LOCATION="us-central1"
export CLOUD_ML_REGION="us-central1"
```

If the model is passed as a full Vertex resource, `project_id` can be omitted:

```text
projects/<project>/locations/<location>/publishers/google/models/<model>
```

## DB Provider Configuration

Provider type:

```text
vertex
```

Recommended model:

```text
gemini-2.5-flash
```

Provider settings JSON:

```json
{
  "project_id": "your-gcp-project-id",
  "location": "us-central1",
  "model": "gemini-2.5-flash"
}
```

Notes:

- `api_base` is usually empty. GoClaw derives `https://<location>-aiplatform.googleapis.com/v1`.
- Use `api_base` only for tests, proxies, or non-standard endpoints.
- `api_key` may contain a temporary OAuth access token, but service account env vars are preferred for long-running local or server deployments.

## Local Gateway Startup

Postgres-backed local startup:

```bash
export GOCLAW_POSTGRES_DSN="postgres://goclaw:goclaw@localhost:5432/goclaw?sslmode=disable"
export GOCLAW_GATEWAY_TOKEN="dev-token"
export GOCLAW_ALLOWED_ORIGINS="http://localhost:3000,http://127.0.0.1:3000,http://localhost:5175,http://127.0.0.1:5175"
export GOCLAW_VERTEX_PROJECT_ID="your-gcp-project-id"
export GOCLAW_VERTEX_LOCATION="us-central1"
export GOOGLE_APPLICATION_CREDENTIALS="/absolute/path/to/service-account.json"

go run . gateway --http-addr 0.0.0.0:9600
```

For temporary gcloud auth:

```bash
export GOCLAW_VERTEX_ACCESS_TOKEN="$(gcloud auth print-access-token)"
go run . gateway --http-addr 0.0.0.0:9600
```

## Chat Completions Smoke Test

```bash
curl -sS http://127.0.0.1:9600/v1/chat/completions \
  -H "Authorization: Bearer $GOCLAW_GATEWAY_TOKEN" \
  -H "Content-Type: application/json" \
  -H "X-GoClaw-User-Id: local-user" \
  -d '{
    "model": "agent:closy",
    "messages": [
      { "role": "user", "content": "你好，给我一句简短的穿搭建议" }
    ],
    "stream": false
  }'
```

To test the provider directly, assign an agent to provider `vertex` and model `gemini-2.5-flash`, or call a provider verification path from the console UI.

## Live WebSocket Configuration

Existing Vertex Live bridge environment variables for `/v1/vertex/live/ws` and `/v1/closy/live/ws`:

```bash
export GOCLAW_VERTEX_LIVE_MODEL="gemini-live-2.5-flash-native-audio"
export GOCLAW_VERTEX_LIVE_INPUT_MIME="audio/pcm;rate=16000"
export GOCLAW_VERTEX_LIVE_INPUT_TRANSCRIPTION="true"
export GOCLAW_VERTEX_LIVE_OUTPUT_TRANSCRIPTION="true"
export GOCLAW_VERTEX_LIVE_TIMEOUT="10m"
```

Independent Gemini Live bridge environment variables for `/v1/gemini/live/ws` and `/v1/closy/live/gemini/ws`:

```bash
export GOCLAW_GEMINI_LIVE_AGENT="closy"
export GOCLAW_GEMINI_LIVE_MODEL="gemini-live-2.5-flash-preview-native-audio-09-2025"
export GOCLAW_GEMINI_LIVE_PROJECT_ID="$GOCLAW_VERTEX_PROJECT_ID"
export GOCLAW_GEMINI_LIVE_LOCATION="us-central1"
export GOCLAW_GEMINI_LIVE_API_VERSION="v1beta1"
export GOCLAW_GEMINI_LIVE_INPUT_MIME="audio/pcm;rate=16000"
export GOCLAW_GEMINI_LIVE_OUTPUT_MIME=""
export GOCLAW_GEMINI_LIVE_INPUT_TRANSCRIPTION="true"
export GOCLAW_GEMINI_LIVE_OUTPUT_TRANSCRIPTION="true"
export GOCLAW_GEMINI_LIVE_VAD_START_SENSITIVITY="START_SENSITIVITY_HIGH"
export GOCLAW_GEMINI_LIVE_VAD_END_SENSITIVITY="END_SENSITIVITY_HIGH"
export GOCLAW_GEMINI_LIVE_VAD_PREFIX_PADDING="150ms"
export GOCLAW_GEMINI_LIVE_VAD_SILENCE_DURATION="500ms"
export GOCLAW_GEMINI_LIVE_TIMEOUT="10m"
```

Optional query parameters override env values:

```text
agent
model
project_id
location
base_url
api_version
input_mime
output_mime
input_transcription
output_transcription
vad_start_sensitivity
vad_end_sensitivity
vad_prefix_padding
vad_silence_duration
timeout
```

For non-browser clients, prefer the `Authorization` header instead of putting the gateway token in the URL:

```text
Authorization: Bearer <GOCLAW_GATEWAY_TOKEN>
```

Independent Gemini Live route:

```text
ws://127.0.0.1:9600/v1/closy/live/gemini/ws?project_id=<project>&location=us-central1
```

## Live WebSocket Events

Client audio event:

```json
{
  "type": "audio",
  "mime_type": "audio/pcm;rate=16000",
  "data": "<base64-pcm-audio>"
}
```

Client text event:

```json
{
  "type": "text",
  "content": "帮我判断今天这套穿搭的状态"
}
```

Client stream end:

```json
{ "type": "audio_end" }
```

Server ready event:

```json
{
  "type": "live_ready",
  "data": {
    "model": "gemini-live-2.5-flash-native-audio",
    "input_audio_mime_type": "audio/pcm;rate=16000"
  }
}
```

Server transcript event:

```json
{
  "type": "live_transcript",
  "role": "assistant",
  "content": "..."
}
```

Server audio event:

```json
{
  "type": "live_audio",
  "role": "assistant",
  "data": {
    "mime_type": "audio/pcm",
    "data": "<base64-audio>"
  }
}
```

## Common Errors

### `vertex project ID is required`

Set one of:

```bash
export GOCLAW_VERTEX_PROJECT_ID="your-gcp-project-id"
```

or provider settings:

```json
{ "project_id": "your-gcp-project-id", "location": "us-central1" }
```

or use a full model resource:

```text
projects/<project>/locations/<location>/publishers/google/models/gemini-2.5-flash
```

### `vertex access token is required`

Set an access token or service account credentials:

```bash
export GOCLAW_VERTEX_ACCESS_TOKEN="$(gcloud auth print-access-token)"
```

or:

```bash
export GOOGLE_APPLICATION_CREDENTIALS="/absolute/path/to/service-account.json"
```

### `403 Forbidden`

Check:

- Vertex AI API is enabled.
- The service account or gcloud user has permission for the configured project.
- The region supports the selected model.

### `404 Not Found`

Check:

- `project_id`
- `location`
- model ID
- whether the model should be passed as a full Vertex resource path
