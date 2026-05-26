# Mochi v1 任务实施文档

本文档根据 `/Users/ding/projectSrc/lumi_Pro/task.md` 整理，用于把 Mochi v1 的任务清单转化为可执行、可验收、可持续更新的工程实施方案。

## 1. 目标

Mochi v1 要完成一个面向 Gen Z 女性的穿搭搭子型单角色 Agent。核心不是做一个通用聊天助手，而是让用户在文字、图片、Live 语音三种输入方式下，都能获得稳定的 Mochi 角色体验：有审美判断、能看图、能承接轻情绪、能记住用户偏好，并能围绕 OOTD 生成可分享内容。

## 2. 实施原则

| 原则 | 说明 |
| --- | --- |
| 单角色优先 | 用户侧固定为 Mochi，不暴露多 Agent 复杂度。后端可继续使用 `agent:closy` 作为兼容 key，前端展示统一为 Mochi。 |
| 多模态统一会话 | 文字、图片、语音都进入同一会话上下文，避免图片、语音和文字被拆成孤立任务。 |
| 角色一致性优先 | 所有回复先满足 Mochi 角色边界，再满足任务完成度。 |
| 记忆可控 | 用户偏好、雷区、常见场景等要沉淀为可解释、可修改的记忆，不依赖黑盒摘要。 |
| 先闭环后优化 | 先完成“上传/输入 -> 理解 -> 回复 -> 记忆 -> 分享”的可用闭环，再优化体验和指标。 |

## 3. 阶段划分

| 阶段 | 目标 | 对应任务 | 交付物 | 状态 |
| --- | --- | --- | --- | --- |
| Phase 1 | 固定 Mochi 角色与回复策略 | Mochi 角色定义、对话提示词与策略开发、核心场景回复能力建设、轻情绪承接与非任务陪伴 | 角色 Prompt、话题边界、回复策略、核心场景模板、测试用例 | 后端已完成，前端未做 |
| Phase 2 | 打通多模态输入与上下文 | 多模态对话基础能力搭建、多模态上下文管理 | 文字/图片/Live 语音链路、附件上下文协议、会话关联规则 | 后端已完成，C 端 Live 接入已完成 |
| Phase 3 | 建立记忆与个性化 | 用户记忆与个性化偏爱感 | 结构化记忆字段、记忆抽取规则、记忆引用策略、用户可见记忆页数据协议 | 后端已完成，前端未做 |
| Phase 4 | OOTD 点评闭环 | OOTD 穿搭点评功能实现 | OOTD 提交流程、点评结构、图片分析 Prompt、结果数据结构 | 后端已完成，前端未做 |
| Phase 5 | 分享卡与增长回流 | Mochi OOTD 可分享卡片生成 | 9:16 分享卡、保存/分享能力、短链/二维码/CTA 回流 | 后端已完成，前端未做 |
| Phase 6 | 测试、上线与观察 | 角色一致性与安全边界测试、上线发布与发布后观察 | 测试集、验收报告、埋点看板、上线检查表 | 待执行 |

## 4. 详细实施计划

### 2026-05-24：Phase 1 后端改造进展

- 已更新 Mochi seed 版本为 `2026-05-24.1`。
- 已在后端 seed 中新增 `RESPONSE_STRATEGY.md`，用于注入强相关、弱相关、无关、风险类问题的回复路由策略。
- 已在后端 seed 中新增 `CORE_SCENARIOS.md`，用于注入 OOTD / 风格方向 / 社交呈现 / 买前决策 / 轻情绪 / 越界问题的核心场景回复模板。
- 已扩展 Mochi `Description()`，把 Phase 1 回复策略写入 agent description。
- 已新增后端 `Phase1PromptRegressionCases()`，沉淀 Phase 1 角色一致性与安全边界测试契约；测试用例不注入运行时 Prompt，避免污染正常会话。
- 已补充 seed 测试，覆盖新增 context files、缺失文件补齐、重复 seed 无副作用、用户修改文件不覆盖、seed 版本升级可控。
- 本次只完成后端改造，没有修改 C 端前端页面。

### 2026-05-24：Phase 2 后端改造进展

