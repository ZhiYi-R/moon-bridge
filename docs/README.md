# Moon Bridge 文档

Moon Bridge 是一个将 OpenAI Responses API（Codex CLI 原生协议）转换为 Anthropic Messages API 请求的透明代理服务器。它使 Codex CLI 用户可以接入任何兼容 Anthropic API 的 LLM 提供商，同时保留完整的响应流式传输、缓存、工具调用和 Web Search 能力。

## 文档目录

- **架构与设计**：项目整体架构、工作模式、数据流
- **开发约定**：Go 包结构、编码规范、测试准则、配置演进策略
- **API 接口**：对外暴露的 HTTP API 参考（Responses API 端点、模型列举端点）
- **Extension 系统**：Plugin 接口定义、能力类型清单、实现 Demo、注册与生命周期
- **现有 Extension 一览**：deepseek_v4、web_search_injected 的详细说明

## 快速导航

| 文档 | 说明 |
|------|------|
| [系统架构](architecture.md) | 三层架构、三种运行模式、请求生命周期 |
| [开发约定](development-conventions.md) | 包结构、编码规范、测试、配置演进 |
| [API 接口](api.md) | HTML 端点、请求/响应格式、错误处理 |
| [Extension 系统](extension-system.md) | Plugin 接口、能力类型、注册流程、Demo 实现 |
| [Extension 一览](extensions-overview.md) | deepseek_v4、web_search_injected 详解 |
