// Value panel docked under a grid: the active cell's full content as Text, JSON
// (pretty, highlighted), Hex or Image. Edits become pending cell edits, applied or
// discarded with the grid's ✓ / ✕ like any other change.
import { Ban, X } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import type { ResultColumn } from "../api";
import { Monaco, editorTheme, monaco } from "../editor/monaco";
import type { Cell } from "./DataGrid";

type Mode = "text" | "json" | "hex" | "image";

/** Bytes of a cell: "0x…" hex for binary columns, UTF-8 otherwise. */
export function cellBytes(v: string): Uint8Array {
  if (/^0x[0-9a-f]*$/i.test(v) && v.length % 2 === 0) {
    const out = new Uint8Array((v.length - 2) / 2);
    for (let i = 0; i < out.length; i++) out[i] = parseInt(v.slice(2 + i * 2, 4 + i * 2), 16);
    return out;
  }
  return new TextEncoder().encode(v);
}

/** Image MIME type from magic bytes, or null. */
export function imageType(b: Uint8Array): string | null {
  const at = (i: number, ...xs: number[]) => xs.every((x, j) => b[i + j] === x);
  if (at(0, 0x89, 0x50, 0x4e, 0x47)) return "image/png";
  if (at(0, 0xff, 0xd8, 0xff)) return "image/jpeg";
  if (at(0, 0x47, 0x49, 0x46, 0x38)) return "image/gif";
  if (at(0, 0x52, 0x49, 0x46, 0x46) && at(8, 0x57, 0x45, 0x42, 0x50)) return "image/webp";
  if (at(0, 0x42, 0x4d)) return "image/bmp";
  return null;
}

export function prettyJSON(v: string): string | null {
  const t = v.trim();
  if (!(t.startsWith("{") || t.startsWith("["))) return null;
  try {
    return JSON.stringify(JSON.parse(t), null, 2);
  } catch {
    return null;
  }
}

function hexDump(b: Uint8Array, max = 256 * 1024): string {
  const lines: string[] = [];
  for (let off = 0; off < Math.min(b.length, max); off += 16) {
    const row = b.slice(off, off + 16);
    const hex = Array.from(row, (x) => x.toString(16).padStart(2, "0")).join(" ");
    const ascii = Array.from(row, (x) => (x >= 0x20 && x < 0x7f ? String.fromCharCode(x) : ".")).join("");
    lines.push(`${off.toString(16).padStart(8, "0")}  ${hex.padEnd(47)}  ${ascii}`);
  }
  if (b.length > max) lines.push(`… ${(b.length - max).toLocaleString()} more bytes`);
  return lines.join("\n");
}

