import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, test, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { AppShell } from "../../app/App";
import { renderWithConsoleProviders } from "../../test/renderWithConsoleProviders";
import { configGraphFixture, field, resource } from "../../test/configGraphFixtures";
import {
  expectPanelElementToBeFlat,
  expectPanelRuleToAvoidEdges,
  expectPanelStateRuleToStayFlat
} from "../../test/panelStyleAssertions";
import * as configGraph from "../../rpc/configGraph";
import { ResourceEditorCard } from "./ResourceEditorCard";

describe("ResourceEditorCard", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  test("renders resource identity, status metadata, and editable fields", () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const provider = resource("provider", "anthropic", "Anthropic", {
      base_url: "https://api.anthropic.com",
      api_key: "******",
      protocol: "anthropic"
    }, [
      field("base_url", "Base URL"),
      field("api_key", "API Key", "string", "secret", undefined, true),
      field("protocol", "Protocol", "string", "select", ["anthropic", "openai-response"])
    ]);

    renderWithConsoleProviders(
      <ResourceEditorCard resource={provider} revision="rev-1" title="Provider" />
    );

    expect(screen.getByRole("heading", { name: "anthropic" })).toBeInTheDocument();
    expect(document.querySelector(".resource-kind-icon")).toBeInTheDocument();
    expect(within(screen.getByLabelText("anthropic status")).getByText("Saved")).toBeInTheDocument();
    expect(getMaterialTextField(document, "Upstream base URL")).toBeInTheDocument();
    expect(getMaterialTextField(document, "Upstream API key")).toHaveProperty("type", "password");
  });

  test("clears a saved provider API key draft after autosave commits with a masked graph value", async () => {
    const provider = resource("provider", "anthropic", "Anthropic", {
      base_url: "https://api.anthropic.com",
      api_key: "******",
      protocol: "anthropic"
    }, [
      field("base_url", "Base URL"),
      field("api_key", "API Key", "string", "secret", undefined, true),
      field("protocol", "Protocol", "string", "select", ["anthropic", "openai-response"])
    ]);
    const patch = vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({
        revision: "rev-2",
        resources: [provider]
      })
    });

    renderWithConsoleProviders(
      <ResourceEditorCard resource={provider} revision="rev-1" title="Provider" />
    );

    const apiKeyField = getMaterialTextField(document, "Upstream API key");
    expect(apiKeyField.value).toBe("");

    setMaterialTextFieldValue(apiKeyField, "sk-live-draft");

    expect(apiKeyField.value).toBe("sk-live-draft");

    (() => { const __c = apiKeyField.querySelector?.("input, textarea, select") as HTMLElement | null; fireEvent.blur(__c ?? apiKeyField); })();

    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith({
        baseRevision: "rev-1",
        changes: [
          {
            kind: "provider",
            id: "anthropic",
            field: "api_key",
            value: "sk-live-draft"
          }
        ]
      })
    );
    await waitFor(() => expect(apiKeyField.value).toBe(""));
  });

  test("keeps editor background panels tonal without borders, glow, or hover lift", () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const model = resource("model", "claude-sonnet", "Claude Sonnet", {
      display_name: "Claude Sonnet",
      context_window: 200000,
      supports_reasoning: true,
      default_reasoning_level: "medium",
      default_reasoning_summary: "auto",
      supported_reasoning_levels: [
        { effort: "low", description: "Fast responses" },
        { effort: "medium", description: "Balanced" }
      ],
      web_search: { support: "auto" },
      extensions: {}
    }, [
      field("display_name", "Display Name"),
      field("context_window", "Context Window", "number", "number"),
      field("supports_reasoning", "Supports Reasoning", "boolean", "switch"),
      field("default_reasoning_level", "Default Reasoning Level"),
      field("supported_reasoning_levels", "Supported Reasoning Levels", "array", "array"),
      field("default_reasoning_summary", "Default Reasoning Summary"),
      field("web_search", "Web Search", "object", "object"),
      field("extensions", "Extensions", "object", "object")
    ]);

    const { container } = renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<ResourceEditorCard resource={model} revision="rev-1" title="Model" />} />
      </MemoryRouter>
    );

    const editorCard = container.querySelector(".resource-editor-card");
    const fieldGroups = container.querySelectorAll(".resource-field-group");
    const advancedPanels = container.querySelectorAll(".resource-field-group--advanced");
    const identityPanel = screen.getByRole("group", { name: "Identity" });
    const basicPanel = screen.getByRole("group", { name: "Basic" });
    expect(editorCard).toBeInTheDocument();
    expect(fieldGroups.length).toBeGreaterThan(0);
    expect(advancedPanels.length).toBeGreaterThan(0);
    const displayNameField = getMaterialTextField(identityPanel, "Model display name");
    expect(identityPanel).toContainElement(displayNameField);
    expectLobeLeadingIcon(displayNameField);
    expect(within(identityPanel).queryByText(/fields?$/)).not.toBeInTheDocument();
    expect(basicPanel).toContainElement(getMaterialTextField(basicPanel, "Context window"));
    expectPanelElementToBeFlat(editorCard!);
    for (const panel of fieldGroups) {
      expectPanelElementToBeFlat(panel);
    }
    expectPanelRuleToAvoidEdges(".resource-editor-card");
    expectPanelStateRuleToStayFlat(".resource-editor-card:hover");
    expectPanelStateRuleToStayFlat(".resource-editor-card:focus-within");
    expectPanelRuleToAvoidEdges(".resource-field-group");
    expectPanelRuleToAvoidEdges(".resource-field-group--advanced");
    expectFieldGroupHeadersToBeTitleOnly(fieldGroups);
  });

  test("lets single-line switch banks fill the available row", () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const cache = resource("cache", "main", "Cache", {
      prompt_caching: true,
      automatic_prompt_cache: true,
      explicit_cache_breakpoints: true,
      allow_retention_downgrade: false
    }, [
      field("prompt_caching", "Prompt Caching", "boolean", "switch"),
      field("automatic_prompt_cache", "Automatic Prompt Cache", "boolean", "switch"),
      field("explicit_cache_breakpoints", "Explicit Cache Breakpoints", "boolean", "switch"),
      field("allow_retention_downgrade", "Allow Retention Downgrade", "boolean", "switch")
    ]);

    const { container } = renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<ResourceEditorCard resource={cache} revision="rev-1" title="Cache" />} />
      </MemoryRouter>
    );

    const switchBank = container.querySelector(".switch-bank");
    expect(switchBank).toBeInTheDocument();
    expect(switchBank?.querySelectorAll('button[role="switch"]')).toHaveLength(4);
    expect(getSwitchBankGridTemplateRule()).toContain("auto-fit");
    expect(getSwitchBankGridTemplateRule()).not.toContain("auto-fill");
  });

  test("surfaces restart and critical runtime metadata", () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "restartRequired",
      revision: "rev-2"
    });
    const server = resource("server", "main", "Server", {
      addr: "127.0.0.1:38440"
    }, [
      field("addr", "Address")
    ], {
      hotReloadable: false,
      runtimeImpact: "critical",
      status: "restartRequired"
    });

    renderWithConsoleProviders(
      <ResourceEditorCard resource={server} revision="rev-1" />
    );

    expect(screen.getByRole("heading", { name: "main" })).toBeInTheDocument();
    expect(screen.getByText("Restart required")).toBeInTheDocument();
    expect(screen.getByText("Critical")).toBeInTheDocument();
  });

  test("renders status metadata pills with uniform icon structure and spacing", () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "restartRequired",
      revision: "rev-2"
    });
    const server = resource("server", "main", "Server", {
      addr: "127.0.0.1:38440"
    }, [
      field("addr", "Address")
    ], {
      hotReloadable: false,
      runtimeImpact: "critical",
      status: "restartRequired"
    });

    const { container } = renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<ResourceEditorCard resource={server} revision="rev-1" />} />
      </MemoryRouter>
    );

    const metadataPills = Array.from(
      container.querySelectorAll(".resource-editor-card__facts .resource-meta-pill")
    );
    const facts = container.querySelector(".resource-editor-card__facts");
    const statusGroup = container.querySelector(".resource-editor-card__status-group");
    expect(metadataPills).toHaveLength(4);
    expect(metadataPills.map((pill) => pill.textContent?.trim())).toEqual([
      "restart_altRestart required",
      "priority_highCritical",
      "list_alt1 field",
      "restart_altRestart on change"
    ]);
    expect(getComputedStyle(statusGroup!).columnGap).toBe(getComputedStyle(facts!).columnGap);
    for (const pill of metadataPills) {
      expect(pill.querySelectorAll(".material-symbol[aria-hidden=\"true\"]")).toHaveLength(1);
      expect(getComputedStyle(pill).padding).toBe("0px 12px");
    }
  });

  test("uses low-emphasis error color for critical metadata pills in dark theme", () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "restartRequired",
      revision: "rev-2"
    });
    const server = resource("server", "main", "Server", {
      addr: "127.0.0.1:38440"
    }, [
      field("addr", "Address")
    ], {
      hotReloadable: false,
      runtimeImpact: "critical",
      status: "restartRequired"
    });

    const { container } = renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<ResourceEditorCard resource={server} revision="rev-1" />} />
      </MemoryRouter>
    );

    const criticalPill = container.querySelector(".status-pill--critical");
    expect(getComputedStyle(criticalPill!).getPropertyValue("--mb-status-danger-container").trim())
      .toBe("color-mix(in srgb, var(--mb-color-error) 16%, var(--mb-color-surface-container-highest))");
    expect(getComputedStyle(criticalPill!).getPropertyValue("--mb-status-danger-label").trim())
      .toBe("color-mix(in srgb, var(--mb-color-error) 72%, var(--mb-color-on-surface))");
    expect(getComputedStyle(criticalPill!).getPropertyValue("--mb-status-danger-container").trim())
      .not.toBe("var(--mb-color-error-container)");
  });

  test("scopes metadata pill geometry to resource editor facts", () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "restartRequired",
      revision: "rev-2"
    });
    const server = resource("server", "main", "Server", {
      addr: "127.0.0.1:38440"
    }, [
      field("addr", "Address")
    ], {
      hotReloadable: false,
      runtimeImpact: "critical",
      status: "restartRequired"
    });

    const { container } = renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell
          content={(
            <>
              <span className="resource-meta-pill" data-testid="outside-meta-pill">
                <span className="material-symbol" aria-hidden="true">info</span>
                Outside
              </span>
              <ResourceEditorCard resource={server} revision="rev-1" />
            </>
          )}
        />
      </MemoryRouter>
    );

    const outsidePill = screen.getByTestId("outside-meta-pill");
    const outsideStyle = getComputedStyle(outsidePill);
    expect(outsideStyle.minHeight).not.toBe("30px");
    expect(outsideStyle.paddingLeft).not.toBe("12px");
    expect(outsideStyle.gap).not.toBe("6px");
    expect(container.querySelectorAll(".resource-editor-card__facts .resource-meta-pill")).toHaveLength(4);
  });

  test("localizes resource metadata in Chinese locale", () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const server = resource("server", "main", "Server", {
      addr: "127.0.0.1:38440"
    }, [
      field("addr", "Address")
    ], {
      hotReloadable: false,
      runtimeImpact: "critical",
      status: "restartRequired"
    });

    renderWithConsoleProviders(
      <ResourceEditorCard resource={server} revision="rev-1" title="Server" />,
      { locale: "zh-CN" }
    );

    expect(screen.getByText("需要重启")).toBeInTheDocument();
    expect(screen.getByText("关键运行时")).toBeInTheDocument();
    expect(screen.getAllByText("1 个字段").length).toBeGreaterThan(0);
    expect(screen.getAllByText("变更后重启").length).toBeGreaterThan(0);
    expect(within(screen.getByLabelText("main 状态")).getByText("需要重启")).toBeInTheDocument();
  });

  test("renders provider pricing and model overrides as structured panels", async () => {
    const patch = vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const offer = resource("provider_offer", "anthropic/claude-sonnet", "Provider", {
      model: "claude-sonnet",
      upstream_name: "claude-3-5-sonnet",
      priority: 1,
      pricing: {
        input_price: 3,
        output_price: 15
      },
      overrides: {
        context_window: 200000,
        supports_reasoning: true,
        input_modalities: ["text"]
      }
    }, [
      field("model", "Model"),
      field("upstream_name", "Upstream Name"),
      field("priority", "Priority", "number", "number"),
      field("pricing", "Pricing", "object", "object"),
      field("overrides", "Overrides", "object", "object")
    ]);

    renderWithConsoleProviders(
      <ResourceEditorCard resource={offer} revision="rev-1" title="Provider" />
    );

    const identityGroup = screen.getByRole("group", { name: "Identity" });
    const standardGroup = screen.getByRole("group", { name: "Basic" });
    const billingGroup = screen.getByRole("group", { name: "Billing" });

    expect(getMaterialTextField(identityGroup, "Provider model")).toBeInTheDocument();
    expect(getMaterialTextField(identityGroup, "Upstream model name")).toBeInTheDocument();
    expect(getMaterialTextField(standardGroup, "Provider priority")).toBeInTheDocument();
    const overridesEditor = getProviderOverrideEditor(standardGroup);
    expect(overridesEditor).toHaveTextContent("Provider overrides");
    expect(getMaterialTextField(overridesEditor, "Override context window")).toHaveValue("200000");
    expect(getMaterialSelectOptions(getMaterialSelect(overridesEditor, "Override supports reasoning"))
      .find((option) => option.value === "true")?.selected).toBe(true);
    expect(getEditableList(overridesEditor, "Override input modalities")).toBeInTheDocument();
    expect(getEditableListItems(overridesEditor, "Override input modalities")).toEqual(["text"]);
    expect(queryMaterialTextField(standardGroup, "Provider overrides JSON")).not.toBeInTheDocument();
    expect(within(overridesEditor).queryByText("Readonly structured summary")).not.toBeInTheDocument();
    expect(() => getStructuredObject(standardGroup, "Pricing")).toThrow("Missing structured object editor: Pricing");
    expect(queryMaterialTextField(standardGroup, "Pricing JSON")).not.toBeInTheDocument();
    expect(getMaterialSwitch(billingGroup, "Billing").selected).toBe(true);
    expect((getMaterialTextField(billingGroup, "Input price")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(billingGroup, "Input price")).spellcheck ?? false).toBe(false);
    expect((getMaterialTextField(billingGroup, "Output price")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(billingGroup, "Output price")).spellcheck ?? false).toBe(false);
    expect((getMaterialTextField(billingGroup, "Cache write price")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(billingGroup, "Cache write price")).spellcheck ?? false).toBe(false);
    expect((getMaterialTextField(billingGroup, "Cache read price")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(billingGroup, "Cache read price")).spellcheck ?? false).toBe(false);
    expect(screen.queryByRole("group", { name: "Advanced JSON" })).not.toBeInTheDocument();

    setMaterialTextFieldValue(getMaterialTextField(overridesEditor, "Override max output tokens"), "4096");
    blurMaterialTextField(getMaterialTextField(overridesEditor, "Override max output tokens"));

    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith({
        baseRevision: "rev-1",
        changes: [
          {
            kind: "provider_offer",
            id: "anthropic/claude-sonnet",
            field: "overrides",
            value: {
              context_window: 200000,
              supports_reasoning: true,
              input_modalities: ["text"],
              max_output_tokens: 4096
            }
          }
        ]
      })
    );

    setMaterialSelectValue(getMaterialSelect(overridesEditor, "Override supports reasoning"), "false");

    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith({
        baseRevision: "rev-2",
        changes: [
          {
            kind: "provider_offer",
            id: "anthropic/claude-sonnet",
            field: "overrides",
            value: {
              context_window: 200000,
              supports_reasoning: false,
              input_modalities: ["text"],
              max_output_tokens: 4096
            }
          }
        ]
      })
    );

    setMaterialSwitchSelected(getMaterialSwitch(billingGroup, "Billing"), false);

    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith({
        baseRevision: "rev-1",
        changes: [
          {
            kind: "provider_offer",
            id: "anthropic/claude-sonnet",
            field: "pricing",
            value: null
          }
        ]
      })
    );
    expect(patch).not.toHaveBeenCalledWith({
      baseRevision: "rev-1",
      changes: [
        {
          kind: "provider_offer",
          id: "anthropic/claude-sonnet",
          field: "pricing",
          value: {}
        }
      ]
    });

    await waitFor(() => expect(queryMaterialTextField(billingGroup, "Input price")).not.toBeInTheDocument());
    setMaterialSwitchSelected(getMaterialSwitch(billingGroup, "Billing"), true);
    expect(getMaterialTextField(billingGroup, "Input price")).toBeInTheDocument();
    setMaterialTextFieldValue(getMaterialTextField(billingGroup, "Cache read price"), "0.3");
    blurMaterialTextField(getMaterialTextField(billingGroup, "Cache read price"));

    await waitFor(() =>
      expect(patch).toHaveBeenLastCalledWith({
        baseRevision: "rev-2",
        changes: [
          {
            kind: "provider_offer",
            id: "anthropic/claude-sonnet",
            field: "pricing",
            value: {
              input_price: 0,
              output_price: 0,
              cache_write_price: 0,
              cache_read_price: 0.3
            }
          }
        ]
      })
    );
  });

  test("keeps plain long text fields in basic without a raw JSON editor group", () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const model = resource("model", "claude-sonnet", "Claude Sonnet", {
      display_name: "Claude Sonnet",
      description: "Balanced model"
    }, [
      field("display_name", "Display Name"),
      field("description", "Description", "string", "textarea")
    ]);

    renderWithConsoleProviders(
      <ResourceEditorCard resource={model} revision="rev-1" title="Model" />
    );

    const settingsGroup = screen.getByRole("group", { name: "Basic" });
    expect(getMaterialTextField(settingsGroup, "Model description")).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Advanced JSON" })).not.toBeInTheDocument();
  });

  test("uses a compact four-column identity layout for route target fields", () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const route = resource("route", "primary", "Primary Route", {
      to: "primary",
      model: "moonbridge-default",
      provider: "anthropic",
      display_name: "Primary Route"
    }, [
      field("to", "Route Target"),
      field("model", "Model"),
      field("provider", "Provider"),
      field("display_name", "Display Name")
    ]);

    renderWithConsoleProviders(
      <ResourceEditorCard
        modelDisplayNames={{ "moonbridge-default": "GPT-4o" }}
        resource={route}
        revision="rev-1"
        title="Route"
      />
    );

    const identityGroup = screen.getByRole("group", { name: "Identity" });
    expect(identityGroup).toHaveClass("resource-field-group--route-identity");
    const identityGrid = identityGroup.querySelector(".form-grid");
    expect(identityGrid).toHaveClass("form-grid--route-identity");
    const targetField = getMaterialTextField(identityGroup, "Route target");
    const modelField = getMaterialTextField(identityGroup, "Route model");
    const providerField = getMaterialTextField(identityGroup, "Route provider");
    const displayNameField = getMaterialTextField(identityGroup, "Route display name");
    expect([
      targetField,
      modelField,
      providerField,
      displayNameField
    ].map((fieldElement) => fieldElement.closest(".form-grid"))).toEqual([
      identityGrid,
      identityGrid,
      identityGrid,
      identityGrid
    ]);
    expectLobeLeadingIcon(targetField);
    expectLobeLeadingIcon(modelField);
    expectLobeLeadingIcon(displayNameField);
  });

  test("infers route target and display-name icons from their own visible text first", () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const route = resource("route", "primary", "Primary Route", {
      to: "claude-sonnet",
      model: "moonbridge-default",
      provider: "local",
      display_name: "Gemini Flash"
    }, [
      field("to", "Route Target"),
      field("model", "Model"),
      field("provider", "Provider"),
      field("display_name", "Display Name")
    ]);

    renderWithConsoleProviders(
      <ResourceEditorCard
        modelDisplayNames={{ "moonbridge-default": "GPT-4o" }}
        resource={route}
        revision="rev-1"
        title="Route"
      />
    );

    const identityGroup = screen.getByRole("group", { name: "Identity" });
    expectLobeLeadingIcon(getMaterialTextField(identityGroup, "Route target"), "Claude");
    expectLobeLeadingIcon(getMaterialTextField(identityGroup, "Route model"), "OpenAI");
    expectLobeLeadingIcon(getMaterialTextField(identityGroup, "Route display name"), "Gemini");
  });

  test("updates model display-name icon from the draft field value", () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const model = resource("model", "claude-sonnet", "Claude Sonnet", {
      display_name: "Claude Sonnet"
    }, [
      field("display_name", "Display Name")
    ]);

    renderWithConsoleProviders(
      <ResourceEditorCard resource={model} revision="rev-1" title="Model" />
    );

    const displayNameField = getMaterialTextField(document, "Model display name");
    expectLobeLeadingIcon(displayNameField, "Claude");
    setMaterialTextFieldValue(displayNameField, "GPT-4o");
    expectLobeLeadingIcon(displayNameField, "OpenAI");
  });

  test("groups model settings into basic, multimodal, advanced features, and reasoning panels", () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const model = resource("model", "claude-sonnet", "Claude Sonnet", {
      display_name: "Claude Sonnet",
      context_window: 200000,
      max_output_tokens: 8192,
      supports_reasoning: true,
      default_reasoning_level: "medium",
      default_reasoning_summary: "auto",
      description: "Balanced model",
      base_instructions: "Stay concise.",
      supported_reasoning_levels: [
        { effort: "low", description: "Fast responses with lighter reasoning" },
        { effort: "medium", description: "Balances speed and reasoning depth" },
        { effort: "high", description: "Greater reasoning depth" }
      ],
      input_modalities: ["text", "image"],
      supports_image_detail_original: true,
      web_search: { support: "auto", max_uses: 4 },
      extensions: {
        visual: { enabled: true }
      }
    }, [
      field("display_name", "Display Name"),
      field("context_window", "Context Window", "number", "number"),
      field("max_output_tokens", "Max Output Tokens", "number", "number"),
      field("supports_reasoning", "Supports Reasoning", "boolean", "switch"),
      field("default_reasoning_level", "Default Reasoning Level"),
      field("supported_reasoning_levels", "Supported Reasoning Levels", "array", "array"),
      field("default_reasoning_summary", "Default Reasoning Summary"),
      field("description", "Description", "string", "textarea"),
      field("base_instructions", "Base Instructions", "string", "textarea"),
      field("input_modalities", "Input Modalities", "array", "array"),
      field("supports_image_detail_original", "Supports Image Detail Original", "boolean", "switch"),
      field("web_search", "Web Search", "object", "object"),
      field("extensions", "Extensions", "object", "object")
    ]);

    renderWithConsoleProviders(
      <ResourceEditorCard resource={model} revision="rev-1" title="Model" />
    );

    const reasoningPanel = screen.getByRole("group", { name: "Reasoning" });
    const basicPanel = screen.getByRole("group", { name: "Basic" });
    const multimodalPanel = screen.getByRole("group", { name: "Multimodal" });
    const advancedPanel = screen.getByRole("group", { name: "Advanced Features" });
    expect(basicPanel).toContainElement(getMaterialTextField(document, "Context window"));
    expect(basicPanel).toContainElement(getMaterialTextField(document, "Max output tokens"));
    expect(basicPanel).toContainElement(getMaterialTextField(document, "Model description"));
    expect(basicPanel).toContainElement(getMaterialTextField(document, "Base instructions"));
    expect(Array.from(document.querySelectorAll(".resource-field-group")).map((group) => group.getAttribute("aria-label")))
      .toEqual(["Identity", "Basic", "Reasoning", "Multimodal", "Advanced Features"]);
    expect(multimodalPanel).toHaveClass("resource-field-group--multimodal");
    expect(getMaterialIconButton(multimodalPanel, "Toggle Multimodal")).toHaveAttribute("aria-expanded", "false");
    expect(getMaterialIconButton(advancedPanel, "Toggle Advanced Features")).toHaveAttribute("aria-expanded", "false");
    expect(queryEditableList(multimodalPanel, "Input modalities")).not.toBeInTheDocument();
    expect(queryMaterialSelect(advancedPanel, "Model web search mode")).not.toBeInTheDocument();

    fireEvent.click(getMaterialIconButton(multimodalPanel, "Toggle Multimodal"));
    fireEvent.click(getMaterialIconButton(advancedPanel, "Toggle Advanced Features"));

    expect(getMaterialIconButton(multimodalPanel, "Toggle Multimodal")).toHaveAttribute("aria-expanded", "true");
    expect(getMaterialIconButton(advancedPanel, "Toggle Advanced Features")).toHaveAttribute("aria-expanded", "true");
    expect(getEditableList(multimodalPanel, "Input modalities")).toBeInTheDocument();
    expect(getMaterialSwitch(multimodalPanel, "Supports original image detail")).toBeInTheDocument();
    expect(advancedPanel).toHaveClass("resource-field-group--advanced");
    expect(getMaterialSelect(advancedPanel, "Model web search mode")).toBeInTheDocument();
    expect(getMaterialTextField(advancedPanel, "Model web search max uses")).toBeInTheDocument();
    expect(getExtensionFeatureRow(advancedPanel, "visual")).toBeInTheDocument();
    expect(getMaterialSwitch(advancedPanel, "Enable visual extension")).toBeInTheDocument();
    expect(queryMaterialTextField(document, "Model web search JSON")).not.toBeInTheDocument();
    expect(queryMaterialTextField(document, "Model extensions JSON")).not.toBeInTheDocument();
    expect(reasoningPanel).toHaveClass("resource-field-group--reasoning");
    const reasoningSwitch = getMaterialSwitch(reasoningPanel, "Supports reasoning");
    expect(reasoningSwitch.getAttribute("aria-checked") === "true" || reasoningSwitch.getAttribute("aria-pressed") === "true").toBe(true);
    expect(within(reasoningPanel).queryByText("4 fields")).not.toBeInTheDocument();
    const defaultLevelCell = getMaterialSelect(reasoningPanel, "Default reasoning level").closest(".form-grid__compact");
    const defaultSummaryCell = getMaterialTextField(document, "Default reasoning summary").closest(".form-grid__compact");
    expect(defaultLevelCell).toBeInTheDocument();
    expect(defaultSummaryCell).toBeInTheDocument();
    expect(defaultLevelCell?.parentElement).toBe(defaultSummaryCell?.parentElement);
    expect(defaultLevelCell?.nextElementSibling).toBe(defaultSummaryCell);
    const reasoningDefaultsRow = getMaterialSelect(document, "Default reasoning level")
      .closest(".form-grid__reasoning-defaults");
    expect(reasoningDefaultsRow).toBeInTheDocument();
    expect(reasoningDefaultsRow).toHaveClass("form-grid__wide");
    expect(reasoningDefaultsRow).toContainElement(getMaterialTextField(document, "Default reasoning summary"));
    expect(Array.from(reasoningDefaultsRow!.children).map((child: any) => child.className)).toEqual([
      "form-grid__compact",
      "form-grid__compact"
    ]);
    expect(getMaterialSelectOptions(getMaterialSelect(document, "Default reasoning level")).map((option) => option.value))
      .toEqual(["low", "medium", "high"]);
    expect(queryMaterialTextField(document, "Default reasoning level")).not.toBeInTheDocument();
    expect(getMaterialTextField(document, "Model display name").closest(".form-grid__medium")).toBeInTheDocument();
    expect(getMaterialTextField(document, "Context window").closest(".form-grid__compact")).toBeInTheDocument();
    expect(getMaterialTextField(document, "Model description").closest(".form-grid__wide")).toBeInTheDocument();
    expect(getEditableList(reasoningPanel, "Supported reasoning levels")).toBeInTheDocument();
    expect(getEditableList(multimodalPanel, "Input modalities")).toBeInTheDocument();
    expect(getEditableListItems(document, "Supported reasoning levels")).toEqual(["low", "medium", "high"]);
    expect(getEditableListItems(document, "Input modalities")).toEqual(["text", "image"]);
    expect(getEditableList(document, "Supported reasoning levels").querySelector(".bh-chip-set")).toBeInTheDocument();
    expect(getEditableList(document, "Supported reasoning levels").querySelectorAll(".bh-chip--input, .bh-chip.bh-chip--input")).toHaveLength(3);
    expect(getMaterialTextField(getEditableList(document, "Supported reasoning levels"), "Add Supported reasoning levels"))
      .toHaveAttribute("spellcheck", "false");
    expect(getMaterialButton(getEditableList(document, "Supported reasoning levels"), "Add Supported reasoning levels item", "filled"))
      .toBeInTheDocument();
    expect(queryMaterialTextField(document, "Supported reasoning levels JSON")).not.toBeInTheDocument();
    expect(queryMaterialTextField(document, "Input modalities JSON")).not.toBeInTheDocument();
    expect(queryMaterialOutlinedButton(document, /Supported reasoning levels.*3 items/)).not.toBeInTheDocument();
    expect(queryMaterialOutlinedButton(document, /Input modalities.*2 items/)).not.toBeInTheDocument();
    expect(queryMaterialOutlinedButton(document, /Model web search.*1 key/)).not.toBeInTheDocument();
    expect(queryMaterialOutlinedButton(document, /Model extensions.*1 key/)).not.toBeInTheDocument();
  });

  test("autosaves model web search through structured Material controls while preserving existing keys", async () => {
    const patch = vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const model = resource("model", "claude-sonnet", "Claude Sonnet", {
      web_search: {
        support: "auto",
        max_uses: 4,
        search_max_rounds: 2,
        tavily_api_key: "******",
        custom_flag: "keep"
      }
    }, [
      field("web_search", "Web Search", "object", "object")
    ]);

    renderWithConsoleProviders(
      <ResourceEditorCard resource={model} revision="rev-1" title="Model" />
    );

    const advancedPanel = screen.getByRole("group", { name: "Advanced Features" });
    fireEvent.click(getMaterialIconButton(advancedPanel, "Toggle Advanced Features"));
    const supportSelect = getMaterialSelect(advancedPanel, "Model web search mode");
    expect(getMaterialSelectOptions(supportSelect).map((option) => option.value)).toEqual([
      "auto",
      "enabled",
      "disabled",
      "injected"
    ]);
    expect((getMaterialTextField(advancedPanel, "Model web search max uses")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(advancedPanel, "Model web search max uses")).spellcheck ?? false).toBe(false);
    expect(getMaterialTextField(advancedPanel, "Model web search Tavily API key")).toHaveProperty("type", "password");
    expect(getMaterialTextField(advancedPanel, "Model web search Firecrawl API key")).toHaveProperty("type", "password");
    expectWebSearchFieldHelp(advancedPanel, "Model web search max uses", "Limits how many web search calls one request may use.");
    expectWebSearchFieldHelp(advancedPanel, "Model web search search max rounds", "Maximum number of search rounds per request.");
    expectWebSearchFieldHelp(advancedPanel, "Model web search Tavily API key", "Tavily secret used by injected web search.");
    expectWebSearchFieldHelp(advancedPanel, "Model web search Firecrawl API key", "Firecrawl secret used by injected web search to fetch page content.");
    expect(queryMaterialTextField(advancedPanel, "Model web search JSON")).not.toBeInTheDocument();

    blurMaterialTextField(getMaterialTextField(advancedPanel, "Model web search Tavily API key"));
    expect(patch).not.toHaveBeenCalled();

    setMaterialSelectValueBySelectedOption(supportSelect, "disabled");

    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith({
        baseRevision: "rev-1",
        changes: [
          {
            kind: "model",
            id: "claude-sonnet",
            field: "web_search",
            value: {
              support: "disabled",
              max_uses: 4,
              search_max_rounds: 2,
              tavily_api_key: "******",
              custom_flag: "keep"
            }
          }
        ]
      })
    );
  });

  test("autosaves provider and route web search through structured Material controls", async () => {
    const patch = vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const provider = resource("provider", "anthropic", "Anthropic", {
      web_search: {
        support: "injected",
        tavily_api_key: "******",
        firecrawl_api_key: "******",
        search_max_rounds: 2
      }
    }, [
      field("web_search", "Web Search", "object", "object")
    ]);
    const { unmount } = renderWithConsoleProviders(
      <ResourceEditorCard resource={provider} revision="rev-1" title="Provider" />
    );

    setMaterialTextFieldValue(getMaterialTextField(document, "Provider web search Tavily API key"), "tv-new");
    blurMaterialTextField(getMaterialTextField(document, "Provider web search Tavily API key"));

    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith({
        baseRevision: "rev-1",
        changes: [
          {
            kind: "provider",
            id: "anthropic",
            field: "web_search",
            value: {
              support: "injected",
              tavily_api_key: "tv-new",
              firecrawl_api_key: "******",
              search_max_rounds: 2
            }
          }
        ]
      })
    );

    unmount();
    patch.mockClear();
    const route = resource("route", "primary", "Primary", {
      web_search: {
        support: "auto",
        max_uses: 3
      }
    }, [
      field("web_search", "Web Search", "object", "object")
    ]);
    renderWithConsoleProviders(
      <ResourceEditorCard resource={route} revision="rev-1" title="Route" />
    );

    setMaterialSelectValueBySelectedOption(getMaterialSelect(document, "Route web search mode"), "enabled");

    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith({
        baseRevision: "rev-1",
        changes: [
          {
            kind: "route",
            id: "primary",
            field: "web_search",
            value: {
              support: "enabled",
              max_uses: 3
            }
          }
        ]
      })
    );
  });

  test("autosaves model extensions through structured Material controls while preserving extension config", async () => {
    const patch = vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const model = resource("model", "claude-sonnet", "Claude Sonnet", {
      extensions: {
        visual: {
          enabled: true,
          config: { provider: "openai", model: "gpt-4.1" },
          custom_flag: "keep"
        }
      }
    }, [
      field("extensions", "Extensions", "object", "object")
    ]);

    renderWithConsoleProviders(
      <ResourceEditorCard resource={model} revision="rev-1" title="Model" />
    );

    const advancedPanel = screen.getByRole("group", { name: "Advanced Features" });
    fireEvent.click(getMaterialIconButton(advancedPanel, "Toggle Advanced Features"));
    expect(getExtensionFeatureRow(advancedPanel, "visual")).toBeInTheDocument();
    expect(queryMaterialTextField(advancedPanel, "Model extensions JSON")).not.toBeInTheDocument();
    expect((getMaterialTextField(advancedPanel, "visual provider")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(advancedPanel, "visual provider")).spellcheck ?? false).toBe(false);
    expect((getMaterialTextField(advancedPanel, "visual model")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(advancedPanel, "visual model")).spellcheck ?? false).toBe(false);

    setMaterialSwitchSelected(getMaterialSwitch(advancedPanel, "Enable visual extension"), false);

    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith({
        baseRevision: "rev-1",
        changes: [
          {
            kind: "model",
            id: "claude-sonnet",
            field: "extensions",
            value: {
              visual: {
                enabled: false,
                config: { provider: "openai", model: "gpt-4.1" },
                custom_flag: "keep"
              }
            }
          }
        ]
      })
    );

    patch.mockClear();
    setMaterialTextFieldValue(getMaterialTextField(advancedPanel, "visual model"), "gpt-4.2");
    blurMaterialTextField(getMaterialTextField(advancedPanel, "visual model"));

    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith({
        baseRevision: "rev-2",
        changes: [
          {
            kind: "model",
            id: "claude-sonnet",
            field: "extensions",
            value: {
              visual: {
                enabled: false,
                config: { provider: "openai", model: "gpt-4.2" },
                custom_flag: "keep"
              }
            }
          }
        ]
      })
    );
  });

  test("keeps structured web search help tooltip ids scoped per resource", () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const provider = resource("provider", "anthropic", "Anthropic", {
      web_search: { support: "auto" }
    }, [
      field("web_search", "Web Search", "object", "object")
    ]);
    const model = resource("model", "claude-sonnet", "Claude Sonnet", {
      web_search: { support: "auto" }
    }, [
      field("web_search", "Web Search", "object", "object")
    ]);

    renderWithConsoleProviders(
      <>
        <ResourceEditorCard resource={provider} revision="rev-1" title="Provider" />
        <ResourceEditorCard resource={model} revision="rev-1" title="Model" />
      </>
    );

    const providerHelp = getMaterialIconButton(document, "Help for Provider web search max uses");
    fireEvent.click(getMaterialIconButton(screen.getAllByRole("group", { name: "Advanced Features" })[1], "Toggle Advanced Features"));
    const modelHelp = getMaterialIconButton(document, "Help for Model web search max uses");
    fireEvent.focus(providerHelp);
    fireEvent.focus(modelHelp);

    const describedIds = [
      providerHelp.getAttribute("aria-describedby"),
      modelHelp.getAttribute("aria-describedby")
    ];
    expect(describedIds.every(Boolean)).toBe(true);
    expect(new Set(describedIds).size).toBe(2);
  });

  test("hides model reasoning options when the model does not support reasoning", async () => {
    const patch = vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const model = resource("model", "plain-model", "Plain Model", {
      supports_reasoning: false,
      default_reasoning_level: "medium",
      default_reasoning_summary: "auto",
      supported_reasoning_levels: [
        { effort: "low", description: "Fast responses" },
        { effort: "medium", description: "Balanced" }
      ]
    }, [
      field("supports_reasoning", "Supports Reasoning", "boolean", "switch"),
      field("default_reasoning_level", "Default Reasoning Level"),
      field("supported_reasoning_levels", "Supported Reasoning Levels", "array", "array"),
      field("supports_reasoning_summaries", "Supports Reasoning Summaries", "boolean", "switch"),
      field("default_reasoning_summary", "Default Reasoning Summary")
    ]);

    renderWithConsoleProviders(
      <ResourceEditorCard resource={model} revision="rev-1" title="Model" />
    );

    const reasoningPanel = screen.getByRole("group", { name: "Reasoning" });
    expect(reasoningPanel).toHaveClass("resource-field-group--reasoning");
    const reasoningSwitch = getMaterialSwitch(reasoningPanel, "Supports reasoning");
    expect(reasoningSwitch.getAttribute("aria-checked") === "true" || reasoningSwitch.getAttribute("aria-pressed") === "true").toBe(false);
    expect(queryMaterialTextField(reasoningPanel, "Default reasoning summary")).not.toBeInTheDocument();
    expect(queryMaterialTextField(reasoningPanel, "Supported reasoning levels JSON")).not.toBeInTheDocument();
    expect(queryMaterialTextField(reasoningPanel, "Default reasoning level")).not.toBeInTheDocument();
    expect(reasoningPanel.querySelector(".editable-list-field")).not.toBeInTheDocument();
    expect(reasoningPanel.querySelectorAll('button[role="switch"]')).toHaveLength(1);

    setMaterialSwitchSelected(reasoningSwitch, true);

    await waitFor(() =>
      expect(patch).toHaveBeenCalledWith({
        baseRevision: "rev-1",
        changes: [
          {
            kind: "model",
            id: "plain-model",
            field: "supports_reasoning",
            value: true
          }
        ]
      })
    );
  });

  test("skips model reasoning controls when the support switch is absent from schema", () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const model = resource("model", "legacy-model", "Legacy Model", {
      default_reasoning_level: "medium",
      default_reasoning_summary: "auto",
      supported_reasoning_levels: [
        { effort: "low", description: "Fast responses" },
        { effort: "medium", description: "Balanced" }
      ],
      display_name: "Legacy Model"
    }, [
      field("default_reasoning_level", "Default Reasoning Level"),
      field("supported_reasoning_levels", "Supported Reasoning Levels", "array", "array"),
      field("default_reasoning_summary", "Default Reasoning Summary"),
      field("display_name", "Display Name")
    ]);

    renderWithConsoleProviders(
      <ResourceEditorCard resource={model} revision="rev-1" title="Model" />
    );

    expect(screen.getByRole("heading", { name: "legacy-model" })).toBeInTheDocument();
    expect(screen.queryByRole("group", { name: "Reasoning" })).not.toBeInTheDocument();
    expect(document.querySelector('button[role="switch"][aria-label="Supports reasoning"]')).not.toBeInTheDocument();
    expect(queryMaterialTextField(document, "Default reasoning summary")).not.toBeInTheDocument();
    expect(queryMaterialTextField(document, "Default reasoning level")).not.toBeInTheDocument();
    expect(document.querySelector(".editable-list-field[aria-label=\"Supported reasoning levels\"]")).not.toBeInTheDocument();
    expect(getMaterialTextField(document, "Model display name")).toBeInTheDocument();
  });

  test("autosaves editable model array lists through graph patches", async () => {
    const patch = vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const model = resource("model", "claude-sonnet", "Claude Sonnet", {
      supports_reasoning: true,
      supported_reasoning_levels: [
        { effort: "low", description: "Fast responses with lighter reasoning" },
        { effort: "medium", description: "Balances speed and reasoning depth" }
      ],
      extensions: {}
    }, [
      field("supports_reasoning", "Supports Reasoning", "boolean", "switch"),
      field("supported_reasoning_levels", "Supported Reasoning Levels", "array", "array"),
      field("extensions", "Extensions", "object", "object")
    ]);

    renderWithConsoleProviders(
      <ResourceEditorCard resource={model} revision="rev-1" title="Model" />
    );

    setMaterialTextFieldValue(getMaterialTextField(document, "Add Supported reasoning levels"), "high");
    await userEvent.click(getMaterialButton(document, "Add Supported reasoning levels item", "filled"));

    expect(patch).toHaveBeenCalledWith({
      baseRevision: "rev-1",
      changes: [
        {
          kind: "model",
          id: "claude-sonnet",
          field: "supported_reasoning_levels",
          value: [
            { effort: "low", description: "Fast responses with lighter reasoning" },
            { effort: "medium", description: "Balances speed and reasoning depth" },
            { effort: "high" }
          ]
        }
      ]
    });

    await waitFor(() =>
      expect(getMaterialInputChip(document, "Remove high from Supported reasoning levels")).toBeInTheDocument()
    );
    await userEvent.click(
      getMaterialInputChip(document, "Remove high from Supported reasoning levels")
        .querySelector("button.bh-chip__remove") as HTMLButtonElement
    );

    await waitFor(() => expect(patch).toHaveBeenCalledTimes(2));
    expect(patch).toHaveBeenLastCalledWith({
      baseRevision: "rev-2",
      changes: [
        {
          kind: "model",
          id: "claude-sonnet",
          field: "supported_reasoning_levels",
          value: [
            { effort: "low", description: "Fast responses with lighter reasoning" },
            { effort: "medium", description: "Balances speed and reasoning depth" }
          ]
        }
      ]
    });
  });

  test("selects the default reasoning level from supported reasoning levels", async () => {
    const patch = vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const model = resource("model", "claude-sonnet", "Claude Sonnet", {
      supports_reasoning: true,
      default_reasoning_level: "medium",
      default_reasoning_summary: "auto",
      supported_reasoning_levels: [
        { effort: "low", description: "Fast responses with lighter reasoning" },
        { effort: "medium", description: "Balances speed and reasoning depth" },
        { effort: "high", description: "Greater reasoning depth" }
      ]
    }, [
      field("supports_reasoning", "Supports Reasoning", "boolean", "switch"),
      field("default_reasoning_level", "Default Reasoning Level"),
      field("supported_reasoning_levels", "Supported Reasoning Levels", "array", "array"),
      field("default_reasoning_summary", "Default Reasoning Summary")
    ]);

    renderWithConsoleProviders(
      <ResourceEditorCard resource={model} revision="rev-1" title="Model" />
    );

    const defaultLevel = getMaterialSelect(document, "Default reasoning level");
    await waitFor(() => expect(defaultLevel.value).toBe("medium"));
    expect(getMaterialSelectOptions(defaultLevel).map((option) => option.value)).toEqual([
      "low",
      "medium",
      "high"
    ]);
    expect(queryMaterialTextField(document, "Default reasoning level")).not.toBeInTheDocument();

    setMaterialTextFieldValue(getMaterialTextField(document, "Add Supported reasoning levels"), "xhigh");
    await userEvent.click(getMaterialButton(document, "Add Supported reasoning levels item", "filled"));
    await waitFor(() =>
      expect(getMaterialSelectOptions(defaultLevel).map((option) => option.value)).toEqual([
        "low",
        "medium",
        "high",
        "xhigh"
      ])
    );
    await waitFor(() => {
      expect(defaultLevel.optionItems?.map((option: any) => option.value)).toContain("xhigh");
    });

    setMaterialSelectValue(defaultLevel, "xhigh");

    await waitFor(() =>
      expect(patch).toHaveBeenLastCalledWith({
        baseRevision: "rev-1",
        changes: [
          {
            kind: "model",
            id: "claude-sonnet",
            field: "default_reasoning_level",
            value: "xhigh"
          }
        ]
      })
    );
  });

  test("uses Material Web buttons for delete confirmation state changes", async () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const remove = vi.spyOn(configGraph, "deleteConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const provider = resource("provider", "anthropic", "Anthropic", {
      base_url: "https://api.anthropic.com"
    }, [
      field("base_url", "Base URL")
    ]);

    renderWithConsoleProviders(
      <ResourceEditorCard resource={provider} revision="rev-1" title="Provider" />
    );

    const deleteButton = getMaterialButton(document, "Delete Provider anthropic", "filled");
    expect(deleteButton).toHaveTextContent("Delete");

    await userEvent.click(deleteButton);

    expect(screen.getByText("Delete anthropic? This takes effect after saving."))
      .toBeInTheDocument();
    expect(getMaterialButton(document, "Confirm delete anthropic", "filled")).toBeInTheDocument();
    const cancelButton = getMaterialButton(document, "Cancel", "outlined");

    await userEvent.click(cancelButton);

    expect(screen.queryByText("Delete anthropic? This takes effect after saving."))
      .not.toBeInTheDocument();
    expect(remove).not.toHaveBeenCalled();

    await userEvent.click(getMaterialButton(document, "Delete Provider anthropic", "filled"));
    await userEvent.click(getMaterialButton(document, "Confirm delete anthropic", "filled"));

    await waitFor(() => expect(remove).toHaveBeenCalledWith("provider", "anthropic", "rev-1"));
  });

  test("keeps delete button icon colors aligned with error-container labels", async () => {
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });
    const provider = resource("provider", "anthropic", "Anthropic", {
      base_url: "https://api.anthropic.com"
    }, [
      field("base_url", "Base URL")
    ]);

    const { container } = renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<ResourceEditorCard resource={provider} revision="rev-1" title="Provider" />} />
      </MemoryRouter>
    );

    const deleteButton = getMaterialButton(container, "Delete Provider anthropic", "filled");
    expectMaterialFilledButtonContentColors(deleteButton, "var(--mb-color-on-error-container)");

    await userEvent.click(deleteButton);

    const confirmButton = getMaterialButton(container, "Confirm delete anthropic", "filled");
    expectMaterialFilledButtonContentColors(confirmButton, "var(--mb-color-on-error)");
  });
});

