# API 接口

Moon Bridge 对外暴露 **4 种入站协议** 端点（OpenAI Responses / OpenAI Chat / Anthropic Messages / Google Gemini）、模型列表端点和可选的管理 API。入站协议通过 URL 路径自动识别，所有入站→出站组合均可工作。

## 基础信息

- **Base URL**：`http://127.0.0.1:38440`（默认）
- **认证**：通过 `auth_token` 配置启用 Bearer Token
- **内容类型**：`application/json`

## Web Console

生产二进制会在 `/console/` 提供嵌入式 Web Console。Console 使用同源 RPC：

- `/api/v1/config/graph`：配置图 API，Console 通过它读取资源图并实时保存字段编辑。
- `/api/v1/logs/recent`、`/api/v1/logs/stream`：日志 API，Console Logs 页面使用。
- `/v1/models`、`/v1/responses`：RPC smoke test 面板使用的 OpenAI-compatible 端点。

管理 API 只有在配置启用 `persistence.active_provider` 且配置存储初始化成功时可用；否则 `/api/v1/*` 可能返回 404 或 `store_unavailable`。Console 的常规配置流程不暴露配置文件路径或 YAML 编辑器；用户通过 UI 字段直接修改配置图资源。

## 核心端点

### POST /v1/responses (OpenAI Responses)

OpenAI Responses API 兼容的主端点。使用 `openai.responses.create()` SDK 调用。

**关键请求字段**：

| 字段 | 类型 | 说明 |
|-------|------|-------------|
| `model` | string | 模型名或路由别名 |
| `input` | string/array | 输入文本或消息数组 |
| `include` | array | 控制返回内容（如推理内容） |
| `tools` | array | 工具定义列表 |
| `tool_choice` | object | 工具选择策略 |
| `max_output_tokens` | number | 最大输出 token 数 |
| `temperature` | number | 采样温度 |
| `stream` | boolean | 是否启用流式响应 |

**响应格式**：

```json
{
  "id": "resp_xxx",
  "status": "completed",
  "model": "deepseek-v4-flash(deepseek)",
  "output": [
    {"type": "message", "role": "assistant", "content": [{"type": "output_text", "text": "Hello!"}]}
  ],
  "usage": {
    "input_tokens": 10,
    "output_tokens": 42,
    "total_tokens": 52
  }
}
```

**流式响应**（`stream: true`）使用 Server-Sent Events 格式：

```
event: response.output_item.added
data: {"type": "reasoning", ...}
event: response.output_text.delta
data: {"delta": "Hello"}
event: response.completed
data: {"response": {...}}
```

### POST /v1/chat/completions (OpenAI Chat)

OpenAI Chat Completions API 兼容端点。使用 `openai.chat.completions.create()` SDK 调用。支持 `tool_calls`、`tool_choice`、流式等标准 Chat 能力。

### POST /v1/messages (Anthropic Messages)

Anthropic Messages API 兼容端点。使用 `anthropic.messages.create()` SDK 调用。支持 `system` 指令、`tool_use`/`tool_result` 内容块、流式事件。

### POST /v1beta/models/{model}:generateContent (Google Gemini)

Google Generative AI (Gemini) API 兼容端点。使用 `google.genai.models.generate_content()` SDK 调用。支持 `functionCall`/`functionResponse` 工具调用、流式变体 `:streamGenerateContent`。

### GET /v1/models

列出所有可用模型列表。

响应为 Moon Bridge 目录形态：

```json
{
  "models": [
    {"slug": "moonbridge", "name": "Moon Bridge", "provider": "route", "model": "claude-sonnet"}
  ]
}
```

## 管理 API

当 `persistence.active_provider` 启用时，管理 API 在 `/api/v1/` 下可用。当前 Console 主要使用配置图 API；旧的资源端点仍保留给脚本或兼容调用。

