# 配置迁移

Moon Bridge 还没有公开发布，配置结构变更时会直接切到当前格式，不在运行时保留旧字段别名。旧配置请用 `scripts/migrate_config.py` 做一次性迁移，然后按新结构维护。

## 使用方式

```bash
python scripts/migrate_config.py --dry-run config.yml
python scripts/migrate_config.py config.yml
python scripts/migrate_config.py old.yml new.yml
```

脚本会保留 YAML 注释和引号，默认原地覆盖输入文件。先跑 `--dry-run` 检查输出，再覆盖真实配置。

## Provider 模型与路由

旧格式把客户端别名放在 `provider.providers.<key>.models` 下面，并用 `name` 写上游模型名：

```yaml
provider:
  providers:
    deepseek:
      models:
        moonbridge:
          name: deepseek-v4-pro
```

当前格式要求 Provider 模型目录以真实上游模型名为 key，客户端别名单独放到 `provider.routes`：

```yaml
provider:
  providers:
    deepseek:
      models:
        deepseek-v4-pro: {}
  routes:
    moonbridge:
      to: "deepseek/deepseek-v4-pro"
```

## DeepSeek V4

旧的 `provider.deepseek_v4: true`、`provider.providers.<key>.deepseek_v4: true` 或模型级 `deepseek_v4: true` 会迁移到统一 extension 插槽：

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

## Visual

旧的 `provider.visual: true`、`provider.providers.<key>.visual: true`、模型级 `visual: true` / `enable_visual_extension: true` 会迁移到两层新配置：

```yaml
extensions:
  visual:
    config:
      provider: kimi
      model: kimi-for-coding
      max_rounds: 4
      max_tokens: 2048
provider:
  providers:
    deepseek:
      models:
        deepseek-v4-pro:
          extensions:
            visual:
              enabled: true
    kimi:
      base_url: "https://api.moonshot.cn/anthropic"
      api_key: "replace-with-kimi-api-key"
      models:
        kimi-for-coding: {}
```

迁移脚本会把 `provider.providers.<key>.visual: true` 下推到该 provider 的所有模型，并把旧的模型级 `visual: true` / `enable_visual_extension: true` 改为 `extensions.visual.enabled: true`。旧的全局 `provider.visual: true` 会下推到所有非 Kimi 的 Anthropic 模型，Kimi/Moonshot provider 只作为视觉 provider 使用。如果配置里已有 Kimi/Moonshot provider，会自动填 `extensions.visual.config.provider` 和 `extensions.visual.config.model`；无法推断时会打印 warning，需要手动补齐。

Visual 只支持 Anthropic-routed 主模型和 Anthropic-compatible 视觉 provider。`protocol: "openai-response"` 的模型不能设置 `extensions.visual.enabled: true`，也不能作为 `extensions.visual.config.provider`。
