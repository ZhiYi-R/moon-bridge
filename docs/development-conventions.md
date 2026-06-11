# 开发约定

## 包结构约定

### 目录布局

```mermaid
flowchart TD
  subgraph internal["internal/"]
    direction TB
    config["config/ — 配置加载/校验/Schema"]
    logger["logger/ — 结构化日志(slog封装)"]
    openai_dto["openai_dto/ — 共享 OpenAI DTO"]
    modelref["modelref/ — 模型引用解析"]
    session["session/ — 会话管理"]
    db["db/ — 数据库抽象与注册表"]
    fmt["format/ — Core类型/Registry/Adapter接口"]

    subgraph protocol["protocol/ — 协议转换层"]
      direction TB
      pa["anthropic/ — Anthropic Messages Adapter"]
      pc["cache/ — Prompt 缓存规划"]
      pch["chat/ — OpenAI Chat Adapter"]
      pf["format/ — (遗留层，功能已迁移到 internal/format)"]
      pg["google/ — Google Gemini Adapter"]
      po["openai/ — OpenAI Responses Adapter"]
    end

    subgraph service["service/ — 业务编排层"]
      direction TB
      sa["api/ — 管理 REST API"]
      sapp["app/ — 应用生命周期管理、Extension 目录"]
      se["e2e/ — 服务层 E2E 测试"]
      sp["provider/ — Provider 管理器"]
      spr["proxy/ — Capture 模式代理"]
      srt["runtime/ — 运行时上下文"]
      subgraph srv["server/ — HTTP服务器/路由/认证/Adapter分发"]
        direction TB
        ss["session/ — 会话管理"]
        st["trace/ — 请求跟踪写入"]
        su["usage/ — 用量跟踪"]
      end
      sst["stats/ — 用量统计"]
      str["trace/ — 请求跟踪记录"]
    end

    subgraph extension["extension/ — 可插拔扩展"]
      direction TB
      ec["codex/ — Codex 模型目录"]
      subgraph edb["db/ — 数据库 Provider"]
        es["sqlite/"]
        ed1["d1/"]
      end
      eds["deepseek_v4/ — DeepSeek V4 推理优化"]
      ek["kimi_workaround/ — Kimi tool call 轮次限制"]
      em["metrics/ — 用量指标采集与查询"]
      ep["plugin/ — Plugin 接口+能力接口+注册表"]
      ev["visual/ — 视觉模型分发(CoreProvider模式)"]
      ew["websearch/ — Web Search 编排器"]
      ewi["websearchinjected/ — 注入式搜索插件"]
      ectp["codex_tool_proxy/ — apply_patch 代理扩展"]
      ect["codextool/ — 工具类型定义与工具映射"]
    end

    e2e["e2e/ — 端到端集成测试(协议转换)"]
  end
```

### 依赖方向

```
extension → config, format, protocol
service → config, format, protocol, extension
protocol → config, format
format → config, openai_dto
config, logger, modelref, session, db → （无内部依赖）
```

禁止反向依赖。特别是：

- `extension` 包不能依赖 `service` 包
- `protocol` 包不能依赖 `extension` 包（通过 `format.CorePluginHooks` 函数结构体解耦）
- 基础组件（`config`、`logger`、`modelref`、`session`、`db`）不能依赖 `protocol`、`service` 或 `extension`

### 循环依赖预防策略

插件与协议层通过 `internal/format/adapter.go` 中定义的 `CorePluginHooks` 函数结构体解耦：

1. `extension/plugin/Registry.CorePluginHooks()` 方法串联已注册插件的所有能力，返回 `format.CorePluginHooks`
2. Adapter 和 Server 层接收 `CorePluginHooks` 作为依赖，在请求处理过程中调用对应的 hook 函数
3. 插件通过 `extension/plugin/capabilities.go` 中定义的能力接口（`CoreRequestMutator`、`CoreContentFilter` 等）实现功能，无需直接引用 protocol 或 service 层

## 编码规范

### Go 语言版本

使用 `go 1.25`，利用最新的语言特性。

### 命名规则

- **包名**：全小写，单数形式（`plugin`、`config`）
- **接口名**：行为驱动（`InputPreprocessor`、`ContentFilter`、`DBProvider`）
- **错误变量**：以 `Err` 前缀（`ErrNotFound`）
- **常量**：CamelCase（`ProtocolAnthropic`、`ModeTransform`）

### 包文档

每个包应有包级别文档注释，说明包的职责和使用方式（如 `internal/extension/plugin/plugin.go` 和 `internal/extension/websearchinjected/websearchinjected.go`）。

### 错误处理

- 使用 `fmt.Errorf("context: %w", err)` 包裹错误链
- 定义具名错误类型（`RequestError`、`ProviderError`、`CachePlanError`）
- 错误消息使用中文（项目测试用户为中文用户）

### 日志

- 使用 `internal/logger` 包，基于 `slog`
- 调用 `slog.Info()`, `slog.Warn()`, `slog.Error()`, `slog.Debug()` 或 `slog.Default().With(...)`
- 使用 `With("key", value)` 添加结构化字段，对相关属性群使用 `WithGroup` 或 `slog.Group`
- 日志级别支持：`debug`、`info`、`warn`、`error`

### 配置演进

项目仍在开发中，不需要保留旧配置兼容性。配置结构变更时直接：

1. 更新 `config.example.yml`
2. 更新 `internal/config/config_loader.go` 的 `FileConfig` 和 `LoadFromFileWithOptions()`
3. 更新相关脚本（`scripts/` 目录）
4. 更新 README、`docs/config-migration.md` 和本文档

Extension 专属配置不得再直接加到 core config struct。新增 extension 配置时应由 extension 实现 `plugin.ConfigSpecProvider`，在 spec 中声明 `extensions.<name>.enabled/config` 的 scope、默认值、typed config factory 和校验函数；core config 只保留通用 `extensions` 插槽和 `ExtensionEnabled` / `ExtensionConfig` resolver。

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
- 核心协议层应保持高覆盖率
- 新功能应伴随测试

### 测试模式

- 单元测试：测试单个包，mock 外部依赖
- 端到端测试（`internal/e2e/`）：测试完整请求-响应链路
- 服务层 E2E 测试（`internal/service/e2e/`）：完整 HTTP 请求/响应链路测试

### 测试数据

- 测试数据内联或使用 `testdata/` 目录
- 避免外部网络依赖，mock HTTP 客户端

## Extension 开发约定

- 每个插件放在 `internal/extension/<name>/` 目录
- 插件必须实现 `plugin.Plugin` 接口（Name + Init + Shutdown + EnabledForModel）
- 可按需实现零个或多个能力接口（`CoreRequestMutator`、`CoreContentFilter` 等，定义在 `internal/extension/plugin/capabilities.go`）
- 在 `plugin.go` 的末尾用编译期断言验证接口实现
- 需要配置的 extension 实现 `plugin.ConfigSpecProvider`，配置来自 `extensions.<name>.config`，启用状态通过 `extensions.<name>.enabled` 按 global/provider/model/route 继承解析
- `PluginContext.Config` 接收 typed config，`PluginContext.AppConfig` 提供只读全局配置和 per-model resolver
- 内置 extension 通过 `internal/service/app/extensions.go` 的 catalog 汇总 specs 并创建 `plugin.Registry`
