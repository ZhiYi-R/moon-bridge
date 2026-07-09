import {
  type CSSProperties,
  type KeyboardEvent,
  type RefObject,
  type ReactNode,
  useEffect,
  useMemo,
  useRef,
  useState
} from "react";
import { configDescriptions, type ConfigPath } from "../../configDocs/configDescriptions";
import type { FieldSchema } from "../../rpc/types";
import { useI18n } from "../../i18n/I18nProvider";
import type { MessageKey } from "../../i18n/messages";
import { MaterialIconButton } from "../../components/MaterialButton";
import { MaterialOutlinedTextField, type MaterialTextFieldElement } from "../../components/MaterialTextField";
import { MaterialSwitch } from "../../components/MaterialSwitch";
import { SelectMenu, type SelectMenuOption } from "./SelectMenu";
import { type TooltipPosition, useAnchoredTooltipPosition } from "./helpTooltipPosition";
import { protocolIconForValue } from "./modelProviderIcons";

export type SchemaFieldProps = {
  field: FieldSchema;
  value: unknown;
  onChange: (value: unknown) => void;
  onCommit?: () => void;
  onCommitValue?: (value: unknown) => void;
  clearSecretDraft?: boolean;
  disabled?: boolean;
  idPrefix?: string;
  docPath?: ConfigPath;
  error?: string;
  /** Explicit select options (e.g. route model/provider picked from existing resources).
   *  When provided, the field renders as a select regardless of enum/control. */
  options?: SelectMenuOption[];
  /** Soft warning shown beneath the field (e.g. a route references a missing model). */
  warning?: string;
  leadingIconNode?: ReactNode;
  objectDisplay?: "collapsible" | "expandedFixed";
};