- 已为 `POST /v1/chat/completions` 增加 `session_id`，C 端传入同一个 `session_id` 时会进入稳定的 `agent:{agent}:cchat:direct:{user}-{session}` 会话，不再每轮强制新建临时 HTTP 会话。
- 已为 chat 请求增加后端可识别的 `scenario` 与 `input_context` 字段，可携带 `source`、`mode`、`voice_transcript`、`note`、`refers_to_media_id(s)` 等多模态上下文。
- 已扩展 `attachments` 元数据，支持 `caption`、`source`、`role`，后端会把附件信息整理进 `<mochi_multimodal_context>`，帮助模型理解图片、文字、语音转写之间的指代关系。
- 已修复 agent pipeline 的会话持久化：当前轮输入经过媒体富化后，会把 `MediaRefs` 和富化后的媒体标签一起写回 session history。后续文字或 Live 语音轮次可以继续读取最近图片/音频/文档引用。
- 已调整 Gemini Live 的 `session_id` 处理：显式传入 `session_id` 时与 C 端 chat 使用同一 `cchat` 会话命名空间；未传时保留临时 Live 会话行为。
- 已让 Gemini Live 的 `systemInstruction` 在注入历史对话时包含最近消息的 `media_refs` 摘要，用于语音轮次承接前文图片/附件语境。
- 已保留原有无 `session_id` 的 HTTP chat 兼容路径，旧请求仍按原来的临时 session 方式执行。
- 本次只完成后端改造，没有修改 C 端前端页面。

### 2026-05-24：Phase 2 Live 图片事件后端进展

- 已在 `GET /v1/closy/live/gemini/ws` 支持新的客户端事件 `media`。
- 前端仍先调用 `POST /v1/chat/attachments/upload` 上传图片并拿到 `media_id`，然后在 Live WS 内发送 `media_id`；后端会读取 `media_assets`，将本地图片转为 Gemini Live `clientContent` inline image part。
- Live `media` 事件会把图片引用写入同一个 `session_id` 的 session history，保存的是 `MediaRef` 与上下文文本，不保存 base64。
- Live `media` 事件默认 `turn_complete=false`，适合“边说边补图”；前端可传 `turn_complete=true`，让 Gemini 收图后立刻基于图片回应。
- 当前仅支持 `image/*`，不支持在 Live WS 内直接注入视频、PDF 或任意文件。
- 本次只完成后端改造，没有修改 C 端前端页面。

### 2026-05-25：Phase 2 Live 前后端接入进展

- 已将 `claude-codex` Live 前端中的 PCM 采集/播放思路移植到 `lumi`：前端不再用 `MediaRecorder` 发送 `audio/webm`，改为通过 Web Audio 采集单声道音频、降采样为 `audio/pcm;rate=16000` 后发送给 Gemini Live。
- 已补齐 Live 事件处理：支持 `live_ready`、`live_setup_complete`、`live_transcript`、`live_audio`、`message`、`done`、`error`，其中 Gemini 返回的 PCM 音频会在浏览器侧按 `mime_type` sample rate 排队播放。
- 已强化麦克风生命周期：停止 Live、切回文本、页面卸载或重连时，会关闭 WebSocket、停止 MediaStream tracks、断开 ScriptProcessor/Source、关闭 AudioContext，并中断未播放完的语音队列。
- 已在 C 端上传图片成功后，如果 Live WS 正在连接中，自动发送 `media` 事件，把 `media_id` 注入当前 Live 会话；开始 Live 时也会把已准备好的图片上下文同步给后端。
- 已让后端 Gemini Live 兼容前端控制帧 `start`、`client_trace`、`done`，并过滤明显噪声转写，避免控制帧导致 Live WS 提前结束。
- 已同步 `lumi` 环境变量：`NEXT_PUBLIC_LUMI_LIVE_WS_URL=ws://127.0.0.1:9600/v1/closy/live/gemini/ws`，前端直连 GoClaw Live WebSocket，本地默认后端地址改为 `http://127.0.0.1:9600`。
- 验证：`go test ./internal/http ./cmd`、`pnpm lint`、`pnpm test -- --runInBand`、`pnpm build` 均已通过。

前端后续需要发送的 Live WS 事件格式：

```json
{
  "type": "media",
  "media_id": "6c1fe3cd-39a3-4a6d-913e-09315a1030d8",
  "caption": "你看这张新图，帮我判断外套要不要换",
  "source": "camera",
  "role": "current_outfit",
  "turn_complete": false
}
```

前端接收成功确认事件：

```json
{
  "type": "media_received",
  "session_id": "agent:closy:cchat:direct:user-a-fit",
  "role": "user",
  "content": "你看这张新图，帮我判断外套要不要换",
  "data": {
    "media_id": "6c1fe3cd-39a3-4a6d-913e-09315a1030d8",
    "mime_type": "image/jpeg",
    "filename": "outfit.jpg",
    "turn_complete": false
  }
}
```

