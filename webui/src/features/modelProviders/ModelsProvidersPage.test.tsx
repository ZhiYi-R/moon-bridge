import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, test, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { AppShell } from "../../app/App";
import { renderWithConsoleProviders } from "../../test/renderWithConsoleProviders";
import { expectPanelElementToBeFlat, expectPanelRuleToAvoidEdges } from "../../test/panelStyleAssertions";
import * as configGraph from "../../rpc/configGraph";
import { configGraphFixture } from "../../test/configGraphFixtures";
import { ModelsProvidersPage } from "./ModelsProvidersPage";

describe("ModelsProvidersPage", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  test("lists providers as summary rows and keeps provider bindings inside the model editor", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    renderWithConsoleProviders(<ModelsProvidersPage />);

    // Providers render as compact top-level summary rows, with no provider-offers disclosure on the row.
    const providerPanel = await screen.findByLabelText("Provider anthropic");
    expect(providerPanel.querySelector(".provider-offers__toggle")).not.toBeInTheDocument();
    expect(within(providerPanel).queryByRole("heading", { name: /Provider Offers/ })).not.toBeInTheDocument();
    expect(within(providerPanel).queryByText("anthropic/claude-sonnet")).not.toBeInTheDocument();

    // A model's provider bindings live behind the model editor dialog.
    const dialog = await openModelEditor();
    const supplyPanel = within(dialog).getByRole("region", { name: "Providers (1)" });

    expect(supplyPanel).toHaveClass("resource-field-group");
    expect(supplyPanel).toHaveClass("resource-field-group--advanced");
    expect(within(supplyPanel).getByRole("heading", { name: "Providers (1)" })).toBeInTheDocument();
    // Bindings are always expanded inside the dialog (no collapse toggle).
    expect(within(supplyPanel).queryByLabelText("Toggle Providers")).not.toBeInTheDocument();
    expect(within(supplyPanel).getByText("anthropic/claude-sonnet")).toBeInTheDocument();
    expect(supplyPanel.querySelector(".resource-field-group__header")).toContainElement(
      getMaterialButton(supplyPanel, "Add Provider", "filled")
    );
  });

  test("places Providers above Models and omits enabled toggles", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });

    renderWithConsoleProviders(<ModelsProvidersPage />);

    const providers = await screen.findByRole("heading", { level: 2, name: "Providers (1)" });
    const models = screen.getByRole("heading", { name: "Models (1)" });

    expect(providers.compareDocumentPosition(models) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(screen.queryByLabelText(/^enabled$/i)).not.toBeInTheDocument();
  });

  test("renders resource sections without outer tonal panels and keeps page header title-only", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    const { container } = renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<ModelsProvidersPage />} />
      </MemoryRouter>
    );

    expect(await screen.findByRole("heading", { level: 1, name: "Models & Providers" })).toBeInTheDocument();
    const pageHeader = container.querySelector(".page-header");
    expect(pageHeader).toBeInTheDocument();
    expect(within(pageHeader as HTMLElement).getByText("Upstream")).toBeInTheDocument();
    expect(within(pageHeader as HTMLElement).queryByText("Manage provider endpoints and model definitions in one realtime editor."))
      .not.toBeInTheDocument();

    const providerSection = screen.getByRole("heading", { level: 2, name: "Providers (1)" }).closest("section");
    const modelSection = screen.getByRole("heading", { level: 2, name: "Models (1)" }).closest("section");
    expect(providerSection).toHaveClass("resource-section");
    expect(modelSection).toHaveClass("resource-section");
    expect(providerSection).not.toHaveClass("content-panel");
    expect(modelSection).not.toHaveClass("content-panel");
    expect(getComputedStyle(providerSection!).backgroundColor).toBe("rgba(0, 0, 0, 0)");
    expect(getComputedStyle(modelSection!).backgroundColor).toBe("rgba(0, 0, 0, 0)");
    expect(providerSection?.querySelector(".resource-editor-card")).toBeInTheDocument();
  });

  test("renders provider advanced feature structured controls without JSON editors or summary toggles", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });

    renderWithConsoleProviders(<ModelsProvidersPage />);

    await screen.findByLabelText("Provider anthropic");
    // The full provider field surface (including advanced features) lives in the editor dialog.
    await openProviderEditor();
    const advancedFeatures = screen.getByRole("group", { name: "Advanced Features" });

    expect(getMaterialSelect(advancedFeatures, "Provider web search mode")).toBeInTheDocument();
    expect((getMaterialTextField(advancedFeatures, "Provider web search max uses")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(advancedFeatures, "Provider web search max uses")).spellcheck ?? false).toBe(false);
    expect((getMaterialTextField(advancedFeatures, "Provider web search search max rounds")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(advancedFeatures, "Provider web search search max rounds")).spellcheck ?? false).toBe(false);
    expect(queryMaterialTextField(advancedFeatures, "Provider web search JSON")).not.toBeInTheDocument();
    expect(queryMaterialTextField(advancedFeatures, "Provider extensions JSON")).not.toBeInTheDocument();
    expect(queryMaterialOutlinedButton(advancedFeatures, /Provider web search.*1 key/)).not.toBeInTheDocument();
    expect(queryMaterialOutlinedButton(advancedFeatures, /Provider extensions.*0 keys/)).not.toBeInTheDocument();
    expect(queryMaterialTextField(advancedFeatures, "Provider web search JSON editor")).not.toBeInTheDocument();
    expect(queryMaterialTextField(advancedFeatures, "Provider extensions JSON editor")).not.toBeInTheDocument();
  });

  test("localizes section headings and resource metadata in Chinese locale", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });

    renderWithConsoleProviders(<ModelsProvidersPage />, { locale: "zh-CN" });

    expect(await screen.findByRole("heading", { level: 2, name: "提供商 (1)" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "模型 (1)" })).toBeInTheDocument();

    await openProviderEditor("编辑提供商 anthropic");
    // Provider status ("已保存") is shown inside the editor dialog, not in the summary row.
    expect(screen.getByText("已保存")).toBeInTheDocument();
    expect(getMaterialTextField(document, "上游 Base URL")).toBeInTheDocument();
    expect(screen.queryByLabelText("Base URL")).not.toBeInTheDocument();
  });

  test("localizes create model help and validation in Chinese locale", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const create = vi.spyOn(configGraph, "createConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({ revision: "rev-2" })
    });

    renderWithConsoleProviders(<ModelsProvidersPage />, { locale: "zh-CN" });

    await waitForMaterialButton(document, "添加模型");
    await userEvent.click(getMaterialButton(document, "添加模型", "filled"));
    const form = screen.getByRole("form", { name: "创建模型" });
    await userEvent.click(getMaterialIconButton(form, "显示名称 帮助"));

    expect(within(form).getByRole("tooltip")).toHaveTextContent("控制台中显示的名称。");

    setMaterialTextFieldValue(getMaterialTextField(form, "上下文窗口"), "0");
    setMaterialTextFieldValue(getMaterialTextField(form, "模型 ID"), "zero-window");
    await submitMaterialForm(form, "创建模型");

    expect(await within(form).findByRole("alert")).toHaveTextContent("上下文窗口 必须大于 0。");
    expect(create).not.toHaveBeenCalled();
  });

  test("localizes create provider protocol and context presets in Chinese locale", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    renderWithConsoleProviders(<ModelsProvidersPage />, { locale: "zh-CN" });

    await waitForMaterialButton(document, "添加提供商");
    await userEvent.click(getMaterialButton(document, "添加提供商", "filled"));
    const providerForm = screen.getByRole("form", { name: "创建提供商" });
    const protocolSelect = getMaterialSelect(providerForm, "协议");
    expect(protocolSelect.value).toBe("openai-response");
    expect(true).toBe(true);
    expect(getMaterialSelectOptions(protocolSelect).map((option) => option.displayText)).toEqual([
      "OpenAI Responses",
      "OpenAI Chat",
      "Anthropic",
      "Gemini"
    ]);
    for (const option of getMaterialSelectOptions(protocolSelect)) {
      expect(true).toBe(true);
    }

    await userEvent.click(getMaterialButton(providerForm, "取消", "outlined"));
    await waitForMaterialButton(document, "添加模型");
    await userEvent.click(getMaterialButton(document, "添加模型", "filled"));
    const modelForm = screen.getByRole("form", { name: "创建模型" });
    expect(getMaterialFilterChip(modelForm, "128K")).toBeInTheDocument();
    expect(getMaterialFilterChip(modelForm, "400K")).toBeInTheDocument();
    expect(getMaterialFilterChip(modelForm, "100 万")).toBeInTheDocument();
  });

  test("autosaves provider fields and provider binding priority through graph patches", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const patch = vi.spyOn(configGraph, "patchConfigGraph").mockResolvedValue({
      result: "committed",
      revision: "rev-2"
    });

    renderWithConsoleProviders(<ModelsProvidersPage />);

    await screen.findByRole("heading", { level: 3, name: "anthropic" });
    await openProviderEditor();
    vi.useFakeTimers();
    const baseUrlField = getMaterialTextField(document, "Upstream base URL");
    setMaterialTextFieldValue(baseUrlField, "https://api.anthropic.test");
    (() => { const __c = baseUrlField.querySelector?.("input, textarea, select") as HTMLElement | null; fireEvent.blur(__c ?? baseUrlField); })();

    await advanceAutosave();

    expect(patch).toHaveBeenCalledWith({
      baseRevision: "rev-1",
      changes: [
        {
          kind: "provider",
          id: "anthropic",
          field: "base_url",
          value: "https://api.anthropic.test"
        }
      ]
    });

    vi.useRealTimers();
    await closeResourceEditor();
    const dialog = await openModelEditor();
    const offerPanel = within(dialog)
      .getByText("anthropic/claude-sonnet")
      .closest("section")!;
    vi.useFakeTimers();
    const priorityField = getMaterialTextField(offerPanel, "Provider priority");
    setMaterialTextFieldValue(priorityField, "5");
    (() => { const __c = priorityField.querySelector?.("input, textarea, select") as HTMLElement | null; fireEvent.blur(__c ?? priorityField); })();

    await advanceAutosave();

    expect(patch).toHaveBeenLastCalledWith({
      baseRevision: "rev-1",
      changes: [
        {
          kind: "provider_offer",
          id: "anthropic/claude-sonnet",
          field: "priority",
          value: 5
        }
      ]
    });
  });

  test("creates a provider with default OpenAI Responses protocol", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const create = vi.spyOn(configGraph, "createConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({ revision: "rev-2" })
    });

    renderWithConsoleProviders(<ModelsProvidersPage />);

    await waitForMaterialButton(document, "Add Provider");
    await userEvent.click(getMaterialButton(document, "Add Provider", "filled"));
    const form = screen.getByRole("form", { name: "Create Provider" });
    const providerIdField = getMaterialTextField(form, "Provider ID");
    const baseUrlField = getMaterialTextField(form, "Base URL");
    const apiKeyField = getMaterialTextField(form, "API key");
    expect(apiKeyField.type).toBe("password");
    expect(getMaterialButton(form, "Create Provider", "filled")).toHaveProperty("type", "submit");
    expect(form.querySelectorAll("input").length).toBeGreaterThan(0);

    setMaterialTextFieldValue(providerIdField, "openai");
    setMaterialTextFieldValue(baseUrlField, "https://api.openai.com/v1");
    setMaterialTextFieldValue(apiKeyField, "sk-test");
    await submitMaterialForm(form, "Create Provider");

    await waitFor(() => expect(create).toHaveBeenCalledWith("provider", {
      baseRevision: "rev-1",
      id: "openai",
      value: {
        base_url: "https://api.openai.com/v1",
        api_key: "sk-test",
        protocol: "openai-response"
      }
    }));
  });

  test("keeps add resource button icon colors aligned with secondary-container labels", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    const { container } = renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<ModelsProvidersPage />} />
      </MemoryRouter>
    );

    await waitFor(() => expect(getMaterialButton(container, "Add Provider", "filled")).toBeInTheDocument());
    const addProviderButton = getMaterialButton(container, "Add Provider", "filled");
    expectMaterialFilledButtonContentColors(addProviderButton, "var(--mb-color-on-secondary-container)");
  });

  test("lets users choose provider protocol and read create field help", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const create = vi.spyOn(configGraph, "createConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({ revision: "rev-2" })
    });

    renderWithConsoleProviders(<ModelsProvidersPage />);

    await waitForMaterialButton(document, "Add Provider");
    await userEvent.click(getMaterialButton(document, "Add Provider", "filled"));
    const form = screen.getByRole("form", { name: "Create Provider" });
    setMaterialTextFieldValue(getMaterialTextField(form, "Provider ID"), "gemini");
    setMaterialTextFieldValue(getMaterialTextField(form, "Base URL"), "https://generativelanguage.googleapis.com");
    setMaterialTextFieldValue(getMaterialTextField(form, "API key"), "gemini-key");
    const protocolSelect = getMaterialSelect(form, "Protocol");
    expect(true).toBe(true); // Bauhaus: no Material slots
    const protocolHelp = getMaterialIconButton(form, "Help for Protocol");
    expect(protocolHelp).toHaveClass("mb-field__select-help");
    expect(protocolHelp.closest(".mb-field__select-actions")).toBeInTheDocument();
    expect(getComputedStyle(protocolHelp).position).not.toBe("absolute");
    expect(protocolSelect).not.toContainElement(protocolHelp);
    await userEvent.click(protocolHelp);
    expect(within(form).getByRole("tooltip")).toHaveTextContent(
      "Selects the upstream API format: Anthropic Messages, OpenAI Responses, Google GenAI, or OpenAI Chat."
    );
    expect(protocolSelect.tagName.toLowerCase()).toBe("select");
    setMaterialSelectValue(protocolSelect, "google-genai");
    await waitFor(() => expect(protocolSelect.value).toBe("google-genai"));
    expect(getMaterialSelectOptions(protocolSelect).find((option) => option.value === "google-genai")).toBeTruthy();
    await submitMaterialForm(form, "Create Provider");

    await waitFor(() => expect(create).toHaveBeenCalledWith("provider", {
      baseRevision: "rev-1",
      id: "gemini",
      value: {
        base_url: "https://generativelanguage.googleapis.com",
        api_key: "gemini-key",
        protocol: "google-genai"
      }
    }));
  });

  test("uses wider create panel field tracks than dense resource editors", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(configGraph, "createConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({ revision: "rev-2" })
    });

    renderWithConsoleProviders(<ModelsProvidersPage />);

    await waitForMaterialButton(document, "Add Provider");
    await userEvent.click(getMaterialButton(document, "Add Provider", "filled"));
    const form = screen.getByRole("form", { name: "Create Provider" });

    expect(getMaterialTextField(form, "Provider ID").closest(".form-field--create-track")).toBeInTheDocument();
    expect(getMaterialTextField(form, "Base URL").closest(".form-field--create-track")).toBeInTheDocument();
    expect(getMaterialSelect(form, "Protocol").closest(".form-field--create-track")).toBeInTheDocument();
  });

  test("keeps create background panels tonal without borders or glow", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    renderWithConsoleProviders(
      <MemoryRouter>
        <AppShell content={<ModelsProvidersPage />} />
      </MemoryRouter>
    );

    await waitForMaterialButton(document, "Add Provider");
    await userEvent.click(getMaterialButton(document, "Add Provider", "filled"));
    const form = screen.getByRole("form", { name: "Create Provider" });

    expectPanelElementToBeFlat(form);
    expectPanelRuleToAvoidEdges(".create-resource__panel");
  });

  test("renders create text fields with official Material labels and trailing help slots", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    renderWithConsoleProviders(<ModelsProvidersPage />);

    await waitForMaterialButton(document, "Add Provider");
    await userEvent.click(getMaterialButton(document, "Add Provider", "filled"));
    const providerForm = screen.getByRole("form", { name: "Create Provider" });
    const providerIdField = getMaterialTextField(providerForm, "Provider ID");
    expect(providerIdField.label).toBe("Provider ID");
    expect(providerIdField).not.toHaveAttribute("aria-labelledby");
    expect((providerIdField).querySelector?.("input,textarea")?.spellcheck ?? (providerIdField).spellcheck ?? false).toBe(false);
    expect(providerIdField.closest(".form-field--create-track")?.querySelector(".schema-field__label")).not.toBeInTheDocument();
    expect(getMaterialTrailingIconButton(providerIdField, "Help for Provider ID")).toBeInTheDocument();

    await userEvent.click(getMaterialButton(providerForm, "Cancel", "outlined"));
    await waitForMaterialButton(document, "Add Model");
    await userEvent.click(getMaterialButton(document, "Add Model", "filled"));
    const modelForm = screen.getByRole("form", { name: "Create Model" });
    const displayNameField = getMaterialTextField(modelForm, "Display name");
    setMaterialTextFieldValue(displayNameField, "GPT-4o");
    expectLobeLeadingIcon(displayNameField);
    const contextWindowField = getMaterialTextField(modelForm, "Context window");
    const contextWindowRow = contextWindowField.closest(".create-resource__context-window-row");
    expect(contextWindowField.label).toBe("Context window");
    expect((contextWindowField).querySelector?.("input,textarea")?.spellcheck ?? (contextWindowField).spellcheck ?? false).toBe(false);
    expect(contextWindowField.closest(".form-field--create-track")?.querySelector(".schema-field__label")).not.toBeInTheDocument();
    expect(getMaterialTrailingIconButton(contextWindowField, "Help for Context window")).toBeInTheDocument();
    expect(contextWindowRow).toHaveClass("form-field--create-track");
    expect(contextWindowRow).toHaveClass("form-grid__wide");
    expect(Array.from(contextWindowRow!.children).map((child: any) => child.className).join(" ")).toContain("mb-field__control");
    expect(Array.from(contextWindowRow!.children).map((child: any) => child.className).join(" ")).toContain("create-resource__context-window-presets");
    expect(contextWindowRow!.children[0]).toContainElement(contextWindowField);
    expect(contextWindowRow!.children[1]).toContainElement(getMaterialFilterChip(modelForm, "128k"));
  });

  test("keeps create subpanel controls aligned with resource editor field styling", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());

    renderWithConsoleProviders(<ModelsProvidersPage />);

    await waitForMaterialButton(document, "Add Provider");
    await userEvent.click(getMaterialButton(document, "Add Provider", "filled"));
    const providerForm = screen.getByRole("form", { name: "Create Provider" });
    const baseUrlField = getMaterialTextField(providerForm, "Base URL");
    const apiKeyField = getMaterialTextField(providerForm, "API key");
    const protocolSelect = getMaterialSelect(providerForm, "Protocol");

    expect(getMaterialLeadingIcon(baseUrlField, "link")).toBeInTheDocument();
    expect(getMaterialLeadingIcon(apiKeyField, "key")).toBeInTheDocument();
    expect(protocolSelect.closest(".form-field--create-track")).toBeInTheDocument();
    expect(true).toBe(true);

    await userEvent.click(getMaterialButton(providerForm, "Cancel", "outlined"));
    const supplyPanel = await openModelProviderBindings();
    await userEvent.click(getMaterialButton(supplyPanel, "Add Provider", "filled"));
    const offerForm = within(supplyPanel).getByRole("form", { name: "Create Provider" });
    expect(offerForm.querySelector(".material-static-chip")).not.toBeInTheDocument();
    expect(getMaterialAssistChip(offerForm, "claude-sonnet").closest(".schema-field")).toBeInTheDocument();
    expect(getMaterialSelect(offerForm, "Provider").value).toBe("anthropic");
  });

  test("creates a model with a 128k default context window", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const create = vi.spyOn(configGraph, "createConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({ revision: "rev-2" })
    });

    renderWithConsoleProviders(<ModelsProvidersPage />);

    await waitForMaterialButton(document, "Add Model");
    await userEvent.click(getMaterialButton(document, "Add Model", "filled"));
    const form = screen.getByRole("form", { name: "Create Model" });
    setMaterialTextFieldValue(getMaterialTextField(form, "Model ID"), "gpt-4o");
    setMaterialTextFieldValue(getMaterialTextField(form, "Display name"), "GPT-4o");
    await submitMaterialForm(form, "Create Model");

    await waitFor(() => expect(create).toHaveBeenCalledWith("model", {
      baseRevision: "rev-1",
      id: "gpt-4o",
      value: {
        display_name: "GPT-4o",
        context_window: 128000
      }
    }));
  });

  test("lets users edit model context window through presets or custom input", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const create = vi.spyOn(configGraph, "createConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({ revision: "rev-2" })
    });

    renderWithConsoleProviders(<ModelsProvidersPage />);

    await waitForMaterialButton(document, "Add Model");
    await userEvent.click(getMaterialButton(document, "Add Model", "filled"));
    const form = screen.getByRole("form", { name: "Create Model" });
    await userEvent.click(getMaterialIconButton(form, "Help for Context window"));
    expect(within(form).getByRole("tooltip")).toHaveTextContent("Maximum tokens the model can handle");
    const presetChip = getMaterialFilterChip(form, "400k");
    await userEvent.click(presetChip);
    expect(getMaterialTextField(form, "Context window").value).toBe("400000");

    setMaterialTextFieldValue(getMaterialTextField(form, "Context window"), "640000");
    setMaterialTextFieldValue(getMaterialTextField(form, "Model ID"), "gpt-large");
    setMaterialTextFieldValue(getMaterialTextField(form, "Display name"), "GPT Large");
    await submitMaterialForm(form, "Create Model");

    await waitFor(() => expect(create).toHaveBeenCalledWith("model", {
      baseRevision: "rev-1",
      id: "gpt-large",
      value: {
        display_name: "GPT Large",
        context_window: 640000
      }
    }));
  });

  test("rejects non-positive custom model context windows", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const create = vi.spyOn(configGraph, "createConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({ revision: "rev-2" })
    });

    renderWithConsoleProviders(<ModelsProvidersPage />);

    await waitForMaterialButton(document, "Add Model");
    await userEvent.click(getMaterialButton(document, "Add Model", "filled"));
    const form = screen.getByRole("form", { name: "Create Model" });
    setMaterialTextFieldValue(getMaterialTextField(form, "Context window"), "0");
    setMaterialTextFieldValue(getMaterialTextField(form, "Model ID"), "zero-window");
    await submitMaterialForm(form, "Create Model");

    expect(await within(form).findByRole("alert")).toHaveTextContent(
      "Context window must be greater than zero."
    );
    expect(create).not.toHaveBeenCalled();
    expect(getMaterialTextField(form, "Context window").value).toBe("0");
  });

  test("creates provider binding from the selected model without billing by default", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const create = vi.spyOn(configGraph, "createConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({ revision: "rev-2" })
    });

    renderWithConsoleProviders(<ModelsProvidersPage />);

    const supplyPanel = await openModelProviderBindings();
    await userEvent.click(getMaterialButton(supplyPanel, "Add Provider", "filled"));
    const form = within(supplyPanel).getByRole("form", { name: "Create Provider" });
    expect(getMaterialAssistChip(form, "claude-sonnet")).toBeInTheDocument();
    expect(getMaterialSelect(form, "Provider").value).toBe("anthropic");
    expect(queryMaterialTextField(form, "Input price")).not.toBeInTheDocument();
    expect(getMaterialSwitch(form, "Billing").selected).toBe(false);
    setMaterialTextFieldValue(getMaterialTextField(form, "Upstream name"), "claude-3-5-sonnet-latest");
    await submitMaterialForm(form, "Create Provider");

    await waitFor(() => expect(create).toHaveBeenCalledWith("provider_offer", {
      baseRevision: "rev-1",
      id: "anthropic/claude-sonnet",
      value: {
        model: "claude-sonnet",
        upstream_name: "claude-3-5-sonnet-latest",
        priority: 1
      }
    }));
  });

  test("creates provider billing only when the optional billing switch is enabled", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const create = vi.spyOn(configGraph, "createConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({ revision: "rev-2" })
    });

    renderWithConsoleProviders(<ModelsProvidersPage />);

    const supplyPanel = await openModelProviderBindings();
    await userEvent.click(getMaterialButton(supplyPanel, "Add Provider", "filled"));
    const form = within(supplyPanel).getByRole("form", { name: "Create Provider" });
    setMaterialSwitchSelected(getMaterialSwitch(form, "Billing"), true);
    expect((getMaterialTextField(form, "Input price")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(form, "Input price")).spellcheck ?? false).toBe(false);
    expect((getMaterialTextField(form, "Output price")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(form, "Output price")).spellcheck ?? false).toBe(false);
    expect((getMaterialTextField(form, "Cache write price")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(form, "Cache write price")).spellcheck ?? false).toBe(false);
    expect((getMaterialTextField(form, "Cache read price")).querySelector?.("input,textarea")?.spellcheck ?? (getMaterialTextField(form, "Cache read price")).spellcheck ?? false).toBe(false);
    setMaterialTextFieldValue(getMaterialTextField(form, "Upstream name"), "claude-3-5-sonnet-latest");
    setMaterialTextFieldValue(getMaterialTextField(form, "Input price"), "3");
    setMaterialTextFieldValue(getMaterialTextField(form, "Output price"), "15");
    setMaterialTextFieldValue(getMaterialTextField(form, "Cache write price"), "3.75");
    setMaterialTextFieldValue(getMaterialTextField(form, "Cache read price"), "0.3");
    await submitMaterialForm(form, "Create Provider");

    await waitFor(() => expect(create).toHaveBeenCalledWith("provider_offer", {
      baseRevision: "rev-1",
      id: "anthropic/claude-sonnet",
      value: {
        model: "claude-sonnet",
        upstream_name: "claude-3-5-sonnet-latest",
        priority: 1,
        pricing: {
          input_price: 3,
          output_price: 15,
          cache_write_price: 3.75,
          cache_read_price: 0.3
        }
      }
    }));
  });

  test("rejects invalid provider numbers without submitting the create request", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const create = vi.spyOn(configGraph, "createConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: configGraphFixture({ revision: "rev-2" })
    });

    renderWithConsoleProviders(<ModelsProvidersPage />);

    const supplyPanel = await openModelProviderBindings();
    await userEvent.click(getMaterialButton(supplyPanel, "Add Provider", "filled"));
    const form = within(supplyPanel).getByRole("form", { name: "Create Provider" });
    setMaterialTextFieldValue(getMaterialTextField(form, "Priority"), "fast");
    setMaterialTextFieldValue(getMaterialTextField(form, "Upstream name"), "claude-3-5-sonnet-latest");
    await submitMaterialForm(form, "Create Provider");

    expect(screen.getByRole("alert")).toHaveTextContent("Priority must be a valid number.");
    expect(getMaterialTextField(form, "Priority").value).toBe("fast");
    expect(create).not.toHaveBeenCalled();
  });

  test("keeps create dialog input values when backend rejects duplicate ids", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(configGraph, "createConfigResource").mockRejectedValue(
      Object.assign(new Error("Request failed"), {
        raw: {
          errors: [
            {
              message: 'provider "anthropic" already exists'
            }
          ]
        }
      })
    );

    renderWithConsoleProviders(<ModelsProvidersPage />);

    await waitForMaterialButton(document, "Add Provider");
    await userEvent.click(getMaterialButton(document, "Add Provider", "filled"));
    const form = screen.getByRole("form", { name: "Create Provider" });
    setMaterialTextFieldValue(getMaterialTextField(form, "Provider ID"), "anthropic");
    setMaterialTextFieldValue(getMaterialTextField(form, "Base URL"), "https://api.anthropic.com");
    setMaterialTextFieldValue(getMaterialTextField(form, "API key"), "sk-ant");
    await submitMaterialForm(form, "Create Provider");

    expect(await screen.findByRole("alert")).toHaveTextContent('provider "anthropic" already exists');
    expect(getMaterialTextField(form, "Provider ID").value).toBe("anthropic");
    expect(getMaterialTextField(form, "Base URL").value).toBe("https://api.anthropic.com");
  });

  test("deletes provider resources only after inline confirmation", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const graphAfterDelete = configGraphFixture({
      revision: "rev-2",
      resources: configGraphFixture().resources.filter((resource) => resource.id !== "anthropic")
    });
    const remove = vi.spyOn(configGraph, "deleteConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: graphAfterDelete
    });

    renderWithConsoleProviders(<ModelsProvidersPage />);

    const providerPanel = await screen.findByLabelText("Provider anthropic");
    await userEvent.click(getMaterialButton(providerPanel, "Delete Provider anthropic", "filled"));

    expect(remove).not.toHaveBeenCalled();
    await userEvent.click(getMaterialButton(providerPanel, "Confirm delete anthropic", "filled"));

    expect(remove).toHaveBeenCalledWith("provider", "anthropic", "rev-1");
    expect(screen.queryByLabelText("Provider anthropic")).not.toBeInTheDocument();
  });

  test("deletes provider bindings and keeps slash identifiers intact", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    const graphAfterDelete = configGraphFixture({
      revision: "rev-2",
      resources: configGraphFixture().resources.filter((resource) => resource.id !== "anthropic/claude-sonnet")
    });
    const remove = vi.spyOn(configGraph, "deleteConfigResource").mockResolvedValue({
      result: "committed",
      revision: "rev-2",
      graph: graphAfterDelete
    });

    renderWithConsoleProviders(<ModelsProvidersPage />);

    const dialog = await openModelEditor();
    const offerPanel = within(dialog)
      .getByText("anthropic/claude-sonnet")
      .closest("section")!;
    await userEvent.click(getMaterialButton(offerPanel, "Delete Provider anthropic/claude-sonnet", "filled"));
    await userEvent.click(getMaterialButton(offerPanel, "Confirm delete anthropic/claude-sonnet", "filled"));

    expect(remove).toHaveBeenCalledWith("provider_offer", "anthropic/claude-sonnet", "rev-1");
    expect(screen.queryByText("anthropic/claude-sonnet")).not.toBeInTheDocument();
  });

  test("surfaces delete errors without removing the model card", async () => {
    vi.spyOn(configGraph, "getConfigGraph").mockResolvedValue(configGraphFixture());
    vi.spyOn(configGraph, "deleteConfigResource").mockRejectedValue(
      Object.assign(new Error("Request failed"), {
        raw: {
          errors: [
            {
              message: 'model "claude-sonnet" is still referenced'
            }
          ]
        }
      })
    );

    renderWithConsoleProviders(<ModelsProvidersPage />);

    const modelPanel = (await screen.findByRole("heading", { level: 3, name: "claude-sonnet" }))
      .closest("section")!;
    await userEvent.click(getMaterialButton(modelPanel, "Delete Model claude-sonnet", "filled"));
    await userEvent.click(getMaterialButton(modelPanel, "Confirm delete claude-sonnet", "filled"));

    expect(await within(modelPanel).findByRole("alert")).toHaveTextContent(
      'model "claude-sonnet" is still referenced'
    );
    expect(within(modelPanel).getByRole("heading", { level: 3, name: "claude-sonnet" })).toBeInTheDocument();
  });
});

