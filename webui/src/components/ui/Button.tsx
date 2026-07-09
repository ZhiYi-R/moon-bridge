import {
  forwardRef,
  type ButtonHTMLAttributes,
  type KeyboardEvent,
  type MouseEvent,
  type ReactNode
} from "react";
import { Icon, type IconName } from "./Icon";

type CommonProps = {
  children?: ReactNode;
  className?: string;
  disabled?: boolean;
  icon?: IconName | string;
};

export type FilledButtonProps = CommonProps & {
  ariaLabel?: string;
  ariaPressed?: boolean;
  onClick?: () => void;
  type?: "button" | "reset" | "submit";
};

export type OutlinedButtonProps = CommonProps & {
  ariaExpanded?: boolean;
  ariaLabel?: string;
  ariaPressed?: boolean;
  controls?: string;
  id?: string;
  onClick: (event: MouseEvent<HTMLElement>) => void;
  type?: "button" | "reset" | "submit";
};

export type IconButtonProps = {
  ariaExpanded?: boolean;
  className?: string;
  controls?: string;
  describedBy?: string;
  disabled?: boolean;
  icon: IconName | string;
  label: string;
  onBlur?: () => void;
  onClick: (event: MouseEvent<HTMLElement>) => void;
  onFocus?: () => void;
  onKeyDown?: (event: KeyboardEvent<HTMLElement>) => void;
  onMouseDown?: (event: MouseEvent<HTMLElement>) => void;
  onMouseEnter?: () => void;
  onMouseLeave?: () => void;
  slot?: string;
};

function mergeClass(...parts: Array<string | undefined | false>) {
  return parts.filter(Boolean).join(" ");
}

export function FilledButton({
  ariaLabel,
  ariaPressed,
  children,
  className,
  disabled = false,
  icon,
  onClick,
  type = "button"
}: FilledButtonProps) {
  return (
    <button
      aria-label={ariaLabel}
      aria-pressed={ariaPressed}
      className={mergeClass("bh-button", "bh-button--filled", className)}
      disabled={disabled}
      onClick={onClick}
      type={type}
    >
      {icon ? <Icon name={icon} size={18} /> : null}
      {children}
    </button>
  );
}

export const OutlinedButton = forwardRef<HTMLButtonElement, OutlinedButtonProps>(
  function OutlinedButton(
    {
      ariaExpanded,
      ariaLabel,
      ariaPressed,
      children,
      className,
      controls,
      disabled = false,
      id,
      icon,
      onClick,
      type = "button"
    },
    ref
  ) {
    return (
      <button
        aria-controls={controls}
        aria-expanded={ariaExpanded}
        aria-label={ariaLabel}
        aria-pressed={ariaPressed}
        className={mergeClass("bh-button", "bh-button--outlined", className)}
        disabled={disabled}
        id={id}
        onClick={onClick}
        ref={ref}
        type={type}
      >
        {icon ? <Icon name={icon} size={18} /> : null}
        {children}
      </button>
    );
  }
);

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(
  function IconButton(
    {
      ariaExpanded,
      className,
      controls,
      describedBy,
      disabled = false,
      icon,
      label,
      onBlur,
      onClick,
      onFocus,
      onKeyDown,
      onMouseDown,
      onMouseEnter,
      onMouseLeave
    },
    ref
  ) {
    return (
      <button
        aria-controls={controls}
        aria-describedby={describedBy}
        aria-expanded={ariaExpanded}
        aria-label={label}
        className={mergeClass("bh-icon-button", className)}
        disabled={disabled}
        onBlur={onBlur}
        onClick={onClick}
        onFocus={onFocus}
        onKeyDown={onKeyDown}
        onMouseDown={onMouseDown}
        onMouseEnter={onMouseEnter}
        onMouseLeave={onMouseLeave}
        ref={ref}
        type="button"
      >
        <Icon name={icon} size={20} />
      </button>
    );
  }
);

export type NativeButtonProps = ButtonHTMLAttributes<HTMLButtonElement>;
