import { useVirtualizer } from "@tanstack/react-virtual";
import { ArrowDown, ArrowUp } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { copyText, type ResultColumn } from "../api";
import { openMenu, type MenuItem } from "./ContextMenu";

export type Cell = string | null;

/** Pending edits overlaid on fetched rows. Keys are row indexes in the grid. */
export interface GridEdits {
  updated: Map<number, Map<number, Cell>>; // row -> col -> new value
  inserted: Set<number>; // rows (>= base rowCount) that are new
  deleted: Set<number>;
}

export const emptyEdits = (): GridEdits => ({ updated: new Map(), inserted: new Set(), deleted: new Set() });

/** A page of rows. cut lists the cells sent shortened, as [row in page, column]. */
export interface GridPage {
  rows: Cell[][];
  cut?: (number[] | null)[] | null;
}

/** How many characters of a value the grid shows; longer ones are fetched cut to this. */
export const MAX_CELL_CHARS = 300;

export interface DataGridProps {
  columns: ResultColumn[];
  /** Number of rows available from the source (excluding inserted rows). */
  rowCount: number;
  /** Identity of the data; changing it clears the row cache. */
  dataKey: string;
  fetchPage(offset: number, limit: number): Promise<GridPage>;
  /** Whole values for rows that came back cut, for editing and copying. */
  fetchFull?(offset: number, limit: number): Promise<Cell[][]>;
  editable?: boolean;
  /** Columns that can't be edited even when the grid is (computed query result columns). */
  readOnlyColumns?: boolean[];
  edits?: GridEdits;
  onEditCell?(row: number, col: number, value: Cell): void;
  onDeleteRows?(rows: number[]): void;
  onInsertRow?(): void;
  sort?: { column: string; desc: boolean }[];
  onSort?(column: string, desc: boolean | null): void;
  /** Extra context menu items for a cell. */
  extraMenu?(row: number, col: number, value: Cell): MenuItem[];
  /** Report the row/col under the cursor (status bar, value panel), with its value when loaded. */
  onActiveCell?(row: number, col: number, value?: Cell): void;
  /** Absolute row number offset shown in the row header (paged table views). */
  rowNumberOffset?: number;
  /** Changing it refetches rows but keeps the selection (rows edited in place). */
  revision?: number;
  /** How a null cell reads (default NULL). */
  nullText?: string;
}

const ROW_H = 22;
const PAGE = 500;
const HEADER_H = 38; // two lines: name, then type
const ROWHDR_W = 48;
// Pages kept in memory; the least recently shown go first.
const MAX_PAGES = 20;

interface CachedPage {
  rows: Cell[][];
  /** Cut cells as row-in-page * columns + column. */
  cut: Set<number>;
}

interface Sel {
  r0: number;
  c0: number;
  r1: number;
  c1: number;
}