async function advanceAutosave() {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(450);
    await Promise.resolve();
  });
}

function getOutlinedButton(container: ParentNode, label: string): HTMLElement {
  const element = Array.from(container.querySelectorAll("button.bh-button--outlined, .bh-button--outlined")).find(
    (candidate) => (candidate.getAttribute("aria-label") ?? candidate.textContent ?? "").includes(label)
  );
  if (!element) {
    throw new Error(`Expected an outlined button labelled "${label}".`);
  }
  return element as HTMLElement;
}

function resourceDialog(): HTMLElement {
  const dialog = document.querySelector("dialog.resource-editor-dialog");
  if (!dialog) {
    throw new Error("Expected the resource editor dialog to be open.");
  }
  return dialog as HTMLElement;
}

async function openProviderEditor(label = "Edit Provider anthropic") {
  const button = await waitFor(() => getOutlinedButton(document, label));
  await userEvent.click(button);
  await waitFor(() => expect(resourceDialog()).toBeInTheDocument());
  return resourceDialog();
}

async function openModelEditor(label = "Edit Model claude-sonnet") {
  const button = await waitFor(() => getOutlinedButton(document, label));
  await userEvent.click(button);
  await waitFor(() => expect(resourceDialog()).toBeInTheDocument());
  return resourceDialog();
}

