// A page of MongoDB documents as a grid (top-level fields as columns), a tree of
// key / value / type rows, or shell-syntax text.
import { useVirtualizer } from "@tanstack/react-virtual";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Mongo, errorText, type MG, type ResultColumn, copyText } from "../../api";
import { editorTheme, monaco } from "../../editor/monaco";
import { registerMongoLanguage } from "../../editor/mongo";
import { TYPE_LABEL, Val, cellText, editType, field, fields, formatVal, items, summary } from "../../mongo/docs";
import { Cell, DataGrid } from "../DataGrid";
import { alert } from "../Dialogs";
import { MenuItem, openMenu } from "../ContextMenu";

export type DocsMode = "grid" | "tree" | "json";

export interface DocsViewProps {
  mode: DocsMode;
  docs: Val[];
  fields: MG.FieldInfo[];
  /** Identity of the page; changing it resets selection and scrolling. */
  dataKey: string;
  /** Bumped when documents change in place. */
  revision: number;
  /** Number of the first document (paging). */
  rowOffset: number;
  /** Set (value) or remove (null) a field by dotted path; editing is off without it. */
  onSetField?(doc: number, path: string, value: Val | null): Promise<void>;
  onDelete?(docs: number[]): void;
  onEditDoc?(doc: number): void;
  onAdd?(): void;
  sort?: { column: string; desc: boolean }[];
  onSort?(column: string, desc: boolean | null): void;
  onActive?(doc: number | null, path?: string): void;
}

/** Read a cell or tree edit as a value: the field keeps its type unless the text says otherwise. */
export async function parseEdit(old: Val | undefined, text: string | null, columnType?: string): Promise<Val> {
  if (text === null) return { t: "null" };
  const t = editType(old?.t, text, columnType);
  return Mongo.ParseValue(t, text);
}

export function DocsView(p: DocsViewProps) {
  if (p.mode === "tree") return <TreeView {...p} />;
  if (p.mode === "json") return <JsonView {...p} />;
  return <GridView {...p} />;
}

// ---- grid -------------------------------------------------------------------------------

function GridView(p: DocsViewProps) {
  const columns = useMemo<ResultColumn[]>(() => p.fields.map((f) => ({ name: f.name, type: TYPE_LABEL[f.type] ?? f.type, nullable: true })), [p.fields]);
  const docs = useRef(p.docs);
  docs.current = p.docs;
  const editable = !!p.onSetField;

  return (
    <DataGrid
      columns={columns}
      rowCount={p.docs.length}
      dataKey={p.dataKey}
      revision={p.revision}
      nullText="(Null)"
      rowNumberOffset={p.rowOffset}
      fetchPage={async (offset, limit) => docs.current.slice(offset, offset + limit).map((d) => p.fields.map((f) => cellText(field(d, f.name))))}
      editable={editable}
      onEditCell={async (r, c, value: Cell) => {
        const name = p.fields[c]?.name;
        const old = field(docs.current[r], name);
        if (!old && value === "") return;
        try {
          await p.onSetField!(r, name, await parseEdit(old, value, p.fields[c]?.type));
        } catch (e) {
          alert(`Couldn't set ${name}`, errorText(e));
        }
      }}
      onDeleteRows={(rows) => p.onDelete?.(rows)}
      onInsertRow={() => p.onAdd?.()}
      sort={p.sort}
      onSort={p.onSort}
      onActiveCell={(r, c) => p.onActive?.(r < p.docs.length ? r : null, p.fields[c]?.name)}
      extraMenu={(r, c) => {
        const name = p.fields[c]?.name;
        const has = !!field(docs.current[r], name);
        const out: MenuItem[] = [{ separator: true }];
        if (p.onEditDoc) out.push({ label: "Edit Document…", onClick: () => p.onEditDoc!(r) });
        if (editable) out.push({ label: `Delete Field “${name}”`, disabled: !has || name === "_id", onClick: () => p.onSetField!(r, name, null) });
        out.push({ label: "Copy Document", onClick: () => copyText(formatVal(docs.current[r], "  ", true)) });
        return out;
      }}
    />
  );
}

