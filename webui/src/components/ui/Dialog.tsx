import { useEffect, useId, useRef, type ReactNode } from "react";
import { IconButton } from "./Button";

type DialogProps = {
  open: boolean;
  onClose: () => void;
  ariaLabel?: string;
  headline?: ReactNode;
  actions?: ReactNode;
  className?: string;
  children: ReactNode;
};

export function Dialog({
  open,
  onClose,
  ariaLabel,
  headline,
  actions,
  className,
  children
}: DialogProps) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const titleId = useId();

  useEffect(() => {
    const el = dialogRef.current;
    if (!el) {
      return;
    }
    if (open) {
      if (typeof el.showModal === "function" && !el.open) {
        el.showModal();
      } else if (!el.open) {
        el.setAttribute("open", "");
      }
    } else if (el.open || el.hasAttribute("open")) {
      if (typeof el.close === "function") {
        el.close();
      } else {
        el.removeAttribute("open");
      }
    }
  }, [open]);

  useEffect(() => {
    const el = dialogRef.current;
    if (!el) {
      return;
    }
    const handleClose = () => onClose();
    el.addEventListener("close", handleClose);
    return () => el.removeEventListener("close", handleClose);
  }, [onClose]);

  return (
    <dialog
      ref={dialogRef}
      aria-labelledby={headline ? titleId : undefined}
      aria-label={headline ? undefined : ariaLabel}
      className={["bh-dialog", className].filter(Boolean).join(" ")}
      onCancel={(event) => {
        event.preventDefault();
        onClose();
      }}
    >
      {headline ? (
        <div className="bh-dialog__headline material-dialog__headline">
          <span className="bh-dialog__headline-text material-dialog__headline-text" id={titleId}>
            {headline}
          </span>
          <IconButton
            className="bh-dialog__close material-dialog__close"
            icon="close"
            label="Close"
            onClick={onClose}
          />
        </div>
      ) : null}
      <div className="bh-dialog__content material-dialog__content">{children}</div>
      {actions ? <div className="bh-dialog__actions">{actions}</div> : null}
    </dialog>
  );
}