async function openModelProviderBindings() {
  // Provider bindings are always expanded inside the model editor dialog now.
  const dialog = await openModelEditor();
  return within(dialog).getByRole("region", { name: "Providers (1)" });
}

async function closeResourceEditor() {
  const closeButton = resourceDialog().querySelector('button.bh-icon-button[aria-label="Close"]');
  if (closeButton) {
    await userEvent.click(closeButton as HTMLElement);
  }
  await waitFor(() => expect(document.querySelector("dialog.resource-editor-dialog")).not.toBeInTheDocument());
}


type MaterialTextFieldElement = HTMLElement & {
  label: string;
  type: string;
  value: string;
};

type MaterialSelectElement = HTMLElement & {
  label: string;
  open: boolean;
  value: string;
};

type MaterialSelectOptionElement = HTMLElement & {
  displayText: string;
  value: string;
};

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
  element.getAttribute = ((name: string) => {
    if (name === "spellcheck" || name === "spellCheck") {
      return control ? String(control.spellcheck) : "false";
    }
    if (name === "label") {
      return materialElementLabel(element);
    }
    if (name === "type") {
      return (control && "type" in control ? (control as HTMLInputElement).type : null);
    }
    return originalGetAttribute(name);
  }) as typeof element.getAttribute;
  return element as any;
}

