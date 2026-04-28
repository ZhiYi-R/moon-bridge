# Moon Bridge

Moon Bridge 是一个 OpenAI Responses 兼容转发层。你可以像调用 OpenAI Responses API 一样使用 `/v1/responses`，Moon Bridge 会按模型别名把请求路由到 Anthropic Messages 兼容 Provider，或在配置为 `protocol: "openai-response"` 时直接透传到 OpenAI Responses Provider。

## 快速开始

1. 复制示例配置：

```bash
mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/moonbridge"
cp config.example.yml "${XDG_CONFIG_HOME:-$HOME/.config}/moonbridge/config.yml"
```

2. 编辑 `${XDG_CONFIG_HOME:-$HOME/.config}/moonbridge/config.yml`，填入 `provider.providers` 下各上游 Provider 的 `base_url` 和 `api_key`，在各 Provider 的 `models` 中声明可用的上游模型，然后在 `provider.routes` 中配置别名到 `"provider/upstream_model"` 的转发表。

3. 启动服务：

```bash
go run ./cmd/moonbridge
```

默认监听 `127.0.0.1:38440`。启动后即可通过 `http://localhost:38440/v1/responses` 调用。`GET /v1/models` 可查看所有可用模型。

未传 `-config` 时，Moon Bridge 会按 XDG 读取 `${XDG_CONFIG_HOME:-$HOME/.config}/moonbridge/config.yml`。仍可用 `-config /path/to/config.yml` 指定任意配置文件。

## 三种工作模式

在 `config.yml` 中通过 `mode` 选择工作方式：

### `Transform`（默认）

把 OpenAI Responses 请求翻译成 Anthropic Messages 调用。适合想让 Codex CLI 等 OpenAI 客户端跑在 Anthropic 兼容模型上的场景。

### `CaptureResponse`

透明代理 OpenAI Responses 流量。适合抓包分析 Codex CLI 等客户端发给原生 OpenAI 的请求内容。

### `CaptureAnthropic`

透明代理 Anthropic Messages 流量。适合抓包分析 Claude Code 等客户端发给 Anthropic 兼容 Provider 的请求内容。

## 配置说明

### 配置文件位置

默认主配置文件为 `${XDG_CONFIG_HOME:-$HOME/.config}/moonbridge/config.yml`。插件配置可以继续写在主配置的顶层 `plugins:` 下，也可以拆到主配置旁边的 `plugins/` 目录中，例如：

```yaml
# ${XDG_CONFIG_HOME:-$HOME/.config}/moonbridge/plugins/deepseek_v4.yml
reinforce_instructions: true
reinforce_prompt: "[System Reminder]: ...\n[User]:"
```

插件文件名（不含 `.yml` / `.yaml`）就是插件名；文件内容是该插件自己的配置。若内联配置和拆分文件同时存在，同名字段以拆分文件为准，未覆盖字段会保留内联值。

### Provider 与模型路由

Provider 在 `models` 中声明自己提供的上游模型及元信息（context_window、pricing 等），`routes` 则是一张独立的转发表，把客户端使用的模型别名映射到 `"provider/upstream_model"`。此外，API 请求中可直接使用 `provider/model` 格式指定模型（如 `deepseek/deepseek-v4-pro`），无需预先定义 route。例如客户端请求 `model: "moonbridge"` 时，会发往 `deepseek` Provider 的 `deepseek-v4-pro`；请求 `model: "gpt-image"` 时，会按 OpenAI Responses 协议直接发往 `openai` Provider 的 `gpt-image-1.5`：

```yaml
provider:
  providers:
    deepseek:
      base_url: "https://api.deepseek.com"
      api_key: "replace-with-deepseek-api-key"
      version: "2023-06-01"
      models:
        deepseek-v4-pro:
          context_window: 200000
          max_output_tokens: 100000
          deepseek_v4: true
          default_reasoning_level: "high"
          supported_reasoning_levels:
            - effort: "high"
              description: "High reasoning effort"
            - effort: "xhigh"
              description: "Extra high reasoning effort (maps to DeepSeek max)"
    openai:
      base_url: "https://api.openai.com"
      api_key: "replace-with-openai-api-key"
      protocol: "openai-response"
      models:
        gpt-image-1.5: {}

  routes:
    moonbridge: "deepseek/deepseek-v4-pro"
    gpt-image: "openai/gpt-image-1.5"
  default_model: "moonbridge"
```

