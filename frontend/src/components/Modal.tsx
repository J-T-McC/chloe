// The modal shell every dialog uses: a backdrop, the dialog box and its title. A click on
// the backdrop or Escape closes it; pass no onClose while it must stay open (a job running).
import type { KeyboardEvent, ReactNode } from "react";

export function Modal({
  title,
  className,
  onClose,
  backdropCloses = true,
  onKeyDown,
  label,
  children,
}: {
  title: ReactNode;
  /** extra classes after "modal", e.g. "small" or "wizard wide" */
  className?: string;
  onClose?(): void;
  /** false for dialogs that need an answer: only Escape (or a button) closes them */
  backdropCloses?: boolean;
  /** runs after the Escape handling, e.g. Enter to confirm */
  onKeyDown?(e: KeyboardEvent<HTMLDivElement>): void;
  /** aria-label; set it to make the box role="dialog" */
  label?: string;
  children: ReactNode;
}) {
  return (
    <div className="modal-backdrop" onMouseDown={backdropCloses ? () => onClose?.() : undefined}>
      <div
        className={className ? `modal ${className}` : "modal"}
        role={label ? "dialog" : undefined}
        aria-label={label}
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === "Escape") onClose?.();
          onKeyDown?.(e);
        }}
      >
        <div className="modal-title">{title}</div>
        {children}
      </div>
    </div>
  );
}

/** The footer row: anything on the left, then Cancel and the primary button on the right. */
export function ModalFooter({
  children,
  onCancel,
  cancelLabel = "Cancel",
  onOk,
  okLabel = "OK",
  okDisabled,
}: {
  children?: ReactNode;
  onCancel?(): void;
  cancelLabel?: string;
  onOk?(): void;
  okLabel?: ReactNode;
  okDisabled?: boolean;
}) {
  return (
    <div className="modal-footer">
      {children}
      <span className="spacer" />
      {onCancel && <button onClick={onCancel}>{cancelLabel}</button>}
      {onOk && (
        <button className="primary" disabled={okDisabled} onClick={onOk}>
          {okLabel}
        </button>
      )}
    </div>
  );
}
