import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent, type ReactNode } from "react";
import { motion } from "motion/react";
import type { ConfigResource, FieldError, FieldSchema, ResourceKind, ResourceStatus, RuntimeImpact } from "../../rpc/types";
import { configDescriptions, type ConfigPath } from "../../configDocs/configDescriptions";
import { useI18n } from "../../i18n/I18nProvider";
import type { MessageKey } from "../../i18n/messages";
import { springs } from "../../theme/motion";
import { MaterialFilledButton, MaterialIconButton, MaterialOutlinedButton } from "../../components/MaterialButton";
import { ChipSet } from "../../components/ui/Chip";
import { MaterialSymbol } from "../../components/ui/Icon";
import { MaterialInputChip } from "../../components/MaterialInputChip";
import { MaterialSelect, type MaterialSelectOption } from "../../components/MaterialSelect";
import { MaterialSwitch } from "../../components/MaterialSwitch";
import { MaterialOutlinedTextField } from "../../components/MaterialTextField";
import { EditorStatusProvider, useReportFieldStatus, type FieldStatusReporter } from "./editorStatus";
import { GraphResourceField } from "./GraphResourceField";
import { modelIconForName } from "./modelProviderIcons";
import { type TooltipPosition, useAnchoredTooltipPosition } from "./helpTooltipPosition";
import { useAutosaveField, type AutosaveFieldStatus, type SaveFieldRequest } from "./useAutosaveField";
import { useDeleteConfigResource, useGraphFieldSaver } from "./useConfigGraph";
import { configDocPathForResource } from "./configDocPath";

const statusLabelKeys: Record<ResourceStatus, MessageKey> = {
  saved: "resource.status.saved",
  needsAttention: "resource.status.needsAttention",
  restartRequired: "resource.status.restartRequired"
};

const impactLabelKeys: Record<RuntimeImpact, MessageKey> = {
  normal: "resource.impact.normal",
  critical: "resource.impact.critical"
};

const statusIcons: Record<ResourceStatus, string> = {
  saved: "check_circle",
  needsAttention: "report",
  restartRequired: "restart_alt"
};

const impactIcons: Record<RuntimeImpact, string> = {
  normal: "info",
  critical: "priority_high"
};

const deletableKinds = new Set<ResourceKind>([
  "extension",
  "model",
  "provider",
  "provider_offer",
  "route"
]);

export function ResourceEditorCard({
  ariaLabel,
  children,
  embedded = false,
  modelDisplayNames = {},
  onOpenEditor,
  resource,
  revision,
  title,
  variant = "full"
}: {
  ariaLabel?: string;
  children?: ReactNode;
  embedded?: boolean;
  modelDisplayNames?: Record<string, string>;
  onOpenEditor?: () => void;
  resource: ConfigResource;
  revision: string;
  title?: string;
  variant?: "full" | "summary";
}) {
  const { t } = useI18n();
  const deleteResource = useDeleteConfigResource();
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [deleteError, setDeleteError] = useState("");
  const [fieldStatuses, setFieldStatuses] = useState<Record<string, AutosaveFieldStatus>>({});
  const reportFieldStatus = useCallback<FieldStatusReporter>((id, status) => {
    setFieldStatuses((current) => (current[id] === status ? current : { ...current, [id]: status }));
  }, []);
  const liveStatus = useMemo(() => deriveLiveStatus(fieldStatuses), [fieldStatuses]);
  const fieldCount = resource.schema.fields.length;
  const reloadText = resource.hotReloadable
    ? t("resource.reload.hot")
    : t("resource.reload.restart");
  const label = ariaLabel ?? resource.id;
  const fieldGroups = groupFields(resource.kind, resource.schema.fields);
  const resourceTitle = title ?? resource.label;
  const canDelete = deletableKinds.has(resource.kind);
  const summary = variant === "summary";

  async function confirmDelete() {
    setDeleteError("");
    try {
      await deleteResource.mutateAsync({
        kind: resource.kind,
        id: resource.id,
        baseRevision: revision
      });
    } catch (cause) {
      setDeleteError(errorMessage(cause, t("error.requestFailed")));
    }
  }

  const cardClassName = [
    "resource-editor-card",
    summary ? "resource-editor-card--summary" : "",
    embedded ? "resource-editor-card--embedded" : ""
  ].filter(Boolean).join(" ");

  // In summary rows we hide the operational markers (live save state, runtime impact,
  // hot-reload) and only surface a status pill when something actually needs attention,
  // so the list stays scannable and the freed room shows key field details instead.
  const showStatusInSummary = resource.status !== "saved";
  const statusGroup = (!summary || showStatusInSummary) ? (
    <span className="resource-editor-card__status-group" aria-label={t("resource.statusGroupLabel", { label })}>
      <span className={`resource-meta-pill status-pill status-pill--${resource.status}`}>
        <MaterialSymbol className="material-symbol">{statusIcon(resource.status)}</MaterialSymbol>
        {t(statusLabelKeys[resource.status])}
      </span>
      {!summary && resource.runtimeImpact === "critical" ? (
        <span className="resource-meta-pill status-pill status-pill--critical">
          <MaterialSymbol className="material-symbol">{impactIcon(resource.runtimeImpact)}</MaterialSymbol>
          {t(impactLabelKeys[resource.runtimeImpact])}
        </span>
      ) : null}
      {!summary && liveStatus ? (
        <motion.span
          key={liveStatus}
          className={`resource-meta-pill editor-live-status editor-live-status--${liveStatus}`}
          initial={{ opacity: 0, scale: 0.85, y: -2 }}
          animate={{ opacity: 1, scale: 1, y: 0 }}
          transition={springs.spatialFast}
        >
          <MaterialSymbol className="material-symbol">{liveStatusIcon(liveStatus)}</MaterialSymbol>
          {t(liveStatusKeys[liveStatus])}
        </motion.span>
      ) : null}
    </span>
  ) : null;

  return (
    <motion.section
      aria-label={label}
      className={cardClassName}
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={springs.spatial}
    >
      <div className={`resource-editor-card__header${summary ? " resource-editor-card__header--summary" : ""}`}>
        <div className="resource-editor-card__identity">
          <div className="resource-editor-card__identity-line">
            <MaterialSymbol className="resource-kind-icon material-symbol">{kindIcon(resource.kind)}</MaterialSymbol>
            {summary ? summaryIdentityBadge(resource) : null}
            <h3>{resource.id}</h3>
          </div>
          <div className="resource-editor-card__facts">
            {statusGroup}
            {summary
              ? summaryValueFacts(resource, t).map((fact) => (
                  <span className="resource-meta-pill resource-fact" key={fact.key}>
                    <MaterialSymbol className="material-symbol">{fact.icon}</MaterialSymbol>
                    {fact.text}
                  </span>
                ))
              : (
                <span className="resource-meta-pill resource-fact">
                  <MaterialSymbol className="material-symbol">list_alt</MaterialSymbol>
                  {t(fieldCount === 1 ? "resource.fieldCount.one" : "resource.fieldCount.many", { count: fieldCount })}
                </span>
              )}
            {!summary ? (
              <span className={`resource-meta-pill resource-fact resource-fact--${resource.hotReloadable ? "hot" : "restart"}`}>
                <MaterialSymbol className="material-symbol">{resource.hotReloadable ? "bolt" : "restart_alt"}</MaterialSymbol>
                {reloadText}
              </span>
            ) : null}
          </div>
        </div>
        <div className="resource-editor-card__meta">
          {summary && onOpenEditor ? (
            <MaterialOutlinedButton
              ariaLabel={t("resource.openEditor", { title: resourceTitle, id: resource.id })}
              icon="tune"
              onClick={() => onOpenEditor()}
            >
              {t("resource.editShort")}
            </MaterialOutlinedButton>
          ) : null}
          {canDelete ? (
            <MaterialFilledButton
              ariaLabel={t("resource.delete", { title: resourceTitle, id: resource.id })}
              className="fab-button fab-button--danger"
              icon="delete"
              onClick={() => {
                setConfirmingDelete(true);
                setDeleteError("");
              }}
            >
              {t("resource.deleteShort")}
            </MaterialFilledButton>
          ) : null}
        </div>
      </div>

      {confirmingDelete ? (
        <motion.div
          className="resource-delete-confirmation"
          initial={{ opacity: 0, y: -4 }}
          animate={{ opacity: 1, y: 0 }}
          transition={springs.spatial}
        >
          <p>{t("resource.deletePrompt", { id: resource.id })}</p>
          {deleteError ? (
            <p className="field-error" role="alert">
              {deleteError}
            </p>
          ) : null}
          <div className="resource-delete-confirmation__actions">
            <MaterialFilledButton
              ariaLabel={t("resource.confirmDelete", { id: resource.id })}
              className="resource-delete-confirmation__confirm"
              disabled={deleteResource.isPending}
              onClick={confirmDelete}
            >
              {t("resource.confirmDeleteShort")}
            </MaterialFilledButton>
            <MaterialOutlinedButton
              ariaLabel={t("resource.cancelDelete")}
              className="secondary-button"
              onClick={() => {
                setConfirmingDelete(false);
                setDeleteError("");
              }}
            >
              {t("resource.cancelDelete")}
            </MaterialOutlinedButton>
          </div>
        </motion.div>
      ) : null}

      {summary ? null : (
        <EditorStatusProvider report={reportFieldStatus}>
          <ResourceFieldGroups
            alwaysExpanded={embedded}
            fieldGroups={fieldGroups}
            modelDisplayNames={modelDisplayNames}
            resource={resource}
            revision={revision}
          >
            {children}
          </ResourceFieldGroups>
        </EditorStatusProvider>
      )}
    </motion.section>
  );
}

/** Brand/icon badge shown next to a resource id in summary rows (e.g. model/route). */
function summaryIdentityBadge(resource: ConfigResource) {
  const candidate =
    resource.kind === "route"
      ? stringValue(resource.value.model) || resource.id
      : resource.kind === "model"
        ? stringValue(resource.value.display_name) || resource.id
        : resource.id;
  return modelIconForName(candidate) ?? null;
}

type SummaryFact = { key: string; icon: string; text: string };

/**
 * A few value-derived facts shown in summary rows so users can tell entries apart
 * without opening the editor. Kept defensive: missing values simply yield no pill.
 */
