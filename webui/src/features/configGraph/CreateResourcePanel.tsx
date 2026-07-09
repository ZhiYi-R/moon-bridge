import { useId, useRef, useState, type FormEvent, type ReactNode } from "react";
import { configDescriptions, type ConfigPath } from "../../configDocs/configDescriptions";
import type { ConfigGraph } from "../../rpc/types";
import { useI18n } from "../../i18n/I18nProvider";
import { MaterialFilledButton, MaterialIconButton, MaterialOutlinedButton } from "../../components/MaterialButton";
import { MaterialAssistChip, MaterialFilterChip } from "../../components/MaterialFilterChip";
import { ChipSet } from "../../components/ui/Chip";
import { MaterialSelect, type MaterialSelectOption } from "../../components/MaterialSelect";
import { MaterialOutlinedTextField } from "../../components/MaterialTextField";
import type { MessageKey } from "../../i18n/messages";
import { MaterialSwitch } from "../../components/MaterialSwitch";
import { useCreateConfigResource } from "./useConfigGraph";
import { useAnchoredTooltipPosition } from "./helpTooltipPosition";
import { modelIconForName, modelSelectOptions, protocolIconForValue } from "./modelProviderIcons";

type CreatableKind = "provider" | "model" | "provider_offer" | "route" | "extension";

type CreateResourcePanelProps = {
  availableExtensionIds?: string[];
  graph: ConfigGraph;
  kind: CreatableKind;
  modelId?: string;
  providerId?: string;
};

type FormValues = {
  id: string;
  baseUrl: string;
  apiKey: string;
  protocol: string;
  displayName: string;
  contextWindow: string;
  model: string;
  provider: string;
  upstreamName: string;
  priority: string;
  inputPrice: string;
  outputPrice: string;
  cacheWritePrice: string;
  cacheReadPrice: string;
  billingEnabled: boolean;
  enabled: boolean;
};

const initialValues: FormValues = {
  id: "",
  baseUrl: "",
  apiKey: "",
  protocol: "openai-response",
  displayName: "",
  contextWindow: "128000",
  model: "",
  provider: "",
  upstreamName: "",
  priority: "1",
  inputPrice: "0",
  outputPrice: "0",
  cacheWritePrice: "0",
  cacheReadPrice: "0",
  billingEnabled: false,
  enabled: true
};

const createTextKeys: Record<CreatableKind, { add: MessageKey; submit: MessageKey; title: MessageKey }> = {
  extension: {
    add: "create.extension.add",
    submit: "create.extension.submit",
    title: "create.extension.title"
  },
  model: {
    add: "create.model.add",
    submit: "create.model.submit",
    title: "create.model.title"
  },
  provider: {
    add: "create.provider.add",
    submit: "create.provider.submit",
    title: "create.provider.title"
  },
  provider_offer: {
    add: "create.offer.add",
    submit: "create.offer.submit",
    title: "create.offer.title"
  },
  route: {
    add: "create.route.add",
    submit: "create.route.submit",
    title: "create.route.title"
  }
};

