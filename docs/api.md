# API 接口

Moon Bridge 对外暴露两个 HTTP 端点，兼容 OpenAI Responses API 的子集。

## 基础信息

| 项目 | 默认值 |
|------|--------|
| 监听地址 | `127.0.0.1:38440` |
| 认证 | 无（内置认证在上游提供商层处理） |

可通过 `-addr` 覆盖监听地址，通过 `config.yml` 的 `server.addr` 配置。

## `POST /v1/responses`

主要端点，Codex CLI 发送对话请求。支持流式和非流式。

### 请求格式

兼容 OpenAI Responses API 格式。完整定义见 `internal/foundation/openai/types.go`。

```json
{
  "model": "moonbridge",
  "input": [
    {"role": "user", "content": [{"type": "input_text", "text": "Hello"}]}
  ],
  "instructions": "System prompt here",
  "max_output_tokens": 65536,
  "temperature": 0.7,
  "tools": [
    {"type": "function", "name": "get_weather", "description": "...", "parameters": {...}},
    {"type": "web_search_preview"},
    {"type": "local_shell"},
    {"type": "custom", "name": "edit", "format": {"definition": "..."}}
  ],
  "tool_choice": "auto",
  "stream": false,
  "reasoning": {"effort": "high"}
}
```

### 支持的字段

| 字段 | 支持情况 | 说明 |
|------|----------|------|
| `model` | ✅ 必填 | 模型别名或 `provider/model` 引用 |
| `input` | ✅ | 消息数组或纯文本字符串 |
| `instructions` | ✅ | 系统指令，与 system prompt 合并 |
| `max_output_tokens` | ✅ | 默认 65536 |
| `temperature` | ✅ | 映射到 Anthropic temperature |
| `top_p` | ✅ | 映射到 Anthropic top_p |
| `stop` | ✅ | 停止序列 |
| `tools` | ✅ | 支持 function / web_search_preview / local_shell / custom |
| `tool_choice` | ✅ | auto / none / required / {"type":"function","name":"..."} |
| `stream` | ✅ | true = SSE 流式 |
| `reasoning` | ✅ | 传递 reasoning.effort 给支持层 |

### 支持的 Tool 类型

#### `function`
标准函数调用，转换为 Anthropic tool_use。

#### `web_search_preview` / `web_search`
根据提供商配置转换为：
- **enabled**：转换为 Anthropic 原生 `web_search_20250305` server tool
- **injected**：转换为 `tavily_search` + `firecrawl_fetch` function tools，由服务端执行
- **disabled**：忽略

#### `local_shell`
Codex 本地 shell 执行，转换为 Anthropic `local_shell` tool。

#### `custom`
Codex 自定义工具，根据 grammar 类型转换为：
- **raw**：通用自定义工具
- **apply_patch**：拆分为 `add_file` / `update_file` / `delete_file` / `replace_file` / `batch` 五个子工具
- **exec**：转换为 Code Mode exec 代理工具

### 响应格式（非流式）

```json
{
  "id": "resp_...",
  "object": "response",
  "created_at": 1234567890,
  "status": "completed",
  "model": "moonbridge",
  "output": [
    {
      "type": "message",
      "id": "msg_item_0",
      "status": "completed",
      "role": "assistant",
      "content": [{"type": "output_text", "text": "Hello!"}]
    },
    {
      "type": "reasoning",
      "summary": [{"type": "summary_text", "text": "Thinking..."}]
    },
    {
      "type": "function_call",
      "id": "fc_tooluse_0",
      "call_id": "toolu_...",
      "name": "get_weather",
      "arguments": "{\"location\": \"Beijing\"}",
      "status": "completed"
    }
  ],
  "usage": {
    "input_tokens": 100,
    "output_tokens": 50,
    "input_tokens_details": {"cached_tokens": 30}
  }
}
```

### 流式响应格式（SSE）

SSE 事件格式：