function summaryValueFacts(resource: ConfigResource, t: ReturnType<typeof useI18n>["t"]): SummaryFact[] {
  const value = resource.value ?? {};
  const facts: SummaryFact[] = [];
  const push = (key: string, icon: string, text: string) => {
    if (text) facts.push({ key, icon, text });
  };

  if (resource.kind === "provider") {
    const protocol = stringValue(value.protocol);
    push("protocol", "swap_horiz", protocolLabel(protocol, t));
    push("host", "link", hostFromUrl(stringValue(value.base_url)));
    if (stringValue(value.api_key)) {
      push("key", "vpn_key", t("resource.fact.keySet"));
    }
    const version = stringValue(value.version);
    if (version) {
      push("version", "history", version);
    }
  } else if (resource.kind === "model") {
    const displayName = stringValue(value.display_name);
    if (displayName && displayName !== resource.id) {
      push("displayName", "label", displayName);
    }
    const ctx = numberValue(value.context_window);
    if (typeof ctx === "number") {
      push("context", "memory", formatContextWindow(ctx));
    }
    const maxOutput = numberValue(value.max_output_tokens);
    if (typeof maxOutput === "number") {
      push("maxout", "output", formatContextWindow(maxOutput));
    }
  } else if (resource.kind === "route") {
    const model = stringValue(value.model);
    const provider = stringValue(value.provider);
    if (model) {
      push("model", "smart_toy", model);
    }
    if (provider) {
      push("provider", "cloud", provider);
    }
    const ctx = numberValue(value.context_window);
    if (typeof ctx === "number") {
      push("context", "memory", formatContextWindow(ctx));
    }
  } else if (resource.kind === "provider_offer") {
    const priority = numberValue(value.priority);
    if (typeof priority === "number") {
      push("priority", "format_list_numbered", `#${priority}`);
    }
    const upstream = stringValue(value.upstream_name);
    if (upstream) {
      push("upstream", "arrow_forward", upstream);
    }
  }

  return facts;
}

function protocolLabel(protocol: string, t: ReturnType<typeof useI18n>["t"]): string {
  switch (protocol) {
    case "anthropic":
      return t("provider.protocol.anthropic");
    case "google-genai":
    case "googleGenai":
      return t("provider.protocol.googleGenai");
    case "openai-chat":
    case "openaiChat":
      return t("provider.protocol.openaiChat");
    case "openai-response":
    case "openaiResponses":
      return t("provider.protocol.openaiResponses");
    default:
      return protocol;
  }
}