export function DataGrid(p: DataGridProps) {
  const scroller = useRef<HTMLDivElement>(null);
  const cache = useRef(new Map<number, CachedPage>());
  const pending = useRef(new Set<number>());
  // Bumped whenever the caches reset, so a page fetched for older data is dropped.
  const generation = useRef(0);
  const [, bump] = useState(0);
  const [widths, setWidths] = useState<number[]>([]);
  const [sel, setSel] = useState<Sel | null>(null);
  // orig is the whole value being edited, so an unchanged commit is no change.
  const [editing, setEditing] = useState<{ r: number; c: number; value: string; isNull: boolean; orig: Cell } | null>(null);
  const dragging = useRef(false);

  const inserted = p.edits ? [...p.edits.inserted].sort((a, b) => a - b) : [];
  const total = p.rowCount + inserted.length;

  // Reset caches when the data changes.
  useEffect(() => {
    cache.current.clear();
    pending.current.clear();
    generation.current++;
    setSel(null);
    setEditing(null);
    bump((x) => x + 1);
  }, [p.dataKey]);

  useEffect(() => {
    if (p.revision === undefined) return;
    cache.current.clear();
    pending.current.clear();
    generation.current++;
    bump((x) => x + 1);
  }, [p.revision]);

  // Initial column widths from header and type.
  useEffect(() => {
    setWidths(p.columns.map((c) => Math.min(320, Math.max(70, c.name.length * 8 + 34, guessWidth(c.type)))));
  }, [p.columns]);

  const v = useVirtualizer({ count: total, getScrollElement: () => scroller.current, estimateSize: () => ROW_H, overscan: 30 });
  const items = v.getVirtualItems();

  const toCached = (pg: GridPage): CachedPage => ({ rows: pg.rows, cut: new Set((pg.cut ?? []).map((rc) => rc![0] * p.columns.length + rc![1])) });

  // Fetch pages for visible rows; keep the most recently shown MAX_PAGES.
  useEffect(() => {
    if (!items.length) return;
    const first = Math.floor(items[0].index / PAGE);
    const last = Math.floor(Math.min(items[items.length - 1].index, p.rowCount - 1) / PAGE);
    const c = cache.current;
    for (let pg = first; pg <= last; pg++) {
      const hit = c.get(pg);
      if (hit) {
        c.delete(pg);
        c.set(pg, hit);
      }
    }
    for (const pg of c.keys()) {
      if (c.size <= MAX_PAGES) break;
      if (pg < first || pg > last) c.delete(pg);
    }
    for (let pg = first; pg <= last; pg++) {
      if (c.has(pg) || pending.current.has(pg) || pg * PAGE >= p.rowCount) continue;
      pending.current.add(pg);
      const gen = generation.current;
      p.fetchPage(pg * PAGE, PAGE)
        .then((page) => {
          if (gen !== generation.current) return;
          c.set(pg, toCached(page));
          bump((x) => x + 1);
        })
        .finally(() => {
          if (gen === generation.current) pending.current.delete(pg);
        });
    }
  });

  // A partially filled last page needs refetching when more rows stream in.
  useEffect(() => {
    for (const [pg, { rows }] of cache.current) {
      if (rows.length < PAGE && pg * PAGE + rows.length < p.rowCount) cache.current.delete(pg);
    }
    bump((x) => x + 1);
  }, [p.rowCount]);

  const baseRow = (r: number): Cell[] | undefined => {
    if (r >= p.rowCount) return undefined;
    return cache.current.get(Math.floor(r / PAGE))?.rows[r % PAGE];
  };

  /** Whether the grid has only the start of a fetched value. */
  const isCut = (r: number, c: number) =>
    r < p.rowCount && !p.edits?.updated.get(r)?.has(c) && !!cache.current.get(Math.floor(r / PAGE))?.cut.has((r % PAGE) * p.columns.length + c);

  /** The whole value of a cell, fetching it when the grid has it cut. */
  const fullValue = async (r: number, c: number): Promise<Cell | undefined> => {
    if (!isCut(r, c) || !p.fetchFull) return cellValue(r, c);
    return (await p.fetchFull(r, 1))[0]?.[c];
  };

  const cellValue = useCallback(
    (r: number, c: number): Cell | undefined => {
      const upd = p.edits?.updated.get(r);
      if (upd?.has(c)) return upd.get(c)!;
      if (r >= p.rowCount) return null; // new row default
      const row = baseRow(r);
      return row ? row[c] : undefined;
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [p.edits, p.rowCount, p.dataKey],
  );

  const colLeft = useMemo(() => {
    const out = [ROWHDR_W];
    for (const w of widths) out.push(out[out.length - 1] + w);
    return out;
  }, [widths]);
  const totalWidth = colLeft[colLeft.length - 1] ?? ROWHDR_W;

  // ---- selection & keyboard ----------------------------------------------------

  const norm = (s: Sel) => ({ r0: Math.min(s.r0, s.r1), r1: Math.max(s.r0, s.r1), c0: Math.min(s.c0, s.c1), c1: Math.max(s.c0, s.c1) });
  const inSel = (r: number, c: number) => {
    if (!sel) return false;
    const n = norm(sel);
    return r >= n.r0 && r <= n.r1 && c >= n.c0 && c <= n.c1;
  };

  const scrollToCell = (r: number, c: number) => {
    v.scrollToIndex(r, { align: "auto" });
    const el = scroller.current;
    if (!el) return;
    const left = colLeft[c] - ROWHDR_W;
    const right = colLeft[c + 1];
    if (left < el.scrollLeft) el.scrollLeft = left;
    else if (right > el.scrollLeft + el.clientWidth) el.scrollLeft = right - el.clientWidth;
  };

  const moveTo = (r: number, c: number, extend: boolean) => {
    r = Math.max(0, Math.min(total - 1, r));
    c = Math.max(0, Math.min(p.columns.length - 1, c));
    setSel((s) => (extend && s ? { ...s, r1: r, c1: c } : { r0: r, c0: c, r1: r, c1: c }));
    scrollToCell(r, c);
    p.onActiveCell?.(r, c, isCut(r, c) ? undefined : cellValue(r, c));
  };

  const canEdit = (c: number) => !!p.editable && !p.readOnlyColumns?.[c];

  const startEdit = (r: number, c: number, initial?: string) => {
    if (!canEdit(c) || p.edits?.deleted.has(r)) return;
    const open = (cur: Cell) => setEditing({ r, c, value: initial ?? cur ?? "", isNull: initial === undefined && cur === null, orig: cur });
    // Synchronous unless the value has to be fetched whole, so typed keys reach the editor.
    if (isCut(r, c)) fullValue(r, c).then((v) => open(v ?? null));
    else open(cellValue(r, c) ?? null);
  };

  const commitEdit = (value: Cell) => {
    if (!editing) return;
    if (editing.orig !== value) p.onEditCell?.(editing.r, editing.c, value);
    setEditing(null);
    scroller.current?.focus();
  };

  const copySelection = async (withHeaders = false) => {
    if (!sel) return;
    const n = norm(sel);
    const value = await loadWhole(n.r0, n.r1, n.c0, n.c1);
    // One cell copies as it is; a block is tab-separated, quoting fields that hold tabs, line breaks or quotes.
    const one = n.r0 === n.r1 && n.c0 === n.c1 && !withHeaders;
    const field = (v: string) => (one || !/[\t\n\r"]/.test(v) ? v : '"' + v.replace(/"/g, '""') + '"');
    const lines: string[] = [];
    if (withHeaders) lines.push(p.columns.slice(n.c0, n.c1 + 1).map((c) => field(c.name)).join("\t"));
    for (let r = n.r0; r <= n.r1; r++) {
      const vals: string[] = [];
      for (let c = n.c0; c <= n.c1; c++) vals.push(field(value(r, c) ?? "NULL"));
      lines.push(vals.join("\t"));
    }
    await copyText(lines.join("\n"));
  };

  /** Reads rows r0..r1 whole for a copy: the pages it needs are held here, not in
   *  the cache (which keeps only MAX_PAGES), and cut cells are fetched in full. */
  const loadWhole = async (r0: number, r1: number, c0: number, c1: number) => {
    const pages = new Map<number, Cell[][]>();
    const last = Math.min(r1, p.rowCount - 1);
    await Promise.all(
      range(Math.floor(r0 / PAGE), Math.floor(last / PAGE)).map(async (pg) => {
        if (last < 0) return;
        const page = cache.current.get(pg) ?? toCached(await p.fetchPage(pg * PAGE, PAGE));
        const from = Math.max(r0, pg * PAGE), to = Math.min(last, pg * PAGE + PAGE - 1);
        const needsWhole = p.fetchFull && [...page.cut].some((k) => {
          const r = pg * PAGE + Math.floor(k / p.columns.length), c = k % p.columns.length;
          return r >= from && r <= to && c >= c0 && c <= c1;
        });
        if (!needsWhole) return void pages.set(pg, page.rows);
        const whole = await p.fetchFull!(from, to - from + 1);
        const rows = page.rows.slice();
        whole.forEach((row, i) => (rows[from - pg * PAGE + i] = row));
        pages.set(pg, rows);
      }),
    );
    return (r: number, c: number): Cell | undefined => {
      const upd = p.edits?.updated.get(r);
      if (upd?.has(c)) return upd.get(c)!;
      if (r >= p.rowCount) return null;
      return pages.get(Math.floor(r / PAGE))?.[r % PAGE]?.[c];
    };
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (editing) return;
    const meta = e.metaKey || e.ctrlKey;
    if (meta && e.key === "c") {
      e.preventDefault();
      copySelection(e.shiftKey);
      return;
    }
    if (meta && e.key === "a") {
      e.preventDefault();
      setSel({ r0: 0, c0: 0, r1: total - 1, c1: p.columns.length - 1 });
      return;
    }
    if (!sel) {
      if (["ArrowDown", "ArrowUp", "ArrowLeft", "ArrowRight"].includes(e.key) && total) moveTo(0, 0, false);
      return;
    }
    const r = sel.r1;
    const c = sel.c1;
    const page = Math.max(1, Math.floor((scroller.current?.clientHeight ?? 400) / ROW_H) - 1);
    switch (e.key) {
      case "ArrowDown":
        // Down on the last row adds a new row (Navicat's shortcut for "+").
        if (!meta && !e.shiftKey && r === total - 1 && p.editable && p.onInsertRow) {
          p.onInsertRow();
          setSel({ r0: r + 1, c0: c, r1: r + 1, c1: c });
          setTimeout(() => scrollToCell(r + 1, c), 0);
          p.onActiveCell?.(r + 1, c, null);
          break;
        }
        moveTo(meta ? total - 1 : r + 1, c, e.shiftKey);
        break;
      case "ArrowUp":
        moveTo(meta ? 0 : r - 1, c, e.shiftKey);
        break;
      case "ArrowLeft":
        moveTo(r, meta ? 0 : c - 1, e.shiftKey);
        break;
      case "ArrowRight":
        moveTo(r, meta ? p.columns.length - 1 : c + 1, e.shiftKey);
        break;
      case "Tab":
        moveTo(r, e.shiftKey ? c - 1 : c + 1, false);
        break;
      case "PageDown":
        moveTo(r + page, c, e.shiftKey);
        break;
      case "PageUp":
        moveTo(r - page, c, e.shiftKey);
        break;
      case "Home":
        moveTo(meta ? 0 : r, 0, e.shiftKey);
        break;
      case "End":
        moveTo(meta ? total - 1 : r, p.columns.length - 1, e.shiftKey);
        break;
      case "Enter":
      case "F2":
        startEdit(r, c);
        break;
      case "Backspace":
      case "Delete":
        if (p.editable && !meta) {
          const n = norm(sel);
          for (let rr = n.r0; rr <= n.r1; rr++) for (let cc = n.c0; cc <= n.c1; cc++) if (canEdit(cc) && cellValue(rr, cc) !== "") p.onEditCell?.(rr, cc, "");
        } else if (p.editable && meta) {
          const n = norm(sel);
          p.onDeleteRows?.(range(n.r0, n.r1));
        }
        break;
      default:
        if (p.editable && e.key.length === 1 && !meta && !e.altKey) {
          e.preventDefault();
          startEdit(r, c, e.key);
          return;
        }
        return;
    }
    e.preventDefault();
  };

  // ---- column resize -------------------------------------------------------------

  const startResize = (c: number, e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    const x0 = e.clientX;
    const w0 = widths[c];
    const move = (ev: MouseEvent) => setWidths((ws) => ws.map((w, i) => (i === c ? Math.max(40, w0 + ev.clientX - x0) : w)));
    const up = () => {
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", up);
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", up);
  };

  const autoFit = (c: number) => {
    let max = p.columns[c].name.length * 8 + 34;
    for (const it of items) {
      const val = cellValue(it.index, c);
      if (val) max = Math.max(max, Math.min(600, val.length * 7.2 + 16));
    }
    setWidths((ws) => ws.map((w, i) => (i === c ? max : w)));
  };

  useEffect(() => {
    const up = () => (dragging.current = false);
    window.addEventListener("mouseup", up);
    return () => window.removeEventListener("mouseup", up);
  }, []);

  const sortOf = (name: string) => p.sort?.find((s) => s.column === name);

  const menuForCell = (r: number, c: number): MenuItem[] => {
    const val = cellValue(r, c) ?? null;
    const items: MenuItem[] = [
      { label: "Copy", shortcut: "⌘C", onClick: () => copySelection() },
      { label: "Copy with Headers", shortcut: "⇧⌘C", onClick: () => copySelection(true) },
      { label: "Copy Value", onClick: async () => copyText((await fullValue(r, c)) ?? "NULL") },
    ];
    if (p.editable) {
      items.push(
        { separator: true },
        { label: "Edit Cell", shortcut: "↩", disabled: !canEdit(c), onClick: () => startEdit(r, c) },
        { label: "Set to NULL", disabled: !canEdit(c), onClick: () => p.onEditCell?.(r, c, null) },
        { label: "Set to Empty String", disabled: !canEdit(c), onClick: () => p.onEditCell?.(r, c, "") },
        { separator: true },
        { label: "Add Row", onClick: () => p.onInsertRow?.() },
        {
          label: "Delete Selected Rows",
          danger: true,
          shortcut: "⌘⌫",
          onClick: () => {
            const n = sel && inSel(r, c) ? norm(sel) : { r0: r, r1: r };
            p.onDeleteRows?.(range(n.r0, n.r1));
          },
        },
      );
    }
    if (p.extraMenu) items.push({ separator: true }, ...p.extraMenu(r, c, val));
    return items;
  };

  return (
    <div className="grid" ref={scroller} tabIndex={0} onKeyDown={onKeyDown}>
      <div style={{ width: totalWidth, height: v.getTotalSize() + HEADER_H, position: "relative" }}>
        {/* header */}
        <div className="grid-header" style={{ width: totalWidth, height: HEADER_H }}>
          <div className="grid-corner" style={{ width: ROWHDR_W }} onClick={() => total && setSel({ r0: 0, c0: 0, r1: total - 1, c1: p.columns.length - 1 })} />
          {p.columns.map((col, c) => {
            const s = sortOf(col.name);
            return (
              <div
                key={c}
                className="grid-hcell"
                style={{ left: colLeft[c], width: widths[c] }}
                title={`${col.name} — ${col.type || "?"}`}
                onClick={(e) => {
                  if (p.onSort) {
                    // asc -> desc -> none
                    p.onSort(col.name, !s ? false : !s.desc ? true : null);
                  } else if (total) {
                    setSel({ r0: 0, c0: c, r1: total - 1, c1: c });
                  }
                  e.preventDefault();
                }}
                onContextMenu={(e) =>
                  openMenu(e, [
                    { label: "Select Column", onClick: () => total && setSel({ r0: 0, c0: c, r1: total - 1, c1: c }) },
                    { label: "Fit Column Width", onClick: () => autoFit(c) },
                    { label: "Copy Column Name", onClick: () => copyText(col.name) },
                    ...(p.onSort
                      ? [
                          { separator: true },
                          { label: "Sort Ascending", onClick: () => p.onSort!(col.name, false) },
                          { label: "Sort Descending", onClick: () => p.onSort!(col.name, true) },
                          { label: "Remove Sort", disabled: !s, onClick: () => p.onSort!(col.name, null) },
                        ]
                      : []),
                  ])
                }
              >
                <span className="hname">
                  {col.name}
                  {s && (s.desc ? <ArrowDown size={11} /> : <ArrowUp size={11} />)}
                </span>
                <span className="htype">{col.type?.toLowerCase()}</span>
                <span className="resizer" onMouseDown={(e) => startResize(c, e)} onDoubleClick={() => autoFit(c)} onClick={(e) => e.stopPropagation()} />
              </div>
            );
          })}
        </div>

        {/* rows */}
        {items.map((it) => {
          const r = it.index;
          const loaded = r >= p.rowCount || !!baseRow(r);
          const deleted = p.edits?.deleted.has(r);
          const isNew = r >= p.rowCount;
          const upd = p.edits?.updated.get(r);
          return (
            <div key={r} className={"grid-row" + (r % 2 ? " odd" : "") + (deleted ? " deleted" : "") + (isNew ? " new" : "")} style={{ top: it.start + HEADER_H, height: ROW_H, width: totalWidth }}>
              <div
                className={"grid-rowhdr" + (upd || isNew || deleted ? " dirty" : "")}
                style={{ width: ROWHDR_W }}
                onMouseDown={(e) => {
                  setSel((s) => (e.shiftKey && s ? { ...s, r1: r, c0: 0, c1: p.columns.length - 1 } : { r0: r, r1: r, c0: 0, c1: p.columns.length - 1 }));
                  scroller.current?.focus();
                }}
              >
                {isNew ? "*" : r + 1 + (p.rowNumberOffset ?? 0)}
              </div>
              {p.columns.map((_, c) => {
                const val = loaded ? cellValue(r, c) : undefined;
                const isEditing = editing?.r === r && editing.c === c;
                const changed = upd?.has(c);
                return (
                  <div
                    key={c}
                    className={"grid-cell" + (inSel(r, c) ? " sel" : "") + (sel && sel.r1 === r && sel.c1 === c ? " active" : "") + (changed ? " changed" : "")}
                    style={{ left: colLeft[c], width: widths[c] }}
                    onMouseDown={(e) => {
                      if (e.button !== 0) return;
                      dragging.current = true;
                      setSel((s) => (e.shiftKey && s ? { ...s, r1: r, c1: c } : { r0: r, c0: c, r1: r, c1: c }));
                      p.onActiveCell?.(r, c, isCut(r, c) ? undefined : cellValue(r, c));
                    }}
                    onMouseEnter={() => dragging.current && setSel((s) => (s ? { ...s, r1: r, c1: c } : s))}
                    onDoubleClick={() => startEdit(r, c)}
                    onContextMenu={(e) => {
                      if (!inSel(r, c)) setSel({ r0: r, c0: c, r1: r, c1: c });
                      openMenu(e, menuForCell(r, c));
                    }}
                  >
                    {isEditing ? (
                      <CellEditor
                        value={editing.value}
                        isNull={editing.isNull}
                        onCommit={commitEdit}
                        onCancel={() => {
                          setEditing(null);
                          scroller.current?.focus();
                        }}
                        onTab={(back) => {
                          commitEdit(editing.value);
                          moveTo(r, c + (back ? -1 : 1), false);
                        }}
                        onChange={(value) => setEditing({ ...editing, value, isNull: false })}
                      />
                    ) : val === undefined ? (
                      <span className="loading-cell" />
                    ) : isNew && !changed ? (
                      // Untouched cells of a new row are left out of the INSERT.
                      <span className="null">(default)</span>
                    ) : val === null ? (
                      <span className="null">{p.nullText ?? "NULL"}</span>
                    ) : (
                      displayValue(val, isCut(r, c))
                    )}
                  </div>
                );
              })}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function CellEditor(props: { value: string; isNull: boolean; onChange(v: string): void; onCommit(v: Cell): void; onCancel(): void; onTab(back: boolean): void }) {
  const ref = useRef<HTMLInputElement>(null);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.focus();
    el.setSelectionRange(el.value.length, el.value.length);
  }, []);
  return (
    <input
      ref={ref}
      className="cell-editor"
      value={props.value}
      placeholder={props.isNull ? "NULL" : ""}
      onChange={(e) => props.onChange(e.target.value)}
      onBlur={() => props.onCommit(props.isNull && props.value === "" ? null : props.value)}
      onMouseDown={(e) => e.stopPropagation()}
      onKeyDown={(e) => {
        e.stopPropagation();
        if (e.key === "Enter") props.onCommit(props.isNull && props.value === "" ? null : props.value);
        else if (e.key === "Escape") props.onCancel();
        else if (e.key === "Tab") {
          e.preventDefault();
          props.onTab(e.shiftKey);
        }
      }}
    />
  );
}

function displayValue(v: string, cut: boolean) {
  const one = cut || v.length > MAX_CELL_CHARS ? v.slice(0, MAX_CELL_CHARS) + "…" : v;
  return one.includes("\n") ? one.replace(/\n/g, "⏎ ") : one;
}

function guessWidth(type: string) {
  const t = (type || "").toUpperCase();
  if (/INT|SERIAL|BOOL|BIT/.test(t)) return 80;
  if (/DATE|TIME/.test(t)) return 160;
  if (/DECIMAL|NUMERIC|FLOAT|DOUBLE|REAL/.test(t)) return 100;
  if (/UUID/.test(t)) return 280;
  if (/TEXT|JSON|CHAR|BLOB|BYTEA/.test(t)) return 200;
  return 140;
}

function range(a: number, b: number) {
  const out: number[] = [];
  for (let i = a; i <= b; i++) out.push(i);
  return out;
}