### 2026-05-24：Phase 3 后端改造进展

- 已新增 `closy_profiles` 与 `closy_style_preferences` 领域记忆表，schema 版本升至 `59`。
- 已新增 `ClosyMemoryStore` 与 Postgres 实现，用于保存用户风格画像、偏好、雷区、颜色、版型、场景、信心线索和近期选择。
- 已在 `POST /v1/chat/completions` 注入 `<MOCHI_MEMORY>`，让 Mochi 在文字/图文聊天中自然引用用户偏好，避免机械列举。
- 已在 Gemini Live `systemInstruction` 注入同一套 Mochi 记忆，Live 语音会话可以和 C 端 chat 共享个性化上下文。
- 已实现轻量 post-turn 记忆抽取：从用户显式表达中提取“喜欢/不喜欢/雷区/常穿/场景/近期选择/状态线索”，并带来源 session、证据和置信度落库。
- 已新增用户可见记忆页后端协议：
  - `GET /v1/closy/profile?user_id=<USER_ID>`：查询 Mochi 用户画像与偏好列表。
  - `PUT /v1/closy/profile`：手动更新用户风格画像摘要。
  - `POST /v1/closy/profile/preferences`：手动新增或更新单条偏好/雷区。
- 已保留用户手动修改优先级：相同偏好重复写入不会降级高置信度证据，后续 prompt 读取最新结构化记忆。
- 本次只完成后端改造，没有修改 C 端前端页面。

### 2026-05-24：Phase 4 后端改造进展

- 已新增 `closy_ootd_reviews` 表，schema 版本升至 `60`，用于保存 OOTD 图片点评结果。
- 已新增 `ClosyOOTDStore` 与 Postgres 实现，支持创建、单条查询、列表查询。
- 已新增 Mochi OOTD 结构化 Prompt 与解析器，输出字段固定为：
  - `overall_judgement`
  - `style_label`
  - `highlight`
  - `main_issue`
  - `suggestion`
  - `mochi_line`
  - `safety_notes`
- 已新增 OOTD 安全处理：结果解析后会检查并清理身材/外貌攻击类表达，点评只允许围绕衣服、颜色、版型、比例、场景适配与呈现方式。
- 已新增 OOTD 后端接口：
  - `POST /v1/closy/ootd/reviews`：提交 `media_id`，调用 Mochi 多模态 Agent 生成结构化 OOTD 点评并落库。
  - `GET /v1/closy/ootd/reviews/{id}`：查询单条 OOTD 点评结果。
  - `GET /v1/closy/ootd/reviews?user_id=<USER_ID>&limit=30`：查询用户 OOTD 点评列表。
- 已复用 Phase 2 上传链路：前端仍先调用 `POST /v1/chat/attachments/upload` 获得 `media_id`，再提交 OOTD 点评。
- 已复用 Phase 3 记忆：生成 OOTD 点评时会注入 `<MOCHI_MEMORY>`，点评成功后会把本次 OOTD 风格选择写入 `recent_choice` 记忆。
- 本次只完成后端改造，没有修改 C 端前端页面。

### 2026-05-24：Phase 5 后端改造进展

- 已新增 `closy_share_cards` 表，schema 版本升至 `61`，用于保存 OOTD 分享卡 payload、短链 slug、CTA、过期时间和访问次数。
- 已新增 `ClosyShareCardStore` 与 Postgres 实现，支持创建、单条查询、列表查询、按 slug 公开查询和访问计数累加。
- 已新增分享卡 payload 构建逻辑，后端输出前端可直接渲染的 9:16 数据结构：
  - `aspect_ratio: "9:16"`
  - `brand: "Mochi"`
  - `ootd_review_id`
  - `media_id`
  - `overall_judgement`
  - `style_label`
  - `highlight`
  - `main_issue`
  - `suggestion`
  - `mochi_line`
  - `cta`
  - `share_url`
  - `qr_value`
- 已新增分享卡后端接口：
  - `POST /v1/closy/share-cards`：基于 `ootd_review_id` 创建分享卡 payload，并生成短链。
  - `GET /v1/closy/share-cards/{id}`：查询单张分享卡记录。
  - `GET /v1/closy/share-cards?user_id=<USER_ID>&limit=30`：查询用户分享卡列表。
  - `GET /s/closy/{slug}`：公开短链读取分享卡 JSON payload，并累加 `view_count`。
