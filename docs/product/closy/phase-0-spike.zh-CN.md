# Closy Phase 0 技术 Spike 记录

日期：2026-05-22

## 目标

Phase 0 用于确认 Closy 可以在当前 GoClaw Runtime 上固定为单角色 Agent，并复用现有聊天、多模态工具和记忆链路。

## 已完成

- 新增 `internal/closy` seed 模块。
- 启动 gateway 时自动确保 master tenant 下存在固定 agent：`closy`。
- `closy` agent 使用 `predefined` 类型，默认开启 memory，并固定写入 Closy 的 `SOUL.md`、`IDENTITY.md`、`AGENTS.md`、`CAPABILITIES.md`、`USER_PREDEFINED.md`。
- 启动时确保 `read_image`、`read_audio`、`tts` 三个内置工具启用。
- 当默认 provider/model 存在时，为 `read_image` 和 `read_audio` 写入 provider chain，避免 OpenAI-compatible provider 不在硬编码媒体 fallback 中导致工具不可用。
- 确认现有 WebSocket chat 链路可复用：Closy 仍进入 GoClaw Agent Router / Agent Loop / Session Store，不单独重写聊天内核。

## 本地环境检查

当前本地数据库已有 provider：

```text
zai-coding | zai_coding | enabled=true
```

Phase 0 修改前，本地 `builtin_tools` 中：

```text
memory_search | enabled=true
read_audio    | enabled=false
read_image    | enabled=false
tts           | enabled=true
```

Phase 0 代码启动后会自动将 `read_audio` 和 `read_image` 打开，并使用当前默认 provider/model 作为媒体工具链配置。

## Demo 验收路径

文字/普通会话：

```bash
curl -N http://127.0.0.1:9600/v1/chat/completions \
  -H 'Authorization: Bearer <GOCLAW_GATEWAY_TOKEN>' \
  -H 'X-GoClaw-User-Id: local' \
  -H 'Content-Type: application/json' \
  -d '{
    "model": "agent:closy",
    "messages": [{"role": "user", "content": "今天不知道穿什么，你先介绍一下自己"}],
    "stream": true
  }'
```

图片/语音 Demo 继续复用现有控制台聊天附件上传链路；上传图片或音频后选择 `closy` agent 发起会话，Agent 会通过 `read_image` / `read_audio` 工具理解媒体内容。

## 风险与后续

- 已补充 `vertex` provider 与 Vertex Gemini Live WebSocket 桥，Closy 后续可以优先使用 Vertex/Gemini 承载图片理解与实时语音。
- `zai-coding` 是 OpenAI-compatible provider，实际图片/音频能力取决于上游模型 `glm-5.1` 是否支持对应输入；如果不支持，可以将 Closy 切换到 `vertex / gemini-2.5-flash`，实时语音走 `/v1/closy/live/ws`。
- 当前 Phase 0 只固定了 agent 和工具链，不包含 C 端页面；C 端 `/closy` 路由、首页、相机入口属于 Phase 1。
- 当前 `/v1/chat/completions` 的多轮兼容仍偏临时会话；C 端正式聊天应优先复用 WebSocket session 链路，或在 Closy Product API 中固定 session key。