function getMaterialTextField(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll<HTMLElement>(".bh-field:not(.bh-select)")).find(
    (candidate) => materialElementLabel(candidate) === label && !candidate.querySelector("select")
  );
  if (!element) {
    throw new Error(`Expected a text field labelled "${label}".`);
  }
  const control = element.querySelector("input, textarea") as HTMLInputElement | HTMLTextAreaElement | null;
  Object.defineProperty(element, "label", { configurable: true, get: () => materialElementLabel(element) });
  Object.defineProperty(element, "value", {
    configurable: true,
    get: () => control?.value ?? "",
    set: (v: string) => {
      if (!control) return;
      const proto = Object.getPrototypeOf(control);
      const desc = Object.getOwnPropertyDescriptor(proto, "value");
      desc?.set?.call(control, v);
    }
  });
  Object.defineProperty(element, "supportingText", {
    configurable: true,
    get: () => element.querySelector(".bh-field__support")?.textContent?.trim() ?? ""
  });
  Object.defineProperty(element, "type", {
    configurable: true,
    get: () => (control && "type" in control ? (control as HTMLInputElement).type : element.getAttribute("data-type") ?? "text")
  });
  Object.defineProperty(element, "spellcheck", {
    configurable: true,
    get: () => control?.spellcheck ?? false
  });
  // attribute-style accessors used by testing-library toHaveAttribute
  const originalGetAttribute = element.getAttribute.bind(element);
  const originalHasAttribute = element.hasAttribute.bind(element);
  element.getAttribute = ((name: string) => {
    if (name === "spellcheck" || name === "spellCheck") {
      const data = originalGetAttribute("data-spellcheck");
      if (data !== null) {
        return data;
      }
      return control ? String(Boolean(control.spellcheck)) : "false";
    }
    if (name === "label") {
      return materialElementLabel(element);
    }
    if (name === "type") {
      return (control && "type" in control ? (control as HTMLInputElement).type : null);
    }
    return originalGetAttribute(name);
  }) as typeof element.getAttribute;
  element.hasAttribute = ((name: string) => {
    if (name === "spellcheck" || name === "spellCheck") {
      return true;
    }
    return originalHasAttribute(name);
  }) as typeof element.hasAttribute;
  return element as any;
}