- 后端暂不做图片合成渲染；前端 Phase 5 需要基于 payload 用 Canvas/DOM 生成 9:16 图片，或后续再增加服务端渲染。
- 本次只完成后端改造，没有修改 C 端前端页面。

### Phase 1-5：前端改造清单

以下为 C 端前端 `lumi` 需要补齐的改造内容。后端能力已经按 Phase 1-5 提供；前端改造时应保持用户侧单角色体验，统一展示为 Mochi，不暴露 `agent:closy`、provider、session key 等内部概念。

#### Phase 1 前端：固定 Mochi 角色体验

| 改造项 | 前端需要做什么 | 依赖接口/数据 | 验收标准 |
| --- | --- | --- | --- |
| 品牌与角色统一 | 将 C 端所有用户可见 Agent 名称、头像、欢迎语、空状态、输入占位文案统一为 Mochi | 本地静态配置即可；请求仍使用 `model: "agent:closy"` | 用户第一屏明确知道正在和 Mochi 对话；页面不出现 Closy / GoClaw / agent:closy 等内部命名 |
| 首屏欢迎语 | 增加符合 Mochi 人设的初始欢迎消息，强调穿搭、审美、自我表达、出门状态 | 静态文案 | 欢迎语不是通用助手口吻；不承诺医疗、心理咨询、人生决策等越界能力 |
| 快捷入口 | 增加 4-6 个场景入口，例如“今天穿这套行吗”“帮我挑 A/B”“这件要不要买”“我想换个风格”“出门前快速看一下” | 点击后填充输入框或直接发送 chat | 快捷入口都落在 Mochi 可聊范围内，能触发核心穿搭场景 |
| 回复展示 | 聊天气泡支持 Mochi 的自然结构化回复，例如判断、理由、建议、追问 | `POST /v1/chat/completions` SSE | 长回复排版可读，不强行拆成生硬表格；移动端不溢出 |
| 越界体验 | 对无关/风险类问题展示自然的 Mochi 回复，不弹技术错误 | 后端 Prompt 已处理，前端只需正常展示 | 越界问题能温和转回穿搭/表达/状态，不展示内部错误 |

#### Phase 2 前端：文字、图片、Live 语音和上下文

| 改造项 | 前端需要做什么 | 依赖接口/数据 | 验收标准 |
| --- | --- | --- | --- |
| 稳定会话 ID | 每个聊天线程生成并持久化 `session_id`，后续文字、图片、语音、OOTD 共用同一个会话 ID | `POST /v1/chat/completions` 请求体 `session_id` | 同一聊天线程上下文连续；刷新页面后继续同一会话 |
| SSE 聊天渲染 | 接入 `stream: true` 的 SSE，按 chunk 增量渲染 assistant 消息，支持失败重试和停止生成 | `POST /v1/chat/completions` | 回复不等整段结束才出现；错误态可重试；停止后不会重复写消息 |
| 图片上传入口 | 在聊天输入区支持相册选择、拍照、拖拽或移动端文件选择；上传后展示缩略图和删除按钮 | `POST /v1/chat/attachments/upload` | 上传成功后拿到 `media_id`；用户能在发送前删除图片；上传失败有明确反馈 |
| 图文消息发送 | 发送 chat 时把图片作为 `attachments` 传入，并补充 `caption`、`source`、`role` | `attachments: [{ media_id, caption, source, role }]` | 模型能看到图片；用户后续说“这张/刚才那张”可以关联上下文 |
| 多模态上下文参数 | 按场景传 `scenario` 和 `input_context`，例如图片点评、OOTD、Live 语音转文字、引用上一张图 | `scenario`、`input_context.source/mode/voice_transcript/refers_to_media_id` | 图片后补文字、语音后补图片、图片后语音说明都能被后端关联 |
| Live WS 连接 | 实现 Gemini Live WebSocket 连接、鉴权、ready/error/done/message/audio 事件处理 | `GET /v1/closy/live/gemini/ws`，token 放 header | Live 页面能连接、断开、重连；不会把 token 放 URL |
| 麦克风生命周期 | 输入模式从 Live 切到 Text 或页面离开时，立即停止 MediaStream tracks、AudioContext、录音循环和 WS 音频发送 | 浏览器媒体 API | 快速切换模式时浏览器地址栏/权限图标不再残留麦克风采集 |
| Live 图片事件 | Live 中用户拍新图后，先 upload 拿 `media_id`，再通过 WS 发 `media` 事件 | WS `media` 事件：`media_id/caption/source/role/turn_complete` | 用户边语音边发新图，Mochi 能基于新图继续对话 |