export function CreateResourcePanel({
  availableExtensionIds,
  graph,
  kind,
  modelId,
  providerId
}: CreateResourcePanelProps) {
  const { t } = useI18n();
  const create = useCreateConfigResource();
  const extensionIds = availableExtensionIds ?? [];
  const extensionCreateDisabled = kind === "extension" && extensionIds.length === 0;
  const [open, setOpen] = useState(false);
  const [values, setValues] = useState<FormValues>(() => defaultValues(kind, graph, providerId, modelId, extensionIds));
  const [error, setError] = useState("");

  const title = t(createTextKeys[kind].title);
  const addLabel = t(createTextKeys[kind].add);
  const submitLabel = t(createTextKeys[kind].submit);

  function openPanel() {
    setValues(defaultValues(kind, graph, providerId, modelId, extensionIds));
    setError("");
    setOpen(true);
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setError("");
    const draft = createResourceDraft(
      kind,
      values,
      {
        cacheReadPrice: t("create.offer.cacheReadPrice"),
        cacheWritePrice: t("create.offer.cacheWritePrice"),
        contextWindow: t("create.model.contextWindow"),
        inputPrice: t("create.offer.inputPrice"),
        outputPrice: t("create.offer.outputPrice"),
        priority: t("create.offer.priority")
      },
      (field) => t("create.invalidNumber", { field }),
      (field) => t("create.positiveNumber", { field })
    );
    if (!draft.ok) {
      setError(draft.error);
      return;
    }
    try {
      await create.mutateAsync({
        kind,
        body: {
          baseRevision: graph.revision,
          id: draft.id,
          value: draft.value
        }
      });
      setOpen(false);
      setValues(defaultValues(kind, graph, providerId, modelId, extensionIds));
    } catch (cause) {
      setError(errorMessage(cause, t("error.requestFailed")));
    }
  }

  return (
    <div className="create-resource">
      <MaterialFilledButton
        type="button"
        className="create-resource__add"
        disabled={extensionCreateDisabled}
        onClick={openPanel}
        icon="add"
      >
        {addLabel}
      </MaterialFilledButton>
      {open ? (
        <form className="create-resource__panel" aria-label={title} onSubmit={submit}>
          <div className="create-resource__header">
            <h3>{title}</h3>
            <MaterialIconButton
              className="icon-button"
              icon="close"
              label={t("create.close")}
              onClick={() => setOpen(false)}
            />
          </div>
          {error ? (
            <p className="field-error" role="alert">
              {error}
            </p>
          ) : null}
          <div className="form-grid create-resource__fields">
            <CreateFields
              graph={graph}
              kind={kind}
              availableExtensionIds={extensionIds}
              values={values}
              modelId={modelId}
              providerId={providerId}
              setValues={setValues}
            />
          </div>
          <div className="form-actions">
            <MaterialFilledButton type="submit" disabled={create.isPending}>
              {submitLabel}
            </MaterialFilledButton>
            <MaterialOutlinedButton className="secondary-button" onClick={() => setOpen(false)}>
              {t("create.cancel")}
            </MaterialOutlinedButton>
          </div>
        </form>
      ) : null}
    </div>
  );
}

