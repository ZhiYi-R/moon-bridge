# Moon Bridge CookBook

> 按目标找做法的菜谱集。每道菜包含食材、步骤、验证方法和排错。

---

## 菜谱索引

| # | 菜名 | 用时 | 难度 |
|---|------|------|------|
| 0 | [上桌之前](#0-上桌之前) | 2 min | ⭐ |
| 1 | [5 分钟跑通第一个对话](#1-5-分钟跑通第一个对话) | 5 min | ⭐ |
| 2 | [把 Codex CLI 接上 Moon Bridge](#2-把-codex-cli-接上-moon-bridge) | 3 min | ⭐⭐ |
| 3 | [换成另一个 Provider](#3-换成另一个-provider) | 3 min | ⭐⭐ |
| 4 | [打开 DeepSeek V4 推理能力](#4-打开-deepseek-v4-推理能力) | 2 min | ⭐ |
| 5 | [让模型能看图（Visual 扩展）](#5-让模型能看图visual-扩展) | 5 min | ⭐⭐⭐ |
| 6 | [打开 Web Search](#6-打开-web-search) | 5 min | ⭐⭐ |
| 7 | [启用 Prompt 缓存](#7-启用-prompt-缓存) | 2 min | ⭐ |
| 8 | [排错速查](#8-排错速查) | — | — |

---

## 0. 上桌之前

**食材：**

- **Go 1.26+** — `go version` 确认。没有的话去 [go.dev](https://go.dev/dl/) 下载。
- **API Key** — 推荐 DeepSeek，在 [platform.deepseek.com](https://platform.deepseek.com) 注册后创建 API Key。
- **一个终端**

**验证：**

```bash
go version
# go version go1.26.0 linux/amd64
```

**搞不定：**

| 问题 | 原因 | 解决 |
|------|------|------|
| `command not found: go` | Go 没装 | 去 golang.org/dl 下载 |
| `go: command not found` | 没加到 PATH | 装完后重启终端，或 `export PATH=$PATH:/usr/local/go/bin` |

---

## 1. 5 分钟跑通第一个对话

**效果：** 发一段文字，收到 AI 回复。

**食材：**
- [上桌之前](#0-上桌之前) 已完成
- DeepSeek API Key

**步骤：**

### 1.1 创建配置文件

项目根目录下创建 `config.yml`，只改 `api_key`：

```yaml
mode: "Transform"

server:
  addr: "127.0.0.1:38440"

provider:
  providers:
    deepseek:
      base_url: "https://api.deepseek.com"
      api_key: "sk-你的DeepSeek密钥"
      models:
        deepseek-chat:
          context_window: 64000

  routes:
    moonbridge: "deepseek/deepseek-chat"

  default_model: "moonbridge"
```

### 1.2 启动

```bash
go run ./cmd/moonbridge
```

看到 `Transform server listening on 127.0.0.1:38440` 即成功。终端保持运行，新开一个窗口做下一步。

### 1.3 测试

```bash
curl http://localhost:38440/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "moonbridge",
    "input": "你好，用一句话介绍一下自己。",
    "max_output_tokens": 100
  }'
```

**验证：** 返回 `"status": "completed"` 并包含回复内容。

**搞不定：**

| 问题 | 原因 | 解决 |
|------|------|------|
| `command not found: go` | 没装 Go | 见菜谱 0 |
| `connection refused` | 服务没启动 | 检查第一个终端输出 |
| `invalid yaml` / `cannot unmarshal` | 缩进错误 | YAML 每层用 2 空格，不能用 Tab |
| `401 unauthorized` | api_key 不对 | 检查 DeepSeek 官网的 key |
| `402 payment required` | 余额不足 | DeepSeek 官网充值 |
| 服务闪退 + Go 报错 | 依赖没下完 | 首次启动需要联网下载依赖 |

---

## 2. 把 Codex CLI 接上 Moon Bridge

**效果：** Codex CLI 走 Moon Bridge 调用 DeepSeek。

**食材：**
- 菜谱 1 已跑通
- Codex CLI 已装（`npm install -g @openai/codex`）

**步骤：**

Moon Bridge 自带 Codex 配置生成器。先确认它在运行：

```bash
curl -s http://localhost:38440/v1/models | head -3
```

然后用一条命令生成 `config.toml` 和 `models_catalog.json`：

```bash
CODEX_HOME_DIR="${CODEX_HOME:-$HOME/.codex}"
MODEL=$(go run ./cmd/moonbridge --print-codex-model)
go run ./cmd/moonbridge \
  --print-codex-config "$MODEL" \
  --codex-base-url "http://127.0.0.1:38440/v1" \
  --codex-home "$CODEX_HOME_DIR" \
  > "$CODEX_HOME_DIR/config.toml"
```

这会在 `$CODEX_HOME_DIR` 下写入两个文件：
- `config.toml` — Codex 的模型提供商配置
- `models_catalog.json` — 模型能力描述（context window、推理档位、工具类型等）

启动 Codex：

```bash
CODEX_HOME="$CODEX_HOME_DIR" MOONBRIDGE_CLIENT_API_KEY="local-dev" codex --cd "$PWD"
```

**验证：** Codex 正常启动，提问后 Moon Bridge 终端出现 `POST /v1/responses` 日志。

**搞不定：**

| 问题 | 原因 | 解决 |
|------|------|------|
| `connection refused` | Moon Bridge 没启动 | 先跑菜谱 1 |
| 看不懂的错误 | `CODEX_HOME` 指向的目录没有 `models_catalog.json` | 检查 `--codex-home` 生成的路径 |
---

## 3. 换成另一个 Provider

**效果：** 从 DeepSeek 换到其他模型（如 Anthropic）。

**食材：** 菜谱 1 已跑通 + 新 Provider 的 API Key。

**步骤：**

替换 `config.yml` 中 `provider.providers` 的内容：

```yaml
provider:
  providers:
    anthropic:
      base_url: "https://api.anthropic.com"
      api_key: "sk-ant-你的密钥"
      version: "2023-06-01"
      models:
        claude-sonnet-4-6:
          context_window: 200000

  routes:
    moonbridge:
      to: "anthropic/claude-sonnet-4-6"

  default_model: "moonbridge"
```

重启 Moon Bridge（Ctrl+C 停掉，再 `go run`），curl 测试。

**验证：** 同样请求，回复变成了 Claude 的语气。

> 换 Provider 只改 `config.yml`，不需要改 Codex 配置。

---

## 4. 打开 DeepSeek V4 推理能力

**效果：** DeepSeek V4 的 thinking_mode（深度推理）可用。

**食材：** DeepSeek V4 模型权限 + 菜谱 1 已跑通。

**步骤：**

```yaml
extensions:
  deepseek_v4:
    config:
      reinforce_instructions: true
      reinforce_prompt: "[System Reminder]: Please pay close attention to the system instructions...\n[User]:"

provider:
  providers:
    deepseek:
      base_url: "https://api.deepseek.com"
      api_key: "sk-你的密钥"
      models:
        deepseek-v4-pro:
          context_window: 1000000
          max_output_tokens: 384000
          extensions:
            deepseek_v4:
              enabled: true
          default_reasoning_level: "high"
          supported_reasoning_levels:
            - effort: "high"
              description: "High reasoning effort"
            - effort: "xhigh"
              description: "Extra high reasoning effort"

  routes:
    moonbridge:
      to: "deepseek/deepseek-v4-pro"

  default_model: "moonbridge"
```

重启 Moon Bridge。

**验证：** curl 请求加 `"reasoning": {"effort": "high"}`，复杂问题的回复会包含推理过程。

> `xhigh` 映射为 DeepSeek 的 `max` 档位，思考更深，也更慢更贵。

---

## 5. 让模型能看图（Visual 扩展）

**效果：** 纯文本主模型（如 DeepSeek）通过 Visual 扩展把图片委派给专门的视觉模型处理。

**食材：**
- 菜谱 1 已跑通
- 一个支持 Anthropic 的视觉模型 Provider（如 Kimi `api.moonshot.cn`）
- 两个 API Key：主模型 + 视觉模型

**步骤：**

```yaml
extensions:
  visual:
    config:
      provider: "kimi"
      model: "kimi-for-coding"
      max_rounds: 4
      max_tokens: 2048

provider:
  providers:
    deepseek:
      base_url: "https://api.deepseek.com"
      api_key: "sk-你的DeepSeek密钥"
      models:
        deepseek-v4-pro:
          context_window: 1000000
          extensions:
            deepseek_v4:
              enabled: true
            visual:
              enabled: true

    kimi:
      base_url: "https://api.moonshot.cn/anthropic"
      api_key: "sk-你的Kimi密钥"
      models:
        kimi-for-coding:
          context_window: 128000

  routes:
    moonbridge:
      to: "deepseek/deepseek-v4-pro"

  default_model: "moonbridge"
```

重启 Moon Bridge。

**验证：** 发一条带图片的请求，模型能描述图片内容。

---

## 6. 打开 Web Search

**效果：** 模型能联网搜索。

**食材：** 菜谱 1 已跑通 + Tavily API Key（免费 [tavily.com](https://tavily.com)）。

**步骤：**

```yaml
provider:
  web_search:
    support: "injected"
    tavily_api_key: "tvly-你的密钥"

  providers:
    deepseek:
      base_url: "https://api.deepseek.com"
      api_key: "sk-你的密钥"
      models:
        deepseek-chat:
          context_window: 64000

  routes:
    moonbridge: "deepseek/deepseek-chat"

  default_model: "moonbridge"
```

重启 Moon Bridge。

**验证：** 问时效性问题（如"今天天气"），回复应包含搜索来源。

> `support` 可选值：`auto`（自动探测）、`enabled`（强制）、`disabled`（关闭）、`injected`（走 Tavily/Firecrawl，不依赖 Provider）。

---

## 7. 启用 Prompt 缓存

**效果：** 减少重复输入消耗。

**食材：** 一个 Anthropic 协议的 Provider。

**步骤：**

```yaml
cache:
  mode: "explicit"
  ttl: "5m"
```

加到 `config.yml` 顶层，重启 Moon Bridge。

> `mode`：`off`（关闭）、`automatic`（自动）、`explicit`（手动标记，推荐）、`hybrid`（全开）。

---

## 8. 排错速查

### YAML 缩进

用 2 空格，不能用 Tab：

```yaml
# 错误
provider:
    base_url: "..."    # 4 空格

# 正确
provider:
  base_url: "..."      # 2 空格
```

### 服务起不来

```bash
go run ./cmd/moonbridge -config /path/to/config.yml 2>&1 | head -30
```

| 错误 | 原因 |
|------|------|
| `no such file or directory` | config.yml 路径不对 |
| `cannot unmarshal` | YAML 格式错误 |
| `unsupported protocol` | protocol 只能是 `anthropic` 或 `openai-response` |
| `connection refused` | Provider 的 base_url 写错或不可达 |
| `401` / `403` | API Key 不对 |
| `402` | DeepSeek 余额不足 |
| `rate limit` | 请求太频繁 |

### curl 不通

```bash
curl -s http://localhost:38440/v1/models | head -3
```

没输出则 Moon Bridge 未运行；有输出但请求失败则检查 model 名字。

### Visual 不工作

- extensions.visual.config.provider 对应的 Provider 是否存在
- 视觉 Provider 是否支持 Anthropic
- 主模型上 visual.enabled: true 是否设置

---

## 贡献菜谱

有常用的配置组合？提 PR 加一道新菜。