// ---- tree --------------------------------------------------------------------------------

interface Row {
  key: string;
  doc: number;
  /** dotted path below the document ("" for the document itself) */
  path: string;
  label: string;
  value: Val;
  depth: number;
}

function TreeView(p: DocsViewProps) {
  const [open, setOpen] = useState<Record<string, boolean>>({});
  const [sel, setSel] = useState<string | null>(null);
  const [editing, setEditing] = useState<{ key: string; text: string } | null>(null);
  // Escape removes the input, whose blur must not then save it.
  const cancelled = useRef(false);
  const scroller = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setOpen({});
    setSel(null);
  }, [p.dataKey]);

  const rows = useMemo(() => {
    const out: Row[] = [];
    const walk = (v: Val, doc: number, path: string, depth: number) => {
      const kids = v.t === "object" ? fields(v).map((f) => [f.k, f.v] as const) : items(v).map((e, i) => [`[${i}]`, e] as const);
      kids.forEach(([k, child], i) => {
        const seg = v.t === "array" ? String(i) : k;
        const cp = path ? `${path}.${seg}` : seg;
        const key = `${doc}:${cp}`;
        out.push({ key, doc, path: cp, label: k, value: child, depth });
        if (open[key] && (child.t === "object" || child.t === "array")) walk(child, doc, cp, depth + 1);
      });
    };
    p.docs.forEach((d, i) => {
      const key = `${i}:`;
      const id = field(d, "_id");
      out.push({ key, doc: i, path: "", label: `(${p.rowOffset + i + 1}) ${id ? cellText(id) ?? "null" : ""}`, value: d, depth: 0 });
      if (open[key]) walk(d, i, "", 1);
    });
    return out;
  }, [p.docs, p.rowOffset, open]);

  const v = useVirtualizer({ count: rows.length, getScrollElement: () => scroller.current, estimateSize: () => 22, overscan: 30 });
  const toggle = (r: Row) => setOpen((o) => ({ ...o, [r.key]: !o[r.key] }));
  const canEdit = (r: Row) => !!p.onSetField && r.path !== "" && r.path !== "_id" && r.value.t !== "object" && r.value.t !== "array";

  const commit = async (r: Row, text: string) => {
    setEditing(null);
    if (cancelled.current) {
      cancelled.current = false;
      return;
    }
    try {
      await p.onSetField!(r.doc, r.path, await parseEdit(r.value, text));
    } catch (e) {
      alert(`Couldn't set ${r.path}`, errorText(e));
    }
  };

  const menu = (e: React.MouseEvent, r: Row) => {
    setSel(r.key);
    p.onActive?.(r.doc, r.path);
    openMenu(e, [
      { label: "Edit Value", disabled: !canEdit(r), onClick: () => setEditing({ key: r.key, text: cellText(r.value) ?? "" }) },
      ...(p.onEditDoc ? [{ label: "Edit Document…", onClick: () => p.onEditDoc!(r.doc) }] : []),
      ...(p.onSetField && r.path && r.path !== "_id" ? [{ label: "Delete Field", onClick: () => p.onSetField!(r.doc, r.path, null) }] : []),
      ...(p.onDelete ? [{ label: "Delete Document", onClick: () => p.onDelete!([r.doc]) }] : []),
      { separator: true },
      { label: "Copy Value", onClick: () => copyText(r.value.t === "object" || r.value.t === "array" ? formatVal(r.value, "  ", true) : cellText(r.value) ?? "null") },
      { label: "Copy Field Path", disabled: !r.path, onClick: () => copyText(r.path) },
      ...(p.onAdd ? [{ separator: true }, { label: "Add Document…", onClick: () => p.onAdd!() }] : []),
    ]);
  };

  return (
    <div className="doc-tree">
      <div className="doc-tree-head">
        <span>Key</span>
        <span>Value</span>
        <span>Type</span>
      </div>
      <div className="doc-tree-body" ref={scroller} tabIndex={0}>
        <div style={{ height: v.getTotalSize(), position: "relative" }}>
          {v.getVirtualItems().map((vi) => {
            const r = rows[vi.index];
            const container = r.value.t === "object" || r.value.t === "array";
            return (
              <div
                key={r.key}
                className={"doc-tree-row" + (sel === r.key ? " selected" : "")}
                style={{ top: vi.start }}
                onClick={() => {
                  setSel(r.key);
                  p.onActive?.(r.doc, r.path);
                }}
                onDoubleClick={() => {
                  cancelled.current = false;
                  if (container) toggle(r);
                  else if (canEdit(r)) setEditing({ key: r.key, text: cellText(r.value) ?? "" });
                }}
                onContextMenu={(e) => menu(e, r)}
              >
                <span className="doc-key" style={{ paddingLeft: 6 + r.depth * 16 }}>
                  {container ? (
                    <button className="twisty" onClick={(e) => (e.stopPropagation(), toggle(r))}>
                      {open[r.key] ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
                    </button>
                  ) : (
                    <span className="twisty" />
                  )}
                  {r.label}
                </span>
                <span className={"doc-val t-" + r.value.t}>
                  {editing?.key === r.key ? (
                    <input
                      autoFocus
                      value={editing.text}
                      onChange={(e) => setEditing({ key: r.key, text: e.target.value })}
                      onBlur={() => commit(r, editing.text)}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") commit(r, editing.text);
                        if (e.key === "Escape") {
                          cancelled.current = true;
                          setEditing(null);
                        }
                      }}
                    />
                  ) : r.value.t === "null" ? (
                    <span className="null">(Null)</span>
                  ) : (
                    summary(r.value)
                  )}
                </span>
                <span className="doc-type">{TYPE_LABEL[r.value.t] ?? r.value.t}</span>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}

// ---- JSON -----------------------------------------------------------------------------------

function JsonView(p: DocsViewProps) {
  const host = useRef<HTMLDivElement>(null);
  const editorRef = useRef<ReturnType<typeof monaco.editor.create> | null>(null);
  const starts = useRef<number[]>([]);
  const pRef = useRef(p);
  pRef.current = p;

  useEffect(() => {
    registerMongoLanguage();
    const model = monaco.editor.createModel("", "mongo");
    const ed = monaco.editor.create(host.current!, {
      model,
      theme: editorTheme(),
      readOnly: true,
      automaticLayout: true,
      minimap: { enabled: false },
      fontSize: 13,
      fontFamily: "SF Mono, Menlo, Consolas, monospace",
      lineNumbersMinChars: 4,
      scrollBeyondLastLine: false,
      folding: true,
    });
    editorRef.current = ed;
    const docAt = (line: number) => {
      let i = 0;
      while (i + 1 < starts.current.length && starts.current[i + 1] <= line) i++;
      return i;
    };
    ed.onMouseDown((e) => {
      if (e.event.detail === 2 && e.target.position && pRef.current.onEditDoc) pRef.current.onEditDoc(docAt(e.target.position.lineNumber));
    });
    ed.onDidChangeCursorPosition((e) => pRef.current.onActive?.(docAt(e.position.lineNumber)));
    return () => {
      ed.dispose();
      model.dispose();
    };
  }, []);

  useEffect(() => {
    const parts: string[] = [];
    const lines: number[] = [];
    let line = 1;
    p.docs.forEach((d, i) => {
      const t = `/* ${p.rowOffset + i + 1} */\n` + formatVal(d, "  ", true);
      lines.push(line);
      parts.push(t);
      line += t.split("\n").length + 1;
    });
    starts.current = lines;
    const ed = editorRef.current;
    const pos = ed?.getScrollTop() ?? 0;
    ed?.getModel()?.setValue(parts.join("\n\n"));
    if (ed && p.revision) ed.setScrollTop(pos);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [p.docs, p.dataKey, p.revision]);

  return <div className="doc-json" ref={host} />;
}
