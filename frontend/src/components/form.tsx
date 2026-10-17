// Form rows shared by dialogs and option panes: a label beside its control, and a checkbox
// with its label.
import type { ReactNode } from "react";

export function Field({ label, className, children }: { label?: ReactNode; className?: string; children: ReactNode }) {
  return (
    <div className={className ? `field ${className}` : "field"}>
      <label>{label}</label>
      <div className="field-input">{children}</div>
    </div>
  );
}

export function Check({
  label,
  checked,
  onChange,
  disabled,
  title,
}: {
  label: ReactNode;
  checked: boolean;
  onChange(v: boolean): void;
  disabled?: boolean;
  title?: string;
}) {
  return (
    <label className="check" title={title}>
      <input type="checkbox" checked={checked} disabled={disabled} onChange={(e) => onChange(e.target.checked)} /> {label}
    </label>
  );
}
