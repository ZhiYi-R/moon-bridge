import type { ReactNode } from "react";

type FilterChipProps<Value extends string> = {
  children: ReactNode;
  onSelect: (value: Value) => void;
  selected: boolean;
  value: Value;
};

export function FilterChip<Value extends string>({
  children,
  onSelect,
  selected,
  value
}: FilterChipProps<Value>) {
  return (
    <button
      aria-pressed={selected}
      className={selected ? "bh-chip bh-chip--selected" : "bh-chip"}
      type="button"
      onClick={() => onSelect(value)}
    >
      {children}
    </button>
  );
}

export function AssistChip({ children }: { children: ReactNode }) {
  return <span className="bh-chip bh-chip--assist">{children}</span>;
}

type InputChipProps = {
  children: ReactNode;
  className?: string;
  disabled?: boolean;
  label: string;
  onRemove: () => void;
};

export function InputChip({
  children,
  className,
  disabled = false,
  label,
  onRemove
}: InputChipProps) {
  return (
    <span
      aria-label={label}
      className={["bh-chip", "bh-chip--input", className].filter(Boolean).join(" ")}
    >
      <span className="bh-chip__label">{children}</span>
      <button
        aria-label={label}
        className="bh-chip__remove"
        disabled={disabled}
        type="button"
        onClick={onRemove}
      >
        ×
      </button>
    </span>
  );
}

export function ChipSet({
  children,
  className,
  role,
  "aria-label": ariaLabel
}: {
  children: ReactNode;
  className?: string;
  role?: string;
  "aria-label"?: string;
}) {
  return (
    <div
      aria-label={ariaLabel}
      className={["bh-chip-set", className].filter(Boolean).join(" ")}
      role={role}
    >
      {children}
    </div>
  );
}
