# Extension 系统

Moon Bridge 的 Extension 系统基于能力接口（capability interfaces）的插件架构。插件通过实现 `Plugin` 基础接口和零个或多个能力接口来扩展桥接能力。

## 核心接口

### Plugin（基础接口）

所有插件必须实现 `plugin.Plugin` 接口：

```go
// internal/extension/plugin/plugin.go
type Plugin interface {
    Name() string                    // 唯一标识符（如 "deepseek_v4"）
    Init(ctx PluginContext) error    // 初始化，接收配置
    Shutdown() error                 // 关闭，释放资源
    EnabledForModel(modelAlias string) bool  // 是否对指定模型启用
}
```

```go
type PluginContext struct {
    Config    any           // 已按 extension config spec 解码的 typed config
    AppConfig config.Config  // 全局配置（只读）
    Logger    *slog.Logger   // 带插件名的 logger
}
```

内置工具：`BasePlugin` 提供所有方法的 no-op 默认实现，插件只需覆盖需要的方法。

### RequestContext 与 StreamContext

插件能力方法的第一个参数通常是 `*RequestContext` 或 `*StreamContext`，定义在 `internal/extension/plugin/context.go`：

```go
type RequestContext struct {
    ModelAlias  string               // 模型别名（如 "moonbridge"）
    SessionData map[string]any       // 跨请求会话数据，按插件名索引
    Reasoning   map[string]any       // OpenAI reasoning 配置
    WebSearch   WebSearchInfo        // 解析后的 Web Search 设置
}

type StreamContext struct {
    RequestContext
    StreamState any  // 该插件本次流的 per-stream 状态
}

func (ctx *RequestContext) SessionState(pluginName string) any {
    // 返回指定插件的会话状态
}
```

会话数据的隔离由 `session.Session` 保证——不同的会话（由 `session_id` 或 `X-Codex-Window-Id` 头标识）使用不同的 `ExtensionData` 映射。

### ConfigSpecProvider

插件通过 `ConfigSpecProvider` 声明自己的配置结构，支持跨作用域（全局/Provider/Model/Route）的配置：

```go
type ConfigSpecProvider interface {
    ConfigSpecs() []config.ExtensionConfigSpec
}
```

### 能力接口（Capability Interfaces）

插件可按需实现以下能力接口。`plugin.Registry` 在注册时通过类型断言自动检测，并在 `CorePluginHooks()` 方法中串联所有实现的插件。

#### 请求管道（Request Pipeline）

| 接口 | 方法 | 作用时机 |
|------|------|----------|
| `InputPreprocessor` | `PreprocessInput(ctx, raw) RawMessage` | 输入 JSON 反序列化之前 |
| `MessageRewriter` | `RewriteMessages(ctx, messages) []CoreMessage` | 输入消息列表转换后 |
| `RequestMutator` | `MutateRequest(ctx, req)` | CoreRequest 构建后、发送到 Provider Adapter 前 |
| `ToolInjector` | `InjectTools(ctx) []CoreTool` | 工具转换时注入额外工具（返回 CoreTool 列表） |

#### 提供商管道（Provider Pipeline）

| 接口 | 方法 | 作用时机 |
|------|------|----------|
| `ProviderWrapper` | `WrapProvider(ctx, provider) any` | 包装上游 Provider 客户端 |

#### 响应管道（Response Pipeline）

| 接口 | 方法 | 作用时机 |
|------|------|----------|
| `ContentFilter` | `FilterContent(ctx, block) bool` | 逐块检查响应内容块，返回 true 表示跳过该块 |
| `ResponsePostProcessor` | `PostProcessResponse(ctx, resp)` | 最终 OpenAI Response 构建后 |
| `ContentRememberer` | `RememberContent(ctx, content)` | 完整响应内容可用时（如流式完成） |

#### 流式管道（Streaming Pipeline）

| 接口 | 方法 | 作用时机 |
|------|------|----------|
| `StreamInterceptor` | `NewStreamState() any` | 创建 per-request 流状态 |
| | `OnStreamEvent(ctx, event) (consumed, emit)` | 每个流事件，返回 consumed=true 则 bridge 跳过正常处理 |
| | `OnStreamComplete(ctx, outputText)` | 流完成 |

