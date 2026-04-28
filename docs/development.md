# 开发提示

## 环境配置

### 配置文件

项目使用 YAML 配置。未指定 `-config` 时，默认读取 `${XDG_CONFIG_HOME:-$HOME/.config}/moonbridge/config.yml`。参考 `config.example.yml` 创建配置：

```bash
mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/moonbridge"
cp config.example.yml "${XDG_CONFIG_HOME:-$HOME/.config}/moonbridge/config.yml"
# 编辑 XDG 配置文件，填入 provider.providers.* 的真实 base_url / api_key，
# 并在各 Provider 的 models 中配置模型别名、上游模型名和可选价格。
```

支持的配置字段还包括：

- `log.level`：日志级别（`debug` / `info` / `warn` / `error`）
- `log.format`：日志格式（`text` / `json`）
- `system_prompt`：全局系统提示词，注入到 Transform 请求的 Anthropic `system` 块

插件配置可以写在主配置顶层 `plugins:` 下，也可以拆到独立文件，例如 `${XDG_CONFIG_HOME:-$HOME/.config}/moonbridge/plugins/deepseek_v4.yml`。文件名就是插件名，文件内容是该插件的配置；同名字段由拆分文件覆盖内联配置。

仓库根目录的 `config.yml` 已在 `.gitignore` 中，不会提交到仓库。也可通过 `--config` 参数指定其他路径。
注：`MOONBRIDGE_CONFIG` 不再被读取，请使用 `--config` 标志。

### 文件结构

```
.
├── cmd/moonbridge/          # 命令行入口
├── internal/
│   ├── app/                 # 应用组装
│   ├── config/
│   │   ├── config.go        # 运行时方法 + DTO
│   │   └── config_loader.go # YAML 加载和校验
│   ├── bridge/              # 协议转换核心
│   ├── extensions/          # Provider 扩展（如 DeepSeek V4, Codex 兼容层）
│   ├── cache/               # Prompt cache 规划
│   ├── anthropic/           # Anthropic Messages 客户端
│   ├── openai/              # OpenAI Responses DTO
│   ├── provider/            # 多 Provider 路由和连接池
│   ├── extensions/codex/    # Codex catalog/config + toolcall codec
│   ├── server/              # HTTP 服务器
│   ├── session/             # 每请求状态隔离
│   ├── stats/               # session usage / billing 统计
│   ├── proxy/               # 透明代理
│   ├── trace/               # 请求/响应转储
│   └── e2e/                 # 端到端测试
├── helloagents/             # 知识库
├── docs/                    # 文档
├── scripts/
│   ├── start_codex_with_moonbridge.sh     # Transform / CaptureResponse 模式
│   └── start_claude_code_with_moonbridge.sh  # CaptureAnthropic 模式
├── go.mod
└── config.example.yml
```

### 启动服务

```bash
# 构建并启动 Transform 模式
go build -o moonbridge ./cmd/moonbridge
./moonbridge

# 指定配置文件
./moonbridge -config /path/to/config.yml

# 覆盖地址和模式
./moonbridge -addr 0.0.0.0:8080 -mode CaptureResponse

# 打印配置信息供脚本使用
./moonbridge -print-addr
./moonbridge -print-codex-model
./moonbridge -print-codex-config moonbridge -codex-home /path/to/codex_home
```

启动脚本 `scripts/start_codex_with_moonbridge.sh` 和 `scripts/start_claude_code_with_moonbridge.sh` 会自动构建二进制、管理服务进程生命周期，并设置临时 `CODEX_HOME` / `CLAUDE_CONFIG_DIR`。
Codex 脚本会从 `${MOONBRIDGE_CODEX_CONFIG:-$HOME/.codex/config.toml}` 逐字复制 `[tui].status_line` 到 `FakeHome/Codex/config.toml`，但不会改动全局配置。
因此 `status_line` 中包含的 `"fast-mode"` 等字段直接继承自全局 Codex 配置；如需移除，请编辑全局 `~/.codex/config.toml` 的 `status_line` 列表。
两个脚本每次运行都会先清空对应的 `logs/moonbridge-*.log`，然后把脚本生命周期输出和 Moon Bridge 服务输出追加到同一个日志文件中，方便看到启动前配置、客户端退出状态和服务端退出汇总。