export function SchemaField({
  field,
  value,
  onChange,
  onCommit,
  onCommitValue,
  clearSecretDraft = false,
  disabled = false,
  idPrefix,
  docPath,
  error,
  options,
  warning,
  leadingIconNode,
  objectDisplay = "collapsible"
}: SchemaFieldProps) {
  const { locale, t } = useI18n();
  const id = useMemo(() => {
    const prefix = idPrefix ? `${idPrefix}-` : "";
    return `schema-field-${prefix}${field.path}`.replace(/[^a-zA-Z0-9_-]/g, "-");
  }, [field.path, idPrefix]);
  const [text, setText] = useState(displayValue(field, value));
  const [parseError, setParseError] = useState("");
  const [helpOpen, setHelpOpen] = useState(false);
  const [revealed, setRevealed] = useState(false);
  const emittedSecretDraftRef = useRef<string | undefined>(undefined);
  const trailingHelpAnchorRef = useRef<HTMLButtonElement>(null);
  const displayLabel = fieldLabel(field, docPath, locale);

  useEffect(() => {
    if (isSecretField(field)) {
      if (typeof value === "string" && value === emittedSecretDraftRef.current) {
        setText(value);
      } else {
        emittedSecretDraftRef.current = undefined;
        setText("");
      }
      setParseError("");
      return;
    }
    setText(displayValue(field, value));
    setParseError("");
  }, [field, value]);

  useEffect(() => {
    if (!clearSecretDraft || !isSecretField(field)) {
      return;
    }
    emittedSecretDraftRef.current = undefined;
    setText("");
    setParseError("");
  }, [clearSecretDraft, field]);

  const wide = isWideField(field);
  const errorId = `${id}-error`;
  const warnId = `${id}-warning`;
  const helpId = `${id}-help`;
  const helpParts = fieldHelpParts(field, displayLabel, docPath, locale, {
    default: t("configDoc.default"),
    defaultEmpty: t("configDoc.default.empty"),
    optional: t("configDoc.optional"),
    required: t("configDoc.required"),
    restartMayBeRequired: t("configDoc.restartMayBeRequired"),
    savedRealtime: t("configDoc.savedRealtime"),
    sensitive: t("configDoc.sensitive"),
    type: t("configDoc.type"),
    typeArray: t("configDoc.type.array"),
    typeBoolean: t("configDoc.type.boolean"),
    typeHostPort: t("configDoc.type.hostPort"),
    typeNumber: t("configDoc.type.number"),
    typeObject: t("configDoc.type.object"),
    typeString: t("configDoc.type.string"),
    typeUrl: t("configDoc.type.url")
  });
  const fieldError = error || parseError;
  const fieldDescribedBy = [
    helpOpen ? helpId : undefined,
    fieldError ? errorId : undefined
  ].filter(Boolean).join(" ") || undefined;
  const commitOnBlur = onCommit ? () => onCommit() : undefined;
  const commit = onCommitValue ?? onChange;

  if (field.control === "select" || (field.enum?.length ?? 0) > 0 || (options?.length ?? 0) > 0) {
    const selected = typeof value === "string" ? value : "";
    const useProtocolIcons = isProviderProtocolField(field, docPath);
    const resolvedOptions: SelectMenuOption[] = options && options.length > 0
      ? options
      : (field.enum ?? []).map((option) => ({
          value: option,
          label: optionLabel(option, t),
          leadingIcon: useProtocolIcons ? protocolIconForValue(option) : undefined
        }));
    const selectedOption = resolvedOptions.find((option) => option.value === selected);
    return (
      <div className={wide ? "mb-field mb-field--wide" : "mb-field"} data-variant="select">
        <div className="mb-field__control">
          <SelectMenu
            id={id}
            options={resolvedOptions}
            value={selected}
            onChange={(next) => commit(next)}
            disabled={disabled}
            ariaLabel={displayLabel}
            describedBy={fieldError ? errorId : warning ? warnId : undefined}
            error={Boolean(fieldError)}
            errorText={fieldError}
            leadingIcon={useProtocolIcons ? protocolIconForValue(selected) : selectedOption?.leadingIcon}
            required={field.required}
          />
        </div>
        <FieldA11yMessages errorId={errorId} error={fieldError} />
        {warning && !fieldError ? (
          <p className="field-warning" id={warnId} role="note">
            {warning}
          </p>
        ) : null}
      </div>
    );
  }

  if (field.type === "boolean" || field.control === "switch") {
    return (
      <div className="schema-field schema-field--inline">
        <div className="schema-field__switch-line">
          <span className="schema-field__label-row">
            <span className="schema-field__label">
              {displayLabel}
              {field.required ? <span className="schema-field__required" aria-hidden="true">*</span> : null}
            </span>
            <FieldHelpButton
              field={field}
              label={displayLabel}
              helpId={helpId}
              helpOpen={helpOpen}
              helpParts={helpParts}
              setHelpOpen={setHelpOpen}
            />
          </span>
          <MaterialSwitch
            disabled={disabled}
            label={displayLabel}
            selected={Boolean(value)}
            onChange={(selected) => commit(selected)}
          />
        </div>
        {fieldError ? (
          <p className="field-error" id={errorId} role="alert">
            {fieldError}
          </p>
        ) : null}
      </div>
    );
  }

  if (field.control === "textarea") {
    return (
      <div className={wide ? "mb-field mb-field--wide" : "mb-field"} data-variant="textarea">
        <div className="mb-field__control">
          <MaterialOutlinedTextField
            ariaDescribedBy={fieldDescribedBy}
            ariaLabel={displayLabel}
            ariaInvalid={Boolean(fieldError)}
            className="schema-text-field"
            disabled={disabled}
            error={Boolean(fieldError)}
            errorText={fieldError}
            id={id}
            label={displayLabel}
            required={field.required}
            rows={6}
            supportingText={fieldSupportingText(field, t("field.secretReplacementHint"))}
            trailingIcon={(
              <FieldHelpIconButton
                anchorRef={trailingHelpAnchorRef}
                field={field}
                label={displayLabel}
                helpId={helpId}
                helpOpen={helpOpen}
                setHelpOpen={setHelpOpen}
                slot="trailing-icon"
              />
            )}
            type="textarea"
            value={text}
            onInput={(next) => updateTextValue(next, onChange, field)}
            onBlur={commitOnBlur}
          />
          <FieldHelpTooltip
            anchorRef={trailingHelpAnchorRef}
            helpId={helpId}
            helpOpen={helpOpen}
            helpParts={helpParts}
          />
        </div>
        <FieldA11yMessages errorId={errorId} error={fieldError} />
      </div>
    );
  }

  if (field.type === "object" || field.type === "array" || field.control === "object" || field.control === "array") {
    const summary = structuredSummary(value, field, displayLabel, t);
    if (isObjectFieldValue(value)) {
      return (
        <div className={schemaFieldClass(wide)}>
          {objectDisplay === "expandedFixed" ? null : (
            <FieldTopline
              field={field}
              label={displayLabel}
              helpId={helpId}
              helpOpen={helpOpen}
              helpParts={helpParts}
              id={id}
              setHelpOpen={setHelpOpen}
            />
          )}
          <StructuredObjectEditor
            describedBy={fieldDescribedBy}
            disabled={disabled}
            id={id}
            label={displayLabel}
            objectDisplay={objectDisplay}
            onCommit={commit}
            summary={summary}
            value={value}
            helpButton={objectDisplay === "expandedFixed" ? (
              <FieldHelpIconButton
                anchorRef={trailingHelpAnchorRef}
                field={field}
                label={displayLabel}
                helpId={helpId}
                helpOpen={helpOpen}
                setHelpOpen={setHelpOpen}
              />
            ) : null}
          />
          {objectDisplay === "expandedFixed" ? (
            <FieldHelpTooltip
              anchorRef={trailingHelpAnchorRef}
              helpId={helpId}
              helpOpen={helpOpen}
              helpParts={helpParts}
            />
          ) : null}
          <FieldA11yMessages errorId={errorId} error={fieldError} />
        </div>
      );
    }
    return (
      <div className={schemaFieldClass(wide)}>
        {objectDisplay === "expandedFixed" ? null : (
          <FieldTopline
            field={field}
            label={displayLabel}
            helpId={helpId}
            helpOpen={helpOpen}
            helpParts={helpParts}
            id={id}
            setHelpOpen={setHelpOpen}
          />
        )}
        <div
          aria-describedby={fieldDescribedBy}
          aria-label={t("field.structuredSummaryLabel", { label: displayLabel })}
          className="schema-structured-summary"
          id={id}
        >
          <div className="schema-structured-summary__header">
            <span>{summary.title}</span>
            <strong>{t("field.structuredReadonly")}</strong>
            {objectDisplay === "expandedFixed" ? (
              <FieldHelpIconButton
                anchorRef={trailingHelpAnchorRef}
                field={field}
                label={displayLabel}
                helpId={helpId}
                helpOpen={helpOpen}
                setHelpOpen={setHelpOpen}
              />
            ) : null}
          </div>
          {summary.rows.length ? (
            <dl className="schema-structured-summary__rows">
              {summary.rows.map((row) => (
                <div className="schema-structured-summary__row" key={row.key}>
                  <dt>{row.key}</dt>
                  <dd>{row.value}</dd>
                </div>
              ))}
            </dl>
          ) : (
            <p className="schema-structured-summary__empty">{t("field.structuredEmpty")}</p>
          )}
        </div>
        {objectDisplay === "expandedFixed" ? (
          <FieldHelpTooltip
            anchorRef={trailingHelpAnchorRef}
            helpId={helpId}
            helpOpen={helpOpen}
            helpParts={helpParts}
          />
        ) : null}
        <FieldA11yMessages errorId={errorId} error={fieldError} />
      </div>
    );
  }

  return (
    <div className={wide ? "mb-field mb-field--wide" : "mb-field"} data-variant="input">
      <div className="mb-field__control">
        <MaterialOutlinedTextField
          ariaDescribedBy={fieldDescribedBy}
          ariaLabel={displayLabel}
          ariaInvalid={Boolean(fieldError)}
          autoComplete={field.secret ? "new-password" : undefined}
          className="schema-text-field"
          disabled={disabled}
          error={Boolean(fieldError)}
          errorText={fieldError}
          id={id}
          label={displayLabel}
          leadingIcon={fieldLeadingIcon(field)}
          leadingIconNode={leadingIconNode}
          required={field.required}
          supportingText={fieldSupportingText(field, t("field.secretReplacementHint"))}
          trailingIcon={renderFieldTrailing({
            field,
            revealed,
            setRevealed,
            revealLabel: t("auth.revealToken"),
            hideLabel: t("auth.hideToken"),
            displayLabel,
            helpId,
            helpOpen,
            setHelpOpen,
            trailingHelpAnchorRef
          })}
          type={isSecretField(field) && !revealed ? "password" : "text"}
          value={text}
          onInput={(next) => updateTextValue(next, onChange, field)}
          onBlur={commitOnBlur}
        />
        <FieldHelpTooltip
          anchorRef={trailingHelpAnchorRef}
          helpId={helpId}
          helpOpen={helpOpen}
          helpParts={helpParts}
        />
      </div>
      <FieldA11yMessages errorId={errorId} error={fieldError} />
    </div>
  );

  function updateTextValue(next: string, emit: (value: unknown) => void, schema: FieldSchema) {
    setText(next);
    if (isSecretField(schema)) {
      emittedSecretDraftRef.current = next;
    }
    if (schema.type === "number" || schema.control === "number") {
      if (next === "") {
        setParseError("");
        emit(undefined);
        return;
      }
      const parsed = Number(next);
      if (!Number.isFinite(parsed)) {
        setParseError(t("field.invalidNumber"));
        return;
      }
      setParseError("");
      emit(parsed);
      return;
    }
    setParseError("");
    emit(next);
  }
}