#### Phase 3 前端：记忆与个性化

| 改造项 | 前端需要做什么 | 依赖接口/数据 | 验收标准 |
| --- | --- | --- | --- |
| 记忆页入口 | 在个人页或设置页增加“我的风格记忆”入口 | `GET /v1/closy/profile?user_id=<USER_ID>` | 用户能看到 Mochi 记住的偏好、雷区、颜色、版型、场景、近期选择 |
| 风格画像展示 | 把 profile 摘要和 preferences 分组展示为用户可理解的标签/列表 | `profile`、`preferences` | 不直接展示数据库字段名；空状态说明 Mochi 会在对话中慢慢记住 |
| 手动编辑画像 | 支持用户编辑风格摘要、状态线索等画像内容 | `PUT /v1/closy/profile` | 用户修改后刷新仍保留；后续聊天能反映修改后的记忆 |
| 偏好/雷区管理 | 支持新增、编辑或删除式隐藏偏好项；最小实现可先支持新增/覆盖 | `POST /v1/closy/profile/preferences` | 用户可以明确告诉 Mochi “不要再推荐 X” 或 “我喜欢 Y” |
| 轻提示 | 当本轮对话明显沉淀记忆时，可显示轻提示，例如“已记住：你偏好低饱和色” | 后端目前不返回专门事件，可先基于请求成功或后续 profile refresh 实现 | 提示不打断聊天，不频繁刷屏 |
| 隐私说明 | 记忆页提供简短说明：这些记忆用于让 Mochi 更懂你的穿搭偏好，可修改 | 静态文案 | 用户知道记忆用途，且能主动修正 |

#### Phase 4 前端：OOTD 点评闭环

| 改造项 | 前端需要做什么 | 依赖接口/数据 | 验收标准 |
| --- | --- | --- | --- |
| OOTD 入口 | 在首页或聊天输入区增加“今日 OOTD”入口 | 前端路由/入口 | 用户 3 步内完成：拍/选图 -> 预览 -> 提交 |
| OOTD 上传 | 复用 Phase 2 上传能力，提交前必须拿到 `media_id` | `POST /v1/chat/attachments/upload` | OOTD 图片上传成功率 ≥ 95%；失败可重试 |
| OOTD 提交 | 提交 `media_id`、`session_id`、`occasion`、`note` 到专用接口 | `POST /v1/closy/ootd/reviews` | 返回结构化点评并落库；失败时保留用户图片和文字，不丢输入 |
| 点评结果页 | 展示今日判断、风格标签、亮点、主要问题、修改建议、Mochi 一句话 | OOTD response `result/review` | 至少展示亮点、问题、建议和 Mochi line；字段缺失时有降级展示 |
| 继续追问 | OOTD 结果页提供“继续问 Mochi”入口，把当前 review/session 带回聊天 | `session_id`、可选 `input_context.refers_to_media_id` | 用户能围绕刚才 OOTD 继续追问，不需要重新上传图片 |
| 安全展示 | 不展示 `safety_notes` 给普通用户；仅在调试环境可看 | OOTD `safety_notes` | 用户侧不会看到内部安全备注 |
| 历史记录 | 提供最近 OOTD 点评列表入口 | `GET /v1/closy/ootd/reviews?user_id=<USER_ID>&limit=30` | 用户能回看最近点评，并进入详情 |

#### Phase 5 前端：分享卡与回流

