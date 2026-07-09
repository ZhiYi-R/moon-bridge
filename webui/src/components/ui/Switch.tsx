type SwitchProps = {
  disabled?: boolean;
  label: string;
  onChange: (selected: boolean) => void;
  selected: boolean;
};

export function Switch({ disabled = false, label, onChange, selected }: SwitchProps) {
  return (
    <button
      aria-checked={selected}
      aria-label={label}
      className={selected ? "bh-switch bh-switch--on" : "bh-switch"}
      disabled={disabled}
      role="switch"
      type="button"
      onClick={() => onChange(!selected)}
    >
      <span aria-hidden="true" className="bh-switch__track">
        <span className="bh-switch__thumb" />
      </span>
    </button>
  );
}