function CreateFields({
  availableExtensionIds = [],
  graph,
  kind,
  modelId,
  providerId,
  values,
  setValues
}: {
  availableExtensionIds?: string[];
  graph: ConfigGraph;
  kind: CreatableKind;
  modelId?: string;
  providerId?: string;
  values: FormValues;
  setValues: (values: FormValues) => void;
}) {
  const { locale, t } = useI18n();
  const models = graph.resources.filter((resource) => resource.kind === "model");
  const providers = graph.resources.filter((resource) => resource.kind === "provider");
  const fieldHelp = (docPath: ConfigPath, fallbackKey: MessageKey) =>
    configDescriptions[docPath]?.description[locale] ?? t(fallbackKey);

  if (kind === "provider") {
    return (
      <>
        <TextInput
          helpText={fieldHelp("providers.<key>.key", "create.help.providerId")}
          label={t("create.provider.id")}
          path="key"
          value={values.id}
          onChange={(id) => setValues({ ...values, id })}
        />
        <TextInput
          helpText={fieldHelp("providers.<key>.base_url", "create.help.providerBaseUrl")}
          label={t("create.provider.baseUrl")}
          path="base_url"
          value={values.baseUrl}
          onChange={(baseUrl) => setValues({ ...values, baseUrl })}
        />
        <TextInput
          helpText={fieldHelp("providers.<key>.api_key", "create.help.providerApiKey")}
          label={t("create.provider.apiKey")}
          path="api_key"
          value={values.apiKey}
          onChange={(apiKey) => setValues({ ...values, apiKey })}
          secret
        />
        <SelectInput
          helpText={fieldHelp("providers.<key>.protocol", "create.help.providerProtocol")}
          label={t("create.provider.protocol")}
          options={protocolSelectOptions(t)}
          value={values.protocol}
          onChange={(protocol) => setValues({ ...values, protocol })}
        />
      </>
    );
  }

  if (kind === "model") {
    return (
      <>
        <TextInput
          helpText={fieldHelp("models.<slug>.slug", "create.help.modelId")}
          label={t("create.model.id")}
          path="slug"
          value={values.id}
          onChange={(id) => setValues({ ...values, id })}
        />
        <TextInput
          helpText={t("create.help.modelDisplayName")}
          label={t("create.model.displayName")}
          leadingIconNode={modelIconForName(values.displayName || values.id)}
          path="display_name"
          value={values.displayName}
          onChange={(displayName) => setValues({ ...values, displayName })}
        />
        <ContextWindowInput
          helpText={fieldHelp("models.<slug>.context_window", "create.help.modelContextWindow")}
          label={t("create.model.contextWindow")}
          value={values.contextWindow}
          onChange={(contextWindow) => setValues({ ...values, contextWindow })}
        />
      </>
    );
  }

  if (kind === "route") {
    return (
      <>
        <TextInput
          helpText={fieldHelp("routes.<alias>.alias", "create.help.routeId")}
          label={t("create.route.id")}
          path="alias"
          value={values.id}
          onChange={(id) => setValues({ ...values, id })}
        />
        <SelectInput
          helpText={fieldHelp("routes.<alias>.model", "create.help.routeModel")}
          label={t("create.route.model")}
          options={modelSelectOptions(models)}
          value={values.model}
          onChange={(model) => setValues({ ...values, model })}
        />
        <SelectInput
          helpText={fieldHelp("routes.<alias>.provider", "create.help.routeProvider")}
          label={t("create.route.provider")}
          options={toSelectOptions(providers.map((provider) => provider.id))}
          value={values.provider}
          onChange={(provider) => setValues({ ...values, provider })}
        />
      </>
    );
  }

  if (kind === "provider_offer") {
    return (
      <>
        <div className="schema-field form-field--create-track">
          <CreateFieldLabel
            helpText={fieldHelp("providers.<key>.offers[].model", "create.help.offerModel")}
            label={t("create.offer.model")}
          />
          <MaterialAssistChip>{modelId ?? values.model}</MaterialAssistChip>
        </div>
        <SelectInput
          helpText={fieldHelp("providers.<key>.key", "create.help.offerProvider")}
          label={t("create.offer.provider")}
          options={toSelectOptions(providers.map((provider) => provider.id))}
          value={values.provider}
          onChange={(provider) => setValues({ ...values, provider })}
        />
        <TextInput helpText={fieldHelp("providers.<key>.offers[].upstream_name", "create.help.offerUpstreamName")} label={t("create.offer.upstreamName")} path="upstream_name" value={values.upstreamName} onChange={(upstreamName) => setValues({ ...values, upstreamName })} />
        <TextInput helpText={t("create.help.offerPriority")} label={t("create.offer.priority")} path="priority" value={values.priority} onChange={(priority) => setValues({ ...values, priority })} />
        <SwitchInput
          helpText={t("create.help.offerBilling")}
          label={t("create.offer.billing")}
          value={values.billingEnabled}
          onChange={(billingEnabled) => setValues({ ...values, billingEnabled })}
        />
        {values.billingEnabled ? (
          <>
            <TextInput helpText={fieldHelp("providers.<key>.offers[].pricing", "create.help.offerInputPrice")} label={t("create.offer.inputPrice")} path="input_price" value={values.inputPrice} onChange={(inputPrice) => setValues({ ...values, inputPrice })} />
            <TextInput helpText={fieldHelp("providers.<key>.offers[].pricing", "create.help.offerOutputPrice")} label={t("create.offer.outputPrice")} path="output_price" value={values.outputPrice} onChange={(outputPrice) => setValues({ ...values, outputPrice })} />
            <TextInput helpText={fieldHelp("providers.<key>.offers[].pricing", "create.help.offerCacheWritePrice")} label={t("create.offer.cacheWritePrice")} path="cache_write_price" value={values.cacheWritePrice} onChange={(cacheWritePrice) => setValues({ ...values, cacheWritePrice })} />
            <TextInput helpText={fieldHelp("providers.<key>.offers[].pricing", "create.help.offerCacheReadPrice")} label={t("create.offer.cacheReadPrice")} path="cache_read_price" value={values.cacheReadPrice} onChange={(cacheReadPrice) => setValues({ ...values, cacheReadPrice })} />
          </>
        ) : null}
      </>
    );
  }

  return (
    <>
      <SelectInput
        helpText={t("create.help.extensionId")}
        label={t("create.extension.id")}
        options={toSelectOptions(availableExtensionIds)}
        value={values.id}
        onChange={(id) => setValues({ ...values, id })}
      />
      <SwitchInput
        helpText={fieldHelp("extensions.<name>.enabled", "create.help.extensionEnabled")}
        label={t("create.extension.enabled")}
        value={values.enabled}
        onChange={(enabled) => setValues({ ...values, enabled })}
      />
    </>
  );
}

