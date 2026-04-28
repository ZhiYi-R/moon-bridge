# Moon Bridge

<p align="center">
  <a href="https://www.gnu.org/licenses/gpl-3.0">
    <img src="https://img.shields.io/badge/License-GPL%20v3-blue.svg" alt="GPL v3 License">
  </a>
</p>

Moon Bridge 是一个协议转换和模型路由代理。它对外暴露一个 **OpenAI Responses** 端点（`/v1/responses`），背后可以接任意兼容 **Anthropic Messages** 协议的上游 Provider，也可以直通上游 OpenAI Responses Provider。客户端指定不同的模型别名时，它自动把请求路由到对应的上游 Provider，并在不同协议之间自动转换。
只需要一个 `config.yml` 和一条 `go run` 命令就能跑起来。

---

- [快速开始](#快速开始)
- [三种工作模式](#三种工作模式)
  - [Transform（默认）](#transform默认)
  - [CaptureResponse](#captureresponse)
  - [CaptureAnthropic](#captureanthropic)
- [配置指南](#配置指南)
  - [多提供商与模型路由](#多提供商与模型路由)
  - [Web Search 配置](#web-search-配置)
  - [缓存配置](#缓存配置)
  - [DeepSeek V4 专有配置](#deepseek-v4-专有配置)
  - [Plugin 配置](#plugin-配置)
- [与 Codex CLI 一起使用](#与-codex-cli-一起使用)
- [与 Claude Code 一起使用](#与-claude-code-一起使用)
- [Docker 部署](#docker-部署)
- [命令行选项](#命令行选项)
- [HTTP API 参考](#http-api-参考)
  - [POST /v1/responses](#post-v1responses)
  - [GET /v1/models](#get-v1models)
  - [错误处理](#错误处理)
- [用量统计与日志](#用量统计与日志)
- [请求跟踪](#请求跟踪)
- [Extension（插件）系统](#extension插件系统)
- [开源许可](#开源许可)

---

## 快速开始

### 1. 准备配置

从示例配置开始：

```bash
mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/moonbridge"
cp config.example.yml "${XDG_CONFIG_HOME:-$HOME/.config}/moonbridge/config.yml"
```

编辑 `config.yml`，至少填入一个上游 Provider 的 `base_url` 和 `api_key`。

最小的可工作配置：

```yaml
mode: "Transform"

server:
  addr: "127.0.0.1:38440"

provider:
  providers:
    my-provider:
      base_url: "https://api.example.com"
      api_key: "sk-..."
      models:
        my-model:
          context_window: 128000
      # 协议：默认 "anthropic"，设为 "openai-response" 可直通 OpenAI Responses API
      # protocol: "anthropic"
  routes:
    moonbridge: "my-provider/my-model"
  default_model: "moonbridge"
```

### 2. 启动服务

```bash
go run ./cmd/moonbridge
```

默认监听 `127.0.0.1:38440`。启动后即可向 `http://localhost:38440/v1/responses` 发送 OpenAI Responses 格式的 POST 请求。

未传 `-config` 时，Moon Bridge 会按 XDG 读取 `${XDG_CONFIG_HOME:-$HOME/.config}/moonbridge/config.yml`。仍可用 `-config /path/to/config.yml` 指定任意配置文件。

---

## 三种工作模式

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

旧配置可用迁移脚本更新到当前 Provider/routes/Visual 结构：

```bash
python scripts/migrate_config.py --dry-run config.yml
python scripts/migrate_config.py config.yml
```

迁移细节见 [docs/config-migration.md](docs/config-migration.md)。

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

### Visual 扩展

Visual 是给 Anthropic-routed 主模型注入视觉能力的转发层扩展。主模型仍走原本 Provider，Visual 工具调用会通过 `provider.visual.provider` 指定的现有 Anthropic-compatible Provider 执行，例如 Kimi；Moon Bridge 不引入单独的 Kimi 客户端或自定义桥接。

启用方式分两层：`provider.visual.enabled` 打开全局 Visual，`provider.providers.<main>.models.<upstream>.visual: true` 让具体主模型暴露两个工具：

- `visual_brief`：第一轮图片简介，返回画面概览、重要细节、OCR、疑点和后续追问建议。
- `visual_qa`：后续澄清问题，主模型可带上 `prior_visual_context` 或继续引用同一张图。

配置示例：

```yaml
provider:
  providers:
    deepseek:
      # ...
      models:
        deepseek-v4-pro:
          deepseek_v4: true
          visual: true
    kimi:
      base_url: "https://api.moonshot.cn/anthropic"
      api_key: "replace-with-kimi-api-key"
      version: "2023-06-01"
      models:
        kimi-for-coding: {}

  visual:
    enabled: true
    provider: "kimi"
    model: "kimi-for-coding"
    max_rounds: 4
    max_tokens: 2048
```

Codex / OpenAI Responses 的 `input_image` 会先转成 Anthropic image block。进入主模型前，Visual 包装器会把图片从主请求中拿出并替换成 `Image #1` 这类可引用提示，防止纯文本主模型或上游把 `Image #1` 当 URL 传给 Kimi。工具参数里可以用 `image_refs: ["Image #1"]`；如果本轮有附件且工具没有显式传图，Visual 会自动使用可用附件。

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

### 3. 验证

查看可用模型：

```bash
curl http://localhost:38440/v1/models
```

发送一条测试请求：

```bash
curl http://localhost:38440/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "moonbridge",
    "input": "你好，请用一句话介绍自己。",
    "max_output_tokens": 100
  }'
```

---

## 三种工作模式

通过 `config.yml` 的 `mode` 字段切换。

### Transform（默认）

```
Codex CLI ──→ Moon Bridge ──→ LLM Provider
  (OpenAI     协议转换        (Anthropic
   Responses)                  Messages)
```

收到 OpenAI Responses 格式的请求后，Moon Bridge 将其翻译为 Anthropic Messages API 请求，发送给上游提供商，再把响应转换回 OpenAI 格式返回。

这是与 Codex CLI 搭配使用的模式。支持非流式（JSON）和流式（SSE）两种响应方式。

### CaptureResponse

```
Client ──→ Moon Bridge ──→ OpenAI Responses Provider
             (透明代理)
```

纯代理模式，不转换协议。Moon Bridge 简单地将请求原样转发到上游 OpenAI Responses 端点，并将响应回传。可用于抓包分析或请求跟踪。

需要配置 `developer.proxy.response`：

```yaml
developer:
  proxy:
    response:
      model: "gpt-5.5"
      provider:
        base_url: "https://api.openai.com"
        api_key: "sk-..."
```

### CaptureAnthropic

```
Client ──→ Moon Bridge ──→ Anthropic Provider
             (透明代理)
```

同样为透明代理，但转发的是 Anthropic Messages API 请求（`/v1/messages`）。适用于 Claude Code 等 Anthropic 原生客户端。

需要配置 `developer.proxy.anthropic`：

```yaml
developer:
  proxy:
    anthropic:
      model: "claude-sonnet-4-6"
      provider:
        base_url: "https://api.anthropic.com"
        api_key: "sk-ant-..."
        version: "2023-06-01"
```

---

## 配置指南

完整的配置选项见 `config.example.yml`。

### 多提供商与模型路由

Moon Bridge 支持同时配置多个上游提供商，并按模型别名路由。

```yaml
provider:
  providers:
    deepseek:          # 提供商标识符（自定义）
      base_url: "https://api.deepseek.com"
      api_key: "sk-..."
      protocol: "anthropic"         # anthropic（默认）或 openai-response
      models:
        deepseek-v4-pro:
          context_window: 1000000
          max_output_tokens: 384000
          display_name: "DeepSeek V4 Pro"
          deepseek_v4: true         # 启用 DeepSeek V4 扩展
          default_reasoning_level: "high"
          supported_reasoning_levels:
            - effort: "high"
              description: "High reasoning effort"
            - effort: "xhigh"
              description: "Extra high reasoning effort"
          supports_reasoning_summaries: true
          pricing:
            input_price: 2          # 每百万 token 输入价格（RMB）
            output_price: 8         # 每百万 token 输出价格（RMB）
            cache_write_price: 1
            cache_read_price: 0.2
    openai:
      base_url: "https://api.openai.com"
      api_key: "sk-..."
      protocol: "openai-response"   # 直通 OpenAI Responses API
      models:
        gpt-5.5: {}
  routes:
    moonbridge: "deepseek/deepseek-v4-pro"
    gpt5: "openai/gpt-5.5"
  default_model: "moonbridge"
```

**模型别名规则**：

- 客户端请求时使用 `model: "moonbridge"` 或 `model: "deepseek/deepseek-v4-pro"`
- 支持两种引用格式：`provider/model` 和 `model(provider)`
- `routes` 是可选的，用于提供友好的简短别名
- `provider/providers` 中的模型目录会自动出现在 `GET /v1/models` 的返回中

### Web Search 配置

Moon Bridge 支持三种 Web Search 方式：

| 模式 | 说明 |
|------|------|
| `enabled` | 使用 Anthropic 原生 `web_search_20250305` server tool（上游必须支持） |
| `injected` | 注入 `tavily_search` + `firecrawl_fetch` 作为 function tool，由 Moon Bridge 服务端执行搜索 |
| `disabled` | 禁用 Web Search |
| `auto` | 启动时自动探测上游是否支持（默认） |

配置优先级：**模型级 > 提供商级 > 全局**。

```yaml
provider:
  web_search:
    support: "auto"          # 全局默认，可选 auto/enabled/disabled/injected
    max_uses: 8
    tavily_api_key: "tvly-..."
    firecrawl_api_key: "fc-..."
    search_max_rounds: 5

  providers:
    anthropic:
      base_url: "https://api.anthropic.com"
      api_key: "sk-ant-..."
      web_search:
        support: "enabled"   # 提供商级覆盖
      models:
        claude-sonnet-4-6:
          web_search:
            support: "auto"  # 模型级覆盖
            max_uses: 15
```

### 缓存配置

Moon Bridge 支持 Anthropic 协议中的 Prompt Caching 特性，通过 `cache` 节配置：

```yaml
cache:
  mode: "explicit"          # off / automatic / explicit / hybrid
  ttl: "5m"                 # 5m 或 1h
  prompt_caching: true
  automatic_prompt_cache: false
  explicit_cache_breakpoints: true
  min_cache_tokens: 1024
  min_breakpoint_tokens: 1024
```

缓存策略：

- **automatic**：由 Anthropic 自动检测重复前缀并缓存
- **explicit**：由 Moon Bridge 在 system、tools、user messages 等稳定段落的末尾注入 `cache_control` 标记
- **hybrid**：同时使用两种策略
- **off**：完全禁用缓存

### DeepSeek V4 专有配置

使用 DeepSeek V4 模型的额外配置：

```yaml
plugins:
  deepseek_v4:
    reinforce_instructions: true
    reinforce_prompt: "[System Reminder]: ...\n[User]:"
```

模型标记 `deepseek_v4: true` 后，插件会自动处理：

- 移除输入中的 `reasoning_content` 字段（DeepSeek 不接受该字段）
- 清空 `temperature` / `top_p` 参数
- 将 `reasoning.effort` 映射为 `output_config.effort`
- 缓存和管理 thinking 块（跨对话历史重建）
- 可选地在用户消息前注入强化指令

### Plugin 配置

在 `plugins` 节下配置各插件的参数：

```yaml
plugins:
  deepseek_v4:
    reinforce_instructions: true
  web_search_injected:    # 由 enabled/injected 模式自动激活，一般无需显式配置
```

---

## 与 Codex CLI 一起使用

项目提供了一个自动化脚本 `scripts/start_codex_with_moonbridge.sh`：

```bash
# 一条命令启动 Moon Bridge + 启动 Codex CLI
./scripts/start_codex_with_moonbridge.sh
```

该脚本会自动：

1. 构建 Moon Bridge 二进制
2. 从 `config.yml` 中解析模型配置
3. 生成 Codex 使用的 `config.toml`（含模型目录）
4. 启动 Moon Bridge 服务
5. 设置 `CODEX_HOME` 和 `MOONBRIDGE_CLIENT_API_KEY` 环境变量
6. 启动 Codex CLI
7. Codex 退出后自动停止 Moon Bridge

也可以分步操作：

```bash
# 手动启动服务
go run ./cmd/moonbridge &

# 生成 Codex 配置并启动 Codex
CODEX_HOME="$PWD/FakeHome/Codex"
go run ./cmd/moonbridge \
  --print-codex-config "$(go run ./cmd/moonbridge --print-codex-model)" \
  --codex-base-url "http://127.0.0.1:38440/v1" \
  --codex-home "$CODEX_HOME"

CODEX_HOME="$CODEX_HOME" MOONBRIDGE_CLIENT_API_KEY="local-dev" codex --cd "$PWD"
```

生成的 Codex `config.toml` 包含模型提供商信息和预配置的 MCP server（如 deepwiki）。

---

## 与 Claude Code 一起使用

使用 `scripts/start_claude_code_with_moonbridge.sh`：

```bash
# CaptureAnthropic 模式下启动 Moon Bridge + Claude Code
./scripts/start_claude_code_with_moonbridge.sh
```

该脚本会自动构建 Moon Bridge（若未构建）、启动服务、配置 Claude Code 使用 Moon Bridge 作为代理、启动 Claude Code，并在 Claude Code 退出后停止 Moon Bridge。

或在 CaptureAnthropic 模式下单独启动 Moon Bridge 后，用 `scripts/start_claude_code.sh` 连接到已有服务。

---

## Docker 部署

项目提供了 `Dockerfile`（基于 `golang:1.26` 多阶段构建，最终镜像为 `gcr.io/distroless/static-debian12`）：

```bash
# 构建镜像
docker build -t moonbridge .

# 运行容器（配置文件挂载到 /config/config.yml）
docker run -d \
  --name moonbridge \
  -p 38440:38440 \
  -v "$PWD/config.yml:/config/config.yml" \
  moonbridge
```

容器默认监听 `0.0.0.0:38440`。

---

## 命令行选项

```
moonbridge -config config.yml [选项]
```

| 选项 | 说明 |
|------|------|
| `-config 路径` | 配置文件路径（默认 `config.yml`） |
| `-addr 地址` | 覆盖监听地址 |
| `-mode 模式` | 覆盖运行模式：`Transform`、`CaptureResponse`、`CaptureAnthropic` |
| `-print-addr` | 打印监听地址并退出 |
| `-print-mode` | 打印运行模式并退出 |
| `-print-default-model` | 打印默认模型别名并退出 |
| `-print-codex-model` | 打印 Codex 模型别名并退出 |
| `-print-claude-model` | 打印 Claude Code 模型别名并退出 |
| `-print-codex-config 别名` | 生成指定模型的 Codex `config.toml` 片段并退出 |
| `-codex-base-url URL` | 生成 config.toml 时使用的 Base URL |
| `-codex-home 路径` | 指定 CODEX_HOME，同时写入 `models_catalog.json` |

---

## HTTP API 参考

### POST /v1/responses

兼容 OpenAI Responses API。支持流式和非流式。

**请求**：

```json
{
  "model": "moonbridge",
  "input": [{"role": "user", "content": [{"type": "input_text", "text": "Hello"}]}],
  "instructions": "You are a helpful assistant.",
  "max_output_tokens": 65536,
  "temperature": 0.7,
  "stream": false,
  "tools": [
    {"type": "function", "name": "get_weather", "description": "...", "parameters": {...}},
    {"type": "web_search_preview"},
    {"type": "local_shell"}
  ],
  "reasoning": {"effort": "high"}
}
```

**非流式响应**：

```json
{
  "id": "resp_abc123",
  "object": "response",
  "created_at": 1714290000,
  "status": "completed",
  "model": "moonbridge",
  "output": [
    {
      "type": "message",
      "id": "msg_item_0",
      "status": "completed",
      "role": "assistant",
      "content": [{"type": "output_text", "text": "Hello! How can I help?"}]
    }
  ],
  "usage": {"input_tokens": 50, "output_tokens": 10, "input_tokens_details": {"cached_tokens": 20}}
}
```

**流式响应**（SSE）：

```
event: response.created
data: {"type":"response.created","response":{"id":"resp_...","status":"in_progress",...}}

event: response.output_item.added
data: {"type":"response.output_item.added","output_index":0,"item":{...}}

event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"Hel"}

event: response.output_text.done
data: {"type":"response.output_text.done","text":"Hello!"}

event: response.completed
data: {"type":"response.completed","response":{...}}
```

支持的 tool 类型：

| 类型 | 说明 |
|------|------|
| `function` | 标准函数调用，转换为 Anthropic tool_use |
| `web_search_preview` / `web_search` | 根据配置转换为原生 server tool 或注入式搜索工具 |
| `local_shell` | Codex 本地 shell 执行 |
| `custom` | Codex 自定义工具（apply_patch / exec / raw） |

### GET /v1/models

返回可用模型列表，格式兼容 Codex CLI 的模型发现接口。

```json
{
  "models": [
    {
      "slug": "deepseek-v4-pro(deepseek)",
      "display_name": "DeepSeek V4 Pro(deepseek)",
      "default_reasoning_level": "high",
      "supported_reasoning_levels": [{"effort": "high", "description": "..."}],
      "shell_type": "unified_exec",
      "visibility": "list",
      "supported_in_api": true,
      "context_window": 1000000,
      "max_context_window": 1000000
    }
  ]
}
```

### 错误处理

错误以 OpenAI 风格返回：

```json
{
  "error": {
    "message": "提供商错误：rate limit exceeded",
    "type": "server_error",
    "code": "provider_error"
  }
}
```

| HTTP 状态码 | 含义 |
|-------------|------|
| 400 | 请求参数错误（unsupported_parameter / 无效 tool 类型等） |
| 401 | API Key 无效 |
| 403 | 权限不足或模型不可用 |
| 429 | 速率限制 |
| 502 | 上游提供商错误 |
| 504 | 上游超时 |

---

## 用量统计与日志

Moon Bridge 会在标准错误输出（stderr）实时打印每次请求的用量统计：

```
模型: moonbridge ➡️ deepseek-v4-pro
输入: 读取 1024K + 写入 512K + 首次 0
输出: 256K
计费: 本请求 0.0040 元, 累计 1.2340 元
缓存: 命中率 33.33%, 写入率 16.67%, 读写比 2.00
---
会话统计: 42 次请求, 耗时 5m30s
累计费用: ¥1.234000
```

包含的信息：

- 请求模型和实际调用的上游模型名
- 输入/输出 token 数量（含缓存读取、缓存写入、首次输入）
- 单次请求费用和累计费用（需配置 `pricing`）
- 缓存命中率、写入率、读写比
- 按模型细分的消费明细

日志级别通过 `log.level` 配置：`debug`、`info`、`warn`、`error`。

---

## 请求跟踪

设置 `trace_requests: true` 后，每次请求的完整数据会写入磁盘：

```
trace/
└── Transform/
    ├── Response/
    │   ├── 000001.json  # OpenAI 请求/响应
    │   ├── 000002.json
    │   └── ...
    └── Anthropic/
        ├── 000001.json  # 转换后的 Anthropic 请求/响应
        ├── 000002.json
        └── ...
```

每条记录包含：HTTP 头部、请求体、响应体（或流事件序列）、以及错误信息。流式请求还会记录中间转换后的 SSE 事件序列。

---

## Extension（插件）系统

Moon Bridge 内置了基于能力接口的插件系统。插件通过实现预定义接口来拦截和扩展请求/响应的各个处理阶段。

已内置的插件：

| 插件名 | 用途 |
|--------|------|
| `deepseek_v4` | DeepSeek V4 模型适配（thinking 历史重建、参数映射、错误消息转换） |
| `web_search_injected` | 注入式 Web Search（用 Tavily/Firecrawl 替代原生 server tool） |

插件的能力接口包括：

- `InputPreprocessor` — 预处理原始输入 JSON
- `RequestMutator` — 修改转换后的 Anthropic 请求
- `ToolInjector` — 注入额外工具定义
- `MessageRewriter` — 重写消息列表
- `ContentFilter` — 过滤响应内容块
- `ResponsePostProcessor` — 后处理最终 OpenAI 响应
- `StreamInterceptor` — 拦截流事件
- `ErrorTransformer` — 改写错误消息
- `ThinkingPrepender` — 重建 thinking 历史
- 等

详细文档见 [docs/extension-system.md](docs/extension-system.md)。

---

## 开源许可

Copyright (C) 2026 Moon Bridge Contributors

This program is free software: you can redistribute it and/or modify it under the terms of the GNU General Public License as published by the Free Software Foundation, either version 3 of the License, or (at your option) any later version.

This program is distributed in the hope that it will be useful, but WITHOUT ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the GNU General Public License for more details.

You should have received a copy of the GNU General Public License along with this program. If not, see <https://www.gnu.org/licenses/>.
