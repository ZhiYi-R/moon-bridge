# 现有 Extension 一览

## deepseek_v4（DeepSeek V4 扩展）

用于使 DeepSeek V4 模型通过 Anthropic 兼容端点正常工作的扩展。DeepSeek V4 实现的是 Anthropic Messages API 的子集，但有一些独特的差异需要处理。

**位置**：`internal/extension/deepseek_v4/`

**文件清单**：

| 文件 | 用途 |
|------|------|
| `plugin.go` | Plugin 实现，注册所有能力 |
| `deepseek_v4.go` | 核心转换函数（reasoning_content 处理） |
| `state.go` | thinking 缓存状态管理 |

**实现的能力**：

```go
// 编译期接口断言（plugin.go）
var (
    _ plugin.Plugin               = (*DSPlugin)(nil)
    _ plugin.InputPreprocessor    = (*DSPlugin)(nil)
    _ plugin.RequestMutator       = (*DSPlugin)(nil)
    _ plugin.MessageRewriter      = (*DSPlugin)(nil)
    _ plugin.ContentFilter        = (*DSPlugin)(nil)
    _ plugin.ContentRememberer    = (*DSPlugin)(nil)
    _ plugin.StreamInterceptor    = (*DSPlugin)(nil)
    _ plugin.ErrorTransformer     = (*DSPlugin)(nil)
    _ plugin.SessionStateProvider  = (*DSPlugin)(nil)
    _ plugin.ThinkingPrepender    = (*DSPlugin)(nil)
    _ plugin.ReasoningExtractor   = (*DSPlugin)(nil)
)
```

**各能力详解**：

### InputPreprocessor

`PreprocessInput()` — 移除输入消息中的 `reasoning_content` 字段。DeepSeek 如果在输入消息中出现 `reasoning_content` 会返回 400 错误，因为该字段是输出专用字段。

### RequestMutator

`MutateRequest()` — 调用 `ToAnthropicRequest()` 对请求做 DeepSeek 适配：

- 清空 `Temperature` 和 `TopP`（DeepSeek 可能拒绝拒绝这些参数）
- 将 OpenAI `reasoning.effort` 映射到 Anthropic `output_config.effort`（`high` → `high`，`xhigh`/`max` → `max`）

### MessageRewriter

`RewriteMessages()` — 可选地向用户消息前注入强化指令（reinforce prompt），用于提醒模型遵守 system prompt 和 AGENTS.md。

### ContentFilter + ContentRememberer + ThinkingPrepender + ReasoningExtractor

这是 DeepSeek V4 扩展最核心的部分，解决 **thinking 历史重建**问题。

**问题**：DeepSeek V4 下一次对话时，API 要求输入历史中包含上一次的 `thinking` 块（Anthropic 协议中 `type: "thinking"` 的 `ContentBlock`），否则返回错误。

**Codex 的限制**：Codex 在 Conversations API 中只保留 `reasoning` summary（`OutputItem.Type: "reasoning"`），不保留完整的 thinking 文本。

**解决方案**（四步走）：

```
1. 响应时（ContentFilter）→ 拦截 upstream 的 thinking 块，提取为 reasoning summary
2. 记忆时（ContentRememberer）→ 将 thinking 块按 tool_call_id / text_hash 缓存到 SessionData
3. 回放时（ThinkingPrepender + ReasoningExtractor）→ 在下一轮请求时：
   a. 优先从 reasoning summary 恢复原始 thinking 块（Encode/DecodeThinkingSummary）
   b. 回退到 SessionData 中按 tool_call_id 查找缓存的 thinking
   c. 最后兜底插入空 thinking 块
4. 持续学习（StreamInterceptor）→ 流式场景下同样捕获 thinking 并缓存
```

### StreamInterceptor

流式场景下拦截 `thinking_delta` / `reasoning_content_delta` 事件，累积完整的 thinking 文本，在流结束时缓存到 session state。

### ErrorTransformer

处理 DeepSeek 特有的错误消息。将关于 "thinking mode" 的错误转换为更友好的人类可读消息。

### SessionStateProvider