| 端点 | 方法 | 功能 |
|------|------|------|
| `/api/v1/config/graph` | GET | 获取当前配置图、资源 schema、校验状态和运行状态 |
| `/api/v1/config/graph` | PATCH | 按字段实时修改配置图 |
| `/api/v1/config/graph/validate` | POST | 验证一组配置图修改，但不提交 |
| `/api/v1/config/resources/{kind}` | POST | 创建配置资源 |
| `/api/v1/config/resources/{kind}/{id}` | DELETE | 删除配置资源 |
| `/api/v1/logs/recent` | GET | 获取最近后端日志 |
| `/api/v1/logs/stream` | GET | 以 Server-Sent Events 形式跟随后端日志 |
| `/api/v1/status` | GET | 运行态状态 |
| `/api/v1/status/providers` | GET | Provider 运行状态 |
| `/api/v1/providers` | GET | 分页列出 Provider |
| `/api/v1/providers/{key}` | GET/PUT/PATCH/DELETE | 查看、创建、更新、删除 Provider |
| `/api/v1/providers/{key}/offers` | POST | 新增 Provider offer |
| `/api/v1/providers/{key}/offers/{model}` | PATCH/DELETE | 更新或删除 Provider offer |
| `/api/v1/providers/{key}/test` | POST | 测试 Provider 连通性 |
| `/api/v1/models` | GET | 分页列出模型定义 |
| `/api/v1/models/{slug}` | GET/PUT/DELETE | 查看、创建、删除模型定义 |
| `/api/v1/routes` | GET | 分页列出路由别名 |
| `/api/v1/routes/{alias}` | GET/PUT/DELETE | 查看、创建、删除路由别名 |
| `/api/v1/defaults` | GET/PUT | 查看和 stage 默认模型设置 |
| `/api/v1/web-search` | GET/PUT | 查看和 stage Web Search 设置 |
| `/api/v1/extensions` | GET | 列出扩展名 |
| `/api/v1/extensions/{name}` | GET/PUT | 查看和 stage 扩展 JSON 配置 |
| `/api/v1/config/effective` | GET | 获取 masked 有效配置 |
| `/api/v1/config/export` | GET | 导出 YAML 配置 |
| `/api/v1/config/import` | POST | 导入 YAML 并 stage 变更 |
| `/api/v1/config/validate` | POST | 校验 YAML |
| `/api/v1/changes` | GET | 列出待应用变更 |
| `/api/v1/changes/apply` | POST | 应用待变更并 reload |
| `/api/v1/changes/discard` | POST | 丢弃待变更 |
| `/api/v1/sessions` | GET | 列出活跃会话 |
| `/api/v1/stats` | GET | 用量统计 |
| `/api/v1/stats/summary` | GET | 用量统计摘要 |
| `/api/v1/logs` | GET | 日志查询 |
| `/api/v1/version` | GET | 版本信息 |

### 配置图 API

`GET /api/v1/config/graph` 返回当前可编辑资源图：

```json
{
  "revision": "rev-...",
  "resources": [
    {
      "kind": "defaults",
      "id": "main",
      "label": "Defaults",
      "value": {"model": "moonbridge", "max_tokens": 65536},
      "schema": {"fields": [{"path": "model", "type": "string", "label": "Model"}]},
      "status": "saved",
      "runtimeImpact": "normal",
      "hotReloadable": true
    }
  ],
  "validation": {"valid": true},
  "runtime": {"status": "ok"},
  "capabilities": {"autosave": true, "logs": true}
}
```

资源类型包括：`mode`、`trace`、`log`、`server`、`defaults`、`model`、`provider`、`provider_offer`、`route`、`web_search`、`cache`、`persistence`、`extension`、`proxy`。

`PATCH /api/v1/config/graph` 请求体：

```json
{
  "baseRevision": "rev-...",
  "changes": [
    {"kind": "defaults", "id": "main", "field": "model", "value": "claude-sonnet"}
  ]
}
```

返回结果：

| `result` | HTTP 状态 | 含义 |
|----------|-----------|------|
| `committed` | 200 | 已提交并重新构建配置图 |
| `restartRequired` | 200 | 已提交，但变更需要重启后完全生效 |
| `revisionConflict` | 409 | `baseRevision` 不是当前 revision |
| `validationRejected` | 400 | 候选配置未通过静态校验 |
| `runtimeRejected` | 400 | 候选配置通过静态校验，但运行时 reload 拒绝；可能包含 `rollbackValue` |
| `draftRejected` | 400 | 请求草稿无法应用到配置结构 |

`POST /api/v1/config/graph/validate` 使用同样的请求体和响应结构，但不会提交变更；成功时返回候选 `graph` 且 `revision` 保持不变。

创建资源：

```http
POST /api/v1/config/resources/{kind}
Content-Type: application/json
```

```json
{"baseRevision": "rev-...", "id": "new-id", "value": {}}
```

删除资源可在 query 或 JSON body 中传入 base revision：

```http
DELETE /api/v1/config/resources/{kind}/{id}?baseRevision=rev-...
```

### 日志 API

`GET /api/v1/logs/recent?limit=200` 返回最近日志数组；`limit` 默认为 100，最大 1000。每条记录保留后端原始日志文本：

```json
[
  {
    "timestamp": "2026-06-07T00:00:00Z",
    "level": "INFO",
    "message": "server started",
    "attrs": {"addr": "127.0.0.1:38440"},
    "raw": "time=... level=INFO msg=server-started"
  }
]
```

`GET /api/v1/logs/stream` 返回 `text/event-stream`，每个事件为一条 JSON 日志：

```text
data: {"timestamp":"...","level":"INFO","message":"...","raw":"..."}
```

导出带 secrets 的配置时必须使用：

```http
GET /api/v1/config/export?include_secrets=true
X-Confirm-Secrets: true
```

`POST /api/v1/config/validate` 请求字段名为 `config`，内容是 YAML 字符串；校验失败时可能返回 `200` 且 `{"valid":false,"errors":[...]}`。

## 错误处理

错误响应格式：

```json
{"error": {"message": "...", "code": "error_code"}}
```

| HTTP 状态码 | 场景 |
|--------------|------|
| 400 | 请求参数错误 |
| 401 | 认证失败 |
| 404 | 模型/端点不存在 |
| 502 | 上游 Provider 错误 |

## 与 Codex CLI 集成

在 Codex 配置中指向 Moon Bridge 地址：

```toml
[openai]
base_url = "http://127.0.0.1:38440/v1"
api_key = "any-non-empty-value"
```

Moon Bridge 自动处理路由和协议转换。