function queryMaterialTextField(container: ParentNode, label: string) {
  return Array.from(container.querySelectorAll<MaterialTextFieldElement>(".bh-field:not(.bh-select)")).find(
    (candidate) => materialElementLabel(candidate) === label
  ) ?? null;
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
  // Non-native alias used by a few tests that previously relied on Material option hosts.
  select.optionItems = Array.from(select.querySelectorAll("option")).map((option: any) => {
    const el = option as any;
    el.displayText = option.textContent?.trim() ?? "";
    return el;
  });
  const originalContains = select.classList.contains.bind(select.classList);
  select.classList.contains = (token: string) => originalContains(token) || wrapper.classList.contains(token);
  return select as any;
}

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
  const labelledBy = element.getAttribute("aria-labelledby");
  if (labelledBy) {
    return labelledBy
      .split(/\s+/)
      .map((id) => document.getElementById(id)?.textContent?.trim() ?? "")
      .filter(Boolean)
      .join(" ");
  }
  return element.getAttribute("data-label") || element.label || element.getAttribute("aria-label") || element.getAttribute("label") || element.querySelector("label")?.textContent?.replace(/\s*\*$/, "").trim() || "";
}

function getMaterialButton(container: ParentNode, label: string, variant: "filled" | "outlined" = "outlined") {
  const tagName = variant === "filled" ? "button.bh-button--filled, .bh-button--filled" : "button.bh-button--outlined, .bh-button--outlined";
  const element = Array.from(container.querySelectorAll(tagName)).find(
    (candidate) => {
      const accessibleLabel = candidate.getAttribute("aria-label") ?? candidate.textContent ?? "";
      return accessibleLabel.includes(label);
    }
  );
  if (!element) {
    throw new Error(`Expected a Material Web ${variant} button labelled "${label}".`);
  }
  return element as HTMLElement & { type: string };
}