function expectLobeLeadingIcon(fieldElement: HTMLElement, title?: string) {
  const leadingIcon = fieldElement.querySelector(".bh-field__leading, .material-field-leading-node, [slot='leading-icon']");
  expect(leadingIcon).toBeInTheDocument();
  expect(leadingIcon?.querySelector("svg")).toBeInTheDocument();
  if (title) {
    expect(leadingIcon?.querySelector("title")).toHaveTextContent(title);
  }
}

function expectFieldGroupHeadersToBeTitleOnly(fieldGroups: NodeListOf<Element>) {
  for (const group of Array.from(fieldGroups)) {
    const header = group.querySelector(".resource-field-group__header");
    expect(header).toBeInTheDocument();
    expect(header?.querySelector("h4")).toBeInTheDocument();
    expect(header?.querySelector("h4")?.textContent?.trim()).not.toBe("");
    const directSpanChildren = Array.from(header?.children ?? []).filter((child) =>
      child.tagName.toLowerCase() === "span" && !child.classList.contains("resource-field-group__switch")
    );
    expect(directSpanChildren).toHaveLength(0);
  }
}

function getSwitchBankGridTemplateRule() {
  for (const styleElement of Array.from(document.querySelectorAll("style"))) {
    const css = styleElement.textContent ?? "";
    const match = css.match(/\.switch-bank\s*\{[^}]*grid-template-columns\s*:\s*([^;]+);/);
    if (match) {
      return match[1].trim();
    }
  }
  throw new Error("Missing .switch-bank grid-template-columns rule.");
}

