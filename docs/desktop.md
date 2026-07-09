# Moon Bridge 桌面端（Tauri）

桌面端用 **Tauri 2** 作为原生壳，把现有 **Go `moonbridge` 进程** 作为 sidecar 拉起，窗口导航到本地 Bauhaus Web Console（`http://127.0.0.1:<port>/console/`）。业务逻辑仍在 Go / WebUI 中，桌面壳只负责进程生命周期与窗口。

> 当前文档以 **macOS** 为主；Linux / Windows 可构建但尚未作为一等支持验证。

## 架构

```text
Tauri 主进程 (Rust)
  ├─ 启动 splash（desktop/public/index.html）
  ├─ 启动/复用 moonbridge sidecar
  ├─ 就绪探测：GET /console/ → HTTP 200
  └─ 导航到 http://127.0.0.1:PORT/console/

moonbridge (Go sidecar)
  ├─ 嵌入 WebUI（make webui-build）
  ├─ /api/v1/* 管理 API
  └─ /v1/* 协议代理
```

## 前置依赖

| 工具 | 说明 |
|------|------|
| Go 1.25+ | 编译 sidecar |
| Node.js + npm | WebUI 与 Tauri CLI |
| Rust 1.77+（rustup） | Tauri 主进程 |
| macOS | Xcode Command Line Tools |

```bash
# Rust（bash/zsh）
curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
source "$HOME/.cargo/env"
# fish: source "$HOME/.cargo/env.fish"
```

## 首次运行（冷启动）

```bash
# 安装前端与桌面依赖
make desktop-install-deps
# 或：npm --prefix webui install && npm --prefix desktop install

# 开发：构建 WebUI → 嵌入 → 编译 sidecar → tauri dev
make desktop-dev
```

首次会同时编译 WebUI、Go sidecar 与 Rust，可能需数分钟。

### 常用 Make 目标

| 目标 | 说明 |
|------|------|
| `make desktop-install-deps` | `webui` + `desktop` 的 npm install |
| `make desktop-sidecar` | 构建 WebUI 嵌入 + sidecar 二进制 |
| `make desktop-sidecar-fast` | `SKIP_WEBUI=1`，复用已有 embed dist |
| `make desktop-dev` | sidecar + `tauri dev` |
| `make desktop-dev-fast` | 快速迭代壳层（不重编 WebUI） |
| `make desktop-build` | 完整 release 打包 |
| `make desktop-build-app` | 仅 `.app`（更快本地验证） |
| `make desktop-icons` | 从 SVG 渲染图标并生成 Tauri icons |
| `make desktop-app-install` | 安装到 `/Applications/Moon Bridge.app` |

## 环境变量

| 变量 | 作用 |
|------|------|
| `MOONBRIDGE_CONFIG` | 传给 sidecar 的 `-config` 路径 |
| `MOONBRIDGE_SIDECAR` | 覆盖 sidecar 可执行文件（**必须是已存在的绝对路径**） |
| `SKIP_WEBUI=1` | 构建 sidecar 时跳过 WebUI（需已有 embed dist） |
| `TARGET` / `TAURI_ENV_TARGET_TRIPLE` | 交叉构建时覆盖 sidecar 目标三元组 |

默认配置仍走 Go 侧逻辑：未指定时读取 `$HOME/moonbridge/config.yml`，不存在则创建 starter 配置。

### 安全提示（本地工具）

- 桌面端强制 `-addr 127.0.0.1:…`，不会跟随配置把服务绑到 `0.0.0.0`。
- starter 配置默认可能**无** `auth_token`：本机其他进程可访问管理 API 与密钥。单人开发可接受；共享机器请配置 `server.auth_token`。
- 复用已有 moonbridge 时，桌面端**不会**在退出时结束该进程。

## 端口策略

1. 优先 `127.0.0.1:38440`
2. 若 38440 被占用且 `GET /console/` 返回 **HTTP 200** → **复用**已有实例
3. 否则在 `38440..38471` 扫描空闲端口，用 `-addr` 启动新 sidecar
4. 就绪等待最长 **30 秒**；子进程提前退出会立即报错（并指向日志）

## 日志

- macOS 典型路径：`~/Library/Logs/com.moonbridge.desktop/moonbridge-sidecar.log`（以系统 app log dir 为准）
- 启动失败时 stderr 会打印 `See log: …`

## 打包

```bash
make desktop-build
# 产物在 desktop/src-tauri/target/release/bundle/
# macOS: macos/Moon Bridge.app 、 dmg/…

# 仅 app 包（迭代更快）
make desktop-build-app

# 安装到本机 Applications
make desktop-app-install
```

Sidecar 命名需带目标三元组，由 `desktop/scripts/build-sidecar.sh` 生成，例如：

```text
desktop/src-tauri/binaries/moonbridge-aarch64-apple-darwin
```

`tauri.conf.json` 中为 `externalBin: ["binaries/moonbridge"]`（不含 triple）。

## 图标（矢量）

当前 App 图标源：`desktop/brand/logo-mark.svg`（扁平字形方案 F）。

```bash
make desktop-icons
# 或：npm --prefix desktop run icons
```

## 目录结构

```text
desktop/
  package.json
  public/index.html          # 启动占位页
  scripts/build-sidecar.sh
  scripts/render-icon.mjs
  brand/logo-mark.svg        # 矢量主标
  src-tauri/
    Cargo.toml
    tauri.conf.json
    binaries/                # sidecar 产物（gitignore）
    icons/
    src/
      main.rs
      lib.rs
      sidecar.rs
      port.rs
```

## 故障排查

1. **找不到 sidecar 二进制**  
   `make desktop-sidecar`，确认 `desktop/src-tauri/binaries/moonbridge-<triple>` 存在。

2. **`tauri: command not found`**  
   `npm --prefix desktop install`

3. **WebUI 构建失败 / 缺依赖**  
   `make webui-install` 后重试。

4. **启动超时 / 子进程提前退出**  
   查看 sidecar 日志；检查配置 YAML、端口占用。

5. **UI 不是最新**  
   不要长期使用 `SKIP_WEBUI=1`；完整路径会执行 `webui-build`。

6. **退出后进程仍在**  
   若复用了外部已运行的 moonbridge，不会在退出时杀死它。

## 与 CLI 的关系

| 方式 | 说明 |
|------|------|
| `moonbridge` / `go run ./cmd/moonbridge` | 纯 CLI/服务，浏览器打开 Console |
| 桌面端 | 同一二进制逻辑 + 窗口壳；配置目录默认一致 |

桌面端 **不修改** WebUI 的 `API_BASE`；Console 由 sidecar 同源提供。