创建 `*State` 实例，用于跨请求缓存 thinking 块。State 内部维护两个 LRU 映射：

- `records`：按 `tool_use_id` 索引的 thinking 块（最多 1024 条）
- `textRecords`：按助手文本 SHA256 索引的 thinking 块（最多 1024 条）

### 启用方式

在模型配置中设置 `extensions.deepseek_v4.enabled: true`：

```yaml
provider:
  providers:
    deepseek:
      models:
        deepseek-v4-pro:
          extensions:
            deepseek_v4:
              enabled: true
```

或通过 routes 启用：

```yaml
provider:
  routes:
    moonbridge:
      to: "deepseek/deepseek-v4-pro"
    # routes 自动继承模型配置中的 deepseek_v4 extension 设置
```

插件的 `EnabledForModel` 函数通过 `Config.ExtensionEnabled("deepseek_v4", model)` 检查模型别名是否启用该 extension。

---

## web_search_injected（注入式 Web Search 扩展）

当上游提供商不支持 Anthropic 原生 `web_search_20250305` server tool 时，Moon Bridge 可以改用"注入式"模式——将 `tavily_search` 和 `firecrawl_fetch` 作为 function-type tool 注入请求，由服务端自动执行搜索。

**位置**：`internal/extension/websearchinjected/`

**文件清单**：

| 文件 | 用途 |
|------|------|
| `plugin.go` | Plugin 实现 |
| `websearchinjected.go` | 核心工具函数 |

**实现的能力**：

```go
var (
    _ plugin.Plugin          = (*WSInjectedPlugin)(nil)
    _ plugin.ToolInjector    = (*WSInjectedPlugin)(nil)
    _ plugin.ProviderWrapper = (*WSInjectedPlugin)(nil)
)
```

### 工作流程

```
1. Codex 请求中包含 web_search_preview tool
2. Bridge 检查模型 Web Search 模式 → "injected"
3. WSInjectedPlugin.InjectTools() 注入：
   - tavily_search（function tool）
   - firecrawl_fetch（function tool，如果配置了 Firecrawl key）
4. WSInjectedPlugin.WrapProvider() 将上游 Client 包装为 Orchestrator
5. 请求发送后：
   a. 如果上游返回工具调用（tavily_search/firecrawl_fetch）
   b. Orchestrator 自动执行 Tavily 搜索或 Firecrawl 抓取
   c. 将结果作为 tool_result 追加到下一轮请求
   d. 反复直到模型满意或达到最大轮次
```

### Orchestrator

`websearch.NewInjectedOrchestrator()` 创建一个搜索编排器，包装 `*anthropic.Client`，暴露相同的 `CreateMessage` / `StreamMessage` 接口。编器在内部以循环方式执行搜索工具，直到模型不再请求搜索或达到 `SearchMaxRounds`。

### 配置方式

```yaml
provider:
  providers:
    my-provider:
      base_url: "https://..."
      api_key: "..."
      web_search:
        support: "injected"
        tavily_api_key: "tvly-..."
        firecrawl_api_key: "fc-..."
        search_max_rounds: 5
```

或全局配置：

```yaml
provider:
  web_search:
    support: "injected"
    tavily_api_key: "tvly-..."
    firecrawl_api_key: "fc-..."
    search_max_rounds: 5
```

模型级别覆盖：

```yaml
provider:
  providers:
    my-provider:
      models:
        my-model:
          web_search:
            support: "enabled"  # 覆盖提供商级别的 injected
```

---

## codex（Codex 兼容性工具包）

虽然不是传统意义上的 Plugin，但 `internal/extension/codex/` 是 Extension 系统的重要部分。

**位置**：`internal/extension/codex/`

**文件清单**：