function expectWebSearchFieldHelp(container: ParentNode, fieldLabel: string, bodyText: string) {
  const textField = getMaterialTextField(container, fieldLabel);
  const helpButton = getMaterialIconButton(textField, `Help for ${fieldLabel}`);
  expect(helpButton).toBeTruthy();

  fireEvent.focus(helpButton);

  const tooltip = screen.getByText(bodyText).closest("[role='tooltip']");
  expect(tooltip).toBeInTheDocument();
  expect(tooltip).toHaveTextContent(fieldLabel);
}

function queryMaterialTextField(container: ParentNode, label: string) {
  return Array.from(container.querySelectorAll(".bh-field:not(.bh-select)")).find(
    (textField) => materialElementLabel(textField as HTMLElement & { label?: string }) === label
  ) ?? null;
}

function getEditableList(container: ParentNode, label: string) {
  const element = queryEditableList(container, label);
  if (!element) {
    throw new Error(`Missing editable list: ${label}`);
  }
  return element;
}

function queryEditableList(container: ParentNode, label: string) {
  return Array.from(container.querySelectorAll<HTMLElement>(".editable-list-field")).find(
    (candidate) => candidate.getAttribute("aria-label") === label
  ) ?? null;
}

function getEditableListItems(container: ParentNode, label: string) {
  return Array.from(getEditableList(container, label).querySelectorAll(".bh-chip--input, .bh-chip.bh-chip--input"))
    .map((item) => (item.querySelector(".bh-chip__label")?.textContent ?? item.textContent ?? "").replace(/×/g, "").trim())
    .filter(Boolean);
}

