// A text input with a type-to-filter suggestion list: type "utf8" to narrow a long
// list of charsets, arrows to move, Enter to pick. With strict set, text that
// isn't one of the options reverts when the field loses focus.
import { ChevronDown } from "lucide-react";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";

export interface ComboboxProps {
  value: string;
  options: string[];
  onChange(v: string): void;
  placeholder?: string;
  disabled?: boolean;
  /** Only accept listed options (empty is always allowed). */
  strict?: boolean;
  autoFocus?: boolean;
  className?: string;
}

/** Options matching the text: prefix matches first, then substring matches, in list order. */
export function filterOptions(options: string[], text: string): string[] {
  const q = text.trim().toLowerCase();
  if (!q) return options;
  const prefix: string[] = [];
  const inner: string[] = [];
  for (const o of options) {
    const l = o.toLowerCase();
    if (l === q) prefix.unshift(o);
    else if (l.startsWith(q)) prefix.push(o);
    else if (l.includes(q)) inner.push(o);
  }
  return [...prefix, ...inner];
}

export function Combobox({ value, options, onChange, placeholder, disabled, strict, autoFocus, className }: ComboboxProps) {
  const [text, setText] = useState(value);
  const [open, setOpen] = useState(false);
  // Filter only once the user types; on open, show everything with the value highlighted.
  const [typed, setTyped] = useState(false);
  const [active, setActive] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const [rect, setRect] = useState<DOMRect | null>(null);

  useEffect(() => setText(value), [value]);

  const shown = useMemo(() => (typed ? filterOptions(options, text) : options), [typed, options, text]);

  useLayoutEffect(() => {
    if (!open) return;
    setRect(input.current!.getBoundingClientRect());
    const i = typed ? 0 : Math.max(0, options.indexOf(value));
    setActive(i);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, typed, text]);

  useEffect(() => {
    list.current?.querySelector(".combo-option.active")?.scrollIntoView({ block: "nearest" });
  }, [active, open]);

  const commit = (v: string) => {
    setText(v);
    setOpen(false);
    setTyped(false);
    if (v !== value) onChange(v);
  };

  const finish = () => {
    setOpen(false);
    setTyped(false);
    const t = text.trim();
    if (t === value) return;
    if (!t) return onChange("");
    // Accept a case-insensitive exact match as the option's spelling.
    const exact = options.find((o) => o.toLowerCase() === t.toLowerCase());
    if (exact) return commit(exact);
    if (strict) setText(value);
    else onChange(t);
  };

  return (
    <div className={"combobox" + (className ? " " + className : "")}>
      <input
        ref={input}
        value={text}
        placeholder={placeholder}
        disabled={disabled}
        autoFocus={autoFocus}
        onFocus={() => setOpen(true)}
        onClick={() => setOpen(true)}
        onBlur={finish}
        onChange={(e) => {
          setText(e.target.value);
          setTyped(true);
          setOpen(true);
        }}
        onKeyDown={(e) => {
          if (e.key === "ArrowDown" || e.key === "ArrowUp") {
            e.preventDefault();
            if (!open) return setOpen(true);
            setActive((a) => Math.min(shown.length - 1, Math.max(0, a + (e.key === "ArrowDown" ? 1 : -1))));
          } else if (e.key === "Enter" && open) {
            e.preventDefault();
            e.stopPropagation();
            if (shown[active] !== undefined && (typed || text.trim() !== "")) commit(shown[active]);
            else finish();
          } else if (e.key === "Escape" && open) {
            e.stopPropagation();
            setText(value);
            setOpen(false);
            setTyped(false);
          } else if (e.key === "Tab" && open && typed && shown[active] !== undefined && text.trim()) {
            commit(shown[active]);
          }
        }}
      />
      <button type="button" tabIndex={-1} className="combo-toggle" disabled={disabled} onMouseDown={(e) => (e.preventDefault(), input.current?.focus(), setOpen((o) => !o))}>
        <ChevronDown size={13} />
      </button>
      {open &&
        rect &&
        shown.length > 0 &&
        createPortal(
          <div
            ref={list}
            className="combo-list"
            style={{ left: rect.left, top: rect.bottom + 2, width: rect.width }}
            onMouseDown={(e) => e.preventDefault()} // keep focus in the input
          >
            {shown.slice(0, 500).map((o, i) => (
              <div key={o} className={"combo-option" + (i === active ? " active" : "") + (o === value ? " selected" : "")} onMouseEnter={() => setActive(i)} onClick={() => commit(o)}>
                {o}
              </div>
            ))}
          </div>,
          document.body,
        )}
    </div>
  );
}