```
event: response.created
data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_...","status":"in_progress",...}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{...}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Hel"}

event: response.output_text.done
data: {"type":"response.output_text.done","output_index":0,"content_index":0,"text":"Hello!"}

event: response.completed
data: {"type":"response.completed","response":{...}}
```

支持的事件类型：

| 事件 | 说明 |
|------|------|
| `response.created` | 响应创建 |
| `response.in_progress` | 响应处理中 |
| `response.output_item.added` | 新增输出项 |
| `response.output_item.done` | 输出项完成 |
| `response.content_part.added` | 新增内容片段 |
| `response.content_part.done` | 内容片段完成 |
| `response.output_text.delta` | 文本增量 |
| `response.output_text.done` | 文本完成 |
| `response.function_call_arguments.delta` | 函数参数 JSON 增量 |
| `response.function_call_arguments.done` | 函数参数完成 |
| `response.custom_tool_call_input.delta` | 自定义工具输入增量 |
| `response.completed` | 响应完成 |
| `response.incomplete` | 响应不完整 |
| `response.failed` | 响应失败 |

### 错误响应

```json
{
  "error": {
    "message": "提供商错误：rate limit exceeded",
    "type": "server_error",
    "code": "provider_error"
  }
}
```

错误类型映射：

| HTTP 状态码 | 说明 |
|-------------|------|
| 400 | 请求参数错误 |
| 401 | API Key 无效 |
| 403 | 权限不足 |
| 429 | 速率限制 |
| 502 | 上游提供商错误 |
| 504 | 上游超时 |

## `GET /v1/models`

返回模型目录，用于 Codex CLI 的模型发现。

### 响应格式

```json
{
  "models": [
    {
      "slug": "deepseek-v4-pro(deepseek)",
      "display_name": "DeepSeek V4 Pro(deepseek)",
      "description": "DeepSeek V4 with selectable high/xhigh reasoning effort.",
      "default_reasoning_level": "high",
      "supported_reasoning_levels": [
        {"effort": "high", "description": "High reasoning effort"},
        {"effort": "xhigh", "description": "Extra high reasoning effort (maps to DeepSeek max)"}
      ],
      "shell_type": "unified_exec",
      "visibility": "list",
      "supported_in_api": true,
      "supports_reasoning_summaries": true,
      "default_reasoning_summary": "auto",
      "web_search_tool_type": "text",
      "apply_patch_tool_type": "freeform",
      "truncation_policy": {"mode": "tokens", "limit": 10000},
      "supports_parallel_tool_calls": true,
      "context_window": 1000000,
      "max_context_window": 1000000,
      "effective_context_window_percent": 95,
      "input_modalities": ["text"]
    }
  ]
}
```

模型目录的生成逻辑在 `internal/extension/codex/catalog.go` 的 `BuildModelInfosFromConfig()` 中：

1. 优先使用 `provider.providers.<key>.models` 中的模型目录
2. 追加 `provider.routes` 中的别名作为补充
3. 为每个模型生成 `base_instructions`（来自 `default_instructions.txt` 模板）

## 命令行工具

Moon Bridge 提供以下命令行开关：

| 开关 | 说明 |
|------|------|
| `-config` | 指定配置文件路径（默认 `config.yml`） |
| `-addr` | 覆盖监听地址 |
| `-mode` | 覆盖运行模式 |
| `-print-addr` | 打印监听地址并退出 |
| `-print-mode` | 打印运行模式并退出 |
| `-print-default-model` | 打印默认模型别名并退出 |
| `-print-codex-model` | 打印 Codex 模型别名并退出 |
| `-print-claude-model` | 打印 Claude model 并退出 |
| `-print-codex-config` | 生成指定模型的 Codex config.toml 并退出 |
| `-codex-base-url` | 生成 config.toml 时使用的 Base URL |
| `-codex-home` | 指定 CODEX_HOME，同时写入 models_catalog.json |