type FieldHelpParts = {
  subhead: string;
  body: string;
  metas: { label?: string; value: string }[];
};

function fieldLeadingIcon(field: FieldSchema): string | undefined {
  if (field.secret || field.control === "secret") {
    return "key";
  }
  const path = field.path.toLowerCase();
  if (path.includes("url") || path.includes("endpoint") || path.includes("addr")) {
    return "link";
  }
  if (path.includes("model")) {
    return "smart_toy";
  }
  if (path.includes("agent")) {
    return "badge";
  }
  if (field.type === "number" || field.control === "number") {
    return "tag";
  }
  return undefined;
}

function isProviderProtocolField(field: FieldSchema, docPath: ConfigPath | undefined) {
  return field.path === "protocol" && docPath === "providers.<key>.protocol";
}

function fieldLabel(field: FieldSchema, docPath: ConfigPath | undefined, locale: "en-US" | "zh-CN") {
  const entry = docPath ? configDescriptions[docPath] : undefined;
  return entry?.title[locale] ?? field.label;
}

function FieldTopline({
  field,
  helpId,
  helpOpen,
  helpParts,
  id,
  label,
  labelForControl = true,
  labelId,
  setHelpOpen
}: {
  field: FieldSchema;
  helpId: string;
  helpOpen: boolean;
  helpParts: FieldHelpParts;
  id: string;
  label: string;
  labelForControl?: boolean;
  labelId?: string;
  setHelpOpen: (open: boolean | ((current: boolean) => boolean)) => void;
}) {
  const labelContent = (
    <>
      {label}
      {field.required ? <span className="schema-field__required" aria-hidden="true">*</span> : null}
    </>
  );

  return (
    <div className="schema-field__topline">
      <span className="schema-field__label-row">
        {labelForControl ? (
          <label className="schema-field__label" htmlFor={id}>
            {labelContent}
          </label>
        ) : (
          <span className="schema-field__label" id={labelId}>
            {labelContent}
          </span>
        )}
        <FieldHelpButton
          field={field}
          label={label}
          helpId={helpId}
          helpOpen={helpOpen}
          helpParts={helpParts}
          setHelpOpen={setHelpOpen}
        />
      </span>
    </div>
  );
}