| 文件 | 用途 |
|------|------|
| `catalog.go` | 模型目录 DTO 生成、Codex config.toml 生成 |
| `convert.go` | Codex 特有 tool 类型转换（local_shell/custom/namespace） |
| `input.go` | 输入项类型定义和转换（InputItemConversion） |
| `response.go` | 输出项转换（tool_use → OutputItem） |
| `tools.go` | 工具编解码（apply_patch、exec、custom tool 的输入输出代理） |
| `tool_context.go` | 转换上下文（CustomToolSpec、FunctionToolSpec） |
| `stream_adapter.go` | 流式适配器（管理流状态中的自定义工具、Web Search） |
| `customtool.go` | apply_patch / exec 代理工具的 JSON schema 和输入输出编解码 |
| `default_instructions.go` | 默认模型指令模板（嵌入 default_instructions.txt） |

### 核心职责

1. **工具编解码**：在 OpenAI `custom` / `local_shell` / `function` 和 Anthropic `tool_use` 之间双向转换
2. **apply_patch 代理**：将 Codex 的 apply_patch grammar（`*** Begin Patch` / `*** End Patch` 格式）拆分为结构化 JSON 操作，方便 Anthropic 模型理解
3. **模型目录**：从配置生成 Codex CLI 可用的 `models_catalog.json`
4. **输入过滤**：检测并跳过 Web Search 预置空文本

### ConversionContext

`ConversionContext` 携带工具定义上下文，用于工具名称和输入的双向映射：

```go
type ConversionContext struct {
    CustomTools   map[string]CustomToolSpec    // 自定义工具规格
    FunctionTools map[string]FunctionToolSpec  // 命名空间函数规格
}
```

---

## visual（视觉扩展）


当主模型本身不具备多模态视觉能力时，Moon Bridge 可以将图片分析任务委派给一个专门的视觉 Provider。`visual` 扩展是一个 `ProviderWrapper`，它在主模型的对话中注入 `visual_brief` 和 `visual_qa` 两个工具，在主模型调用这些工具时，自动将图片发往视觉 Provider 分析并返回结果。

**位置**：`internal/extension/visual/`

**文件清单**：

| 文件 | 用途 |
|------|------|
| `plugin.go` | Plugin 实现，注入 `visual_brief` / `visual_qa` 工具 |
| `orchestrator.go` | 视觉编排器，拦截视觉工具调用并委派给视觉 Provider |
| `client.go` | 视觉客户端接口及 BridgeClient 实现，通过 Anthropic 协议发送图片请求 |
| `tools.go` | 工具定义和 schema 生成 |

**实现的能力**：

```go
var (
    _ plugin.Plugin       = (*Plugin)(nil)
    _ plugin.ToolInjector = (*Plugin)(nil)
)
```

### 工作流程

1. 请求到达 Server，Visual orchestrator 包装上游 Provider
2. Orchestrator 扫描请求消息中的 Anthropic image block，将其替换为 `Image #1`、`Image #2` 等文本占位符
3. 主模型处理请求，可选择调用 `visual_brief` / `visual_qa` 工具
4. Orchestrator 拦截工具调用：
   - 提取工具参数中的 `image_refs` 和 `image_urls`
   - 从之前保存的 `availableImages` 中匹配对应图片
   - 通过 `VisionClient.Analyze()` 发送给视觉 Provider
   - 视觉 Provider 返回分析结果
5. 将分析结果作为 `tool_result` 返回给主模型
6. 主模型可以使用分析结果继续推理，或再次调用 `visual_qa` 做进一步追问

### 视觉 Provider

视觉分析通过 `VisionClient` 接口执行。内置的 `BridgeClient` 实现使用一个独立的 Anthropic 兼容 Provider 来发送图片分析请求，这意味着你可以用任意支持多模态的 Provider（如 Kimi、GPT-4o 等）作为视觉后端。

```go
type VisionClient interface {
    Analyze(context.Context, AnalysisRequest) (string, error)
}
```

### 配置

```yaml
extensions:
  visual:
    config:
      provider: "visual-backend"
      model: "kimi-vision-model"
      max_tokens: 4096

provider:
  providers:
    main:
      models:
        my-model:
          extensions:
            visual:
              enabled: true
```

### 与 Provider 的交互

Visual orchestrator 包装上游 Anthropic Provider，与 `web_search_injected` 的包装模式相同。在 server 层通过 `resolveProvider()` → `maybeWrapVisual()` 来组合视觉 orchestrator 包装器。