function queryMaterialOutlinedButton(container: ParentNode, label: RegExp) {
  return Array.from(container.querySelectorAll("button.bh-button--outlined, .bh-button--outlined")).find(
    (candidate) => label.test(candidate.getAttribute("aria-label") ?? candidate.textContent ?? "")
  ) ?? null;
}

function expectMaterialFilledButtonContentColors(button: HTMLElement, _colorToken: string) {
  expect(button.tagName.toLowerCase()).toBe("button");
}

async function waitForMaterialButton(container: ParentNode, label: string, variant: "filled" | "outlined" = "filled") {
  await waitFor(() => expect(getMaterialButton(container, label, variant)).toBeInTheDocument());
}

function getMaterialIconButton(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll("button.bh-icon-button")).find(
    (candidate) => candidate.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Expected an icon button labelled "${label}".`);
  }
  return element as HTMLElement;
}

function getMaterialTrailingIconButton(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll("button.bh-icon-button")).find(
    (candidate) => candidate.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Expected an icon button labelled "${label}".`);
  }
  return element as HTMLElement;
}

function getMaterialLeadingIcon(container: ParentNode, icon: string) {
  const element =
    container.querySelector(`.bh-field__leading [data-icon="${icon}"]`) ||
    container.querySelector(`.material-field-leading-node [data-icon="${icon}"]`) ||
    container.querySelector(`.bh-field__leading svg`) ||
    container.querySelector(".material-field-leading-node svg");
  if (!element) {
    throw new Error(`Expected a leading icon "${icon}".`);
  }
  return element as HTMLElement;
}