function hostFromUrl(url: string): string {
  if (!url) {
    return "";
  }
  try {
    return new URL(url).host;
  } catch {
    return url.replace(/^https?:\/\//, "").split("/")[0];
  }
}

function formatContextWindow(tokens: number): string {
  if (tokens >= 1000) {
    return `${Math.round(tokens / 1000)}k ctx`;
  }
  return `${tokens} ctx`;
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value : "";
}

function numberValue(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

type FieldGroup = {
  key: "identity" | "basic" | "billing" | "multimodal" | "reasoning" | "advancedFeatures";
  labelKey: MessageKey;
  fields: FieldSchema[];
};

function ResourceFieldGroups({
  alwaysExpanded,
  children,
  fieldGroups,
  modelDisplayNames,
  resource,
  revision
}: {
  alwaysExpanded?: boolean;
  children?: ReactNode;
  fieldGroups: FieldGroup[];
  modelDisplayNames: Record<string, string>;
  resource: ConfigResource;
  revision: string;
}) {
  const hasReasoningSupportField = resource.kind === "model" &&
    resource.schema.fields.some((field) => field.path === "supports_reasoning");
  const reasoningLevelsField = hasReasoningSupportField
    ? resource.schema.fields.find((field) => field.path === "supported_reasoning_levels")
    : undefined;
  const modelReasoningLevels = useModelReasoningLevels(resource, revision, reasoningLevelsField);

  return (
    <div className="resource-field-groups">
      {fieldGroups.map((group) => (
        <ResourceFieldGroup
          alwaysExpanded={alwaysExpanded}
          group={group}
          key={group.key}
          modelDisplayNames={modelDisplayNames}
          modelReasoningLevels={modelReasoningLevels}
          resource={resource}
          revision={revision}
        />
      ))}
      {children}
    </div>
  );
}

function ResourceFieldGroup({
  alwaysExpanded,
  group,
  modelDisplayNames,
  modelReasoningLevels,
  resource,
  revision
}: {
  alwaysExpanded?: boolean;
  group: FieldGroup;
  modelDisplayNames: Record<string, string>;
  modelReasoningLevels: ModelReasoningLevelsState | undefined;
  resource: ConfigResource;
  revision: string;
}) {
  const { t } = useI18n();
  const collapsible = !alwaysExpanded && isCollapsibleResourceFieldGroup(resource.kind, group);
  const [open, setOpen] = useState(!collapsible);
  if (group.key === "reasoning") {
    return (
      <ReasoningFieldGroup
        group={group}
        modelReasoningLevels={modelReasoningLevels}
        resource={resource}
        revision={revision}
      />
    );
  }
  if (group.key === "billing") {
    return (
      <ProviderOfferBillingGroup
        group={group}
        resource={resource}
        revision={revision}
      />
    );
  }
  const toggleFields = group.fields.filter(isToggleField);
  const inputFields = group.fields.filter((field) => !isToggleField(field));
  const bodyId = `${resource.kind}-${resource.id}-${group.key}-fields`.replace(/[^a-zA-Z0-9_-]/g, "-");
  const label = t(group.labelKey);

  return (
    <div
      aria-label={label}
      className={fieldGroupClass(resource.kind, group, collapsible && !open)}
      role="group"
    >
      <div className="resource-field-group__header">
        <h4>
          <MaterialSymbol className="material-symbol">{fieldGroupIcon(group)}</MaterialSymbol>
          {label}
        </h4>
        {collapsible ? (
          <MaterialIconButton
            ariaExpanded={open}
            className="resource-field-group__toggle"
            controls={bodyId}
            icon="chevron_right"
            label={t("resource.group.toggle", { label })}
            onClick={() => setOpen((current) => !current)}
          />
        ) : null}
      </div>
      {open ? (
        <div className="resource-field-group__body" id={bodyId}>
          {inputFields.length ? (
            <div className={fieldGridContainerClass(resource.kind, group)}>
              {renderInputFields(resource, revision, group, inputFields, modelDisplayNames, modelReasoningLevels)}
            </div>
          ) : null}
          {toggleFields.length ? (
            <div className="switch-bank">
              {toggleFields.map((field) => (
                <GraphResourceField
                  field={field}
                  modelDisplayNames={modelDisplayNames}
                  objectDisplay={fieldObjectDisplay(resource.kind, field, group)}
                  resource={resource}
                  revision={revision}
                  key={`${resource.kind}-${resource.id}-${field.path}`}
                />
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function ReasoningFieldGroup({
  group,
  modelReasoningLevels,
  resource,
  revision
}: {
  group: FieldGroup;
  modelReasoningLevels: ModelReasoningLevelsState | undefined;
  resource: ConfigResource;
  revision: string;
}) {
  const { t } = useI18n();
  const reasoningSupportField = group.key === "reasoning"
    ? group.fields.find((field) => field.path === "supports_reasoning")
    : undefined;
  const reasoningSupport = useModelReasoningSupport(resource, revision, reasoningSupportField);
  const supportsReasoning = requiredModelReasoningSupport(reasoningSupport).value;
  const visibleFields = group.fields.filter((field) => field.path !== "supports_reasoning");
  const toggleFields = visibleFields.filter(isToggleField);
  const inputFields = visibleFields.filter((field) => !isToggleField(field));

  return (
    <div
      aria-label={t(group.labelKey)}
      className={fieldGroupClass(resource.kind, group)}
      role="group"
    >
      <div className="resource-field-group__header">
        <h4>
          <MaterialSymbol className="material-symbol">{fieldGroupIcon(group)}</MaterialSymbol>
          {t(group.labelKey)}
        </h4>
        {reasoningSupportField ? (
          <ReasoningSupportSwitch
            autosave={requiredModelReasoningSupport(reasoningSupport)}
            field={reasoningSupportField}
            resource={resource}
          />
        ) : null}
      </div>
      {supportsReasoning && inputFields.length ? (
        <div className={fieldGridContainerClass(resource.kind, group)}>
          {renderInputFields(resource, revision, group, inputFields, {}, modelReasoningLevels)}
        </div>
      ) : null}
      {supportsReasoning && toggleFields.length ? (
        <div className="switch-bank">
          {toggleFields.map((field) => (
            <GraphResourceField
              field={field}
              objectDisplay={fieldObjectDisplay(resource.kind, field, group)}
              resource={resource}
              revision={revision}
              key={`${resource.kind}-${resource.id}-${field.path}`}
            />
          ))}
        </div>
      ) : null}
    </div>
  );
}

const advancedFeaturePaths = new Set(["web_search", "extensions"]);
const modelMultimodalPaths = new Set([
  "input_modalities",
  "supports_image_detail_original"
]);
const modelReasoningPaths = new Set([
  "supports_reasoning",
  "default_reasoning_level",
  "supported_reasoning_levels",
  "supports_reasoning_summaries",
  "default_reasoning_summary"
]);
const modelReasoningDefaultsPaths = [
  "default_reasoning_level",
  "default_reasoning_summary"
] as const;
const modelEditableListPaths = new Set(["input_modalities"]);
const structuredFeatureKinds = new Set<ResourceKind>(["model", "provider", "route"]);
const providerOfferBillingPaths = new Set(["pricing"]);

const kindIcons: Record<string, string> = {
  provider: "dns",
  offer: "smart_toy",
  model: "smart_toy",
  route: "alt_route",
  defaults: "tune",
  server: "lan",
  cache: "database",
  persistence: "save",
  store: "database",
  proxy: "swap_horiz",
  plugin: "extension",
  extension: "extension"
};

function kindIcon(kind: string): string {
  return kindIcons[kind] ?? "tune";
}

function statusIcon(status: ResourceStatus): string {
  return statusIcons[status];
}

function impactIcon(impact: RuntimeImpact): string {
  return impactIcons[impact];
}

function isToggleField(field: FieldSchema): boolean {
  return field.type === "boolean" || field.control === "switch";
}

function deriveLiveStatus(
  statuses: Record<string, AutosaveFieldStatus>
): "saving" | "error" | "dirty" | null {
  const values = Object.values(statuses);
  if (values.includes("saving")) {
    return "saving";
  }
  if (values.includes("error")) {
    return "error";
  }
  if (values.includes("dirty")) {
    return "dirty";
  }
  return null;
}

const liveStatusKeys: Record<"saving" | "error" | "dirty", MessageKey> = {
  saving: "editor.liveSaving",
  error: "editor.liveError",
  dirty: "editor.liveUnsaved"
};

function liveStatusIcon(status: "saving" | "error" | "dirty") {
  if (status === "saving") {
    return "progress_activity";
  }
  if (status === "error") {
    return "error";
  }
  return "edit";
}

function groupFields(kind: ResourceKind, fields: FieldSchema[]): FieldGroup[] {
  const canRenderModelReasoning = kind === "model" &&
    fields.some((field) => field.path === "supports_reasoning");
  const groups: Record<FieldGroup["key"], FieldGroup> = {
    identity: { key: "identity", labelKey: "resource.group.identity", fields: [] },
    basic: { key: "basic", labelKey: "resource.group.basic", fields: [] },
    billing: { key: "billing", labelKey: "resource.group.billing", fields: [] },
    multimodal: { key: "multimodal", labelKey: "resource.group.multimodal", fields: [] },
    reasoning: { key: "reasoning", labelKey: "resource.group.reasoning", fields: [] },
    advancedFeatures: { key: "advancedFeatures", labelKey: "resource.group.advancedFeatures", fields: [] }
  };

  const order: FieldGroup["key"][] = kind === "model"
    ? ["identity", "basic", "reasoning", "multimodal", "advancedFeatures", "billing"]
    : ["identity", "basic", "billing", "multimodal", "advancedFeatures", "reasoning"];

  for (const field of fields) {
    if (isIdentityField(field)) {
      groups.identity.fields.push(field);
    } else if (isProviderOfferBillingField(kind, field)) {
      groups.billing.fields.push(field);
    } else if (isModelMultimodalField(kind, field)) {
      groups.multimodal.fields.push(field);
    } else if (isModelReasoningField(kind, field) && canRenderModelReasoning) {
      groups.reasoning.fields.push(field);
    } else if (isAdvancedFeatureField(kind, field)) {
      groups.advancedFeatures.fields.push(field);
    } else if (isModelReasoningField(kind, field)) {
      continue;
    } else {
      groups.basic.fields.push(field);
    }
  }

  if (kind === "model") {
    groups.basic.fields = orderModelBasicFields(groups.basic.fields);
    groups.reasoning.fields = orderModelReasoningFields(groups.reasoning.fields);
  }

  return order.map((key) => groups[key]).filter((group) => group.fields.length > 0);
}

function isIdentityField(field: FieldSchema) {
  return [
    "addr",
    "base_url",
    "display_name",
    "model",
    "mode",
    "provider",
    "protocol",
    "to",
    "upstream_name"
  ].includes(field.path);
}

function isAdvancedFeatureField(kind: ResourceKind, field: FieldSchema) {
  return structuredFeatureKinds.has(kind) && advancedFeaturePaths.has(field.path);
}

function isModelReasoningField(kind: ResourceKind, field: FieldSchema) {
  return kind === "model" && modelReasoningPaths.has(field.path);
}

function isModelMultimodalField(kind: ResourceKind, field: FieldSchema) {
  return kind === "model" && modelMultimodalPaths.has(field.path);
}

function isProviderOfferBillingField(kind: ResourceKind, field: FieldSchema) {
  return kind === "provider_offer" && providerOfferBillingPaths.has(field.path);
}

function isProviderOfferOverridesField(kind: ResourceKind, field: FieldSchema) {
  return kind === "provider_offer" && field.path === "overrides";
}

function fieldGroupClass(kind: ResourceKind, group: FieldGroup, collapsed = false) {
  const base = `resource-field-group resource-field-group--${group.key}`;
  const classes = [base];
  if (group.key === "advancedFeatures" || group.key === "billing" || group.key === "multimodal" || group.key === "reasoning") {
    classes.push("resource-field-group--advanced");
  }
  if (group.key === "multimodal") {
    classes.push("resource-field-group--multimodal");
  }
  if (group.key === "reasoning") {
    classes.push("resource-field-group--reasoning");
  }
  if (kind === "route" && group.key === "identity") {
    classes.push("resource-field-group--route-identity");
  }
  if (collapsed) {
    classes.push("resource-field-group--collapsed");
  }
  return classes.join(" ");
}

function isCollapsibleResourceFieldGroup(kind: ResourceKind, group: FieldGroup) {
  return kind === "model" && (group.key === "multimodal" || group.key === "advancedFeatures");
}

function fieldGridContainerClass(kind: ResourceKind, group: FieldGroup) {
  if (kind === "route" && group.key === "identity") {
    return "form-grid form-grid--route-identity";
  }
  return "form-grid";
}

function fieldGroupIcon(group: FieldGroup) {
  if (group.key === "identity") {
    return "badge";
  }
  if (group.key === "advancedFeatures") {
    return "extension";
  }
  if (group.key === "multimodal") {
    return "image";
  }
  if (group.key === "reasoning") {
    return "psychology";
  }
  if (group.key === "billing") {
    return "payments";
  }
  return "tune";
}

function renderInputFields(
  resource: ConfigResource,
  revision: string,
  group: FieldGroup,
  fields: FieldSchema[],
  modelDisplayNames: Record<string, string>,
  modelReasoningLevels: ModelReasoningLevelsState | undefined
) {
  const rendered: ReactNode[] = [];
  let index = 0;
  while (index < fields.length) {
    if (isModelReasoningDefaultsGroup(resource.kind, fields, index)) {
      const pair = fields.slice(index, index + modelReasoningDefaultsPaths.length);
      rendered.push(
        <div
          className="form-grid__wide form-grid__reasoning-defaults"
          key={`${resource.kind}-${resource.id}-reasoning-defaults`}
        >
          {pair.map((field) => renderInputField(resource, revision, group, field, modelDisplayNames, modelReasoningLevels))}
        </div>
      );
      index += pair.length;
      continue;
    }
    rendered.push(renderInputField(resource, revision, group, fields[index], modelDisplayNames, modelReasoningLevels));
    index += 1;
  }
  return rendered;
}

function renderInputField(
  resource: ConfigResource,
  revision: string,
  group: FieldGroup,
  field: FieldSchema,
  modelDisplayNames: Record<string, string>,
  modelReasoningLevels: ModelReasoningLevelsState | undefined
) {
  if (isModelReasoningLevelsField(resource.kind, field)) {
    return (
      <div
        className="form-grid__wide"
        key={`${resource.kind}-${resource.id}-${field.path}`}
      >
        <EditableListResourceField
          field={field}
          autosave={requiredModelReasoningLevels(modelReasoningLevels)}
          valueFromDraft={reasoningLevelEffort}
          valueFromInput={newReasoningLevel}
        />
      </div>
    );
  }
  if (isModelEditableListField(resource.kind, field)) {
    return (
      <div
        className="form-grid__wide"
        key={`${resource.kind}-${resource.id}-${field.path}`}
      >
        <GenericEditableListResourceField
          field={field}
          resource={resource}
          revision={revision}
        />
      </div>
    );
  }
  if (isProviderOfferOverridesField(resource.kind, field)) {
    return (
      <div
        className="form-grid__wide"
        key={`${resource.kind}-${resource.id}-${field.path}`}
      >
        <ProviderOfferOverridesField
          field={field}
          resource={resource}
          revision={revision}
        />
      </div>
    );
  }
  if (isDefaultReasoningLevelField(resource.kind, field)) {
    return (
      <div
        className={fieldGridClass(field)}
        key={`${resource.kind}-${resource.id}-${field.path}`}
      >
        <DefaultReasoningLevelField
          field={field}
          levels={requiredModelReasoningLevels(modelReasoningLevels).value}
          resource={resource}
          revision={revision}
        />
      </div>
    );
  }
  if (isStructuredWebSearchField(resource.kind, field)) {
    return (
      <div
        className="form-grid__wide"
        key={`${resource.kind}-${resource.id}-${field.path}`}
      >
        <WebSearchFeatureField
          field={field}
          resource={resource}
          revision={revision}
        />
      </div>
    );
  }
  if (isStructuredExtensionsField(resource.kind, field)) {
    return (
      <div
        className="form-grid__wide"
        key={`${resource.kind}-${resource.id}-${field.path}`}
      >
        <ExtensionsFeatureField
          field={field}
          resource={resource}
          revision={revision}
        />
      </div>
    );
  }
  return (
    <div
      className={fieldGridClass(field)}
      key={`${resource.kind}-${resource.id}-${field.path}`}
    >
      <GraphResourceField
        field={field}
        modelDisplayNames={modelDisplayNames}
        objectDisplay={fieldObjectDisplay(resource.kind, field, group)}
        resource={resource}
        revision={revision}
      />
    </div>
  );
}

function isModelEditableListField(kind: ResourceKind, field: FieldSchema) {
  return kind === "model" && modelEditableListPaths.has(field.path);
}

function isModelReasoningLevelsField(kind: ResourceKind, field: FieldSchema) {
  return kind === "model" && field.path === "supported_reasoning_levels";
}

function isDefaultReasoningLevelField(kind: ResourceKind, field: FieldSchema) {
  return kind === "model" && field.path === "default_reasoning_level";
}

function isStructuredWebSearchField(kind: ResourceKind, field: FieldSchema) {
  return structuredFeatureKinds.has(kind) && field.path === "web_search";
}

function isStructuredExtensionsField(kind: ResourceKind, field: FieldSchema) {
  return structuredFeatureKinds.has(kind) && field.path === "extensions";
}

function isModelReasoningDefaultsGroup(kind: ResourceKind, fields: FieldSchema[], index: number) {
  return (
    kind === "model" &&
    modelReasoningDefaultsPaths.every((path, offset) => fields[index + offset]?.path === path)
  );
}

function fieldObjectDisplay(kind: ResourceKind, field: FieldSchema, group: FieldGroup) {
  if (group.key === "advancedFeatures") {
    return "expandedFixed";
  }
  return undefined;
}

function ReasoningSupportSwitch({
  autosave,
  field,
  resource
}: {
  autosave: BooleanFieldState;
  field: FieldSchema;
  resource: ConfigResource;
}) {
  const { locale } = useI18n();
  const docPath = configDocPathForResource(resource, field);
  const label = docPath ? configDescriptions[docPath].title[locale] : field.label;

  return (
    <span className="resource-field-group__switch" aria-label={label}>
      <MaterialSwitch
        disabled={autosave.status === "saving"}
        label={label}
        selected={autosave.value}
        onChange={autosave.commitValue}
      />
    </span>
  );
}

const providerOfferPriceKeys = [
  "input_price",
  "output_price",
  "cache_write_price",
  "cache_read_price"
] as const;

type ProviderOfferPriceKey = typeof providerOfferPriceKeys[number];

function ProviderOfferBillingGroup({
  group,
  resource,
  revision
}: {
  group: FieldGroup;
  resource: ConfigResource;
  revision: string;
}) {
  const { t } = useI18n();
  const pricingField = group.fields.find((field) => field.path === "pricing");
  if (!pricingField) {
    throw new Error("Provider offer billing group requires a pricing field.");
  }
  const autosave = useObjectFieldState(resource, revision, pricingField);
  const [enabled, setEnabled] = useState(() => hasProviderOfferPricing(autosave.value));
  const manuallyEnabled = useRef(false);

  useEffect(() => {
    if (hasProviderOfferPricing(autosave.value)) {
      setEnabled(true);
      return;
    }
    if (!manuallyEnabled.current && autosave.status !== "dirty") {
      setEnabled(false);
    }
  }, [autosave.status, autosave.value]);

  function setBillingEnabled(nextEnabled: boolean) {
    manuallyEnabled.current = nextEnabled;
    setEnabled(nextEnabled);
    if (!nextEnabled) {
      autosave.commitSerializedValue({}, null);
    }
  }

  return (
    <div
      aria-label={t(group.labelKey)}
      className={fieldGroupClass(resource.kind, group)}
      role="group"
    >
      <div className="resource-field-group__header">
        <h4>
          <MaterialSymbol className="material-symbol">{fieldGroupIcon(group)}</MaterialSymbol>
          {t(group.labelKey)}
        </h4>
        <span className="resource-field-group__switch" aria-label={t(group.labelKey)}>
          <MaterialSwitch
            disabled={autosave.status === "saving"}
            label={t(group.labelKey)}
            selected={enabled}
            onChange={setBillingEnabled}
          />
        </span>
      </div>
      {enabled ? (
        <div className="structured-feature-field structured-feature-field--billing" aria-label={t(group.labelKey)}>
          <div className="structured-feature-field__grid">
            {providerOfferPriceKeys.map((fieldKey) => (
              <ProviderOfferPriceField
                autosave={autosave}
                fieldKey={fieldKey}
                key={fieldKey}
                label={providerOfferPriceLabel(fieldKey, t)}
              />
            ))}
          </div>
          {autosave.error ? (
            <p className="field-error" role="alert">
              {autosave.error.message}
            </p>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function ProviderOfferPriceField({
  autosave,
  fieldKey,
  label
}: {
  autosave: ObjectFieldState;
  fieldKey: ProviderOfferPriceKey;
  label: string;
}) {
  const { t } = useI18n();
  const committedDraft = providerOfferPriceDraft(autosave.value[fieldKey]);
  const [draft, setDraft] = useState(committedDraft);
  const [localError, setLocalError] = useState("");

  useEffect(() => {
    setDraft(committedDraft);
    setLocalError("");
  }, [committedDraft]);

  function commit() {
    const trimmed = draft.trim();
    if (trimmed === committedDraft) {
      setLocalError("");
      return;
    }
    const parsed = Number(trimmed);
    if (!Number.isFinite(parsed)) {
      setLocalError(t("field.invalidNumber"));
      return;
    }
    setLocalError("");
    autosave.commitValue({
      ...providerOfferPricingValue(autosave.value),
      [fieldKey]: parsed
    });
  }

  return (
    <div className="mb-field" data-variant="input">
      <div className="mb-field__control">
        <MaterialOutlinedTextField
          ariaInvalid={Boolean(localError)}
          className="structured-feature-field__number"
          disabled={autosave.status === "saving"}
          error={Boolean(localError)}
          errorText={localError}
          inputMode="decimal"
          label={label}
          spellCheck={false}
          type="text"
          value={draft}
          onBlur={commit}
          onInput={(next) => {
            setDraft(next);
            if (localError && next.trim() === committedDraft) {
              setLocalError("");
            }
          }}
        />
      </div>
      {localError ? (
        <p className="field-error field-error--sr" role="alert">
          {localError}
        </p>
      ) : null}
    </div>
  );
}

function providerOfferPriceLabel(key: ProviderOfferPriceKey, t: ReturnType<typeof useI18n>["t"]) {
  const labels: Record<ProviderOfferPriceKey, MessageKey> = {
    input_price: "create.offer.inputPrice",
    output_price: "create.offer.outputPrice",
    cache_write_price: "create.offer.cacheWritePrice",
    cache_read_price: "create.offer.cacheReadPrice"
  };
  return t(labels[key]);
}

function hasProviderOfferPricing(value: Record<string, unknown>) {
  return providerOfferPriceKeys.some((key) => value[key] !== undefined);
}

function providerOfferPricingValue(value: Record<string, unknown>): Record<ProviderOfferPriceKey, number> {
  return {
    input_price: providerOfferPriceNumber(value.input_price),
    output_price: providerOfferPriceNumber(value.output_price),
    cache_write_price: providerOfferPriceNumber(value.cache_write_price),
    cache_read_price: providerOfferPriceNumber(value.cache_read_price)
  };
}

function providerOfferPriceDraft(value: unknown) {
  return String(providerOfferPriceNumber(value));
}

function providerOfferPriceNumber(value: unknown) {
  if (value === undefined || value === null || value === "") {
    return 0;
  }
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw new Error("Provider offer price requires a finite number.");
  }
  return value;
}

const providerOverrideNumberKeys = [
  "context_window",
  "max_output_tokens"
] as const;

const providerOverrideTextKeys = [
  "display_name",
  "default_reasoning_level",
  "default_reasoning_summary"
] as const;

const providerOverrideLongTextKeys = [
  "description",
  "base_instructions"
] as const;

const providerOverrideBooleanKeys = [
  "supports_reasoning",
  "supports_reasoning_summaries",
  "supports_image_detail_original"
] as const;

type ProviderOverrideNumberKey = typeof providerOverrideNumberKeys[number];
type ProviderOverrideTextKey = typeof providerOverrideTextKeys[number];
type ProviderOverrideLongTextKey = typeof providerOverrideLongTextKeys[number];
type ProviderOverrideBooleanKey = typeof providerOverrideBooleanKeys[number];

function ProviderOfferOverridesField({
  field,
  resource,
  revision
}: {
  field: FieldSchema;
  resource: ConfigResource;
  revision: string;
}) {
  const { t } = useI18n();
  const autosave = useObjectFieldState(resource, revision, field);

  function commitKey(key: string, value: unknown) {
    const next = value === undefined ? omitObjectKey(autosave.value, key) : { ...autosave.value, [key]: value };
    commitProviderOverrides(autosave, next);
  }

  const inputModalities = stringArrayValue(autosave.value.input_modalities);
  const reasoningLevels = toReasoningLevelPresets(autosave.value.supported_reasoning_levels);

  return (
    <div className="provider-overrides-editor structured-feature-field" aria-label={t("field.providerOverrides.title")}>
      <div className="schema-structured-object__header">
        <span>{t("field.providerOverrides.title")}</span>
      </div>
      <div className="structured-feature-field__grid">
        {providerOverrideNumberKeys.map((key) => (
          <ProviderOverrideNumberField
            disabled={autosave.status === "saving"}
            key={key}
            label={providerOverrideLabel(key, t)}
            value={autosave.value[key]}
            onCommit={(value) => commitKey(key, value)}
          />
        ))}
        {providerOverrideTextKeys.map((key) => (
          <ProviderOverrideTextField
            disabled={autosave.status === "saving"}
            key={key}
            label={providerOverrideLabel(key, t)}
            value={autosave.value[key]}
            onCommit={(value) => commitKey(key, value)}
          />
        ))}
        {providerOverrideBooleanKeys.map((key) => (
          <ProviderOverrideBooleanField
            disabled={autosave.status === "saving"}
            key={key}
            label={providerOverrideLabel(key, t)}
            value={autosave.value[key]}
            onCommit={(value) => commitKey(key, value)}
          />
        ))}
      </div>
      <div className="structured-feature-field__grid structured-feature-field__grid--wide">
        {providerOverrideLongTextKeys.map((key) => (
          <ProviderOverrideTextField
            disabled={autosave.status === "saving"}
            key={key}
            label={providerOverrideLabel(key, t)}
            multiline
            value={autosave.value[key]}
            onCommit={(value) => commitKey(key, value)}
          />
        ))}
      </div>
      <EditableListResourceField
        autosave={{
          commitValue: (items) => commitKey("input_modalities", items.length ? items : undefined),
          error: autosave.error,
          label: localizedStaticLabel(t("field.providerOverrides.inputModalities")),
          status: autosave.status,
          value: inputModalities
        }}
        field={field}
        valueFromDraft={stringListItemLabel}
        valueFromInput={(input) => input}
      />
      <EditableListResourceField
        autosave={{
          commitValue: (items) => commitKey("supported_reasoning_levels", items.length ? items : undefined),
          error: autosave.error,
          label: localizedStaticLabel(t("field.providerOverrides.supportedReasoningLevels")),
          status: autosave.status,
          value: reasoningLevels
        }}
        field={field}
        valueFromDraft={reasoningLevelEffort}
        valueFromInput={newReasoningLevel}
      />
      {autosave.error ? (
        <p className="field-error" role="alert">
          {autosave.error.message}
        </p>
      ) : null}
    </div>
  );
}

function ProviderOverrideNumberField({
  disabled,
  label,
  onCommit,
  value
}: {
  disabled: boolean;
  label: string;
  onCommit: (value: number | undefined) => void;
  value: unknown;
}) {
  const { t } = useI18n();
  const committedDraft = objectNumberDraft(value);
  const [draft, setDraft] = useState(committedDraft);
  const [localError, setLocalError] = useState("");

  useEffect(() => {
    setDraft(committedDraft);
    setLocalError("");
  }, [committedDraft]);

  function commit() {
    const trimmed = draft.trim();
    if (trimmed === committedDraft) {
      setLocalError("");
      return;
    }
    if (trimmed === "") {
      setLocalError("");
      onCommit(undefined);
      return;
    }
    const parsed = Number(trimmed);
    if (!Number.isFinite(parsed)) {
      setLocalError(t("field.invalidNumber"));
      return;
    }
    setLocalError("");
    onCommit(parsed);
  }

  return (
    <div className="mb-field" data-variant="input">
      <div className="mb-field__control">
        <MaterialOutlinedTextField
          ariaInvalid={Boolean(localError)}
          disabled={disabled}
          error={Boolean(localError)}
          errorText={localError}
          inputMode="numeric"
          label={label}
          spellCheck={false}
          type="text"
          value={draft}
          onBlur={commit}
          onInput={(next) => {
            setDraft(next);
            if (localError && next.trim() === committedDraft) {
              setLocalError("");
            }
          }}
        />
      </div>
      {localError ? (
        <p className="field-error field-error--sr" role="alert">
          {localError}
        </p>
      ) : null}
    </div>
  );
}

function ProviderOverrideTextField({
  disabled,
  label,
  multiline = false,
  onCommit,
  value
}: {
  disabled: boolean;
  label: string;
  multiline?: boolean;
  onCommit: (value: string | undefined) => void;
  value: unknown;
}) {
  const committedDraft = objectStringDraft(value, false);
  const [draft, setDraft] = useState(committedDraft);

  useEffect(() => {
    setDraft(committedDraft);
  }, [committedDraft]);

  function commit() {
    if (draft === committedDraft) {
      return;
    }
    onCommit(draft.trim() === "" ? undefined : draft);
  }

  return (
    <div className="mb-field" data-variant={multiline ? "textarea" : "input"}>
      <div className="mb-field__control">
        <MaterialOutlinedTextField
          disabled={disabled}
          label={label}
          rows={multiline ? 4 : undefined}
          spellCheck={multiline}
          type={multiline ? "textarea" : "text"}
          value={draft}
          onBlur={commit}
          onInput={setDraft}
        />
      </div>
    </div>
  );
}

function ProviderOverrideBooleanField({
  disabled,
  label,
  onCommit,
  value
}: {
  disabled: boolean;
  label: string;
  onCommit: (value: boolean | undefined) => void;
  value: unknown;
}) {
  const { t } = useI18n();
  const selected = typeof value === "boolean" ? String(value) : "inherit";
  return (
    <div className="mb-field" data-variant="select">
      <div className="mb-field__control">
        <MaterialSelect
          ariaLabel={label}
          disabled={disabled}
          label={label}
          options={[
            { value: "inherit", label: t("field.providerOverrides.inherit") },
            { value: "true", label: t("field.providerOverrides.enabled") },
            { value: "false", label: t("field.providerOverrides.disabled") }
          ]}
          value={selected}
          onChange={(next) => {
            if (next === "inherit") {
              onCommit(undefined);
              return;
            }
            onCommit(next === "true");
          }}
        />
      </div>
    </div>
  );
}

function providerOverrideLabel(
  key: ProviderOverrideNumberKey | ProviderOverrideTextKey | ProviderOverrideLongTextKey | ProviderOverrideBooleanKey,
  t: ReturnType<typeof useI18n>["t"]
) {
  const labels: Record<
    ProviderOverrideNumberKey | ProviderOverrideTextKey | ProviderOverrideLongTextKey | ProviderOverrideBooleanKey,
    MessageKey
  > = {
    base_instructions: "field.providerOverrides.baseInstructions",
    context_window: "field.providerOverrides.contextWindow",
    default_reasoning_level: "field.providerOverrides.defaultReasoningLevel",
    default_reasoning_summary: "field.providerOverrides.defaultReasoningSummary",
    description: "field.providerOverrides.description",
    display_name: "field.providerOverrides.displayName",
    max_output_tokens: "field.providerOverrides.maxOutputTokens",
    supports_image_detail_original: "field.providerOverrides.supportsImageDetailOriginal",
    supports_reasoning: "field.providerOverrides.supportsReasoning",
    supports_reasoning_summaries: "field.providerOverrides.supportsReasoningSummaries"
  };
  return t(labels[key]);
}

function commitProviderOverrides(autosave: ObjectFieldState, next: Record<string, unknown>) {
  const cleaned = cleanProviderOverrides(next);
  autosave.commitSerializedValue(cleaned, Object.keys(cleaned).length ? cleaned : null);
}

function cleanProviderOverrides(value: Record<string, unknown>) {
  const next: Record<string, unknown> = {};
  for (const [key, entry] of Object.entries(value)) {
    if (entry === undefined || entry === null || entry === "") {
      continue;
    }
    if (Array.isArray(entry) && entry.length === 0) {
      continue;
    }
    if (typeof entry === "object" && !Array.isArray(entry) && Object.keys(entry).length === 0) {
      continue;
    }
    next[key] = entry;
  }
  return next;
}

function stringArrayValue(value: unknown): string[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter((item): item is string => typeof item === "string");
}

function localizedStaticLabel(label: string): LocalizedLabel {
  return {
    "en-US": label,
    "zh-CN": label
  };
}

type WebSearchStructuredFieldKey = "max_uses" | "search_max_rounds" | "tavily_api_key" | "firecrawl_api_key";

const webSearchConfigPaths: Record<WebSearchStructuredFieldKey, ConfigPath> = {
  max_uses: "web_search.max_uses",
  search_max_rounds: "web_search.search_max_rounds",
  tavily_api_key: "web_search.tavily_api_key",
  firecrawl_api_key: "web_search.firecrawl_api_key"
};

function WebSearchFeatureField({
  field,
  resource,
  revision
}: {
  field: FieldSchema;
  resource: ConfigResource;
  revision: string;
}) {
  const { locale, t } = useI18n();
  const autosave = useObjectFieldState(resource, revision, field);
  const config = autosave.value;
  const docPath = configDocPathForResource(resource, field);
  const title = docPath ? configDescriptions[docPath].title[locale] : field.label;
  const supportLabel = t("field.webSearch.support", { label: title });
  const helpScope = `${resource.kind}-${resource.id}-${field.path}`;
  const maxUsesLabel = t("field.webSearch.maxUses", { label: title });
  const tavilyAPIKeyLabel = t("field.webSearch.tavilyAPIKey", { label: title });
  const firecrawlAPIKeyLabel = t("field.webSearch.firecrawlAPIKey", { label: title });
  const searchMaxRoundsLabel = t("field.webSearch.searchMaxRounds", { label: title });

  function commitKey(key: string, value: unknown) {
    autosave.commitValue(value === undefined ? omitObjectKey(config, key) : { ...config, [key]: value });
  }

  return (
    <div className="structured-feature-field structured-feature-field--web-search" aria-label={title}>
      <div className="structured-feature-field__grid">
        <div className="mb-field" data-variant="select">
          <div className="mb-field__control">
            <MaterialSelect
              ariaLabel={supportLabel}
              disabled={autosave.status === "saving"}
              label={supportLabel}
              options={webSearchSupportOptions}
              value={webSearchSupportValue(config.support)}
              onChange={(value) => commitKey("support", value)}
            />
          </div>
        </div>
        <IntegerObjectField
          autosave={autosave}
          fieldKey="max_uses"
          helpScope={helpScope}
          label={maxUsesLabel}
        />
        <IntegerObjectField
          autosave={autosave}
          fieldKey="search_max_rounds"
          helpScope={helpScope}
          label={searchMaxRoundsLabel}
        />
        <SecretObjectField
          autosave={autosave}
          fieldKey="tavily_api_key"
          helpScope={helpScope}
          label={tavilyAPIKeyLabel}
        />
        <SecretObjectField
          autosave={autosave}
          fieldKey="firecrawl_api_key"
          helpScope={helpScope}
          label={firecrawlAPIKeyLabel}
        />
      </div>
      {autosave.error ? (
        <p className="field-error" role="alert">
          {autosave.error.message}
        </p>
      ) : null}
    </div>
  );
}

type StructuredFeatureFieldHelp = {
  button: ReactNode;
  tooltip: ReactNode;
};

function useStructuredFeatureFieldHelp(
  fieldKey: WebSearchStructuredFieldKey,
  helpScope: string,
  label: string
): StructuredFeatureFieldHelp {
  const i18n = useI18n();
  const anchorRef = useRef<HTMLButtonElement | null>(null);
  const openedByHover = useRef(false);
  const [open, setOpen] = useState(false);
  const docPath = webSearchConfigPaths[fieldKey];
  const helpId = `structured-help-${helpScope}-${docPath}`.replace(/[^a-zA-Z0-9_-]/g, "-");
  const position = useAnchoredTooltipPosition(anchorRef, open);
  const helpParts = structuredFeatureHelpParts(docPath, label, i18n);

  return {
    button: (
      <MaterialIconButton
        className="schema-field__help"
        describedBy={open ? helpId : undefined}
        icon="help"
        label={i18n.t("field.helpFor", { label })}
        onBlur={() => setOpen(false)}
        onClick={() => {
          if (openedByHover.current) {
            openedByHover.current = false;
            setOpen(true);
            return;
          }
          setOpen((current) => !current);
        }}
        onFocus={() => setOpen(true)}
        onKeyDown={(event: KeyboardEvent<HTMLElement>) => {
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
        slot="trailing-icon"
      />
    ),
    tooltip: open ? (
        <StructuredFeatureTooltip
          helpId={helpId}
          helpParts={helpParts}
          position={position}
        />
      ) : null
  };
}

function StructuredFeatureTooltip({
  helpId,
  helpParts,
  position
}: {
  helpId: string;
  helpParts: StructuredFeatureHelpParts;
  position: TooltipPosition | undefined;
}) {
  return (
    <span className="rich-tooltip" id={helpId} role="tooltip" style={tooltipPositionStyle(position)}>
      <span className="rich-tooltip__subhead">{helpParts.subhead}</span>
      <span className="rich-tooltip__body">{helpParts.body}</span>
      <span className="rich-tooltip__metas">
        {helpParts.metas.map((meta, index) => (
          <span className="rich-tooltip__chip" key={index}>
            {meta.label ? `${meta.label}: ${meta.value}` : meta.value}
          </span>
        ))}
      </span>
    </span>
  );
}

type StructuredFeatureHelpParts = {
  subhead: string;
  body: string;
  metas: { label?: string; value: string }[];
};

function structuredFeatureHelpParts(
  docPath: ConfigPath,
  label: string,
  { locale, t }: ReturnType<typeof useI18n>
): StructuredFeatureHelpParts {
  const entry = configDescriptions[docPath];
  const metas: { label?: string; value: string }[] = [
    { label: t("configDoc.type"), value: localizedConfigMetaValue(entry.type, t) }
  ];
  if (entry.defaultValue) {
    metas.push({ label: t("configDoc.default"), value: localizedConfigMetaValue(String(entry.defaultValue), t) });
  }
  if (entry.sensitive) {
    metas.push({ value: t("configDoc.sensitive") });
  }
  return {
    subhead: label,
    body: entry.description[locale],
    metas
  };
}

function localizedConfigMetaValue(value: string, t: ReturnType<typeof useI18n>["t"]) {
  const normalized = value.trim().toLowerCase();
  const localized: Partial<Record<string, MessageKey>> = {
    array: "configDoc.type.array",
    boolean: "configDoc.type.boolean",
    empty: "configDoc.default.empty",
    "host:port": "configDoc.type.hostPort",
    number: "configDoc.type.number",
    object: "configDoc.type.object",
    string: "configDoc.type.string",
    url: "configDoc.type.url"
  };
  return localized[normalized] ? t(localized[normalized]) : value;
}

function tooltipPositionStyle(position: TooltipPosition | undefined): CSSProperties | undefined {
  if (!position) {
    return undefined;
  }
  return {
    left: `${position.left}px`,
    maxWidth: `${position.maxWidth}px`,
    position: "fixed",
    top: `${position.top}px`
  };
}

function SecretObjectField({
  autosave,
  fieldKey,
  helpScope,
  label
}: {
  autosave: ObjectFieldState;
  fieldKey: WebSearchStructuredFieldKey;
  helpScope: string;
  label: string;
}) {
  const help = useStructuredFeatureFieldHelp(fieldKey, helpScope, label);
  const [draft, setDraft] = useState(() => objectStringDraft(autosave.value[fieldKey], true));
  const committedDraft = objectStringDraft(autosave.value[fieldKey], true);

  useEffect(() => {
    setDraft(committedDraft);
  }, [committedDraft]);

  const commit = useCallback(() => {
    if (draft === committedDraft) {
      return;
    }
    autosave.commitValue({ ...autosave.value, [fieldKey]: draft });
  }, [autosave, committedDraft, draft, fieldKey]);

  return (
    <div className="mb-field" data-variant="input">
      <div className="mb-field__control">
        <MaterialOutlinedTextField
          autoComplete="new-password"
          className="structured-feature-field__secret"
          disabled={autosave.status === "saving"}
          label={label}
          spellCheck={false}
          trailingIcon={help.button}
          type="password"
          value={draft}
          onBlur={commit}
          onInput={setDraft}
        />
      </div>
      {help.tooltip}
    </div>
  );
}

function IntegerObjectField({
  autosave,
  fieldKey,
  helpScope,
  label
}: {
  autosave: ObjectFieldState;
  fieldKey: WebSearchStructuredFieldKey;
  helpScope: string;
  label: string;
}) {
  const { t } = useI18n();
  const help = useStructuredFeatureFieldHelp(fieldKey, helpScope, label);
  const [draft, setDraft] = useState(() => objectIntegerDraft(autosave.value[fieldKey]));
  const [localError, setLocalError] = useState("");
  const committedDraft = objectIntegerDraft(autosave.value[fieldKey]);

  useEffect(() => {
    setDraft(committedDraft);
    setLocalError("");
  }, [committedDraft]);

  const commit = useCallback(() => {
    const trimmed = draft.trim();
    if (trimmed === "") {
      setLocalError("");
      autosave.commitValue(omitObjectKey(autosave.value, fieldKey));
      return;
    }
    if (!/^\d+$/.test(trimmed)) {
      setLocalError(t("field.invalidNumber"));
      return;
    }
    const parsed = Number.parseInt(trimmed, 10);
    setLocalError("");
    autosave.commitValue({ ...autosave.value, [fieldKey]: parsed });
  }, [autosave, draft, fieldKey, t]);

  return (
    <div className="mb-field" data-variant="input">
      <div className="mb-field__control">
        <MaterialOutlinedTextField
          ariaInvalid={Boolean(localError)}
          className="structured-feature-field__number"
          disabled={autosave.status === "saving"}
          error={Boolean(localError)}
          errorText={localError}
          inputMode="numeric"
          label={label}
          spellCheck={false}
          trailingIcon={help.button}
          type="text"
          value={draft}
          onBlur={commit}
          onInput={(value) => {
            setDraft(value);
            if (localError && value.trim() === committedDraft) {
              setLocalError("");
            }
          }}
        />
      </div>
      {help.tooltip}
      {localError ? (
        <p className="field-error field-error--sr" role="alert">
          {localError}
        </p>
      ) : null}
    </div>
  );
}

function ExtensionsFeatureField({
  field,
  resource,
  revision
}: {
  field: FieldSchema;
  resource: ConfigResource;
  revision: string;
}) {
  const { locale, t } = useI18n();
  const [draft, setDraft] = useState("");
  const autosave = useObjectFieldState(resource, revision, field);
  const docPath = configDocPathForResource(resource, field);
  const title = docPath ? configDescriptions[docPath].title[locale] : field.label;
  const extensions = extensionEntries(autosave.value);
  const extensionNames = extensions.map((entry) => entry.name);
  const trimmedDraft = draft.trim();
  const duplicateDraft = extensionNames.includes(trimmedDraft);
  const addDisabled = !trimmedDraft || duplicateDraft || autosave.status === "saving";

  function addExtension() {
    if (addDisabled) {
      return;
    }
    setDraft("");
    autosave.commitValue({
      ...autosave.value,
      [trimmedDraft]: { enabled: true }
    });
  }

  function removeExtension(name: string) {
    autosave.commitValue(omitObjectKey(autosave.value, name));
  }

  function setExtensionEnabled(name: string, enabled: boolean) {
    autosave.commitValue({
      ...autosave.value,
      [name]: {
        ...extensionObjectValue(autosave.value[name]),
        enabled
      }
    });
  }

  function setExtensionConfigValue(name: string, key: string, value: unknown) {
    const extension = extensionObjectValue(autosave.value[name]);
    const config = objectRecordOrEmpty(extension.config);
    autosave.commitValue({
      ...autosave.value,
      [name]: {
        ...extension,
        config: value === undefined
          ? omitObjectKey(config, key)
          : { ...config, [key]: value }
      }
    });
  }

  return (
    <div className="structured-feature-field structured-feature-field--extensions" aria-label={title}>
      <div className="extension-feature-list" role="list" aria-label={title}>
        {extensions.map((entry) => (
          <div
            className="extension-feature-row"
            data-extension-name={entry.name}
            key={entry.name}
            role="listitem"
          >
            <MaterialInputChip
              className="extension-feature-row__chip"
              disabled={autosave.status === "saving"}
              label={t("field.extensions.remove", { name: entry.name })}
              onRemove={() => removeExtension(entry.name)}
            >
              {entry.name}
            </MaterialInputChip>
            <span className="extension-feature-row__switch" aria-label={t("field.extensions.enable", { name: entry.name })}>
              <MaterialSwitch
                disabled={autosave.status === "saving"}
                label={t("field.extensions.enable", { name: entry.name })}
                selected={entry.enabled}
                onChange={(enabled) => setExtensionEnabled(entry.name, enabled)}
              />
            </span>
            <ExtensionConfigFields
              disabled={autosave.status === "saving"}
              entry={entry}
              onCommit={(key, value) => setExtensionConfigValue(entry.name, key, value)}
            />
          </div>
        ))}
      </div>
      <div className="editable-list-field__composer">
        <MaterialOutlinedTextField
          ariaLabel={t("field.extensions.addInput", { label: title })}
          className="editable-list-field__input"
          label={t("field.extensions.addInput", { label: title })}
          spellCheck={false}
          value={draft}
          onInput={setDraft}
          onBlur={() => undefined}
        />
        <MaterialFilledButton
          ariaLabel={t("field.extensions.addAction", { label: title })}
          className="editable-list-field__add"
          disabled={addDisabled}
          icon="add"
          onClick={addExtension}
        >
          {t("field.editableList.add")}
        </MaterialFilledButton>
      </div>
      {autosave.error ? (
        <p className="field-error" role="alert">
          {autosave.error.message}
        </p>
      ) : null}
    </div>
  );
}

function ExtensionConfigFields({
  disabled,
  entry,
  onCommit
}: {
  disabled: boolean;
  entry: ExtensionEntry;
  onCommit: (key: string, value: unknown) => void;
}) {
  const fields = extensionConfigFields(entry);
  if (fields.length === 0) {
    return null;
  }
  return (
    <div className="extension-config-grid">
      {fields.map((field) => (
        <ExtensionConfigField
          disabled={disabled}
          field={field}
          key={field.key}
          value={entry.config[field.key]}
          onCommit={(value) => onCommit(field.key, value)}
        />
      ))}
    </div>
  );
}

function ExtensionConfigField({
  disabled,
  field,
  onCommit,
  value
}: {
  disabled: boolean;
  field: ExtensionConfigFieldDef;
  onCommit: (value: unknown) => void;
  value: unknown;
}) {
  if (field.type === "boolean") {
    return (
      <div className="schema-field schema-field--inline">
        <div className="schema-field__switch-line">
          <span className="schema-field__label-row">
            <span className="schema-field__label">{field.label}</span>
          </span>
          <MaterialSwitch
            disabled={disabled}
            label={field.label}
            selected={value === true}
            onChange={onCommit}
          />
        </div>
      </div>
    );
  }
  if (field.type === "number") {
    return (
      <ExtensionConfigNumberField
        disabled={disabled}
        field={field}
        value={value}
        onCommit={onCommit}
      />
    );
  }
  return (
    <ExtensionConfigTextField
      disabled={disabled}
      field={field}
      value={value}
      onCommit={onCommit}
    />
  );
}

function ExtensionConfigTextField({
  disabled,
  field,
  onCommit,
  value
}: {
  disabled: boolean;
  field: ExtensionConfigFieldDef;
  onCommit: (value: unknown) => void;
  value: unknown;
}) {
  const [draft, setDraft] = useState(() => objectStringDraft(value, false));
  const committedDraft = objectStringDraft(value, false);

  useEffect(() => {
    setDraft(committedDraft);
  }, [committedDraft]);

  return (
    <div className="mb-field" data-variant="input">
      <div className="mb-field__control">
        <MaterialOutlinedTextField
          disabled={disabled}
          label={field.label}
          spellCheck={false}
          type="text"
          value={draft}
          onBlur={() => onCommit(draft.trim() === "" ? undefined : draft)}
          onInput={setDraft}
        />
      </div>
    </div>
  );
}

function ExtensionConfigNumberField({
  disabled,
  field,
  onCommit,
  value
}: {
  disabled: boolean;
  field: ExtensionConfigFieldDef;
  onCommit: (value: unknown) => void;
  value: unknown;
}) {
  const { t } = useI18n();
  const [draft, setDraft] = useState(() => objectNumberDraft(value));
  const [localError, setLocalError] = useState("");
  const committedDraft = objectNumberDraft(value);

  useEffect(() => {
    setDraft(committedDraft);
    setLocalError("");
  }, [committedDraft]);

  function commit() {
    const trimmed = draft.trim();
    if (trimmed === "") {
      setLocalError("");
      onCommit(undefined);
      return;
    }
    const parsed = Number(trimmed);
    if (!Number.isFinite(parsed)) {
      setLocalError(t("field.invalidNumber"));
      return;
    }
    setLocalError("");
    onCommit(parsed);
  }

  return (
    <div className="mb-field" data-variant="input">
      <div className="mb-field__control">
        <MaterialOutlinedTextField
          ariaInvalid={Boolean(localError)}
          disabled={disabled}
          error={Boolean(localError)}
          errorText={localError}
          inputMode="numeric"
          label={field.label}
          spellCheck={false}
          type="text"
          value={draft}
          onBlur={commit}
          onInput={(next) => {
            setDraft(next);
            if (localError && next.trim() === committedDraft) {
              setLocalError("");
            }
          }}
        />
      </div>
      {localError ? (
        <p className="field-error field-error--sr" role="alert">
          {localError}
        </p>
      ) : null}
    </div>
  );
}

function GenericEditableListResourceField({
  field,
  resource,
  revision
}: {
  field: FieldSchema;
  resource: ConfigResource;
  revision: string;
}) {
  const autosave = useGenericEditableListState(resource, revision, field);
  return (
    <EditableListResourceField
      autosave={autosave}
      field={field}
      valueFromDraft={stringListItemLabel}
      valueFromInput={(input) => input}
    />
  );
}

function EditableListResourceField<T>({
  autosave,
  field,
  valueFromDraft,
  valueFromInput
}: {
  autosave: EditableListState<T>;
  field: FieldSchema;
  valueFromDraft: (item: T) => string;
  valueFromInput: (input: string) => T;
}) {
  const { locale, t } = useI18n();
  const [draft, setDraft] = useState("");
  const label = autosave.label[locale];
  const items = autosave.value;
  const trimmedDraft = draft.trim();
  const itemLabels = useMemo(
    () => items.map((item) => valueFromDraft(item)),
    [items, valueFromDraft]
  );
  const duplicateDraft = trimmedDraft ? itemLabels.includes(trimmedDraft) : false;
  const addDisabled = !trimmedDraft || duplicateDraft || autosave.status === "saving";

  function commitItems(nextItems: T[]) {
    autosave.commitValue(nextItems);
  }

  function addDraft() {
    if (!trimmedDraft || duplicateDraft) {
      return;
    }
    setDraft("");
    commitItems([...items, valueFromInput(trimmedDraft)]);
  }

  function removeItem(removed: T) {
    const removedLabel = valueFromDraft(removed);
    commitItems(items.filter((item) => valueFromDraft(item) !== removedLabel));
  }

  return (
    <div className="editable-list-field" aria-label={label}>
      <div className="editable-list-field__header">
        <span className="editable-list-field__title">{label}</span>
      </div>
      <ChipSet className="editable-list-field__items" role="list" aria-label={label}>
        {items.map((item) => {
          const itemLabel = valueFromDraft(item);
          return (
            <MaterialInputChip
              className="editable-list-field__chip"
              disabled={autosave.status === "saving"}
              key={itemLabel}
              label={t("field.editableList.remove", { item: itemLabel, label })}
              onRemove={() => removeItem(item)}
            >
              {itemLabel}
            </MaterialInputChip>
          );
        })}
      </ChipSet>
      <div className="editable-list-field__composer">
        <MaterialOutlinedTextField
          ariaLabel={t("field.editableList.addInput", { label })}
          className="editable-list-field__input"
          label={t("field.editableList.addInput", { label })}
          spellCheck={false}
          value={draft}
          onInput={setDraft}
          onBlur={() => undefined}
        />
        <MaterialFilledButton
          ariaLabel={t("field.editableList.addAction", { label })}
          className="editable-list-field__add"
          disabled={addDisabled}
          icon="add"
          onClick={addDraft}
        >
          {t("field.editableList.add")}
        </MaterialFilledButton>
      </div>
      {autosave.error ? (
        <p className="field-error" role="alert">
          {autosave.error.message}
        </p>
      ) : null}
    </div>
  );
}

type ReasoningLevelPreset = {
  effort: string;
  description?: string;
};

type LocalizedLabel = Record<"en-US" | "zh-CN", string>;

type BooleanFieldState = {
  commitValue: (value: boolean) => void;
  status: AutosaveFieldStatus;
  value: boolean;
};

type ObjectFieldState = {
  commitValue: (value: Record<string, unknown>) => void;
  commitSerializedValue: (value: Record<string, unknown>, serialized: Record<string, unknown> | null) => void;
  error?: FieldError;
  status: AutosaveFieldStatus;
  value: Record<string, unknown>;
};

type EditableListState<T> = {
  commitValue: (value: T[]) => void;
  error?: FieldError;
  label: LocalizedLabel;
  status: AutosaveFieldStatus;
  value: T[];
};

type ModelReasoningLevelsState = EditableListState<ReasoningLevelPreset>;

const webSearchSupportOptions: MaterialSelectOption[] = [
  { value: "auto", label: "auto" },
  { value: "enabled", label: "enabled" },
  { value: "disabled", label: "disabled" },
  { value: "injected", label: "injected" }
];

function useObjectFieldState(
  resource: ConfigResource,
  revision: string,
  field: FieldSchema
): ObjectFieldState {
  const { t } = useI18n();
  const value = resource.value[field.path];
  const committedObjectKey = JSON.stringify(objectRecord(value));
  const committedObject = useMemo(
    () => JSON.parse(committedObjectKey) as Record<string, unknown>,
    [committedObjectKey]
  );
  const saveGraphField = useGraphFieldSaver<Record<string, unknown> | null>();
  const save = useCallback(
    (request: SaveFieldRequest<Record<string, unknown> | null>) => saveGraphField(request),
    [saveGraphField]
  );
  const autosave = useAutosaveField<Record<string, unknown>, Record<string, unknown> | null>({
    resourceKind: resource.kind,
    resourceId: resource.id,
    field: field.path,
    committedValue: committedObject,
    revision,
    save,
    configUpdateFailedMessage: (result) => t("field.configUpdateFailed", { result }),
    requestFailedMessage: t("error.requestFailed")
  });
  useReportFieldStatus(`${resource.kind}:${resource.id}:${field.path}`, autosave.status);
  return {
    commitValue: autosave.commitValue,
    commitSerializedValue: autosave.commitSerializedValue,
    error: autosave.error,
    status: autosave.status,
    value: autosave.value
  };
}

function useModelReasoningSupport(
  resource: ConfigResource,
  revision: string,
  field: FieldSchema | undefined
): BooleanFieldState | undefined {
  const { t } = useI18n();
  const selected = field ? resource.value[field.path] === true : false;
  const saveGraphField = useGraphFieldSaver<boolean>();
  const save = useCallback(
    (request: SaveFieldRequest<boolean>) => saveGraphField(request),
    [saveGraphField]
  );
  const autosave = useAutosaveField({
    resourceKind: resource.kind,
    resourceId: resource.id,
    field: "supports_reasoning",
    committedValue: selected,
    revision,
    save,
    disabled: !field,
    configUpdateFailedMessage: (result) => t("field.configUpdateFailed", { result }),
    requestFailedMessage: t("error.requestFailed")
  });
  useReportFieldStatus(`${resource.kind}:${resource.id}:supports_reasoning`, autosave.status);
  if (!field) {
    return undefined;
  }
  return {
    commitValue: autosave.commitValue,
    status: autosave.status,
    value: autosave.value
  };
}

function requiredModelReasoningSupport(state: BooleanFieldState | undefined) {
  if (!state) {
    throw new Error("Model reasoning support field is required to render model reasoning controls.");
  }
  return state;
}

function useGenericEditableListState(
  resource: ConfigResource,
  revision: string,
  field: FieldSchema
): EditableListState<string> {
  const { t } = useI18n();
  const value = resource.value[field.path];
  const committedItemsKey = Array.isArray(value)
    ? JSON.stringify(value.map((item) => String(item)))
    : "[]";
  const committedItems = useMemo(
    () => JSON.parse(committedItemsKey) as string[],
    [committedItemsKey]
  );
  const saveGraphField = useGraphFieldSaver<string[]>();
  const save = useCallback(
    (request: SaveFieldRequest<string[]>) => saveGraphField(request),
    [saveGraphField]
  );
  const autosave = useAutosaveField({
    resourceKind: resource.kind,
    resourceId: resource.id,
    field: field.path,
    committedValue: committedItems,
    revision,
    save,
    configUpdateFailedMessage: (result) => t("field.configUpdateFailed", { result }),
    requestFailedMessage: t("error.requestFailed")
  });
  useReportFieldStatus(`${resource.kind}:${resource.id}:${field.path}`, autosave.status);
  const docPath = configDocPathForResource(resource, field);
  const label = docPath ? configDescriptions[docPath].title : localizedFallback(field.label);
  return {
    commitValue: autosave.commitValue,
    error: autosave.error,
    label,
    status: autosave.status,
    value: autosave.value
  };
}

function useModelReasoningLevels(
  resource: ConfigResource,
  revision: string,
  field: FieldSchema | undefined
): ModelReasoningLevelsState | undefined {
  const { t } = useI18n();
  const value = field ? resource.value[field.path] : undefined;
  const committedItemsKey = JSON.stringify(toReasoningLevelPresets(value));
  const committedItems = useMemo(
    () => JSON.parse(committedItemsKey) as ReasoningLevelPreset[],
    [committedItemsKey]
  );
  const saveGraphField = useGraphFieldSaver<ReasoningLevelPreset[]>();
  const save = useCallback(
    (request: SaveFieldRequest<ReasoningLevelPreset[]>) => saveGraphField(request),
    [saveGraphField]
  );
  const autosave = useAutosaveField({
    resourceKind: resource.kind,
    resourceId: resource.id,
    field: "supported_reasoning_levels",
    committedValue: committedItems,
    revision,
    save,
    disabled: !field,
    configUpdateFailedMessage: (result) => t("field.configUpdateFailed", { result }),
    requestFailedMessage: t("error.requestFailed")
  });
  useReportFieldStatus(`${resource.kind}:${resource.id}:supported_reasoning_levels`, autosave.status);
  if (!field) {
    return undefined;
  }
  const docPath = configDocPathForResource(resource, field);
  return {
    commitValue: autosave.commitValue,
    error: autosave.error,
    label: docPath ? configDescriptions[docPath].title : localizedFallback(field.label),
    status: autosave.status,
    value: autosave.value
  };
}

function requiredModelReasoningLevels(state: ModelReasoningLevelsState | undefined) {
  if (!state) {
    throw new Error("Model reasoning levels field is required to render model reasoning controls.");
  }
  return state;
}

function DefaultReasoningLevelField({
  field,
  levels,
  resource,
  revision
}: {
  field: FieldSchema;
  levels: ReasoningLevelPreset[];
  resource: ConfigResource;
  revision: string;
}) {
  const { locale, t } = useI18n();
  const value = resource.value[field.path];
  const selected = typeof value === "string" ? value : "";
  const saveGraphField = useGraphFieldSaver<string>();
  const save = useCallback(
    (request: SaveFieldRequest<string>) => saveGraphField(request),
    [saveGraphField]
  );
  const autosave = useAutosaveField({
    resourceKind: resource.kind,
    resourceId: resource.id,
    field: field.path,
    committedValue: selected,
    revision,
    save,
    configUpdateFailedMessage: (result) => t("field.configUpdateFailed", { result }),
    requestFailedMessage: t("error.requestFailed")
  });
  useReportFieldStatus(`${resource.kind}:${resource.id}:${field.path}`, autosave.status);
  const docPath = configDocPathForResource(resource, field);
  const label = docPath ? configDescriptions[docPath].title[locale] : field.label;
  const options = useMemo(
    () => reasoningLevelOptions(levels, autosave.value),
    [autosave.value, levels]
  );

  return (
    <div className="mb-field" data-variant="select">
      <div className="mb-field__control">
        <MaterialSelect
          ariaLabel={label}
          disabled={autosave.status === "saving"}
          error={Boolean(autosave.error)}
          errorText={autosave.error?.message}
          label={label}
          options={options}
          required={field.required}
          value={autosave.value}
          onChange={autosave.commitValue}
        />
      </div>
      {autosave.error ? (
        <p className="field-error" role="alert">
          {autosave.error.message}
        </p>
      ) : null}
    </div>
  );
}

function toReasoningLevelPresets(value: unknown): ReasoningLevelPreset[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value
    .map((item) => {
      if (typeof item === "string") {
        return { effort: item };
      }
      if (!item || typeof item !== "object") {
        return undefined;
      }
      const effort = "effort" in item ? (item as { effort?: unknown }).effort : undefined;
      if (typeof effort !== "string" || effort.trim() === "") {
        return undefined;
      }
      const description = "description" in item ? (item as { description?: unknown }).description : undefined;
      return typeof description === "string" && description.trim()
        ? { effort, description }
        : { effort };
    })
    .filter((item): item is ReasoningLevelPreset => item !== undefined);
}

function reasoningLevelOptions(levels: ReasoningLevelPreset[], selected: string): MaterialSelectOption[] {
  const options = levels.map((level) => ({
    value: level.effort,
    label: level.effort
  }));
  if (selected && !options.some((option) => option.value === selected)) {
    return [{ value: selected, label: selected }, ...options];
  }
  return options;
}

function reasoningLevelEffort(item: unknown) {
  if (!item || typeof item !== "object" || !("effort" in item) || typeof item.effort !== "string") {
    throw new Error("Reasoning level preset requires a string effort.");
  }
  return item.effort;
}

function newReasoningLevel(effort: string): ReasoningLevelPreset {
  return { effort };
}

function objectRecord(value: unknown): Record<string, unknown> {
  if (value === undefined || value === null) {
    return {};
  }
  if (typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Structured feature field requires an object value.");
  }
  return value as Record<string, unknown>;
}

function omitObjectKey(source: Record<string, unknown>, key: string): Record<string, unknown> {
  const next = { ...source };
  delete next[key];
  return next;
}

function webSearchSupportValue(value: unknown) {
  if (typeof value !== "string" || value.trim() === "") {
    return "auto";
  }
  if (!webSearchSupportOptions.some((option) => option.value === value)) {
    throw new Error(`Unknown web search support mode: ${value}`);
  }
  return value;
}

function objectIntegerDraft(value: unknown) {
  if (value === undefined || value === null || value === "") {
    return "";
  }
  if (typeof value !== "number" || !Number.isInteger(value)) {
    throw new Error("Structured integer field requires an integer value.");
  }
  return String(value);
}

type ExtensionEntry = {
  config: Record<string, unknown>;
  enabled: boolean;
  name: string;
};

type ExtensionConfigFieldDef = {
  key: string;
  label: string;
  type: "string" | "number" | "boolean";
};

const knownExtensionConfigFields: Record<string, ExtensionConfigFieldDef[]> = {
  deepseek_v4: [
    { key: "reinforce_instructions", label: "deepseek_v4 reinforce instructions", type: "boolean" },
    { key: "reinforce_prompt", label: "deepseek_v4 reinforce prompt", type: "string" }
  ],
  db_d1: [
    { key: "binding", label: "db_d1 binding", type: "string" }
  ],
  db_sqlite: [
    { key: "path", label: "db_sqlite path", type: "string" },
    { key: "wal", label: "db_sqlite WAL", type: "boolean" },
    { key: "busy_timeout_ms", label: "db_sqlite busy timeout ms", type: "number" },
    { key: "max_open_conns", label: "db_sqlite max open conns", type: "number" }
  ],
  kimi_workaround: [
    { key: "max_tool_rounds", label: "kimi_workaround max tool rounds", type: "number" },
    { key: "convergence_margin", label: "kimi_workaround convergence margin", type: "number" }
  ],
  metrics: [
    { key: "default_limit", label: "metrics default limit", type: "number" },
    { key: "max_limit", label: "metrics max limit", type: "number" }
  ],
  visual: [
    { key: "provider", label: "visual provider", type: "string" },
    { key: "model", label: "visual model", type: "string" },
    { key: "max_rounds", label: "visual max rounds", type: "number" },
    { key: "max_tokens", label: "visual max tokens", type: "number" }
  ]
};

function extensionEntries(value: Record<string, unknown>): ExtensionEntry[] {
  return Object.keys(value)
    .sort((left, right) => left.localeCompare(right))
    .map((name) => {
      const objectValue = extensionObjectValue(value[name]);
      return {
        name,
        config: objectRecordOrEmpty(objectValue.config),
        enabled: extensionEnabledValue(value[name])
      };
    });
}

function extensionConfigFields(entry: ExtensionEntry) {
  const knownFields = knownExtensionConfigFields[entry.name] ?? [];
  const knownKeys = new Set(knownFields.map((field) => field.key));
  const extraFields = Object.keys(entry.config)
    .filter((key) => !knownKeys.has(key))
    .sort((left, right) => left.localeCompare(right))
    .flatMap((key) => {
      const fieldType = extensionConfigFieldType(entry.config[key]);
      return fieldType ? [{ key, label: `${entry.name} ${key}`, type: fieldType }] : [];
    });
  return knownFields.concat(extraFields);
}

function extensionConfigFieldType(value: unknown): ExtensionConfigFieldDef["type"] | undefined {
  if (value === undefined || value === null) {
    return "string";
  }
  if (typeof value === "boolean") {
    return "boolean";
  }
  if (typeof value === "number") {
    return "number";
  }
  if (typeof value === "string") {
    return "string";
  }
  return undefined;
}

function extensionEnabledValue(value: unknown) {
  const objectValue = extensionObjectValue(value);
  if (!("enabled" in objectValue)) {
    return true;
  }
  if (typeof objectValue.enabled !== "boolean") {
    throw new Error("Extension enabled value must be boolean when present.");
  }
  return objectValue.enabled;
}

function extensionObjectValue(value: unknown): Record<string, unknown> {
  if (value === undefined || value === null) {
    return {};
  }
  if (typeof value !== "object" || Array.isArray(value)) {
    throw new Error("Extension entry must be an object.");
  }
  return value as Record<string, unknown>;
}

function objectRecordOrEmpty(value: unknown): Record<string, unknown> {
  if (value === undefined || value === null) {
    return {};
  }
  return objectRecord(value);
}

function objectStringDraft(value: unknown, secret: boolean) {
  if (secret && value === "******") {
    return "";
  }
  if (value === undefined || value === null) {
    return "";
  }
  if (typeof value !== "string") {
    throw new Error("Structured string field requires a string value.");
  }
  return value;
}

function objectNumberDraft(value: unknown) {
  if (value === undefined || value === null || value === "") {
    return "";
  }
  if (typeof value !== "number") {
    throw new Error("Structured number field requires a number value.");
  }
  return String(value);
}

function stringListItemLabel(item: unknown) {
  if (typeof item !== "string") {
    throw new Error("Editable string list item must be a string.");
  }
  return item;
}

function localizedFallback(label: string): LocalizedLabel {
  return {
    "en-US": label,
    "zh-CN": label
  };
}

function orderModelBasicFields(fields: FieldSchema[]) {
  return [
    "context_window",
    "max_output_tokens",
    "description",
    "base_instructions"
  ].flatMap((path) => fields.filter((field) => field.path === path))
    .concat(fields.filter((field) => !modelBasicPreferredOrder.has(field.path)));
}

const modelBasicPreferredOrder = new Set([
  "context_window",
  "max_output_tokens",
  "description",
  "base_instructions"
]);

function orderModelReasoningFields(fields: FieldSchema[]) {
  return [
    "supports_reasoning",
    "default_reasoning_level",
    "default_reasoning_summary",
    "supported_reasoning_levels",
    "supports_reasoning_summaries"
  ].flatMap((path) => fields.filter((field) => field.path === path))
    .concat(fields.filter((field) => !modelReasoningPaths.has(field.path)));
}

function isWideField(field: FieldSchema) {
  return (
    field.control === "textarea" ||
    field.control === "object" ||
    field.control === "array" ||
    field.type === "object" ||
    field.type === "array"
  );
}

function fieldGridClass(field: FieldSchema) {
  if (isWideField(field)) {
    return "form-grid__wide";
  }
  if (
    field.type === "number" ||
    field.type === "boolean" ||
    field.control === "number" ||
    field.control === "switch" ||
    compactFieldPaths.has(field.path)
  ) {
    return "form-grid__compact";
  }
  return "form-grid__medium";
}

const compactFieldPaths = new Set([
  "active_provider",
  "addr",
  "default_reasoning_level",
  "default_reasoning_summary",
  "format",
  "level",
  "max_sessions",
  "mode",
  "priority",
  "search_max_rounds",
  "session_ttl",
  "support",
  "ttl",
  "version"
]);

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