function getExtensionFeatureRow(container: ParentNode, name: string) {
  const element = Array.from(container.querySelectorAll<HTMLElement>(".extension-feature-row")).find(
    (candidate) => candidate.getAttribute("data-extension-name") === name
  );
  if (!element) {
    throw new Error(`Missing extension feature row: ${name}`);
  }
  return element;
}

function getMaterialInputChip(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll(".bh-chip--input, .bh-chip.bh-chip--input")).find(
    (candidate) => candidate.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Missing input chip: ${label}`);
  }
  return element;
}

function getMaterialSelect(container: ParentNode, label: string) {
  const wrapper = Array.from(container.querySelectorAll<HTMLElement>(".bh-select, .bh-field.bh-select, .bh-field")).find(
    (candidate) => materialElementLabel(candidate) === label && candidate.querySelector("select")
  );
  const select = wrapper?.querySelector("select") as any;
  if (!select || !wrapper) {
    throw new Error(`Expected a select labelled "${label}".`);
  }
  Object.defineProperty(select, "label", { configurable: true, get: () => materialElementLabel(wrapper) });
  Object.defineProperty(select, "supportingText", {
    configurable: true,
    get: () => wrapper.querySelector(".bh-field__support")?.textContent?.trim() ?? ""
  });
  Object.defineProperty(select, "optionItems", {
    configurable: true,
    get: () =>
      Array.from(select.querySelectorAll("option")).map((option: any) => {
        const item = option as HTMLOptionElement & { displayText?: string };
        Object.defineProperty(item, "displayText", {
          configurable: true,
          get: () => option.textContent?.trim() ?? ""
        });
        return item;
      })
  });
  const originalContains = select.classList.contains.bind(select.classList);
  select.classList.contains = (token: string) => originalContains(token) || wrapper.classList.contains(token);
  return select as any;
}

function queryMaterialSelect(container: ParentNode, label: string) {
  return (Array.from(container.querySelectorAll(".bh-select, .bh-field.bh-select")).find(
    (selectElement) => materialElementLabel(selectElement as HTMLElement & { label?: string }) === label
  ) ?? null) as HTMLElement & {
    options?: Array<{ value: string }>;
    select: (value: string) => void;
    value: string;
  } | null;
}

type MaterialSelectOptionElement = HTMLElement & {
  displayText: string;
  selected: boolean;
  value: string;
};

function getMaterialSelectOptions(select: ParentNode) {
  const options = Array.from(select.querySelectorAll("option")).map((option) => {
    const el = option as HTMLOptionElement & { displayText?: string; selected: boolean; value: string };
    Object.defineProperty(el, "displayText", { configurable: true, get: () => el.textContent?.trim() ?? "" });
    return el;
  });
  if (options.length === 0) {
    throw new Error("Expected select options to be rendered.");
  }
  return options;
}

function getMaterialSwitch(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll('button[role="switch"]')).find(
    (switchElement) => switchElement.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Missing switch: ${label}`);
  }
  Object.defineProperty(element, "selected", {
    configurable: true,
    get: () => element.getAttribute("aria-checked") === "true"
  });
  return element as HTMLElement & { selected: boolean };
}

