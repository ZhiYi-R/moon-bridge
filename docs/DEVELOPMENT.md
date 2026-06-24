# Development

## 前置要求

- Go 1.25+
- 上游 LLM Provider API Key（可选，用于 E2E 测试）

## 项目结构

```
cmd/
  moonbridge/    # 主入口（二进制）
  cloudflare/    # Cloudflare Worker 入口

internal/
  e2e/                    # 端到端集成测试（协议转换）
  extension/              # 可插拔扩展
    codex/                # Codex 模型目录
    db/                   # 数据库 Provider（SQLite / D1）
    deepseek_v4/          # DeepSeek V4 推理优化
    kimi_workaround/      # Kimi 模型 Tool Call 轮次限制
    metrics/              # 用量指标
    plugin/               # Plugin 接口与注册表
    visual/               # 视觉模型分发（CoreProvider 模式）
    websearch/            # Web Search 编排器
    websearchinjected/    # Web Search 注入模式
  config/                 # YAML 配置加载与校验
  logger/                 # 日志系统（slog 封装）
  modelref/               # 模型引用解析
  session/                # 会话管理
  db/                     # 数据库抽象与注册表
  format/                 # Core 类型 + Adapter 接口 + Registry
  protocol/               # 协议转换层
    anthropic/            # Anthropic Messages Adapter
    cache/                # Prompt 缓存规划
    chat/                 # OpenAI Chat Adapter
    google/               # Google Gemini (GenAI) Adapter
    openai/               # OpenAI Responses Adapter
  service/                # 业务编排层
    api/                  # 管理 REST API（路由在 router.go）
    app/                  # 应用生命周期 + Extension 目录
    e2e/                  # 服务层 E2E 测试
    provider/             # Provider 管理器
    proxy/                # Capture 模式代理
    runtime/              # 运行时上下文
    server/               # HTTP 服务器 + 路由 + 认证
    stats/                # 用量统计
    store/                # 配置持久化
    trace/                # 请求跟踪
```

## 构建

```bash
# 构建二进制
go build -o moonbridge ./cmd/moonbridge

# 构建 Cloudflare Worker（WASM）
go build -o worker.wasm ./cmd/cloudflare
```

## Web Console 开发

Moon Bridge Console 是嵌入到 Go 二进制中的 Vite/React 前端，生产路径为 `/console/`。

```bash
# 安装前端依赖
npm --prefix webui install

# 启动前端开发服务器，访问 http://127.0.0.1:5173/console/
npm --prefix webui run dev

# 前端单元测试、E2E 和生产构建
npm --prefix webui test
npm --prefix webui run e2e
npm --prefix webui run build

# 构建并同步到 Go embed 目录
make webui-build

# 构建带嵌入式 Console 的 Go 二进制
make build-with-webui
```

开发服务器会将 `/api`、`/v1`、`/responses`、`/models` 代理到 `127.0.0.1:38440`。运行预览后端时，需要使用启用了 `persistence.active_provider` 的配置，否则 `/api/v1/` 管理 API 不会注册，Console 会显示 setup/unavailable 状态。

Console 配置页使用配置图 API：

- `GET /api/v1/config/graph`
- `PATCH /api/v1/config/graph`
- `POST /api/v1/config/graph/validate`
- `POST /api/v1/config/resources/{kind}`
- `DELETE /api/v1/config/resources/{kind}/{id}`
- `GET /api/v1/logs/recent`
- `GET /api/v1/logs/stream`

相关测试命令：

```bash
npm --prefix webui test -- configGraph logs
npm --prefix webui run e2e
env GOCACHE=/tmp/moonbridge-go-build GOMODCACHE=/tmp/moonbridge-go-mod go test ./internal/service/api ./internal/service/webui ./internal/service/server
```

生产构建产物不会直接提交 `webui/dist/`；`make webui-build` 会把它复制到 `internal/service/webui/dist/`，该目录由 `go:embed` 打包。

## 运行

```bash
go run ./cmd/moonbridge -config config.yml
```

支持热重载：修改配置后通过管理 API 或重启应用应用更改。

## 常用命令

```bash
# 全量单元测试
go test ./...

# 包级别测试
go test ./internal/protocol/anthropic/...

# E2E 测试（Mock 模式，无需 API Key）
go test ./internal/e2e/... -v -count=1

# 特定 Provider 的 E2E 测试
cd internal/e2e && PROVIDER=deepseek go test -v -count=1 -run TestAnthropicE2E
cd internal/e2e && PROVIDER=gemini go test -v -count=1 -run TestGoogleGenAIE2E

# 使用 Makefile 构建与测试
make build
make build-with-webui
make test
```

## 添加新 Provider Adapter

1. 在 `internal/config/config.go` 中添加协议常量（如 `ProtocolMyAdapter`）
2. 创建 `internal/protocol/<adapter>/` 包，实现 `internal/format/adapter.go` 中的 `ProviderAdapter` 和 `ProviderStreamAdapter` 接口
3. 在 `internal/service/app/app.go` 中注册 Adapter 到 Registry
4. 在 `internal/service/server/adapter_dispatch.go` 中添加协议分支
5. 添加对应的 E2E 测试到 `internal/e2e/`

## 管理 API 开发

管理 API 端点定义在 `internal/service/api/` 中，通过 `NewRouter` 创建路由（`router.go`）。

## 代码约定

- 文件名反映其职责（如 `candidate_routing_test.go`），不使用项目管理编号
- 使用 `log/slog` 进行结构化日志
- 包级配置通过 `internal/config` 统一管理
- 协议转换统一使用 `internal/format` 中的 `CoreRequest` / `CoreResponse` 作为中间表示
