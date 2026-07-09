import {
  forwardRef,
  useId,
  type InputHTMLAttributes,
  type ReactNode,
  type TextareaHTMLAttributes
} from "react";
import { Icon } from "./Icon";

export type TextFieldType =
  | "text"
  | "password"
  | "email"
  | "number"
  | "url"
  | "search"
  | "tel"
  | "textarea";

export type TextFieldProps = {
  ariaDescribedBy?: string;
  ariaLabel?: string;
  ariaInvalid?: boolean;
  autoFocus?: boolean;
  autoComplete?: string;
  disabled?: boolean;
  error?: boolean;
  errorText?: string;
  className?: string;
  id?: string;
  label: string;
  leadingIcon?: string;
  leadingIconNode?: ReactNode;
  onBlur?: () => void;
  onInput: (value: string) => void;
  inputMode?: InputHTMLAttributes<HTMLInputElement>["inputMode"];
  rows?: number;
  spellCheck?: boolean;
  step?: string;
  supportingText?: string;
  required?: boolean;
  trailingIcon?: ReactNode;
  type?: TextFieldType;
  value: string;
};

/** Host element type used by callers/tests that previously held md text fields. */
export type TextFieldElement = HTMLInputElement | HTMLTextAreaElement;

function mergeClass(...parts: Array<string | undefined | false>) {
  return parts.filter(Boolean).join(" ");
}

export const TextField = forwardRef<TextFieldElement, TextFieldProps>(
  function TextField(
    {
      ariaDescribedBy,
      ariaLabel,
      ariaInvalid = false,
      autoFocus = false,
      autoComplete,
      className,
      disabled = false,
      error = false,
      errorText,
      id,
      label,
      leadingIcon,
      leadingIconNode,
      onBlur,
      onInput,
      inputMode,
      rows,
      spellCheck,
      step,
      supportingText,
      required = false,
      trailingIcon,
      type = "text",
      value
    },
    ref
  ) {
    const reactId = useId();
    const fieldId = id ?? `bh-field-${reactId}`;
    const supportId = `${fieldId}-support`;
    const hasSupport = Boolean(error && errorText) || Boolean(supportingText);
    const describedBy = [ariaDescribedBy, hasSupport ? supportId : undefined]
      .filter(Boolean)
      .join(" ") || undefined;
    const isTextarea = type === "textarea" || rows !== undefined;
    const singleLine = !isTextarea;
    const spellCheckValue = spellCheck ?? false;

    const common = {
      "aria-describedby": describedBy,
      "aria-invalid": ariaInvalid || error || undefined,
      "aria-label": ariaLabel,
      autoFocus,
      autoComplete,
      className: "bh-field__control",
      disabled,
      id: fieldId,
      onBlur,
      required,
      spellCheck: spellCheckValue,
      value,
      onChange: (
        event: React.ChangeEvent<HTMLInputElement | HTMLTextAreaElement>
      ) => onInput(event.target.value)
    };

    return (
      <div
        className={mergeClass(
          "bh-field",
          error && "bh-field--error",
          disabled && "bh-field--disabled",
          singleLine && "bh-field--single-line",
          singleLine && "material-text-field--single-line",
          className
        )}
        data-label={label}
        data-type={type}
        data-spellcheck={spellCheckValue ? "true" : "false"}
        aria-invalid={ariaInvalid || error || undefined}
      >
        <label className="bh-field__label" htmlFor={fieldId}>
          {label}
          {required ? <span aria-hidden="true"> *</span> : null}
        </label>
        <div className="bh-field__shell">
          {leadingIconNode || leadingIcon ? (
            <span aria-hidden="true" className="bh-field__leading material-field-leading-node">
              {leadingIconNode ?? (leadingIcon ? <Icon name={leadingIcon} size={16} /> : null)}
            </span>
          ) : null}
          {isTextarea ? (
            <textarea
              {...(common as TextareaHTMLAttributes<HTMLTextAreaElement>)}
              ref={ref as React.Ref<HTMLTextAreaElement>}
              rows={rows ?? 4}
              spellCheck={spellCheckValue}
            />
          ) : (
            <input
              {...(common as InputHTMLAttributes<HTMLInputElement>)}
              ref={ref as React.Ref<HTMLInputElement>}
              inputMode={inputMode}
              spellCheck={spellCheckValue}
              step={step}
              type={type}
            />
          )}
          {trailingIcon ? (
            <span className="bh-field__trailing">{trailingIcon}</span>
          ) : null}
        </div>
        {hasSupport ? (
          <p className="bh-field__support" id={supportId}>
            {error && errorText ? errorText : supportingText}
          </p>
        ) : null}
      </div>
    );
  }
);

export const OutlinedTextField = TextField;
export const FilledTextField = TextField;