```go
type StreamEvent struct {
    Type  string  // "block_start", "block_delta", "block_stop"
    Index int
    Block *format.CoreContentBlock  // for block_start
    Delta anthropic.StreamDelta     // for block_delta
}
```

> **已知限制：** `StreamInterceptor` 使用 `anthropic.StreamDelta` 类型，而 Adapter 路径中的 `CorePluginHooks.OnStreamEvent`/`OnStreamComplete`/`NewStreamState` 使用 `format.CoreStreamEvent`。两者语义不兼容，目前无插件通过 `CorePluginHooks` 接入流式钩子——所有流式钩子均为空操作。等价的流式拦截功能（DeepSeek V4 thinking 实时回放）通过旧 `plugin.Registry` 路径仅在 Anthropic 出站协议上工作。其他出站协议使用 post-stream content remembering 方式在流结束后重放内容。

#### 历史重建（History Reconstruction）

| 接口 | 方法 | 作用时机 |
|------|------|----------|
| `ThinkingPrepender` | `PrependThinkingForToolUse(messages, toolCallID, summary, state) []CoreMessage` | 工具调用前补充 thinking 块 |
| | `PrependThinkingForAssistant(blocks, summary, state) []CoreContentBlock` | 助手消息前补充 thinking 块 |
| `ReasoningExtractor` | `ExtractThinkingBlock(ctx, summary) (CoreContentBlock, bool)` | 从 reasoning summary 恢复 thinking 块 |

#### 错误处理

| 接口 | 方法 | 作用时机 |
|------|------|----------|
| `ErrorTransformer` | `TransformError(ctx, msg) string` | 上游错误消息转换 |

#### 会话状态

| 接口 | 方法 | 作用时机 |
|------|------|----------|
| `SessionStateProvider` | `NewSessionState() any` | 新会话创建时 |

#### 日志

| 接口 | 方法 | 作用时机 |
|------|------|----------|
| `LogConsumer` | `ConsumeLog(ctx, entries) []LogEntry` | 每条 slog 日志通过 consume pipeline 分发，可拦截、修改或抑制 |

#### 请求完成与 HTTP 路由

| 接口 | 方法 | 作用时机 |
|------|------|----------|
| `RequestCompletionHook` | `OnRequestCompleted(ctx, result)` | 每次请求完成后，接收模型、token、费用、状态和耗时 |
| `RouteRegistrar` | `RegisterRoutes(register)` | Server 初始化时注册额外 HTTP handler |

#### 持久化

| 接口 | 方法 | 作用时机 |
|------|------|----------|
| `DBProvider` | `DBProvider() db.Provider` | 声明数据库后端，如 SQLite 或 D1 |
| `DBConsumer` | `DBConsumer() db.Consumer` | 声明需要数据库的消费者，如 metrics |

#### Core 格式适配器接口（Adapter 路径）

以下接口专用于 Adapter 路径（从 OpenAI Response 转换到其他协议时），定义在 `internal/extension/plugin/capabilities.go`：

| 接口 | 方法 | 作用时机 |
|------|------|----------|
| `CoreRequestMutator` | `MutateCoreRequest(ctx, req)` | CoreRequest 构建后（标准 context.Context） |
| `CoreContentFilter` | `FilterCoreContent(ctx, block) bool` | 过滤 Core 内容块 |
| `CoreContentRememberer` | `RememberCoreContent(ctx, content)` | 记住 Core 内容块 |

## 注册表（Registry）

`plugin.Registry` 管理所有注册的插件，按能力类型分类存储。

```go
// internal/extension/plugin/registry.go
type Registry struct {
    plugins            []Plugin
    inputPreprocessors []InputPreprocessor
    requestMutators    []RequestMutator
    toolInjectors      []ToolInjector
    dbProviders        []DBProvider
    dbConsumers        []DBConsumer
    requestCompletionHooks []RequestCompletionHook
    routeRegistrars    []RouteRegistrar
    // ... 其他能力列表
}
```