| 改造项 | 前端需要做什么 | 依赖接口/数据 | 验收标准 |
| --- | --- | --- | --- |
| 生成分享卡 | 在 OOTD 结果页增加“生成分享卡”按钮，调用后端创建 payload | `POST /v1/closy/share-cards`，传 `ootd_review_id` | 成功返回分享卡记录、`payload`、`share_url` |
| 9:16 预览 | 根据 payload 渲染 9:16 卡片，包含用户 OOTD 图、Mochi 标识、今日判断、点评、建议、CTA、二维码/短链 | `payload.aspect_ratio/media_id/overall_judgement/highlight/main_issue/suggestion/mochi_line/qr_value` | 移动端不裁切、不溢出；文字过长时优雅截断或缩放 |
| 图片素材读取 | 根据 `media_id` 展示 OOTD 图；如缺少直出图片 URL，需要接入已有媒体读取/签名 URL 能力或补后端读图 URL | `media_id`，现有媒体服务/后续 media URL | 分享卡里能看到用户提交的 OOTD 图片 |
| 保存图片 | 用 Canvas、DOM 截图或平台能力生成本地图片 | 前端渲染能力 | 用户可保存 9:16 图片到本地，相册/下载成功率 ≥ 95% |
| 系统分享 | 移动端优先使用 Web Share API；桌面端提供复制链接/下载图片 | `share_url` | 可复制短链；支持系统分享时可直接唤起分享面板 |
| 分享落地页 | 实现 `/s/closy/{slug}` 对应的前端落地页，读取公开 payload 并展示分享卡 | `GET /s/closy/{slug}` | 未登录用户也能看到分享卡；过期/撤销卡显示温和空状态 |
| 回流 CTA | 分享卡和落地页 CTA 文案统一为“让 Mochi 也看看你”等，点击进入 Mochi 对话或 OOTD 上传入口 | payload `cta` | 新用户能从分享页进入上传/聊天路径 |
| 分享历史 | 在个人页或 OOTD 历史中展示已生成分享卡 | `GET /v1/closy/share-cards?user_id=<USER_ID>&limit=30` | 用户能重新打开、复制、保存已生成卡片 |

### Phase 1：Mochi 角色与回复策略

| 模块 | 实施内容 | 后端改造 | 前端改造 | 验收标准 |
| --- | --- | --- | --- | --- |
| Mochi 角色定义 | 明确人格、语气边界、可聊范围、多模态范围，沉淀到 `IDENTITY.md`、`SOUL.md`、`AGENTS.md` 等 context files | 更新 Mochi seed，保证 context file 可迁移、可补齐、不覆盖用户修改 | 展示名、头像、欢迎语、空状态文案统一为 Mochi | 人格关键词完整；可聊/不可聊范围明确；不做万能陪聊、心理咨询、人生导师、医疗/法律/投资建议 |
| 对话提示词与策略 | 设计强相关、弱相关、无关、风险类问题的回复策略 | 在系统 Prompt 中加入分类策略；必要时增加后处理/安全边界检查 | 输入入口不区分人格策略，统一走 Mochi 回复 | 穿搭/风格问题充分回答；弱相关状态能承接；无关问题不展开成长篇；风险类问题不越界 |
| 核心场景回复 | 建立“判断、理由、建议、追问”的回复结构 | 增加核心场景 Prompt 模板：穿搭、风格、自我表达、社交呈现、出门状态 | 聊天页展示结构化但自然的 Mochi 回复 | 穿搭点评至少包含 1 个亮点、1 个问题、1 条建议；回复体现 Mochi 审美和角色感 |
| 轻情绪承接 | 处理“没状态”“怕太用力”“不想出门”等轻情绪 | Prompt 明确“接住状态 -> 转回审美/表达/出门状态 -> 给轻建议”的边界 | 输入推荐和快捷入口可覆盖轻情绪场景 | 先接住状态再给建议；不跳功能推荐；不滑向心理咨询 |

### Phase 2：多模态输入与上下文

| 模块 | 实施内容 | 后端改造 | 前端改造 | 验收标准 |
| --- | --- | --- | --- | --- |
| 文字聊天 | 保持 C 端 chat 接口稳定，支持流式回复 | 继续使用 `/v1/chat/completions`，model 使用 `agent:closy`；保持 SSE 输出 | 聊天页稳定渲染流式消息，支持中断和错误重试 | 用户可正常发送文字消息 |
| 图片上传/拍摄 | 支持上传、拍摄、预览、发送 | 使用 C 端专用上传接口返回 `media_id`；聊天请求支持 `attachments` | 图片选择、相机拍摄、预览、删除、发送状态 | 图片上传成功率 ≥ 95% |
| Live 语音 | 支持实时语音输入、转写、语音回复、中断 | 使用独立 Gemini Live WS：`/v1/closy/live/gemini/ws`；通过 header 传 token；使用 `GOCLAW_GEMINI_LIVE_*` 配置 | Live 页面接入 WS、麦克风生命周期管理、输入模式切换时停止采集 | 语音转写成功率 ≥ 90%；语音播放成功率 ≥ 90%；切换输入方式不残留采集 |
| 多模态上下文 | 图片 + 文字、图片 + Live 语音、文字 + 语音联合理解 | 会话层保存最近媒体引用、转写文本和用户补充；组装 prompt 时关联当前附件 | 发送时携带当前图片/语音上下文，避免前端丢失引用 | 图片后补文字能指向当前图片；图片后语音说明能结合图片回应；上下文关联准确率 ≥ 85% |