function TextInput({
  helpText,
  label,
  leadingIconNode,
  onChange,
  path,
  secret,
  value
}: {
  helpText: string;
  label: string;
  leadingIconNode?: ReactNode;
  onChange: (value: string) => void;
  path: string;
  secret?: boolean;
  value: string;
}) {
  const id = useStableCreateId(label);
  const help = useCreateFieldHelp(label);
  return (
    <div className="mb-field form-field--create-track" data-variant="input">
      <div className="mb-field__control">
        <MaterialOutlinedTextField
          ariaDescribedBy={help.open ? help.helpId : undefined}
          ariaLabel={label}
          autoComplete={secret ? "new-password" : undefined}
          id={id}
          label={label}
          leadingIcon={fieldLeadingIcon(path, secret)}
          leadingIconNode={leadingIconNode}
          trailingIcon={help.button("trailing-icon")}
          type={secret ? "password" : "text"}
          value={value}
          onInput={onChange}
        />
        <CreateFieldHelpTooltip anchorRef={help.anchorRef} helpId={help.helpId} helpText={helpText} open={help.open} />
      </div>
    </div>
  );
}

function SelectInput({
  helpText,
  label,
  onChange,
  options,
  value
}: {
  helpText: string;
  label: string;
  onChange: (value: string) => void;
  options: MaterialSelectOption[];
  value: string;
}) {
  const help = useCreateFieldHelp(label);
  return (
    <div className="mb-field form-field--create-track" data-variant="select">
      <div className="mb-field__select-actions">
        {help.button(undefined, "mb-field__select-help")}
      </div>
      <div className="mb-field__control">
        <MaterialSelect
          describedBy={help.open ? help.helpId : undefined}
          ariaLabel={label}
          label={label}
          leadingIcon={options.find((option) => option.value === value)?.leadingIcon}
          onChange={onChange}
          options={options}
          value={value}
        />
      </div>
      <CreateFieldHelpTooltip anchorRef={help.anchorRef} helpId={help.helpId} helpText={helpText} open={help.open} />
    </div>
  );
}

function ChipOptionGroup({
  helpText,
  label,
  onChange,
  optionLabel = (option) => option,
  options,
  value
}: {
  helpText: string;
  label: string;
  onChange: (value: string) => void;
  optionLabel?: (option: string) => string;
  options: string[];
  value: string;
}) {
  return (
    <div className="schema-field form-field--create-track">
      <CreateFieldLabel helpText={helpText} label={label} />
      <ChipSet className="material-chip-group" role="group" aria-label={label}>
        {options.map((option) => (
          <MaterialFilterChip
            key={option}
            selected={option === value}
            value={option}
            onSelect={onChange}
          >
            {optionLabel(option)}
          </MaterialFilterChip>
        ))}
      </ChipSet>
    </div>
  );
}