## 测试

### 运行单元测试

```bash
go test ./...
```

### 运行端到端测试

E2E 测试需要有效的 `config.yml`（包含真实 Provider API Key）：

```bash
go test ./internal/service/e2e/ -v -count=1
```

### 测试要点

- 非流式请求：验证 `FromAnthropicWithPlanAndContext()` 在各类停止原因、缓存 usage、多工具调用场景下的正确性。
- 流式请求：验证 `ConvertStreamEventsWithContext()` 的事件顺序、item ID 前缀、custom tool 和 web search 的特殊 delta 事件。
- 历史转换：验证 `convertInput()` 中连续工具调用的归并逻辑，以及 `output_text` 的压缩。
- Codex 兼容：验证空 `cached_tokens` 序列化、namespace 展平、`web_search_call` 过滤、custom grammar 保留。
- 多 Provider：验证模型别名路由、`openai-response` 直通、上游模型名改写和 provider 配置校验。
- Usage / Billing：验证每请求 INFO 使用上游实际模型名、`Billing` 为 session 累计值、`Input` 展示为 `input_tokens + cache_read_input_tokens`。
- Cache planner：验证各种配置组合（off / automatic / explicit / hybrid）下的断点注入与注册表状态管理。
- DTO：验证 `input_tokens_details.cached_tokens` 在值为 `0` 时仍被序列化。
- DeepSeek V4：验证 reasoning_content 剥离、请求级 thinking state、`high/xhigh` 到 DeepSeek `high/max` 的映射、采样参数清理、流式 delta 收集等逻辑。

## Debug

### 启用请求追踪

在 `config.yml` 中设置 `trace_requests: true`，Anthropic 转换路径、OpenAI Responses 直通路径和 Capture 模式的所有请求/响应都会写入 `trace/` 目录（成功和错误场景均会写入）。详见 [architecture.md](architecture.md) trace 模块说明。

### 回放缓存策略

`scripts/replay_anthropic_cache.py` 可以回放 `trace/Transform/<session>/<model>/Anthropic/*.json`，按 Anthropic Messages prompt cache 的 `tools -> system -> messages` 前缀、`cache_control` 断点、TTL、最小 token 门槛和 lookback 规则模拟缓存读写，用于在真实请求之外先筛策略：

```bash
scripts/replay_anthropic_cache.py trace/Transform/20260426T110909Z-79bfa6d6 --compare
scripts/replay_anthropic_cache.py trace/Transform/20260426T110909Z-79bfa6d6 --strategy observed --fit-lookback 20
```

默认会排除没有上游响应的错误 trace；需要分析失败请求对策略连续性的影响时可加 `--include-errors`。`--fit-lookback` 用已观测的 `usage` 估计当前 provider 更接近哪个有效 lookback，适合 Anthropic-compatible Provider 和官方行为存在差异时做校准。

当前 `claude-opus-4-6` 兼容 Provider 在 trace `20260426T110909Z-79bfa6d6` 上的实测校准值为 `lookback_blocks=3`（命令：`--strategy observed --fit-lookback 20`）。官方 prompt cache 文档描述的自动前缀检查会向前寻找约 20 个块边界；本地策略调参时可同时跑默认 `20` 看理论上限、跑 `3` 估计当前 Provider 的保守表现。

### 测试用例编写风格

使用表格驱动测试（table-driven tests）。对于协议转换的断言，优先对比整个请求/响应对象，而非逐字段断言。

## 配置变更

本项目仍在开发中，不需要保留旧配置兼容性。配置结构变更时：

1. 更新 `internal/foundation/config/config.go` 中的 FileConfig 和 FromFileConfig。
2. 更新 `config.example.yml`。
3. 更新启动脚本（如适用）。
4. 更新本目录下的相应文档。

## 变更日志

所有实质性变更需记录在 `helloagents/CHANGELOG.md`。

## 工具映射备忘

