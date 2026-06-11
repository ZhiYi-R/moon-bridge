# API 接口

Moon Bridge 对外暴露 OpenAI Responses 兼容端点、模型列表端点和可选的管理 API。

## 基础信息

- **Base URL**：`http://127.0.0.1:38440`（默认）
- **认证**：通过 `auth_token` 配置启用 Bearer Token
- **内容类型**：`application/json`

## 核心端点

### POST /v1/responses

OpenAI Responses API 兼容的聊天/补全端点。

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

### GET /v1/models

列出所有可用模型列表。

## 管理 API

当 `persistence.active_provider` 启用时，管理 API 在 `/api/v1/` 下可用。

| 端点 | 方法 | 功能 |
|------|------|------|
| `/api/v1/providers` | GET/POST/PUT/PATCH/DELETE | 管理 Provider CRUD |
| `/api/v1/providers/{key}/offers` | POST | 创建 Offer |
| `/api/v1/providers/{key}/offers/{model}` | PATCH/DELETE | 更新/删除 Offer |
| `/api/v1/providers/{key}/test` | POST | 测试 Provider 连通性 |
| `/api/v1/models` | GET | 列出模型定义 |
| `/api/v1/models/{slug}` | GET/PUT/DELETE | 管理单个模型定义 |
| `/api/v1/routes` | GET | 列出路由 |
| `/api/v1/routes/{alias}` | GET/PUT/DELETE | 管理单个路由 |
| `/api/v1/defaults` | GET/PUT | 管理默认配置（model/max_tokens/system_prompt） |
| `/api/v1/web-search` | GET/PUT | 管理全局 Web Search 设置 |
| `/api/v1/extensions` | GET | 列出扩展 |
| `/api/v1/extensions/{name}` | GET/PUT | 管理扩展设置 |
| `/api/v1/config/effective` | GET | 获取生效配置 |
| `/api/v1/config/export` | GET | 导出配置 YAML |
| `/api/v1/config/import` | POST | 导入配置 YAML |
| `/api/v1/config/validate` | POST | 校验配置 |
| `/api/v1/changes` | GET | 列出未提交的配置变更 |
| `/api/v1/changes/apply` | POST | 应用配置变更 |
| `/api/v1/changes/discard` | POST | 放弃配置变更 |
| `/api/v1/status` | GET | 系统状态 |
| `/api/v1/status/providers` | GET | Provider 运行状态 |
| `/api/v1/sessions` | GET | 列出活跃会话 |
| `/api/v1/stats` | GET | 用量统计 |
| `/api/v1/stats/summary` | GET | 用量统计摘要 |
| `/api/v1/logs` | GET | 日志查询 |
| `/api/v1/version` | GET | 版本信息 |

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