function ContextWindowInput({
  helpText,
  label,
  onChange,
  value
}: {
  helpText: string;
  label: string;
  onChange: (value: string) => void;
  value: string;
}) {
  const { t } = useI18n();
  const id = useStableCreateId(label);
  const help = useCreateFieldHelp(label);
  const presets = [
    [t("create.contextWindowPreset.128k"), "128000"],
    [t("create.contextWindowPreset.400k"), "400000"],
    [t("create.contextWindowPreset.1m"), "1000000"]
  ] as const;

  return (
    <div
      className="mb-field form-field--create-track form-grid__wide create-resource__context-window-row"
      data-variant="input"
    >
      <div className="mb-field__control">
        <MaterialOutlinedTextField
          ariaDescribedBy={help.open ? help.helpId : undefined}
          ariaLabel={label}
          id={id}
          inputMode="numeric"
          label={label}
          leadingIcon="tag"
          trailingIcon={help.button("trailing-icon")}
          type="text"
          value={value}
          onInput={onChange}
        />
        <CreateFieldHelpTooltip anchorRef={help.anchorRef} helpId={help.helpId} helpText={helpText} open={help.open} />
      </div>
      <ChipSet
        className="material-chip-group create-resource__context-window-presets"
        role="group"
        aria-label={t("create.contextWindowPresets", { label })}
      >
        {presets.map(([presetLabel, presetValue]) => (
          <MaterialFilterChip
            key={presetValue}
            selected={value === presetValue}
            value={presetValue}
            onSelect={onChange}
          >
            {presetLabel}
          </MaterialFilterChip>
        ))}
      </ChipSet>
    </div>
  );
}

function SwitchInput({
  helpText,
  label,
  onChange,
  value
}: {
  helpText: string;
  label: string;
  onChange: (value: boolean) => void;
  value: boolean;
}) {
  return (
    <div className="schema-field schema-field--inline form-field--create-track">
      <div className="schema-field__switch-line">
        <CreateFieldLabel helpText={helpText} label={label} />
        <MaterialSwitch label={label} selected={value} onChange={onChange} />
      </div>
    </div>
  );
}

function CreateFieldLabel({ helpText, label }: { helpText: string; label: string }) {
  return (
    <span className="schema-field__label-row">
      <span className="schema-field__label">{label}</span>
      <CreateFieldHelpButton helpText={helpText} label={label} />
    </span>
  );
}

function fieldLeadingIcon(path: string, secret = false): string | undefined {
  if (secret) {
    return "key";
  }
  const normalizedPath = path.toLowerCase();
  if (normalizedPath.includes("url") || normalizedPath.includes("endpoint") || normalizedPath.includes("addr")) {
    return "link";
  }
  if (normalizedPath.includes("model")) {
    return "smart_toy";
  }
  if (normalizedPath.includes("agent")) {
    return "badge";
  }
  if (normalizedPath.includes("price") || normalizedPath.includes("priority") || normalizedPath.includes("window")) {
    return "tag";
  }
  return undefined;
}