- `namespace` 下的 `function` 子工具发往 Anthropic 时展平为 `namespace__tool`，如 `mcp__deepwiki__ask_question`；响应回 Codex 时必须拆为 `namespace:"mcp__deepwiki__"` + `name:"ask_question"`，否则 Codex 不能解析为 MCP 调用。
- `namespace` 下的 `custom` 子工具同样展平为 `namespace__tool`，保留 grammar 信息。
- 查询 Codex 内部工具实现必须优先走 DeepWiki；当前确认需要 grammar/freeform 的内置 custom 工具主要是 `apply_patch` 和 Code Mode `exec`。
- `apply_patch` 不直接暴露 raw grammar 给 Anthropic，而是拆成 `apply_patch_add_file`、`apply_patch_delete_file`、`apply_patch_update_file`、`apply_patch_replace_file`、`apply_patch_batch` 一组结构化 schema，响应回 Codex 前统一拼回 raw patch grammar；proxy 描述不能包含 Codex 原始 `FREEFORM` / grammar 提示，避免和 JSON schema 冲突。`replace_file` / `update_file + content` 代表整文件替换，会拼成 `Delete File` + `Add File`，不要生成空 `Update File` hunk。
- Code Mode `exec` 转换成 `{source: string}` schema，响应回 Codex 前再把 `source` 原样作为 custom tool input；proxy 描述也不要暴露 raw grammar。
- MCP / DeepWiki 的使用偏好写在 `AGENTS.md`，不要写进 Transform 转换层；转换层只做协议映射，不注入项目特定提示词。
- `web_search` 桥接使用 Anthropic `web_search_20250305` server tool，不被当成普通 function 处理；`provider.web_search.support:auto` 会在 Transform 启动时用流式请求探测 Provider 是否接受该工具，只有探测证明可用才注入。
- Provider 可能返回空 `text_delta` 紧挨工具调用；流式转换必须忽略空文本增量，历史转换也必须跳过空 `output_text`，否则下一轮 Anthropic 请求会出现缺少 `text` 字段的非法 text block。
- `file_search`、`computer_use_preview`、`image_generation` 目前直接忽略。
- `local_shell` 使用独立 schema 和 output item，不走 `function_call` 路径。

## 当前实测结论

最新一批 Codex→Anthropic trace 显示：`hybrid`（`mode: automatic` + `automatic_prompt_cache: true` + `explicit_cache_breakpoints: true`）可以拿到较高 `cache_read_input_tokens`，但长会话工具循环里也会持续产生较高 `cache_creation_input_tokens`。为降低“每轮都从 tools/system 之后整段重建”的写入成本，planner 现在会把剩余断点预算均匀分布到更早的 user/tool_result 消息前缀，而不是只在最后一条消息落点。若后续 `cache_read_input_tokens` 长期为 0 或成本仍异常，应继续结合 trace 中的 Provider 原始 usage 调整 `mode`、`automatic_prompt_cache` 和 `max_breakpoints`。

## 依赖

- Go 1.22+（项目使用 `range-over-int` 等新特性）
- `gopkg.in/yaml.v3` — YAML 配置解析
- 无其他外部依赖


## Codex 兼容验证

完成模块化重构后，可通过以下清单验证 Codex 兼容性：

### 自动化测试

```bash
go test ./internal/extension/codex ./internal/protocol/bridge ./internal/service/server ./cmd/moonbridge
go test ./...
git diff --check
go build -o /tmp/moonbridge-verify ./cmd/moonbridge
```

### 静态 Catalog 验证

```bash
./moonbridge --print-codex-config gpt-5.4 --codex-base-url http://127.0.0.1:38440/v1 --codex-home /tmp/moonbridge-codex-verify
```

确认生成的 `models_catalog.json` 中：
- `.models | length > 0`
- 所有模型 `visibility == "list"`、`supported_in_api == true`、`apply_patch_tool_type == "freeform"`

### 关键行为清单

| 场景 | 验证方式 |
|------|----------|
| MCP 工具保留 namespace | 请求/响应中 `mcp__*` 工具名保持 namespace 回拆 |
| apply_patch | Codex 入站 custom grammar → Anthropic split tools → 回拼 raw patch |
| Code Mode exec | exec custom grammar → `{"source": "..."}` proxy → 回拼 |
| local_shell_call | 走 local shell item，不降级为 function call |
| streaming custom tool | 使用 `response.custom_tool_call_input.delta` |
| 插件注入工具 | 出现在 Anthropic request tools 中 |
| web search | 行为与 provider 配置一致 |
