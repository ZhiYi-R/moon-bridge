package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"moonbridge/internal/extension/codex"
	"moonbridge/internal/foundation/config"
	"moonbridge/internal/foundation/logger"
	"moonbridge/internal/service/app"
)

const (
	exitOK          = 0
	exitRuntimeErr  = 1
	exitStartupErr  = 2
	defaultProgName = "moonbridge"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet(defaultProgName, flag.ContinueOnError)
	flags.SetOutput(stderr)

	configPath := flags.String("config", "", "Path to config.yml")
	addr := flags.String("addr", "", "Override server listen address")
	mode := flags.String("mode", "", "Override mode: CaptureAnthropic, CaptureResponse, or Transform")
	printAddr := flags.Bool("print-addr", false, "Print configured listen address and exit")
	printMode := flags.Bool("print-mode", false, "Print configured mode and exit")
	printDefaultModel := flags.Bool("print-default-model", false, "Print configured default model alias and exit")
	printCodexModel := flags.Bool("print-codex-model", false, "Print configured Codex model and exit")
	printClaudeModel := flags.Bool("print-claude-model", false, "Print configured Claude Code model and exit")
	printCodexConfig := flags.String("print-codex-config", "", "Print Codex config.toml for the model alias and exit")
	dumpConfigSchema := flags.Bool("dump-config-schema", false, "Generate config.schema.json alongside config and exit")
		codexBaseURL := flags.String("codex-base-url", "", "Base URL to write in generated Codex config")
	codexHome := flags.String("codex-home", "", "CODEX_HOME directory; when set, writes models_catalog.json there")
	if err := flags.Parse(args); err != nil {
		return exitStartupErr
	}

	var cfg config.Config
	var err error
	resolvedConfigPath, err := config.ResolveConfigPath(*configPath)
	if err != nil {
		writeStartupError(stderr, "配置文件路径解析失败", "", err,
			"设置 XDG_CONFIG_HOME，或使用 -config 明确指定配置文件路径。")
		return exitStartupErr
	}
	if *dumpConfigSchema {
		if err := app.DumpConfigSchema(resolvedConfigPath); err != nil {
			writeStartupError(stderr, "Schema dump 失败", resolvedConfigPath, err)
			return exitStartupErr
		}
		fmt.Fprintln(stdout, resolvedConfigPath)
		return exitOK
	}

	cfg, err = config.LoadFromFile(resolvedConfigPath)
	if err != nil {
		writeStartupError(stderr, "配置文件加载失败", resolvedConfigPath, err,
			"未传 -config 时默认读取 ${XDG_CONFIG_HOME:-$HOME/.config}/moonbridge/config.yml。",
			"检查 YAML 语法、字段拼写和缩进。",
			"确认 provider、routes、developer.proxy 等必填配置都已补齐。",
			"如果是 protocol 字段，Responses 直通请使用 openai-response。")
		return exitStartupErr
	}
	if err := logger.Init(logger.Config{Level: logger.Level(cfg.LogLevel), Format: cfg.LogFormat, Output: stderr}); err != nil {
		writeStartupError(stderr, "日志初始化失败", resolvedConfigPath, err,
			"检查 log.level 和 log.format 是否为支持的取值。")
		return exitStartupErr
	}
	logger.Info("配置已加载", "path", resolvedConfigPath, "mode", cfg.Mode, "addr", cfg.Addr)
	if *mode != "" {
		cfg.Mode = config.Mode(*mode)
		if err := cfg.Validate(); err != nil {
			writeStartupError(stderr, "配置校验失败", resolvedConfigPath, fmt.Errorf("-mode %q: %w", *mode, err),
				"检查 -mode 是否为 Transform、CaptureResponse 或 CaptureAnthropic。",
				"对应模式下的 provider / developer.proxy 配置也必须完整。")
			return exitStartupErr
		}
	}
	if *addr != "" {
		cfg.OverrideAddr(*addr)
	}
	if *printAddr {
		fmt.Fprintln(stdout, cfg.Addr)
		return exitOK
	}
	if *printMode {
		fmt.Fprintln(stdout, cfg.Mode)
		return exitOK
	}
	if *printDefaultModel {
		fmt.Fprintln(stdout, cfg.DefaultModelAlias())
		return exitOK
	}
	if *printCodexModel {
		fmt.Fprintln(stdout, cfg.CodexModel())
		return exitOK
	}
	if *printClaudeModel {
		fmt.Fprintln(stdout, cfg.AnthropicProxy.Model)
		return exitOK
	}
	if *printCodexConfig != "" {
		if err := codex.GenerateConfigToml(stdout, *printCodexConfig, *codexBaseURL, *codexHome, cfg); err != nil {
			writeStartupError(stderr, "生成 Codex 配置失败", resolvedConfigPath, err,
				"确认 -codex-home 目录可写，或去掉 -codex-home 只打印 config.toml。")
			return exitRuntimeErr
		}
		return exitOK
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()

	if err := app.RunServer(ctx, cfg, stderr); err != nil {
		writeStartupError(stderr, "服务运行失败", resolvedConfigPath, err,
			"检查监听地址是否被占用，以及上游 provider 配置是否可用。")
		return exitRuntimeErr
	}
	return exitOK
}

func writeStartupError(output io.Writer, title string, configPath string, err error, hints ...string) {
	fmt.Fprintf(output, "Moon Bridge 启动失败：%s\n", title)
	if configPath != "" {
		fmt.Fprintf(output, "配置文件: %s\n", configPath)
	}
	fmt.Fprintln(output, "错误详情:")
	for i, msg := range errorChain(err) {
		fmt.Fprintf(output, "  %d. %s\n", i+1, msg)
	}
	if len(hints) == 0 {
		return
	}
	fmt.Fprintln(output, "处理建议:")
	for _, hint := range hints {
		fmt.Fprintf(output, "  - %s\n", hint)
	}
}

func errorChain(err error) []string {
	if err == nil {
		return []string{"<nil>"}
	}
	var messages []string
	for current := err; current != nil; current = errors.Unwrap(current) {
		messages = append(messages, current.Error())
	}
	return messages
}