function useCreateFieldHelp(label: string) {
  const { t } = useI18n();
  const [open, setOpen] = useState(false);
  const openedByHover = useRef(false);
  const anchorRef = useRef<HTMLButtonElement>(null);
  const helpId = `${useStableCreateId(label)}-help`;

  return {
    anchorRef,
    button: (slot?: string, className = "schema-field__help") => (
      <MaterialIconButton
        className={className}
        describedBy={open ? helpId : undefined}
        icon="help"
        label={t("field.helpFor", { label })}
        onBlur={() => setOpen(false)}
        onClick={(event) => {
          event.stopPropagation();
          if (openedByHover.current) {
            openedByHover.current = false;
            setOpen(true);
            return;
          }
          setOpen((current) => !current);
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={(event) => {
          if (event.key === "Escape") {
            setOpen(false);
          }
        }}
        onMouseDown={(event) => event.preventDefault()}
        onMouseEnter={() => {
          openedByHover.current = true;
          setOpen(true);
        }}
        onMouseLeave={() => {
          openedByHover.current = false;
          setOpen(false);
        }}
        ref={anchorRef}
        slot={slot}
      />
    ),
    helpId,
    open
  };
}

function CreateFieldHelpButton({ helpText, label }: { helpText: string; label: string }) {
  const help = useCreateFieldHelp(label);
  return (
    <span className="schema-field__help-wrap">
      {help.button()}
      <CreateFieldHelpTooltip anchorRef={help.anchorRef} helpId={help.helpId} helpText={helpText} open={help.open} />
    </span>
  );
}

function CreateFieldHelpTooltip({
  anchorRef,
  helpId,
  helpText,
  open
}: {
  anchorRef: React.RefObject<HTMLElement | null>;
  helpId: string;
  helpText: string;
  open: boolean;
}) {
  const position = useAnchoredTooltipPosition(anchorRef, open);
  if (!open) {
    return null;
  }
  return (
    <span className="rich-tooltip" id={helpId} role="tooltip" style={tooltipPositionStyle(position)}>
      {helpText}
    </span>
  );
}

function tooltipPositionStyle(position: { left: number; maxWidth: number; top: number } | undefined) {
  if (!position) {
    return undefined;
  }
  return {
    left: `${position.left}px`,
    maxWidth: `${position.maxWidth}px`,
    position: "fixed" as const,
    top: `${position.top}px`
  };
}

function useStableCreateId(label: string) {
  const id = useId();
  return `create-resource-${id}-${label}`.replace(/[^a-zA-Z0-9_-]/g, "-");
}

function toSelectOptions(options: string[]): MaterialSelectOption[] {
  return options.map((option) => ({ label: option, value: option }));
}

function protocolSelectOptions(t: (key: MessageKey) => string): MaterialSelectOption[] {
  return ["openai-response", "openai-chat", "anthropic", "google-genai"].map((option) => ({
    label: protocolOptionLabel(option, t),
    leadingIcon: protocolIconForValue(option),
    value: option
  }));
}

function defaultValues(
  kind: CreatableKind,
  graph: ConfigGraph,
  providerId?: string,
  modelId?: string,
  availableExtensionIds: string[] = []
): FormValues {
  const firstModel = graph.resources.find((resource) => resource.kind === "model")?.id ?? "";
  const firstProvider = graph.resources.find((resource) => resource.kind === "provider")?.id ?? "";
  return {
    ...initialValues,
    id: kind === "extension" ? availableExtensionIds[0] ?? "" : initialValues.id,
    model: kind === "provider_offer" ? modelId ?? firstModel : kind === "route" ? firstModel : "",
    provider: kind === "route" || kind === "provider_offer" ? providerId ?? firstProvider : ""
  };
}

function createResourceId(kind: CreatableKind, values: FormValues) {
  if (kind === "provider_offer") {
    return `${values.provider}/${values.model}`;
  }
  return values.id;
}

type ResourceDraft =
  | {
      ok: true;
      id: string;
      value: Record<string, unknown>;
    }
  | {
      ok: false;
      error: string;
    };

function createResourceDraft(
  kind: CreatableKind,
  values: FormValues,
  fieldLabels: NumberFieldLabels,
  invalidNumberMessage: (field: string) => string,
  positiveNumberMessage: (field: string) => string
): ResourceDraft {
  const value = createValue(kind, values, fieldLabels, invalidNumberMessage, positiveNumberMessage);
  if (!value.ok) {
    return value;
  }
  return {
    ok: true,
    id: createResourceId(kind, values),
    value: value.value
  };
}

function createValue(
  kind: CreatableKind,
  values: FormValues,
  fieldLabels: NumberFieldLabels,
  invalidNumberMessage: (field: string) => string,
  positiveNumberMessage: (field: string) => string
): { ok: true; value: Record<string, unknown> } | { ok: false; error: string } {
  if (kind === "provider") {
    return {
      ok: true,
      value: {
        base_url: values.baseUrl,
        api_key: values.apiKey,
        protocol: values.protocol
      }
    };
  }
  if (kind === "model") {
    const contextWindow = positiveNumericValue(
      values.contextWindow,
      fieldLabels.contextWindow,
      invalidNumberMessage,
      positiveNumberMessage
    );
    if (!contextWindow.ok) {
      return contextWindow;
    }
    return {
      ok: true,
      value: {
        display_name: values.displayName,
        context_window: contextWindow.value
      }
    };
  }
  if (kind === "route") {
    return {
      ok: true,
      value: {
        model: values.model,
        provider: values.provider
      }
    };
  }
  if (kind === "provider_offer") {
    const priority = numericValue(values.priority, fieldLabels.priority, invalidNumberMessage);
    if (!priority.ok) {
      return priority;
    }
    const offerValue: Record<string, unknown> = {
      model: values.model,
      upstream_name: values.upstreamName,
      priority: priority.value
    };
    if (!values.billingEnabled) {
      return {
        ok: true,
        value: offerValue
      };
    }
    const inputPrice = numericValue(values.inputPrice, fieldLabels.inputPrice, invalidNumberMessage);
    const outputPrice = numericValue(values.outputPrice, fieldLabels.outputPrice, invalidNumberMessage);
    const cacheWritePrice = numericValue(values.cacheWritePrice, fieldLabels.cacheWritePrice, invalidNumberMessage);
    const cacheReadPrice = numericValue(values.cacheReadPrice, fieldLabels.cacheReadPrice, invalidNumberMessage);
    if (!inputPrice.ok) {
      return inputPrice;
    }
    if (!outputPrice.ok) {
      return outputPrice;
    }
    if (!cacheWritePrice.ok) {
      return cacheWritePrice;
    }
    if (!cacheReadPrice.ok) {
      return cacheReadPrice;
    }
    return {
      ok: true,
      value: {
        ...offerValue,
        pricing: {
          input_price: inputPrice.value,
          output_price: outputPrice.value,
          cache_write_price: cacheWritePrice.value,
          cache_read_price: cacheReadPrice.value
        }
      }
    };
  }
  return {
    ok: true,
    value: {
      enabled: values.enabled
    }
  };
}

type NumberFieldLabels = {
  cacheReadPrice: string;
  cacheWritePrice: string;
  contextWindow: string;
  inputPrice: string;
  outputPrice: string;
  priority: string;
};

function numericValue(
  value: string,
  field: string,
  invalidNumberMessage: (field: string) => string
): { ok: true; value: number } | { ok: false; error: string } {
  if (value.trim() === "") {
    return { ok: true, value: 0 };
  }
  const parsed = Number(value);
  if (!Number.isFinite(parsed)) {
    return { ok: false, error: invalidNumberMessage(field) };
  }
  return { ok: true, value: parsed };
}

function positiveNumericValue(
  value: string,
  field: string,
  invalidNumberMessage: (field: string) => string,
  positiveNumberMessage: (field: string) => string
): { ok: true; value: number } | { ok: false; error: string } {
  const parsed = numericValue(value, field, invalidNumberMessage);
  if (!parsed.ok) {
    return parsed;
  }
  if (parsed.value <= 0) {
    return { ok: false, error: positiveNumberMessage(field) };
  }
  return parsed;
}

function errorMessage(cause: unknown, fallback: string) {
  const rawErrors = rawErrorsFrom(cause);
  if (rawErrors.length > 0 && typeof rawErrors[0]?.message === "string") {
    return rawErrors[0].message;
  }
  if (cause instanceof Error) {
    return cause.message;
  }
  return fallback;
}

function rawErrorsFrom(cause: unknown): Array<{ message?: unknown }> {
  if (!cause || typeof cause !== "object") {
    return [];
  }
  const raw = "raw" in cause ? (cause as { raw?: unknown }).raw : undefined;
  if (!raw || typeof raw !== "object" || !("errors" in raw)) {
    return [];
  }
  const errors = (raw as { errors?: unknown }).errors;
  return Array.isArray(errors) ? errors : [];
}

export type { CreatableKind };

function protocolOptionLabel(option: string, t: (key: MessageKey) => string) {
  const labels: Record<string, MessageKey> = {
    anthropic: "provider.protocol.anthropic",
    "openai-response": "provider.protocol.openaiResponses",
    "openai-chat": "provider.protocol.openaiChat",
    "google-genai": "provider.protocol.googleGenai"
  };
  const key = labels[option];
  return key ? t(key) : option;
}
