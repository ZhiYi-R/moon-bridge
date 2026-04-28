# 开发约定

## 包结构约定

### 目录布局

```
internal/
├── extension/         # 可插拔扩展（插件 + 工具集）
│   ├── codex/         # Codex CLI 兼容性（编解码/目录/流适配）
│   ├── deepseek_v4/   # DeepSeek V4 扩展插件
│   ├── plugin/        # Plugin 接口定义 + 注册表 + 能力类型
│   ├── pluginhooks/   # Plugin → bridge.PluginHooks 适配器
│   ├── websearch/     # Web Search 核心（Tavily/Firecrawl 客户端 + 编排器）
│   └── websearchinjected/  # 注入式 Web Search 插件
├── foundation/        # 基础组件（无服务层依赖）
│   ├── config/        # 配置加载/校验
│   ├── logger/        # 缓冲日志系统
│   ├── modelref/      # 模型引用解析
│   ├── openai/        # 共享 OpenAI DTO
│   └── session/       # 会话管理
├── protocol/          # 协议层（依赖 foundation）
│   ├── anthropic/     # Anthropic API 客户端 + 类型
│   ├── bridge/        # OpenAI ↔ Anthropic 协议转换核心
│   └── cache/         # 缓存规划引擎
└── service/           # 服务层（组装各组件）
    ├── app/           # 应用入口（RunServer）
    ├── e2e/           # 端到端测试
    ├── provider/      # 多提供商管理
    ├── proxy/         # Capture 模式代理
    ├── server/        # HTTP 服务器 + 请求处理
    ├── stats/         # 用量统计
    └── trace/         # 请求跟踪
```

### 依赖方向

```
extension → foundation, protocol
service → foundation, protocol, extension
protocol → foundation
foundation → （无内部依赖）
```

禁止反向依赖。特别是：

- `extension` 包不能依赖 `service` 包
- `protocol` 包不能依赖 `extension` 包（通过 `PluginHooks` 函数结构体解耦）
- `foundation` 包不能依赖 `protocol` 或 `service`

### 循环依赖预防策略

当 `protocol/bridge` 需要调用 `extension/plugin` 的功能时，不直接引用 plugin 包，而是：

1. `bridge` 包定义 `PluginHooks` 函数结构体（见 `internal/protocol/bridge/hooks.go`）
2. `pluginhooks` 包实现 `PluginHooksFromRegistry()` 适配函数
3. Server 层在初始化时调用适配函数，将 `*plugin.Registry` 转换为 `bridge.PluginHooks`

## 编码规范

### Go 语言版本

使用 `go 1.25`，利用最新的语言特性。

### 命名规则

- **包名**：全小写，单数形式（`plugin`、`config`、`bridge`）
- **接口名**：行为驱动（`InputPreprocessor`、`ContentFilter`、`ProviderWrapper`）
- **错误变量**：以 `Err` 前缀（`ErrNotFound`）
- **常量**：CamelCase（`ProtocolAnthropic`、`ModeTransform`）

### 包文档

每个包应有包级别文档注释，说明包的职责和使用方式（如 `internal/extension/plugin/plugin.go` 和 `internal/extension/websearchinjected/websearchinjected.go`）。

### 错误处理

- 使用 `fmt.Errorf("context: %w", err)` 包裹错误链
- 定义具名错误类型（`RequestError`、`ProviderError`、`CachePlanError`）
- 错误消息使用中文（项目测试用户为中文用户）

### 日志

- 使用 `internal/foundation/logger` 包，基于 `slog`
- 调用 `logger.Info()`, `logger.Warn()`, `logger.Error()`, `logger.Debug()`
- 使用 `With("key", value)` 添加结构化字段
- 日志级别支持：`debug`、`info`、`warn`、`error`

### 配置演进

项目仍在开发中，不需要保留旧配置兼容性。配置结构变更时直接：

1. 更新 `config.example.yml`
2. 更新 `internal/foundation/config/config_loader.go` 的 `FileConfig` 和 `FromFileConfig()`
3. 更新相关脚本（`scripts/` 目录）
4. 更新本文档

### Makefile

| 命令 | 说明 |
|------|------|
| `make build` | 编译所有包 |
| `make test` | 运行所有测试 |
| `make cover` | 查看覆盖率 |
| `make cover-check` | 检查强制包覆盖率 ≥95%（当前强制包：`internal/extension/plugin`） |

## 测试准则

### 覆盖目标

- `internal/extension/plugin` 强制覆盖率 ≥95%
- 核心协议层（`bridge`、`cache`）应保持高覆盖率
- 新功能应伴随测试

### 测试模式

- 单元测试：测试单个包，mock 外部依赖
- 端到端测试（`internal/service/e2e/`）：测试完整请求-响应链路

### 测试数据

- 测试数据内联或使用 `testdata/` 目录
- 避免外部网络依赖，mock HTTP 客户端

## Extension 开发约定

- 每个插件放在 `internal/extension/<name>/` 目录
- 插件必须实现 `plugin.Plugin` 接口（Name + Init + Shutdown + EnabledForModel）
- 可按需实现零个或多个能力接口（ToolInjector、ContentFilter 等）
- 在 `plugin.go` 的末尾用编译期断言验证接口实现
- 插件的 `Init()` 通过 `PluginContext` 接收配置（来自 `config.yml` 的 `plugins.<name>`）
- 通过 `plugin.Registry` 注册，在 `service/app/app.go` 中 `runTransform()` 中注册
