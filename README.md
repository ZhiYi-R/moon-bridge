# Moon Bridge

Moon Bridge 是一个用 Go 编写的协议转换与模型路由代理。对外暴露 **OpenAI Responses API**（`/v1/responses`），对内支持 **Anthropic Messages**、**Google Gemini（GenAI）**、**OpenAI Chat Completions** 等多种上游协议。客户端指定不同模型别名时，自动将请求路由到对应上游 Provider 并在协议间自动转换。

> 🍳 **新手先看这里** → [CookBook.md](CookBook.md)：一份按目标找做法的菜谱，5 分钟跑通第一个对话。
> 官方qq群：1103798316

## 特别感谢 🙏

<table align="center">
  

  <tr>
    <td align="center" width="160">
      <a href=" "><img src="./Images/volcano.png" alt="火山引擎" height="32"></a ><br>
      <a href="https://dis.chatdesks.cn/chatdesk/hsyqmoon-bridge.html"><strong>方舟 Agent Plan</strong></a >
    </td>
    <td align="left">
      <sub>感谢 <a href="https://dis.chatdesks.cn/chatdesk/hsyqmoon-bridge.html">方舟 Agent Plan </a>模型订阅套餐集成了包含 Doubao-Seed、Doubao-Seedance、Doubao-Seedream 等在内的字节跳动自研 SOTA 级模型，覆盖文本、代码、图像、视频等多模态任务。最新支持 MiniMax-M3、DeepSeek-V4 系列、GLM-5.2、Doubao-Seed-2.0 系列、Kimi-K2.6 等模型，工具不限。超全模态模型与 Harness 升级一步到位，深度支持 Agent 框架与 AI 编程工具。一次订阅，可以为不同任务切换合适的 AI 引擎。 </sub>
    </td>
  </tr>
  
</table>

---

## 快速开始

```bash
# pacman 或二进制安装后直接启动
moonbridge

# 首次无配置启动会创建 $HOME/moonbridge/config.yml
# 打开 http://127.0.0.1:38440/console/
# 在 Web Console 中配置 Provider、Model 和 API Key

# 源码开发也可以直接运行
go run ./cmd/moonbridge

# 另见 CookBook.md 中的详细使用场景
```

源码开发要求 Go 1.25+。

## 核心能力

- **协议转换**：OpenAI Responses → Anthropic Messages / Google Gemini / OpenAI Chat，适配四种上游协议
- **模型路由**：通过 `routes` 配置将模型别名映射到不同 Provider 的上游模型名
- **插件扩展**：`CorePluginHooks` 接口，支持请求预处理、响应后处理、流拦截
- **请求跟踪**：完整链路记录，每步转换均可追溯
- **用量统计**：按会话聚合 token 与费用
- **管理 API**：运行时热重载配置（需启用持久化）
- **Web Search 注入**：自动/注入模式，支持 Tavily、Firecrawl
- **Prompt 缓存**：explicit / automatic / hybrid 三种模式

## 三种工作模式

| 模式 | 行为 |
|------|------|
| `Transform`（默认） | 接收 OpenAI Responses 请求 → 协议转换 → 转发 → 反向转换后返回 |
| `CaptureAnthropic` | 接收 Anthropic Messages 请求 → 透明转发到 Anthropic 上游 |
| `CaptureResponse` | 接收 OpenAI Responses 请求 → 透明转发到 OpenAI 上游 |

## 配置说明

采用 YAML 格式，核心结构为 `models`、`providers`、`routes` 三段式。完整配置说明见 [CONFIGURATION.md](docs/CONFIGURATION.md)。

## 与 Codex CLI 配合使用

将 Moon Bridge 地址设为 Codex 的 OpenAI API Base URL 即可：

```toml
[openai]
base_url = "http://127.0.0.1:38440/v1"
api_key = "any-non-empty-value"
```

然后在 Moon Bridge 配置中定义与 Codex 模型同名的路由。

## 与 Claude Code 配合使用

```bash
claude --model your-alias --api-url http://127.0.0.1:38440 --api-key any-value
```

## Docker 部署

```bash
docker build -t moonbridge .
docker run -p 38440:38440 -v $(pwd)/config.yml:/config/config.yml moonbridge
```

## 命令行选项

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-config` | `$HOME/moonbridge/config.yml` | 配置文件路径 |
| `-addr` | 来自配置文件 | 覆盖监听地址 |
| `-mode` | 来自配置文件 | 覆盖运行模式（Transform/CaptureAnthropic/CaptureResponse） |
| `-print-addr` | — | 打印配置的监听地址后退出 |
| `-print-mode` | — | 打印配置的运行模式后退出 |
| `-print-default-model` | — | 打印默认模型别名后退出 |
| `-print-codex-model` | — | 打印 Codex 模型后退出 |
| `-print-codex-config <model>` | — | 为指定模型生成 Codex config.toml 后退出 |
| `-dump-config-schema` | — | 生成 config.schema.json 后退出 |

## HTTP API 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/v1/responses` | POST | OpenAI Responses API 主入口 |
| `/responses` | POST | 同上（无 `/v1` 前缀） |
| `/v1/models` | GET | 列出可用模型 |
| `/models` | GET | 同上 |
| `/console/` | GET | 嵌入式 Web Console |
| `/api/v1/` | — | 管理 API（需启用持久化） |

详细 API 文档见 [API.md](docs/api.md)。

## 请求跟踪

通过配置中的 `trace.enabled` 或特定工作模式启用请求跟踪，将完整请求/响应链路记录到文件。跟踪文件按 `session/模型名/类别/序号.json` 组织，支持 Chat、Response、Anthropic 三种分类。

## 许可证

[GPL v3](LICENSE)
