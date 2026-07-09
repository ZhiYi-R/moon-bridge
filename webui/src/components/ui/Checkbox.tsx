type CheckboxProps = {
  checked: boolean;
  className?: string;
  label: string;
  onChange: (checked: boolean) => void;
};

function mergeClass(...parts: Array<string | undefined | false>) {
  return parts.filter(Boolean).join(" ");
}

export function Checkbox({ checked, className, label, onChange }: CheckboxProps) {
  return (
    <input
      aria-label={label}
      checked={checked}
      className={mergeClass("bh-checkbox", className)}
      type="checkbox"
      onChange={(event) => onChange(event.target.checked)}
    />
  );
}