export function ValuePanel(p: {
  /** Identity of the cell (row:col); the default view is chosen when it changes. */
  cellId: string;
  column?: ResultColumn;
  value: Cell | undefined;
  readOnly?: boolean;
  onChange?(v: Cell): void;
  onClose(): void;
}) {
  const value = p.value ?? null;
  const json = useMemo(() => (value === null ? null : prettyJSON(value)), [value]);
  const bytes = useMemo(() => (value === null ? new Uint8Array() : cellBytes(value)), [value]);
  const img = useMemo(() => imageType(bytes), [bytes]);
  const binary = value !== null && /^0x[0-9a-f]*$/i.test(value) && value.length > 2;
  const [mode, setMode] = useState<Mode>("text");
  const host = useRef<HTMLDivElement>(null);
  const editor = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null);
  // The value we last sent up, so its echo doesn't reset the editor mid-typing.
  const emitted = useRef<Cell | undefined>(undefined);
  const lastMode = useRef<Mode | null>(null);
  const [invalid, setInvalid] = useState(false);

  // Pick a sensible view when another cell is selected.
  useEffect(() => {
    emitted.current = undefined;
    setMode(img ? "image" : binary ? "hex" : json ? "json" : "text");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [p.cellId]);

  const editable = !p.readOnly && !!p.onChange && (mode === "text" || mode === "json") && !binary;
  const text = value === null ? "" : mode === "json" ? (json ?? value) : value;

  useEffect(() => {
    if (!host.current) return;
    const model = monaco.editor.createModel("", "plaintext");
    const ed = monaco.editor.create(host.current, {
      model,
      theme: editorTheme(),
      automaticLayout: true,
      minimap: { enabled: false },
      wordWrap: "on",
      fontSize: 12,
      fontFamily: "SF Mono, Menlo, Consolas, monospace",
      lineNumbersMinChars: 3,
      scrollBeyondLastLine: false,
      renderLineHighlight: "none",
      quickSuggestions: false,
      wordBasedSuggestions: "off",
      fixedOverflowWidgets: true,
    });
    editor.current = ed;
    return () => {
      ed.dispose();
      model.dispose();
      editor.current = null;
    };
  }, []);

  // Push the cell's value into the editor (not when it's our own edit coming back).
  useEffect(() => {
    const ed = editor.current;
    if (!ed) return;
    const model = ed.getModel()!;
    monaco.editor.setModelLanguage(model, mode === "json" ? "json" : "plaintext");
    ed.updateOptions({ readOnly: !editable });
    const modeChanged = lastMode.current !== mode;
    lastMode.current = mode;
    if (!modeChanged && emitted.current !== undefined && emitted.current === value) return;
    emitted.current = undefined;
    if (model.getValue() !== text) model.setValue(text);
    setInvalid(false);
  }, [text, mode, editable, value]);

  useEffect(() => {
    const ed = editor.current;
    if (!ed || !editable) return;
    const sub = ed.onDidChangeModelContent((e) => {
      if (e.isFlush) return; // setValue from a new cell
      const t = ed.getValue();
      if (mode === "json") {
        const c = compact(t);
        setInvalid(c === null);
        if (c !== null && c !== value) (emitted.current = c), p.onChange!(c);
      } else if (t !== value) (emitted.current = t), p.onChange!(t);
    });
    return () => sub.dispose();
  }, [editable, mode, value, p.onChange]);

  const imgURL = useMemo(() => (mode === "image" && img ? URL.createObjectURL(new Blob([bytes as BlobPart], { type: img })) : null), [mode, img, bytes]);
  useEffect(() => () => void (imgURL && URL.revokeObjectURL(imgURL)), [imgURL]);

  const modes: Array<[Mode, string, boolean]> = [
    ["text", "Text", !binary],
    ["json", "JSON", !!json],
    ["hex", "Hex", value !== null],
    ["image", "Image", !!img],
  ];

  return (
    <div className="value-panel">
      <div className="value-panel-head">
        <span className="muted small value-panel-col">{p.column ? `${p.column.name} · ${p.column.type?.toLowerCase() ?? ""}` : "No cell selected"}</span>
        <div className="seg-tabs centered">
          {modes.map(([m, label, on]) => (
            <button key={m} className={mode === m ? "active" : ""} disabled={!on} onClick={() => setMode(m)}>
              {label}
            </button>
          ))}
        </div>
        <span className="value-panel-actions">
          {invalid && <span className="badge warn">Invalid JSON — not applied</span>}
          {value !== null && <span className="muted small">{bytes.length.toLocaleString()} bytes</span>}
          {!p.readOnly && p.onChange && (
            <button className="icon" title="Set NULL" disabled={value === null || binary} onClick={() => p.onChange!(null)}>
              <Ban size={13} />
            </button>
          )}
          <button className="icon" title="Close" onClick={p.onClose}>
            <X size={14} />
          </button>
        </span>
      </div>
      <div className="value-panel-body">
        <div className="monaco-host" ref={host} style={{ display: mode === "text" || mode === "json" ? "block" : "none" }} />
        {value === null && (mode === "text" || mode === "json") && <div className="value-null">NULL</div>}
        {mode === "hex" && <pre className="value-hex">{hexDump(bytes)}</pre>}
        {mode === "image" && imgURL && (
          <div className="value-image">
            <img src={imgURL} alt="" />
          </div>
        )}
      </div>
    </div>
  );
}

function compact(t: string): string | null {
  try {
    return JSON.stringify(JSON.parse(t));
  } catch {
    return null;
  }
}