### Phase 3：用户记忆与个性化

| 模块 | 实施内容 | 后端改造 | 前端改造 | 验收标准 |
| --- | --- | --- | --- | --- |
| 基础记忆字段 | 建立风格偏好、穿搭雷区、常见场景、自我表达倾向、历史选择 | 新增或复用领域记忆存储；定义字段 schema、来源、置信度、更新时间 | 记忆页展示用户可理解的风格画像 | 记忆字段可被创建、更新、查询 |
| 记忆抽取 | 从对话、图片点评、OOTD 结果中抽取偏好 | Post-turn 任务抽取结构化记忆；避免低置信度覆盖高置信度 | 无需强感知，必要时显示“我记住了”轻提示 | 用户第二次对话能自然引用历史偏好；用户修改偏好后后续回复能同步 |
| 记忆引用 | 回复中自然体现“我记得你” | Prompt 注入用户核心偏好和雷区；引用时避免机械列举 | 回复中不做生硬标签展示 | 不重复询问已知信息；记忆引用准确率 ≥ 80%；体现偏爱感但不无脑迎合 |

### Phase 4：OOTD 穿搭点评

| 模块 | 实施内容 | 后端改造 | 前端改造 | 验收标准 |
| --- | --- | --- | --- | --- |
| OOTD 提交 | 用户上传或拍摄今日 OOTD 图片 | 复用上传接口和附件聊天；新增 OOTD 场景参数或专用 endpoint | 3 步内完成：拍/选图 -> 预览 -> 提交 | 用户可在 3 步内提交；上传成功率 ≥ 95% |
| OOTD 分析 | 输出今日判断、整体风格、亮点、最大问题、修改建议、Mochi 一句话点评 | 定义 OOTD 输出 schema；结合图片多模态模型和 Mochi Prompt | 展示点评结构，保留聊天自然感 | 完整输出 OOTD 点评；至少包含亮点、问题、修改建议、角色化金句 |
| 安全边界 | 避免外貌攻击、身材羞辱、人格攻击 | Prompt 和测试用例覆盖禁区；必要时增加安全审查 | 前端不展示攻击性字段或错误中间态 | 不攻击用户外貌、身材、人格 |

### Phase 5：OOTD 分享卡

| 模块 | 实施内容 | 后端改造 | 前端改造 | 验收标准 |
| --- | --- | --- | --- | --- |
| 分享卡数据 | 将 OOTD 点评结果转为分享卡数据 | 保存 OOTD 结果和分享卡 payload；生成短链/回流参数 | 卡片预览页读取 payload | 分享卡内容与 OOTD 点评一致 |
| 9:16 卡片生成 | 卡片包含用户 OOTD 图、Mochi 标识、今日判断、点评、修改建议、CTA、二维码/短链 | 可先前端 canvas/DOM 截图生成，后续再服务端渲染 | 适配移动端保存和分享 | 用户可一键生成分享卡；可保存本地，成功率 ≥ 95%；移动端不裁切、不溢出 |
| 分享回流 | 通过短链、二维码和 CTA 回到 Mochi | 新增分享落地页或短链解析；记录分享来源 | 分享页明确“让 Mochi 也看看你”入口 | 卡片包含回流入口；可调用系统分享能力 |

### Phase 6：测试、上线与观察

| 模块 | 实施内容 | 后端改造 | 前端改造 | 验收标准 |
| --- | --- | --- | --- | --- |
| 角色一致性测试 | 覆盖文字、图片、实时语音、多模态组合、轻情绪、无关、风险、OOTD 分享 | 建立测试样例与自动化回归脚本；记录模型输出 | 可用测试账号完成端到端操作 | 核心测试用例不少于 30 条；通过率 ≥ 90%；严重越界 0 次 |
| 安全边界测试 | 验证不输出身材羞辱、外貌攻击、人格攻击 | 加入安全类 prompt 测试和人工复核 | 错误提示温和，不暴露内部错误 | 人设明显崩坏 ≤ 5%；无关问题正确转向率 ≥ 90% |
| 上线观察 | 观察对话质量、OOTD 分享率、留存、多模态使用率 | 埋点记录文字使用率、图片上传率、语音使用率、分享卡生成率、分享回流率 | 前端埋点覆盖关键路径 | 用户可完成文字、图片、Live 语音、OOTD 分享链路；数据可正常回收 |