function StructuredObjectEditor({
  describedBy,
  disabled,
  helpButton,
  id,
  label,
  objectDisplay,
  onCommit,
  summary,
  value
}: {
  describedBy?: string;
  disabled: boolean;
  helpButton: ReactNode;
  id: string;
  label: string;
  objectDisplay: "collapsible" | "expandedFixed";
  onCommit: (value: unknown) => void;
  summary: StructuredSummary;
  value: Record<string, unknown>;
}) {
  const { t } = useI18n();
  const editableEntries = Object.entries(value).filter(([, entryValue]) => isStructuredEditableScalar(entryValue));
  const summaryEntries = Object.entries(value).filter(([, entryValue]) => !isStructuredEditableScalar(entryValue));
  return (
    <div
      aria-describedby={describedBy}
      aria-label={t("field.structuredEditorLabel", { label })}
      className="schema-structured-object"
      id={id}
    >
      <div className="schema-structured-object__header">
        <span>{label}</span>
        {helpButton}
      </div>
      {editableEntries.length ? (
        <div className="schema-structured-object__grid">
          {editableEntries.map(([key, entryValue]) => (
            <StructuredObjectEntry
              disabled={disabled}
              key={key}
              label={key}
              value={entryValue}
              onCommit={(nextValue) => onCommit({ ...value, [key]: nextValue })}
            />
          ))}
        </div>
      ) : null}
      {summaryEntries.length || editableEntries.length === 0 ? (
        <StructuredObjectSummary
          objectDisplay={objectDisplay}
          summary={{
            title: editableEntries.length === 0 ? summary.title : t("field.structuredNestedValues"),
            rows: (editableEntries.length === 0 ? summary.rows : summaryEntries.map(([key, entryValue]) => ({
              key,
              value: structuredScalar(entryValue, undefined, t)
            })))
          }}
        />
      ) : null}
    </div>
  );
}