### 注册流程

```go
// 1. 创建注册表
registry := plugin.NewRegistry(logger.L())

// 2. 注册插件（自动检测能力）
registry.Register(deepseekv4.NewPlugin())
registry.Register(visual.NewPlugin())
registry.Register(dbsqlite.NewPlugin())
registry.Register(metrics.NewPlugin())

// 3. 初始化（传递 AppConfig 和 typed extension 配置）
if err := registry.InitAll(&cfg); err != nil {
    // cfg.ExtensionConfig("deepseek_v4", "") → *deepseekv4.Config 解码
}

// 4. 构建 CorePluginHooks（串联所有插件能力）
hooks := registry.CorePluginHooks()
// 返回 format.CorePluginHooks 结构体，传给各 Adapter 使用

// 5. 在应用关闭时清理
defer registry.ShutdownAll()
```

`Registry.CorePluginHooks()` 方法（`registry.go:486`）遍历已注册的插件，对实现了 `CoreRequestMutator`、`CoreContentFilter`、`CoreContentRememberer`、`InputPreprocessor` 接口的插件，依次串联成 `format.CorePluginHooks` 的对应字段。

> **注意：** `CorePluginHooks.PreprocessInput` 于 2026-06 接入。此前 `InputPreprocessor` 能力虽已定义但未被 `CorePluginHooks()` 串联，在 Adapter 路径中为空操作。影响仅限包含 `reasoning_content` 的多轮请求（如 DeepSeek V4 `StripReasoningContent`）。

## 与 Adapter 的集成

Plugin 通过 `format.CorePluginHooks`（定义在 `internal/format/adapter.go`）与 Adapter 路径集成。这是一个函数结构体，`Registry.CorePluginHooks()` 自动构建：

```go
type CorePluginHooks struct {
    PreprocessInput        func(ctx context.Context, model string, raw json.RawMessage) json.RawMessage
    RewriteMessages        func(ctx context.Context, req *CoreRequest)
    InjectTools            func(ctx context.Context) []CoreTool
    MutateCoreRequest      func(ctx context.Context, req *CoreRequest)
    PostProcessCoreResponse func(ctx context.Context, resp *CoreResponse)
    TransformError         func(ctx context.Context, model string, msg string) string
    OnStreamEvent          func(ctx context.Context, event CoreStreamEvent) (skip bool)
    OnStreamComplete       func(ctx context.Context, model string, outputText string)
    FilterContent          func(ctx context.Context, block *CoreContentBlock) (skip bool)
    RememberContent        func(ctx context.Context, content []CoreContentBlock)
    NewStreamState         func(ctx context.Context, model string) any
    PrependThinkingToAssistant func(ctx context.Context, req *CoreRequest)
}

func (hooks CorePluginHooks) WithDefaults() CorePluginHooks {
    // 将所有 nil 函数替换为 no-op，确保安全调用
}
```

Adapter 在转换过程中调用这些 hook：

```go
// 在上游 Provider Adapter 中：
a.hooks.MutateCoreRequest(ctx, req)  // 修改 CoreRequest
a.hooks.RememberContent(ctx, content) // 记录响应内容

// 在 OpenAI Client Adapter 中：
a.hooks.PreprocessInput(ctx, model, raw)      // 预处理输入
a.hooks.PostProcessCoreResponse(ctx, resp)     // 后处理响应
```

Server 层也会直接使用插件能力：

- `LogConsumer`：通过 `logger.SetConsumeFunc()` 接入日志缓冲。
- `DBProvider` / `DBConsumer`：由 `db.Registry` 初始化数据库并绑定消费者。
- `RequestCompletionHook`：请求完成后由 `server.onRequestCompleted()` 触发。
- `RouteRegistrar`：由 `server.registerPluginRoutes()` 挂到 `http.ServeMux`。

内置扩展目录中的 `websearchinjected` 有插件接口实现，但当前运行路径中注入式搜索由 bridge/server 根据模型 resolved web search mode 直接调用 `websearch` / `websearchinjected` 的工具和 Provider 包装函数，不在 `BuiltinExtensions()` 中注册。