`protocol` 默认为 `anthropic`。设置为 `openai-response` 时，本轮请求不会进入 Anthropic 转换层，而是保留 OpenAI Responses 格式，只把模型别名改写为上游真实模型名。

### 模型定价

`provider.providers.<key>.models.<upstream>.pricing` 是可选的 per-model 价格配置，单位是元（¥）/ M tokens。当某个模型配置了价格后，Moon Bridge 会按 session 累加费用，并在每次请求和服务退出时输出费用统计。价格定义在 Provider 的模型目录中，通过 `routes` 关联到别名后自动生效。

```yaml
provider:
  providers:
    deepseek:
      # ...
      models:
        deepseek-v4-pro:
          pricing:
            input_price: 2        # 无缓存输入 元/M tokens
            output_price: 8       # 模型输出
            cache_write_price: 1  # 缓存写入
            cache_read_price: 0.2  # 缓存读取
```

费用计算方式：`(input_tokens × input_price + cache_creation × cache_write_price + cache_read × cache_read_price + output_tokens × output_price) / 1_000_000`，四项均为独立计费。如果价格配置不全（某项为 0 或未设置），该项不产生费用。每请求 INFO 行中的 `Input` 展示采用 OpenAI 语义：`input_tokens + cache_read_input_tokens`，不把 `cache_creation_input_tokens` 额外计入展示值；cache creation 仍按 `cache_write_price` 计费，并会出现在详细汇总里。

### Prompt 缓存

`cache.mode` 控制 Anthropic prompt caching 策略：

- `off`：不注入缓存标记
- `automatic`：在请求顶层自动注入 `cache_control`
- `explicit`：在工具定义、system 提示、历史消息等稳定块上注入块级缓存断点（默认推荐）
- `hybrid`：同时启用顶层自动缓存和块级断点

`cache.ttl` 支持 `5m`（默认）和 `1h`。

### Web Search 能力

Web search 支持按 provider 独立配置。在 `provider.providers.<key>.web_search.support` 中为每个 provider 单独设置；全局 `provider.web_search.support` 作为未在 provider 级别配置时的回退默认值。

可选值：

- `auto`：启动 Transform 时用默认模型发送一次流式轻量探测；只有探测证明可用才注入，否则保守禁用
- `enabled`：跳过探测，始终注入 Anthropic `web_search_20250305`（适合已知支持的 Anthropic provider）
- `disabled`：不注入搜索工具（适合 DeepSeek 等不支持 Anthropic server tool 的 provider）
- `injected`：不依赖上游 Provider 是否支持 Anthropic 服务端搜索，改为注入 `tavily_search` / `firecrawl_fetch` 工具并在 Transform 内部执行搜索。需配置 `tavily_api_key`；`firecrawl_api_key` 可选

`protocol: "openai-response"` 的 provider 会跳过 Anthropic `web_search_20250305` 探测与注入（Tavily/Firecrawl），但会在 resolved web search mode 为 `enabled` 时自动注入 OpenAI Responses 原生 `{"type": "web_search"}` 工具到上游请求中。

配置示例：

```yaml
provider:
  providers:
    anthropic:
      base_url: "https://api.anthropic.com"
      api_key: "replace-with-anthropic-api-key"
      web_search:
        support: "enabled"  # Anthropic 原生支持
    deepseek:
      base_url: "https://api.deepseek.com"
      api_key: "replace-with-deepseek-api-key"
      web_search:
        support: "disabled" # DeepSeek 不支持 Anthropic server tool
      models:
        deepseek-v4-pro:
          deepseek_v4: true
          default_reasoning_level: "high"
          supported_reasoning_levels:
            - effort: "high"
              description: "High reasoning effort"
            - effort: "xhigh"
              description: "Extra high reasoning effort (maps to DeepSeek max)"

  # 全局回退默认值（未在 provider 级别配置时使用）
  web_search:
    support: "auto"
    max_uses: 8
```

### DeepSeek V4 扩展