## 5. 数据与接口设计

### 5.1 聊天请求

| 场景 | 接口 | 说明 |
| --- | --- | --- |
| 文字聊天 | `POST /v1/chat/completions` | OpenAI-compatible chat；model 使用 `agent:closy` |
| 图片上传 | `POST /v1/chat/attachments/upload` | 返回 `media_id`，供聊天请求 `attachments` 使用 |
| 图文聊天 | `POST /v1/chat/completions` | 请求体包含 `attachments: [{ "media_id": "..." }]` |
| Live 语音 | `GET /v1/closy/live/gemini/ws` | WebSocket，token 通过 `Authorization: Bearer <token>` 传递 |
| OOTD 点评 | `POST /v1/closy/ootd/reviews` | 请求体包含 `media_id`，返回结构化 OOTD 点评并落库 |
| OOTD 分享卡 | `POST /v1/closy/share-cards` | 请求体包含 `ootd_review_id`，返回 9:16 分享卡 payload 与短链 |

### 5.2 领域记忆建议字段

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `style_preferences` | array | 用户喜欢的风格，例如干净、松弛、甜酷、极简 |
| `style_avoidances` | array | 用户明确不喜欢或不适合的方向 |
| `color_preferences` | array | 常穿/偏爱的颜色 |
| `silhouette_preferences` | array | 版型、廓形、比例偏好 |
| `occasion_patterns` | array | 高频场景，例如通勤、约会、见朋友、面试 |
| `confidence_cues` | array | 和自我表达、社交呈现相关的轻状态线索 |
| `recent_choices` | array | 最近做出的穿搭/购买/风格选择 |
| `updated_at` | timestamp | 最近更新时间 |

### 5.3 OOTD 结果结构

| 字段 | 说明 |
| --- | --- |
| `overall_judgement` | 今日整体判断 |
| `style_label` | 整体风格标签 |
| `highlight` | 至少一个亮点 |
| `main_issue` | 最大问题，必须表达温和且可行动 |
| `suggestion` | 具体修改建议 |
| `mochi_line` | Mochi 一句话点评 |
| `safety_notes` | 内部安全检查备注，不直接展示给用户 |

## 6. 验收清单

| 验收域 | 必过条件 |
| --- | --- |
| 角色 | Mochi 人格稳定，有审美、有判断、有边界，不变成通用助手 |
| 文字 | 普通聊天和核心穿搭问题可稳定流式回复 |
| 图片 | 上传/拍摄/预览/发送完整可用，模型能看到图片内容 |
| 语音 | Live 语音能转写、回复、播放，中断和模式切换不会残留录音 |
| 上下文 | 图片、文字、语音在同一会话内能正确关联 |
| 记忆 | 能创建、更新、引用用户偏好，不重复询问已知信息 |
| OOTD | 点评完整、有判断、有建议、不攻击外貌身材人格 |
| 分享 | 9:16 卡片可生成、保存、分享，并包含回流入口 |
| 安全 | 严重越界 0 次，人设明显崩坏 ≤ 5% |
| 数据 | 关键埋点可回收，能看文字/图片/语音/OOTD/分享回流指标 |

## 7. 推荐执行顺序

| 顺序 | 工作项 | 依赖 |
| --- | --- | --- |
| 1 | 固化 Mochi context files 与 seed 版本 | 无 |
| 2 | 完成 Prompt 策略与核心场景模板 | 1 |
| 3 | 稳定文字 + 图片聊天链路 | 1、2 |
| 4 | 接入 Live 语音链路与麦克风生命周期控制 | 1、2 |
| 5 | 设计并落库用户领域记忆 | 3、4 |
| 6 | 实现 OOTD 点评 schema 与前端流程 | 3、5 |
| 7 | 实现 9:16 分享卡与回流链路 | 6 |
| 8 | 建立 30+ 测试用例并完成上线验收 | 1-7 |

## 8. 文档维护规则

每完成一个阶段，需要同步更新本文档：

1. 将阶段状态从“待执行”改为“进行中”或“已完成”。
2. 在对应 Phase 下补充实际落地的接口、文件、数据表和测试结果。
3. 如果验收标准变化，必须同步更新 `task.md` 和本文档。
4. 如果实现路径偏离本文档，需要记录偏离原因和新方案。