## 配置方式

在 `config.yml` 的 `extensions` 节配置扩展参数。扩展自己的参数放在 `config:`，启用状态放在对应 scope 的 `enabled` 中：

```yaml
extensions:
  deepseek_v4:
    config:
      reinforce_instructions: true
      reinforce_prompt: "[System Reminder]: ...\n[User]:"
```

插件通过 `ConfigSpecProvider` 声明自己的配置结构：

```go
func (p *DSPlugin) ConfigSpecs() []config.ExtensionConfigSpec {
    return []config.ExtensionConfigSpec{{
        Name: "deepseek_v4",
        Scopes: []config.ExtensionScope{
            config.ExtensionScopeGlobal,
            config.ExtensionScopeProvider,
            config.ExtensionScopeModel,
            config.ExtensionScopeRoute,
        },
        Factory: func() any { return &Config{} },
    }}
}

func (p *DSPlugin) Init(ctx plugin.PluginContext) error {
    p.cfg = plugin.Config[Config](ctx)  // 从 PluginContext 解码
    p.appCfg = ctx.AppConfig
    return nil
}

func (p *DSPlugin) EnabledForModel(model string) bool {
    return p.appCfg.ExtensionEnabled("deepseek_v4", model)
}
```

## 实现 Demo

### 最小化插件

```go
package demo

import (
    "moonbridge/internal/extension/plugin"
)

const PluginName = "demo"

type DemoConfig struct {
    Prefix string `json:"prefix,omitempty" yaml:"prefix"`
}

type DemoPlugin struct {
    plugin.BasePlugin
    prefix string
}

func NewPlugin() *DemoPlugin {
    return &DemoPlugin{}
}

func (p *DemoPlugin) Name() string { return PluginName }

func (p *DemoPlugin) Init(ctx plugin.PluginContext) error {
    cfg := plugin.Config[DemoConfig](ctx)
    if cfg != nil {
        p.prefix = cfg.Prefix
    }
    ctx.Logger.Info("demo plugin initialized", "prefix", p.prefix)
    return nil
}

func (p *DemoPlugin) EnabledForModel(model string) bool {
    return true  // 对所有模型启用
}
```

### 带能力的插件

```go
package demo

import (
    "moonbridge/internal/extension/plugin"
    "moonbridge/internal/format"
    "moonbridge/internal/protocol/openai"
)

// 注入额外工具的插件
type SystemInjectionPlugin struct {
    plugin.BasePlugin
    systemMessage string
}

func (p *SystemInjectionPlugin) Name() string { return "system_inject" }

// --- RequestMutator (修改 CoreRequest) ---
func (p *SystemInjectionPlugin) MutateRequest(ctx *plugin.RequestContext, req *format.CoreRequest) {
    // 追加 system 指令
    req.System = append(req.System, format.CoreContentBlock{
        Type: "text",
        Text: p.systemMessage,
    })
}

// --- ToolInjector (注入额外工具) ---
func (p *SystemInjectionPlugin) InjectTools(ctx *plugin.RequestContext) []format.CoreTool {
    return []format.CoreTool{{
        Name:        "get_current_time",
        Description: "Get the current system time",
        InputSchema: map[string]any{"type": "object"},
    }}
}

// 编译期接口断言
var (
    _ plugin.Plugin           = (*SystemInjectionPlugin)(nil)
    _ plugin.ToolInjector     = (*SystemInjectionPlugin)(nil)
    _ plugin.RequestMutator   = (*SystemInjectionPlugin)(nil)
)
```

### 注册 Demo 插件

```go
// 在 service/app/app.go 的 runTransform() 中：
registry.Register(demo.NewPlugin())
if err := registry.InitAll(&cfg); err != nil {
    return fmt.Errorf("init plugins: %w", err)
}
defer registry.ShutdownAll()
```

之后通过 `registry.CorePluginHooks()` 自动构建 `format.CorePluginHooks` 供 Adapter 使用。