在具体 Provider 模型下设置 `deepseek_v4: true` 可启用 DeepSeek V4 专用兼容逻辑，包括 reasoning_content 剥离与重注入、thinking 回放、推理输出展示等。推理档位使用与 Codex 兼容的 `default_reasoning_level` / `supported_reasoning_levels` 元数据表达；Transform 会把请求里的 `reasoning.effort` 映射到 DeepSeek Anthropic 兼容参数 `output_config.effort`，其中 `xhigh` 会映射为 DeepSeek 的 `max`。流式 signature-only thinking 会编码进 Codex 可回放的 `reasoning.summary`，旧历史缺少 reasoning/缓存时只在请求侧补空 `thinking` block，不生成空 summary。详见 [docs/deepseek-v4.md](docs/deepseek-v4.md)。

### 日志与系统提示

`config.yml` 支持配置日志级别和格式：

```yaml
log:
  level: "info"   # debug / info / warn / error
  format: "text"  # text / json
```

`system_prompt` 可设置全局系统提示词，会注入到每次 Transform 请求的 Anthropic `system` 块中：

```yaml
system_prompt: |
  自定义系统提示内容
```

日志格式支持 `text`（默认，带有 slog 格式的红色信息）和 `json`（结构化 JSON 行）。详见 `config.example.yml` 中的注释。

### 调试抓包

打开 `trace_requests: true` 后，Transform 的请求（包括 Anthropic 转换和 OpenAI Responses 直通）和 Capture 模式的代理流量会按模式写入 `trace/` 目录，方便排查问题。API Key 等敏感 Header 会自动脱敏。Transform 模式下 trace 按模型建立子目录，例如 `trace/Transform/{session_id}/{model}/Response/{n}.json` 和 `trace/Transform/{session_id}/{model}/Anthropic/{n}.json`。

## 配合 Codex CLI 使用

Moon Bridge 兼容 Codex CLI 的 OpenAI Responses 请求，包括 `local_shell` 工具、函数工具、工具结果回传、`web_search` 等。

### 手动配置

在 `~/.codex/config.toml` 中添加：

```toml
model = "moonbridge"
model_provider = "moonbridge"

[model_providers.moonbridge]
name = "Moon Bridge"
base_url = "http://localhost:38440/v1"
wire_api = "responses"
env_key = "MOONBRIDGE_CLIENT_API_KEY"

[mcp_servers.deepwiki]
url = "https://mcp.deepwiki.com/mcp"
startup_timeout_sec = 3600
tool_timeout_sec = 3600
```

也可以让 Moon Bridge 按当前 `config.yml` 生成 Codex 配置片段：

```bash
go run ./cmd/moonbridge -print-codex-config moonbridge -codex-base-url http://127.0.0.1:38440/v1 -codex-home ~/.codex
```

设置客户端 API Key（本地 Moon Bridge 不校验，任意占位值即可）：

```bash
export MOONBRIDGE_CLIENT_API_KEY="local-dev"
```

然后启动 Moon Bridge，再运行 `codex`。

### 一键启动脚本

当 `config.yml` 的 `mode` 为 `Transform` 或 `CaptureResponse` 时，可以使用脚本自动启动 Moon Bridge 和 Codex：

```bash
./scripts/start_codex_with_moonbridge.sh
```

也可以带一个初始任务进入 TUI：

```bash
./scripts/start_codex_with_moonbridge.sh '请运行测试并汇报结果'
```

脚本会自动生成隔离的 Codex 配置到 `FakeHome/Codex/config.toml`，不会修改你全局的 `~/.codex`；
如果全局 `~/.codex/config.toml`（或 `$MOONBRIDGE_CODEX_CONFIG`）中存在 `[tui].status_line`，会逐字复制到隔离配置里。
因此其中包含的 `"fast-mode"` 等字段也来自全局配置——如需移除，请编辑全局 `~/.codex/config.toml` 的 `status_line` 列表。

## 配合 Claude Code 使用

当 `config.yml` 的 `mode` 为 `CaptureAnthropic` 时，可以使用脚本自动启动 Moon Bridge 和 Claude Code：

```bash
./scripts/start_claude_code_with_moonbridge.sh
```

也可以带一个初始任务：

```bash
./scripts/start_claude_code_with_moonbridge.sh '请读取 README 并总结项目用途'
```

脚本会自动设置隔离的 Claude Code 配置，不会修改全局的 `~/.claude`。

## 直接调用 API

非流式调用：