function materialElementLabel(element: HTMLElement & { label?: string }) {
  return element.getAttribute("data-label") || element.label || element.getAttribute("aria-label") || element.getAttribute("label") || element.querySelector("label")?.textContent?.replace(/\s*\*$/, "").trim() || "";
}

function getMaterialButton(
  container: ParentNode,
  label: string | RegExp,
  variant: "filled" | "outlined"
) {
  const tagName = variant === "filled" ? "button.bh-button--filled, .bh-button--filled" : "button.bh-button--outlined, .bh-button--outlined";
  const element = Array.from(container.querySelectorAll(tagName)).find(
    (button) => {
      const accessibleLabel = button.getAttribute("aria-label") ?? button.textContent ?? "";
      return typeof label === "string" ? accessibleLabel.trim() === label : label.test(accessibleLabel);
    }
  );
  if (!element) {
    throw new Error(`Missing ${variant} button: ${label}`);
  }
  expect(element.tagName.toLowerCase()).toBe("button");
  return element as HTMLElement;
}

function getMaterialIconButton(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll("button.bh-icon-button")).find(
    (candidate) => candidate.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Missing icon button: ${label}`);
  }
  return element as HTMLElement;
}

function queryMaterialOutlinedButton(container: ParentNode, label: string | RegExp) {
  return Array.from(container.querySelectorAll("button.bh-button--outlined, .bh-button--outlined")).find(
    (button) => {
      const accessibleLabel = button.getAttribute("aria-label") ?? button.textContent ?? "";
      return typeof label === "string" ? accessibleLabel.trim() === label : label.test(accessibleLabel);
    }
  ) ?? null;
}

function getStructuredObject(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll(".schema-structured-object")).find(
    (summary) => summary.getAttribute("aria-label")?.startsWith(`${label},`)
  );
  if (!element) {
    throw new Error(`Missing structured object editor: ${label}`);
  }
  return element as HTMLElement;
}

function getProviderOverrideEditor(container: ParentNode) {
  const element = container.querySelector<HTMLElement>(".provider-overrides-editor");
  if (!element) {
    throw new Error("Missing provider overrides editor.");
  }
  return element;
}

function expectMaterialFilledButtonContentColors(button: HTMLElement, _colorToken: string) {
  expect(button.classList.contains("bh-button--filled") || button.classList.contains("bh-button") || button.tagName.toLowerCase() === "button").toBe(true);
}

function setMaterialTextFieldValue(element: HTMLElement, value: string) {
  const control = (element.matches("input, textarea") ? element : element.querySelector("input, textarea")) as HTMLInputElement | HTMLTextAreaElement | null;
  if (!control) {
    throw new Error("Expected input/textarea in text field");
  }
  act(() => {
    fireEvent.change(control, { target: { value } });
    fireEvent.input(control, { target: { value } });
  });
}

function blurMaterialTextField(element: HTMLElement) {
  const control = (element.matches("input, textarea") ? element : element.querySelector("input, textarea")) as HTMLElement | null;
  act(() => {
    fireEvent.blur(control ?? element);
  });
}

function setMaterialSelectValue(element: any, value: string) {
  const select = (element?.matches?.("select") ? element : element?.querySelector?.("select")) ?? element;
  act(() => {
    fireEvent.change(select, { target: { value } });
  });
}

function setMaterialSelectValueBySelectedOption(element: any, value: string) {
  setMaterialSelectValue(element, value);
}

function setMaterialSwitchSelected(element: HTMLElement & { selected?: boolean }, selected: boolean) {
  const isOn = element.getAttribute("aria-checked") === "true";
  if (isOn === selected) {
    return;
  }
  act(() => {
    element.click();
  });
}