function StructuredObjectEntry({
  disabled,
  label,
  onCommit,
  value
}: {
  disabled: boolean;
  label: string;
  onCommit: (value: unknown) => void;
  value: unknown;
}) {
  if (typeof value === "boolean") {
    return (
      <div className="schema-field schema-field--inline schema-structured-object__boolean">
        <div className="schema-field__switch-line">
          <span className="schema-field__label-row">
            <span className="schema-field__label">{label}</span>
          </span>
          <MaterialSwitch
            disabled={disabled}
            label={label}
            selected={value}
            onChange={onCommit}
          />
        </div>
      </div>
    );
  }
  if (typeof value === "number") {
    return (
      <StructuredObjectNumberField
        disabled={disabled}
        label={label}
        value={value}
        onCommit={onCommit}
      />
    );
  }
  return (
    <StructuredObjectTextField
      disabled={disabled}
      label={label}
      value={typeof value === "string" ? value : ""}
      onCommit={onCommit}
    />
  );
}

function StructuredObjectTextField({
  disabled,
  label,
  onCommit,
  value
}: {
  disabled: boolean;
  label: string;
  onCommit: (value: unknown) => void;
  value: string;
}) {
  const [draft, setDraft] = useState(value);

  useEffect(() => {
    setDraft(value);
  }, [value]);

  return (
    <div className="mb-field" data-variant="input">
      <div className="mb-field__control">
        <MaterialOutlinedTextField
          disabled={disabled}
          label={label}
          spellCheck={false}
          type="text"
          value={draft}
          onBlur={() => {
            if (draft !== value) {
              onCommit(draft);
            }
          }}
          onInput={setDraft}
        />
      </div>
    </div>
  );
}

