# Moon Bridge 文档

Moon Bridge 是一个 any2any 协议转换与模型路由代理。支持 **4 种入站协议**（OpenAI Responses / OpenAI Chat / Anthropic Messages / Google Gemini）↔ **4 种出站协议** 的任意组合，客户端通过统一入口访问不同协议的上游 LLM Provider，由 Moon Bridge 在中间自动完成协议转换。

## 文档目录

- **架构与设计**：项目整体架构、工作模式、数据流
- **开发约定**：Go 包结构、编码规范、测试准则、配置演进策略
- **API 接口**：对外暴露的 HTTP API 参考（Responses API 端点、模型列举端点）
- **Extension 系统**：Plugin 接口定义、能力类型清单、Server/持久化集成、实现 Demo、注册与生命周期
- **现有 Extension 一览**：deepseek_v4、visual、注入式 Web Search 模块，以及 dev 分支开发中的持久化/metrics 能力说明
- **配置迁移**：旧配置迁移到当前格式的脚本说明

## 快速导航

| 文档 | 说明 |
|------|------|
| [系统架构](architecture.md) | 四层架构、三种运行模式、请求生命周期 |
| [开发约定](development-conventions.md) | 包结构、编码规范、测试、配置演进 |
| [API 接口](api.md) | HTTP 端点、请求/响应格式、错误处理 |
| [Extension 系统](extension-system.md) | Plugin 接口、能力类型、Server/持久化集成、注册流程、Demo 实现 |
| [Extension 一览](extensions-overview.md) | deepseek_v4、visual、注入式 Web Search 模块，以及开发中的持久化/metrics 能力说明 |
| [配置迁移](config-migration.md) | 旧配置迁移脚本使用说明 |
