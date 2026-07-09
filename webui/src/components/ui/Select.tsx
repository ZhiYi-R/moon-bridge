import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
  type ReactNode
} from "react";
import { Icon } from "./Icon";

export type SelectOption = {
  label: string;
  leadingIcon?: ReactNode;
  value: string;
};

type SelectProps = {
  ariaLabel?: string;
  className?: string;
  describedBy?: string;
  /** Compact single-line chrome for app-bar controls. */
  density?: "default" | "compact";
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
  density = "default",
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
  const listboxId = `${selectId}-listbox`;
  const supportId = `${selectId}-support`;
  const rootRef = useRef<HTMLDivElement>(null);
  const [open, setOpen] = useState(false);
  const [highlight, setHighlight] = useState(() =>
    Math.max(
      0,
      options.findIndex((option) => option.value === value)
    )
  );

  const hasSupport = Boolean(error && errorText) || Boolean(supportingText);
  const described =
    [describedBy, hasSupport ? supportId : undefined].filter(Boolean).join(" ") || undefined;

  const selected = useMemo(
    () => options.find((option) => option.value === value) ?? options[0],
    [options, value]
  );

  const close = useCallback(() => setOpen(false), []);

  useEffect(() => {
    if (!open) {
      return;
    }
    const onPointerDown = (event: MouseEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) {
        close();
      }
    };
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === "Escape") {
        close();
      }
    };
    document.addEventListener("mousedown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [close, open]);

  useEffect(() => {
    const index = options.findIndex((option) => option.value === value);
    if (index >= 0) {
      setHighlight(index);
    }
  }, [options, value]);

  const commit = (next: string) => {
    if (next !== value) {
      onChange(next);
    }
    close();
  };

  const onTriggerKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (disabled) {
      return;
    }
    if (event.key === "ArrowDown" || event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      setOpen(true);
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      setOpen(true);
      setHighlight((current) => Math.max(0, current - 1));
    }
  };

  const onListKeyDown = (event: KeyboardEvent<HTMLUListElement>) => {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setHighlight((current) => Math.min(options.length - 1, current + 1));
      return;
    }
    if (event.key === "ArrowUp") {
      event.preventDefault();
      setHighlight((current) => Math.max(0, current - 1));
      return;
    }
    if (event.key === "Home") {
      event.preventDefault();
      setHighlight(0);
      return;
    }
    if (event.key === "End") {
      event.preventDefault();
      setHighlight(Math.max(0, options.length - 1));
      return;
    }
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      const option = options[highlight];
      if (option) {
        commit(option.value);
      }
      return;
    }
    if (event.key === "Escape" || event.key === "Tab") {
      close();
    }
  };

  return (
    <div
      ref={rootRef}
      className={mergeClass(
        "bh-field",
        "bh-select",
        density === "compact" && "bh-select--compact",
        error && "bh-field--error",
        disabled && "bh-field--disabled",
        open && "bh-select--open",
        "bh-field--single-line",
        "material-select--single-line",
        className
      )}
      data-label={label}
    >
      {density === "default" ? (
        <label className="bh-field__label" htmlFor={selectId} id={`${selectId}-label`}>
          {label}
          {required ? <span aria-hidden="true"> *</span> : null}
        </label>
      ) : (
        <span className="bh-select__compact-label" id={`${selectId}-label`}>
          {label}
        </span>
      )}

      <div className="bh-field__shell bh-select__shell">
        {leadingIcon ? (
          <span aria-hidden="true" className="bh-field__leading material-select-leading-node">
            {leadingIcon}
          </span>
        ) : null}

        <button
          type="button"
          id={selectId}
          className="bh-select__trigger"
          aria-controls={listboxId}
          aria-describedby={described}
          aria-expanded={open}
          aria-haspopup="listbox"
          aria-invalid={error || undefined}
          aria-labelledby={`${selectId}-label`}
          aria-label={ariaLabel ?? label}
          disabled={disabled}
          onClick={() => {
            if (!disabled) {
              setOpen((current) => !current);
            }
          }}
          onKeyDown={onTriggerKeyDown}
        >
          <span className="bh-select__value">
            {selected?.leadingIcon ? (
              <span className="bh-select__value-icon" aria-hidden="true">
                {selected.leadingIcon}
              </span>
            ) : null}
            <span className="bh-select__value-text">{selected?.label ?? ""}</span>
          </span>
          <span className="bh-select__chevron" aria-hidden="true">
            <Icon name={open ? "expand_less" : "expand_more"} size={16} />
          </span>
        </button>

        {/* Hidden native select: keeps form semantics + existing test helpers. */}
        <select
          aria-hidden="true"
          className="bh-select__native"
          disabled={disabled}
          required={required}
          tabIndex={-1}
          value={value}
          onChange={(event) => onChange(event.target.value)}
        >
          {options.map((option) => (
            <option key={option.value} value={option.value}>
              {option.label}
            </option>
          ))}
        </select>

        {open ? (
          <ul
            id={listboxId}
            className="bh-select__menu"
            role="listbox"
            aria-labelledby={`${selectId}-label`}
            tabIndex={-1}
            onKeyDown={onListKeyDown}
          >
            {options.map((option, index) => {
              const isSelected = option.value === value;
              const isActive = index === highlight;
              return (
                <li
                  key={option.value}
                  id={`${selectId}-opt-${option.value}`}
                  role="option"
                  aria-selected={isSelected}
                  className={mergeClass(
                    "bh-select__option",
                    isSelected && "bh-select__option--selected",
                    isActive && "bh-select__option--active"
                  )}
                  onMouseEnter={() => setHighlight(index)}
                  onMouseDown={(event) => {
                    // Prevent blur-before-click races.
                    event.preventDefault();
                    commit(option.value);
                  }}
                >
                  {option.leadingIcon ? (
                    <span className="bh-select__option-icon" aria-hidden="true">
                      {option.leadingIcon}
                    </span>
                  ) : null}
                  <span className="bh-select__option-label">{option.label}</span>
                  {isSelected ? (
                    <span className="bh-select__option-check" aria-hidden="true">
                      <Icon name="check" size={14} />
                    </span>
                  ) : null}
                </li>
              );
            })}
          </ul>
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
