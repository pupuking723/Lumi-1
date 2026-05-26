# Lumi 前端 Google 登录接入实施文档

## 后端接口

- `POST /v1/auth/google/login`
  - 请求体支持 Google One Tap / GIS 返回的 `credential`，也支持 `id_token`、`access_token` 或 `token`。
  - 推荐请求体：

```json
{
  "credential": "<google-one-tap-credential>"
}
```

  - 返回：

```json
{
  "authenticated": true,
  "token_type": "Bearer",
  "access_token": "<goclaw-user-session-token>",
  "expires_at": "2026-06-25T00:00:00Z",
  "expires_in": 2592000,
  "user": {
    "id": "google:<sub>",
    "provider": "google",
    "provider_id": "<google-sub>",
    "name": "User Name",
    "email": "user@example.com",
    "avatar": "https://..."
  },
  "tenant": {
    "id": "<tenant-uuid>",
    "slug": "master",
    "role": "operator"
  }
}
```

- `GET /v1/auth/me`
  - Header：`Authorization: Bearer <access_token>`
  - 用于刷新页面后校验当前 GoClaw 会话是否仍然有效。

- `POST /v1/auth/logout`
  - Header：`Authorization: Bearer <access_token>`
  - 当前后端 token 是签名短期 token，logout 为前端清理本地状态提供统一接口；实际失效依赖过期时间。

## 前端改动建议

1. 使用 Google Identity Services 获取 `credential`。
2. 将 `credential` 发送给 `POST /v1/auth/google/login`。
3. 保存返回的 `access_token`，后续所有 GoClaw C 端请求统一带：

```http
Authorization: Bearer <access_token>
```

4. 不再由前端伪造或转发 `X-GoClaw-User-Id` 作为认证来源。后端会从 bearer token 注入 `user_id` 和 `tenant_id`。
5. Live WebSocket 连接前，如果仍需要 cookie 方式传递认证，可由前端把 GoClaw `access_token` 写入现有 live token cookie；后端 Live auth 会从 cookie 中读取 bearer token。
6. 页面刷新时调用 `GET /v1/auth/me` 校验登录态；401 时清理本地 token 并回到登录页。

## 需要配置的后端环境变量

```env
GOCLAW_GOOGLE_CLIENT_ID=<google-web-client-id>
GOCLAW_GOOGLE_AUTH_SESSION_SECRET=<long-random-secret>
GOCLAW_GOOGLE_AUTH_SESSION_TTL=720h
GOCLAW_GOOGLE_AUTH_USER_ID_PREFIX=google
GOCLAW_GOOGLE_AUTH_TENANT=master
GOCLAW_GOOGLE_AUTH_ROLE=operator
# GOCLAW_GOOGLE_CLIENT_IDS=<optional-extra-client-id>
# GOCLAW_GOOGLE_AUTH_ALLOWED_DOMAINS=example.com
```

`GOCLAW_GOOGLE_AUTH_SESSION_SECRET` 生产环境必须显式配置。后端可以回退到 `GOCLAW_ENCRYPTION_KEY` 或 `GOCLAW_GATEWAY_TOKEN`，但不建议生产依赖回退。

## 行为约定

- 新 Google 用户首次登录会自动写入 `tenant_users`，默认 tenant 为 `GOCLAW_GOOGLE_AUTH_TENANT`，默认角色为 `GOCLAW_GOOGLE_AUTH_ROLE`。
- 用户 ID 格式为：`<GOCLAW_GOOGLE_AUTH_USER_ID_PREFIX>:<google-sub>`，默认 `google:<sub>`。
- 如果用户已经存在于该 tenant，登录不会覆盖原有角色，只会保留既有成员关系。
- memory CRUD 暂不接入，本阶段只完成 Google 登录与认证上下文。
