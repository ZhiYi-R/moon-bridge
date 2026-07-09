import { useId, type ReactNode } from "react";

export type SelectOption = {
  label: string;
  leadingIcon?: ReactNode;
  value: string;
};

type SelectProps = {
  ariaLabel?: string;
  className?: string;
  describedBy?: string;
  disabled?: boolean;
  error?: boolean;
  errorText?: string;
  label: string;
  leadingIcon?: ReactNode;
  onChange: (value: string) => void;
  options: SelectOption[];
  required?: boolean;
  supportingText?: string;
  value: string;
};

function mergeClass(...parts: Array<string | undefined | false>) {
  return parts.filter(Boolean).join(" ");
}

export function Select({
  ariaLabel,
  className,
  describedBy,
  disabled = false,
  error = false,
  errorText,
  label,
  leadingIcon,
  onChange,
  options,
  required = false,
  supportingText,
  value
}: SelectProps) {
  const reactId = useId();
  const selectId = `bh-select-${reactId}`;
  const supportId = `${selectId}-support`;
  const hasSupport = Boolean(error && errorText) || Boolean(supportingText);
  const described = [describedBy, hasSupport ? supportId : undefined].filter(Boolean).join(" ") || undefined;

  return (
    <div
      className={mergeClass(
        "bh-field",
        "bh-select",
        error && "bh-field--error",
        disabled && "bh-field--disabled",
        "bh-field--single-line",
        "material-select--single-line",
        className
      )}
      data-label={label}
    >
      <label className="bh-field__label" htmlFor={selectId}>
        {label}
        {required ? <span aria-hidden="true"> *</span> : null}
      </label>
      <div className="bh-field__shell">
        {leadingIcon ? (
          <span aria-hidden="true" className="bh-field__leading material-select-leading-node">
            {leadingIcon}
          </span>
        ) : null}
        <select
          aria-describedby={described}
          aria-invalid={error || undefined}
          aria-label={ariaLabel}
          className="bh-field__control bh-select__control"
          disabled={disabled}
          id={selectId}
          required={required}
          value={value}
          onChange={(event) => onChange(event.target.value)}
        >
          {options.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>
      </div>
      {hasSupport ? (
        <p className="bh-field__support" id={supportId}>
          {error && errorText ? errorText : supportingText}
        </p>
      ) : null}
    </div>
  );
}