```bash
curl -sS http://localhost:38440/v1/responses \
  -H 'content-type: application/json' \
  -d '{"model":"moonbridge","input":"Hello"}'
```

流式调用：

```bash
curl -sS http://localhost:38440/v1/responses \
  -H 'content-type: application/json' \
  -d '{"model":"moonbridge","input":"Hello","stream":true}'
```

支持的标准参数包括 `instructions`、`max_output_tokens`、`temperature`、`top_p`、`stop`、`tools`、`tool_choice`、`parallel_tool_calls`、`prompt_cache_key`、`prompt_cache_retention` 等。

## 工具调用支持

Moon Bridge 支持以下工具类型：

- **函数工具**（`type: "function"`）：标准 JSON Schema 参数定义，Anthropic 返回的工具调用会映射为 OpenAI 的 `function_call`。
- **`local_shell`**：Codex CLI 的本地 shell 工具，自动映射为 Anthropic 兼容格式，shell 执行仍由 Codex 客户端完成。
- **`web_search`**：Codex 的搜索工具，按当前请求路由到的 provider 的 web search 配置决定是注入 Anthropic `web_search_20250305`、注入 injected 工具还是跳过。搜索次数上限由 `web_search.max_uses` 控制（支持 per-provider 覆盖）。
- **Custom grammar 工具**：Codex 内置需要 freeform grammar 的工具目前主要是 `apply_patch` 和 Code Mode `exec`。Moon Bridge 把 `apply_patch` 拆成 add/delete/update/replace/batch 一组结构化工具，把 `exec` 暴露成 `source`；Provider 返回后再拼回 Codex 需要的 raw grammar call。
- **命名空间 / MCP 工具**：支持带命名前缀的工具名称。

## 响应与用量

响应格式与 OpenAI Responses API 一致，包含：

- `output[]`：模型输出的消息或工具调用
- `usage.input_tokens` / `usage.output_tokens`：输入和输出 token 数
- `usage.input_tokens_details.cached_tokens`：命中缓存的 token 数
- `status`：`completed` 或 `incomplete`

当启用 prompt caching 时，Anthropic 侧的缓存创建和命中会归一化到 OpenAI Responses 的 `usage` 字段中；Provider 原始缓存明细会保留在 `metadata.provider_usage` 中，方便排查成本。

如果配置了模型定价，每个成功请求都会输出一行可读 INFO。这里的模型名是实际发往上游的模型名，`Billing` 是当前 session 累计费用，不是单次请求费用：

```
deepseek-v4-pro Usage: 0.120000 M Input, 0.004500 M Output, Session Cache Hit Rate: 25.00%, Billing: 0.28 CNY
gpt-image-1.5 Usage: 1.200000 M Input, 0.500000 M Output, Session Cache Hit Rate: 25.00%, Billing: 3.04 CNY
```

服务终止时会输出 summary 行和详细拆解：

```
Summary：Session Cache Hit Rate(AVG): 25.0%, Billing: 3.04 CNY
Session Stats: 42 requests, 12m30s duration
  Input:  154320 tokens (120000 fresh, 20000 cache creation, 14320 cache read)
  Output: 8500 tokens
  Cache Hit Rate: 9.3% (saved 14320 tokens)
  Total Cost: ¥3.040000
    moonbridge: ¥3.040000 (42 req, 154320 in, 8500 out)
```

## 错误处理

常见错误会返回 OpenAI 风格的错误响应：

- 鉴权失败：`401 invalid_api_key`
- 权限不足或模型不可用：`403 permission_denied`
- 参数不支持：`400 unsupported_parameter`
- 上下文超限：`400 context_length_exceeded`
- 限流：`429 rate_limit_exceeded`
- 上游错误：`502 provider_error`
- 上游超时：`504 provider_timeout`

## 日志

使用脚本启动时，Moon Bridge 的日志分别写入：

- Codex 场景：`logs/moonbridge-codex.log`
- Claude Code 场景：`logs/moonbridge-claude-code.log`

脚本每次启动都会先清空对应日志文件，随后把脚本自身的构建、启动、客户端退出、服务端停止信息和 Moon Bridge 服务日志写入同一个文件。

手动启动时，标准输出即为服务日志。

---

更多细节请参考 `config.example.yml` 中的注释和 `docs/` 目录下的设计文档。