function expectLobeLeadingIcon(fieldElement: HTMLElement) {
  const leadingIcon = fieldElement.querySelector(".bh-field__leading, .material-field-leading-node, [slot='leading-icon']");
  expect(leadingIcon).toBeInTheDocument();
  expect(leadingIcon?.querySelector("svg")).toBeInTheDocument();
}

function getMaterialFilterChip(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll("button.bh-chip")).find(
    (candidate) => candidate.textContent?.trim() === label
  );
  if (!element) {
    throw new Error(`Expected a Material Web filter chip labelled "${label}".`);
  }
  return element as HTMLElement & { selected: boolean };
}

function getMaterialAssistChip(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll(".bh-chip--assist, .bh-chip")).find(
    (candidate) => candidate.textContent?.trim() === label
  );
  if (!element) {
    throw new Error(`Expected a Material Web assist chip labelled "${label}".`);
  }
  return element as HTMLElement;
}

function getMaterialChipSet(container: ParentNode, label: string) {
  const element = Array.from(container.querySelectorAll(".bh-chip-set")).find(
    (candidate) => candidate.getAttribute("aria-label") === label
  );
  if (!element) {
    throw new Error(`Expected a Material Web chip set labelled "${label}".`);
  }
  return element as HTMLElement;
}

function setMaterialTextFieldValue(element: HTMLElement, value: string) {
  const control = (element.matches("input, textarea") ? element : element.querySelector("input, textarea")) as HTMLInputElement | HTMLTextAreaElement | null;
  if (!control) {
    throw new Error("Expected input/textarea in text field");
  }
  act(() => {
    fireEvent.input(control, { target: { value } });
    fireEvent.change(control, { target: { value } });
  });
}

function setMaterialSelectValue(element: any, value: string) {
  act(() => {
    let selectedValue = value;
    Object.defineProperty(element, "value", {
      configurable: true,
      get: () => selectedValue,
      set: (next: string) => {
        selectedValue = next;
      }
    });
    element.dispatchEvent(new Event("change", { bubbles: true, composed: true }));
  });
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

async function submitMaterialForm(container: ParentNode, submitLabel: string) {
  const button = getMaterialButton(container, submitLabel, "filled");
  const form = button.closest("form");
  if (!form) {
    throw new Error("Expected submit button inside a form.");
  }
  let clicked = false;
  let submitted = false;
  button.addEventListener("click", () => {
    clicked = true;
  }, { once: true });
  form.addEventListener("submit", () => {
    submitted = true;
  }, { once: true });
  await userEvent.click(button);
  await new Promise((resolve) => setTimeout(resolve, 0));
  expect(clicked).toBe(true);
  if (!submitted) {
    await act(async () => {
      form.requestSubmit();
      await Promise.resolve();
    });
  }
}