function StructuredObjectNumberField({
  disabled,
  label,
  onCommit,
  value
}: {
  disabled: boolean;
  label: string;
  onCommit: (value: unknown) => void;
  value: number;
}) {
  const { t } = useI18n();
  const [draft, setDraft] = useState(String(value));
  const [localError, setLocalError] = useState("");

  useEffect(() => {
    setDraft(String(value));
    setLocalError("");
  }, [value]);

  function commit() {
    const trimmed = draft.trim();
    if (trimmed === String(value)) {
      setLocalError("");
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
          inputMode="decimal"
          label={label}
          spellCheck={false}
          type="text"
          value={draft}
          onBlur={commit}
          onInput={(next) => {
            setDraft(next);
            if (localError && next.trim() === String(value)) {
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

function StructuredObjectSummary({
  objectDisplay,
  summary
}: {
  objectDisplay: "collapsible" | "expandedFixed";
  summary: StructuredSummary;
}) {
  const { t } = useI18n();
  return (
    <div className="schema-structured-summary schema-structured-summary--nested">
      <div className="schema-structured-summary__header">
        <span>{summary.title}</span>
        <strong>{t(objectDisplay === "expandedFixed" ? "field.structuredReadonly" : "field.structuredSummary")}</strong>
      </div>
      {summary.rows.length ? (
        <dl className="schema-structured-summary__rows">
          {summary.rows.map((row) => (
            <div className="schema-structured-summary__row" key={row.key}>
              <dt>{row.key}</dt>
              <dd>{row.value}</dd>
            </div>
          ))}
        </dl>
      ) : (
        <p className="schema-structured-summary__empty">{t("field.structuredEmpty")}</p>
      )}
    </div>
  );
}

function FieldHelpButton({
  field,
  helpId,
  helpOpen,
  helpParts,
  label,
  setHelpOpen
}: {
  field: FieldSchema;
  helpId: string;
  helpOpen: boolean;
  helpParts: FieldHelpParts;
  label: string;
  setHelpOpen: (open: boolean | ((current: boolean) => boolean)) => void;
}) {
  const anchorRef = useRef<HTMLButtonElement>(null);
  return (
    <span className="schema-field__help-wrap">
      <FieldHelpIconButton
        anchorRef={anchorRef}
        field={field}
        label={label}
        helpId={helpId}
        helpOpen={helpOpen}
        setHelpOpen={setHelpOpen}
      />
      <FieldHelpTooltip
        anchorRef={anchorRef}
        helpId={helpId}
        helpOpen={helpOpen}
        helpParts={helpParts}
      />
    </span>
  );
}

function renderFieldTrailing({
  field,
  revealed,
  setRevealed,
  revealLabel,
  hideLabel,
  displayLabel,
  helpId,
  helpOpen,
  setHelpOpen,
  trailingHelpAnchorRef
}: {
  field: FieldSchema;
  revealed: boolean;
  setRevealed: (next: boolean | ((current: boolean) => boolean)) => void;
  revealLabel: string;
  hideLabel: string;
  displayLabel: string;
  helpId: string;
  helpOpen: boolean;
  setHelpOpen: (open: boolean | ((current: boolean) => boolean)) => void;
  trailingHelpAnchorRef?: RefObject<HTMLButtonElement | null>;
}) {
  // Material Web's text-field trailing slot holds exactly one icon (fixed-width
  // container + absolutely-positioned slotted content). Secret fields use that
  // slot for the visibility toggle; the help affordance is dropped for them
  // (they still show their supporting text).
  if (isSecretField(field)) {
    return (
      <MaterialIconButton
        className="field-visibility-toggle"
        icon={revealed ? "visibility_off" : "visibility"}
        label={revealed ? hideLabel : revealLabel}
        onClick={() => setRevealed((current) => !current)}
        onMouseDown={(event) => event.preventDefault()}
        slot="trailing-icon"
      />
    );
  }
  return (
    <FieldHelpIconButton
      anchorRef={trailingHelpAnchorRef}
      field={field}
      label={displayLabel}
      helpId={helpId}
      helpOpen={helpOpen}
      setHelpOpen={setHelpOpen}
      slot="trailing-icon"
    />
  );
}

function FieldHelpIconButton({
  anchorRef,
  field,
  helpId,
  helpOpen,
  label,
  setHelpOpen,
  slot
}: {
  anchorRef?: RefObject<HTMLButtonElement | null>;
  field: FieldSchema;
  helpId: string;
  helpOpen: boolean;
  label: string;
  setHelpOpen: (open: boolean | ((current: boolean) => boolean)) => void;
  slot?: string;
}) {
  const { t } = useI18n();
  const openedByHover = useRef(false);

  return (
    <MaterialIconButton
      className="schema-field__help"
      describedBy={helpOpen ? helpId : undefined}
      icon="help"
      label={t("field.helpFor", { label })}
      onBlur={() => setHelpOpen(false)}
      onClick={() => {
        if (openedByHover.current) {
          openedByHover.current = false;
          setHelpOpen(true);
          return;
        }
        setHelpOpen((open) => !open);
      }}
      onFocus={() => setHelpOpen(true)}
      onKeyDown={(event: KeyboardEvent<HTMLElement>) => {
        if (event.key === "Escape") {
          setHelpOpen(false);
        }
      }}
      onMouseDown={(event) => event.preventDefault()}
      onMouseEnter={() => {
        openedByHover.current = true;
        setHelpOpen(true);
      }}
      onMouseLeave={() => {
        openedByHover.current = false;
        setHelpOpen(false);
      }}
      ref={anchorRef}
      slot={slot}
    />
  );
}

function FieldHelpTooltip({
  anchorRef,
  helpId,
  helpOpen,
  helpParts
}: {
  anchorRef: RefObject<HTMLElement | null>;
  helpId: string;
  helpOpen: boolean;
  helpParts: FieldHelpParts;
}) {
  const position = useAnchoredTooltipPosition(anchorRef, helpOpen);
  const style = tooltipPositionStyle(position);
  return helpOpen ? (
    <span className="rich-tooltip" id={helpId} role="tooltip" style={style}>
      {helpParts.subhead ? <span className="rich-tooltip__subhead">{helpParts.subhead}</span> : null}
      {helpParts.body ? <span className="rich-tooltip__body">{helpParts.body}</span> : null}
      {helpParts.metas.length ? (
        <span className="rich-tooltip__metas">
          {helpParts.metas.map((meta, index) => (
            <span className="rich-tooltip__chip" key={index}>
              {meta.label ? `${meta.label}: ${meta.value}` : meta.value}
            </span>
          ))}
        </span>
      ) : null}
    </span>
  ) : null;
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

function FieldA11yMessages({
  errorId,
  error
}: {
  errorId: string;
  error: string;
}) {
  return (
    <>
      {error ? (
        <p className="field-error field-error--sr" id={errorId} role="alert">
          {error}
        </p>
      ) : null}
    </>
  );
}

function fieldSupportingText(field: FieldSchema, secretReplacementHint: string) {
  if (field.secret) {
    return secretReplacementHint;
  }
  return "";
}

function schemaFieldClass(wide: boolean) {
  return wide ? "schema-field schema-field--wide" : "schema-field";
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

function displayValue(field: FieldSchema, value: unknown) {
  if (isSecretField(field)) {
    return "";
  }
  if (value === undefined || value === null) {
    return "";
  }
  if (field.type === "object" || field.type === "array" || field.control === "object" || field.control === "array") {
    return JSON.stringify(value, null, 2);
  }
  return String(value);
}

function inputType(field: FieldSchema) {
  if (isSecretField(field)) {
    return "password";
  }
  if (field.type === "number" || field.control === "number") {
    return "text";
  }
  return "text";
}

function isSecretField(field: FieldSchema) {
  return field.secret || field.control === "secret";
}

function fieldHelpParts(
  field: FieldSchema,
  label: string,
  docPath: ConfigPath | undefined,
  locale: "en-US" | "zh-CN",
  labels: FieldHelpLabels
): FieldHelpParts {
  const entry = docPath ? configDescriptions[docPath] : undefined;
  const metas: { label?: string; value: string }[] = [];
  if (entry) {
    metas.push({ label: labels.type, value: localizedConfigMetaValue(entry.type, labels) });
    if (entry.defaultValue) {
      metas.push({ label: labels.default, value: localizedConfigMetaValue(String(entry.defaultValue), labels) });
    }
    if (entry.sensitive || field.secret) {
      metas.push({ value: labels.sensitive });
    }
    return { subhead: label, body: entry.description[locale], metas };
  }
  metas.push({ label: labels.type, value: field.type });
  metas.push({ value: field.required ? labels.required : labels.optional });
  if (field.secret) {
    metas.push({ value: labels.sensitive });
  }
  metas.push({ value: field.hotReloadable ? labels.savedRealtime : labels.restartMayBeRequired });
  return { subhead: label, body: "", metas };
}

type FieldHelpLabels = {
  default: string;
  defaultEmpty: string;
  optional: string;
  required: string;
  restartMayBeRequired: string;
  savedRealtime: string;
  sensitive: string;
  type: string;
  typeArray: string;
  typeBoolean: string;
  typeHostPort: string;
  typeNumber: string;
  typeObject: string;
  typeString: string;
  typeUrl: string;
};

function localizedConfigMetaValue(value: string, labels: FieldHelpLabels) {
  const normalized = value.trim().toLowerCase();
  const localized: Record<string, string> = {
    array: labels.typeArray,
    boolean: labels.typeBoolean,
    empty: labels.defaultEmpty,
    "host:port": labels.typeHostPort,
    number: labels.typeNumber,
    object: labels.typeObject,
    string: labels.typeString,
    url: labels.typeUrl
  };
  return localized[normalized] ?? value;
}

function optionLabel(option: string, t: (key: MessageKey) => string) {
  const labels: Record<string, MessageKey> = {
    anthropic: "provider.protocol.anthropic",
    "openai-response": "provider.protocol.openaiResponses",
    "openai-chat": "provider.protocol.openaiChat",
    "google-genai": "provider.protocol.googleGenai"
  };
  const key = labels[option];
  return key ? t(key) : option;
}

type StructuredSummary = {
  rows: Array<{ key: string; value: string }>;
  title: string;
};

function structuredSummary(
  value: unknown,
  field: FieldSchema,
  label: string,
  t: (key: MessageKey, values?: Record<string, string | number>) => string
): StructuredSummary {
  if (Array.isArray(value)) {
    return {
      title: label,
      rows: value.slice(0, 6).map((item, index) => ({
        key: String(index + 1),
        value: structuredScalar(item, field, t)
      }))
    };
  }
  if (value && typeof value === "object") {
    const entries = Object.entries(value as Record<string, unknown>);
    return {
      title: label,
      rows: entries.slice(0, 6).map(([key, item]) => ({
        key,
        value: structuredScalar(item, field, t)
      }))
    };
  }
  return {
    title: label,
    rows: []
  };
}

function structuredScalar(
  value: unknown,
  field: FieldSchema | undefined,
  t: (key: MessageKey, values?: Record<string, string | number>) => string
) {
  if (value === undefined || value === null || value === "") {
    return t("field.structuredEmptyValue");
  }
  if (Array.isArray(value)) {
    return t(summaryKey("field.summary.items", value.length), { count: value.length });
  }
  if (value && typeof value === "object") {
    const count = Object.keys(value).length;
    return t(summaryKey("field.summary.keys", count), { count });
  }
  if (field?.secret) {
    return "******";
  }
  return String(value);
}

function summaryKey(prefix: "field.summary.items" | "field.summary.keys", count: number): MessageKey {
  return `${prefix}.${count === 1 ? "one" : "many"}` as MessageKey;
}

function isObjectFieldValue(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function isStructuredEditableScalar(value: unknown) {
  return value === undefined || value === null || typeof value === "string" || typeof value === "number" || typeof value === "boolean";
}
