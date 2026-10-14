import { Check, ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight, Download, Filter, Minus, PencilRuler, Plus, RefreshCw, Upload, X } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { ChangeKind, Explorer, ObjectKind, Query, RowChange, StatementResult, TableDetail, arr, errorText } from "../api";
import type { Tab } from "../store";
import { Cell, DataGrid, GridEdits, emptyEdits } from "./DataGrid";
import { confirm } from "./Dialogs";
import { designTable } from "../tableActions";
import { openExport, openImport } from "./transfer/state";

const PAGE_SIZES = [100, 500, 1000, 5000, 10000];

export function TableViewer({ tab }: { tab: Tab }) {
  const ref = tab.ref!;
  const connId = tab.connId!;
  const editable = ref.kind === ObjectKind.KindTable;

  const [detail, setDetail] = useState<TableDetail | null>(null);
  const [result, setResult] = useState<StatementResult | null>(null);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [where, setWhere] = useState("");
  const [whereDraft, setWhereDraft] = useState("");
  const [showFilter, setShowFilter] = useState(false);
  const [sort, setSort] = useState<{ column: string; desc: boolean }[]>([]);
  const [page, setPage] = useState(0);
  const [pageSize, setPageSize] = useState(1000);
  const [total, setTotal] = useState<number | null>(null);
  const [edits, setEdits] = useState<GridEdits>(emptyEdits);
  const [active, setActive] = useState<{ r: number; c: number } | null>(null);
  const [message, setMessage] = useState("");
  const loadSeq = useRef(0);

  useEffect(() => {
    Explorer.TableDetail(connId, ref).then(setDetail).catch((e) => setError(errorText(e)));
  }, [connId, ref.database, ref.schema, ref.name]);

  const load = useCallback(async () => {
    const seq = ++loadSeq.current;
    setLoading(true);
    setError("");
    try {
      const res = await Query.BrowseTable(connId, { ref, where, sort, offset: page * pageSize, limit: pageSize });
      if (seq !== loadSeq.current) {
        if (res.resultId) Query.ReleaseResult(res.resultId);
        return;
      }
      setResult((prev) => {
        if (prev?.resultId) Query.ReleaseResult(prev.resultId);
        return res;
      });
      if (res.error) setError(res.error);
      setEdits(emptyEdits());
    } catch (e) {
      setError(errorText(e));
    } finally {
      if (seq === loadSeq.current) setLoading(false);
    }
  }, [connId, ref, where, sort, page, pageSize]);

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    setTotal(null);
    Query.CountRows(connId, ref, where)
      .then(setTotal)
      .catch(() => setTotal(null));
  }, [connId, ref, where]);

  // Release the server-side buffer when the tab closes.
  useEffect(() => () => void (result?.resultId && Query.ReleaseResult(result.resultId)), [result?.resultId]);

  const dirty = edits.updated.size > 0 || edits.inserted.size > 0 || edits.deleted.size > 0;
  const columns = arr(result?.columns);
  const rowCount = result?.rowCount ?? 0;

  const fetchPage = useCallback(
    async (offset: number, limit: number): Promise<Cell[][]> => {
      if (!result?.resultId) return [];
      const p = await Query.FetchRows(result.resultId, offset, limit);
      return arr(p.rows) as Cell[][];
    },
    [result?.resultId],
  );

  const guardDirty = async () => !dirty || (await confirm("Discard unsaved changes?", { danger: true, okLabel: "Discard" }));

  const onEditCell = (row: number, col: number, value: Cell) => {
    setEdits((e) => {
      const updated = new Map(e.updated);
      const m = new Map(updated.get(row) ?? []);
      m.set(col, value);
      updated.set(row, m);
      return { ...e, updated };
    });
  };

  const onInsertRow = () => {
    setEdits((e) => {
      const inserted = new Set(e.inserted);
      inserted.add(rowCount + inserted.size);
      return { ...e, inserted };
    });
  };

  const onDeleteRows = (rows: number[]) => {
    setEdits((e) => {
      const deleted = new Set(e.deleted);
      const inserted = new Set(e.inserted);
      for (const r of rows) {
        if (inserted.has(r)) inserted.delete(r);
        else deleted.add(r);
      }
      return { ...e, deleted, inserted };
    });
  };

  const apply = async () => {
    if (!result?.resultId) return;
    const cols = columns.map((c) => c.name);
    const pkCols = arr(detail?.columns).filter((c) => c.primaryKey).map((c) => c.name);
    const original = async (r: number) => arr((await Query.FetchRows(result.resultId!, r, 1)).rows)[0] as Cell[];
    const keyOf = (row: Cell[]) => {
      const key: Record<string, Cell> = {};
      const use = pkCols.length ? pkCols : cols;
      for (const name of use) key[name] = row[cols.indexOf(name)];
      return key;
    };

    const changes: RowChange[] = [];
    const changeRows: number[] = [];
    for (const r of [...edits.deleted].sort((a, b) => a - b)) {
      changes.push({ kind: ChangeKind.ChangeDelete, key: keyOf(await original(r)), values: {} });
      changeRows.push(r);
    }
    for (const [r, m] of edits.updated) {
      if (edits.deleted.has(r) || edits.inserted.has(r)) continue;
      const values: Record<string, Cell> = {};
      for (const [c, v] of m) values[cols[c]] = v;
      changes.push({ kind: ChangeKind.ChangeUpdate, key: keyOf(await original(r)), values });
      changeRows.push(r);
    }
    for (const r of [...edits.inserted].sort((a, b) => a - b)) {
      const values: Record<string, Cell> = {};
      for (const [c, v] of edits.updated.get(r) ?? []) values[cols[c]] = v;
      changes.push({ kind: ChangeKind.ChangeInsert, key: {}, values });
      changeRows.push(r);
    }
    if (!changes.length) return;

    const res = await Query.ApplyChanges(connId, ref, changes);
    const results = arr(res.results);
    if (res.error) {
      const bad = results.findIndex((r) => r.error);
      const detailMsg = bad >= 0 ? `Row ${changeRows[bad] + 1}: ${results[bad].error}\n\n${results[bad].sql}` : res.error;
      setError(detailMsg);
      return;
    }
    const n = results.reduce((a, r) => a + r.rowsAffected, 0);
    setMessage(`${changes.length} change${changes.length === 1 ? "" : "s"} applied (${n} row${n === 1 ? "" : "s"} affected)`);
    await load();
    Query.CountRows(connId, ref, where).then(setTotal);
  };

  const pages = total !== null ? Math.max(1, Math.ceil(total / pageSize)) : null;
  const goPage = async (p: number) => {
    if (p === page || !(await guardDirty())) return;
    setPage(Math.max(0, pages ? Math.min(pages - 1, p) : p));
  };

  return (
    <div className="viewer">
      <div className="toolbar">
        <button title="Filter" className={showFilter || where ? "on" : ""} onClick={() => setShowFilter((x) => !x)}>
          <Filter size={14} /> Filter
        </button>
        <span className="tb-sep" />
        {editable && (
          <button title="Design Table" onClick={() => designTable(connId, ref)}>
            <PencilRuler size={14} /> Design
          </button>
        )}
        <button title="Import Wizard" disabled={!editable} onClick={() => openImport({ connId, database: ref.database, schema: ref.schema, table: ref })}>
          <Download size={14} /> Import
        </button>
        <button title="Export Wizard (uses the current filter)" onClick={() => openExport({ connId, database: ref.database, schema: ref.schema, tables: [ref] })}>
          <Upload size={14} /> Export
        </button>
        <span className="spacer" />
        {!editable && <span className="badge">read-only</span>}
        {editable && detail && !arr(detail.columns).some((c) => c.primaryKey) && <span className="badge warn" title="Edits match on all column values">no primary key</span>}
      </div>

      {showFilter && (
        <div className="filter-bar">
          <span className="kw">WHERE</span>
          <input
            autoFocus
            placeholder={`e.g. ${columns[0]?.name ?? "id"} > 10`}
            value={whereDraft}
            onChange={(e) => setWhereDraft(e.target.value)}
            onKeyDown={async (e) => {
              if (e.key === "Enter" && (await guardDirty())) {
                setPage(0);
                setWhere(whereDraft);
              }
            }}
          />
          <button onClick={async () => (await guardDirty()) && (setPage(0), setWhere(whereDraft))}>Apply Filter</button>
          {where && (
            <button
              onClick={() => {
                setWhereDraft("");
                setWhere("");
              }}
            >
              Clear
            </button>
          )}
        </div>
      )}

      <div
        className="viewer-body"
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === "s") {
            e.preventDefault();
            apply();
          }
          if ((e.metaKey || e.ctrlKey) && e.key === "r") {
            e.preventDefault();
            guardDirty().then((ok) => void (ok && load()));
          }
        }}
      >
        {error && (
          <div className="error-banner" onClick={() => setError("")}>
            {error}
          </div>
        )}
        {result && (
          <DataGrid
            columns={columns}
            rowCount={rowCount}
            dataKey={result.resultId ?? "none"}
            fetchPage={fetchPage}
            editable={editable}
            edits={edits}
            onEditCell={onEditCell}
            onInsertRow={onInsertRow}
            onDeleteRows={onDeleteRows}
            sort={sort}
            rowNumberOffset={page * pageSize}
            onSort={async (column, desc) => {
              if (!(await guardDirty())) return;
              setSort(desc === null ? [] : [{ column, desc }]);
            }}
            onActiveCell={(r, c) => setActive({ r, c })}
          />
        )}
      </div>

      <div className="statusbar">
        <div className="tb-group">
          <button title="Add record (↓ on the last row)" disabled={!editable} onClick={onInsertRow}>
            <Plus size={14} />
          </button>
          <button title="Delete record (⌘⌫)" disabled={!editable || !active} onClick={() => active && onDeleteRows([active.r])}>
            <Minus size={14} />
          </button>
          <button title="Apply changes (⌘S)" className={dirty ? "accent" : ""} disabled={!dirty} onClick={apply}>
            <Check size={14} />
          </button>
          <button title="Discard changes" disabled={!dirty} onClick={() => setEdits(emptyEdits())}>
            <X size={14} />
          </button>
          <button title="Refresh (⌘R)" onClick={async () => (await guardDirty()) && load()}>
            <RefreshCw size={14} className={loading ? "spin" : ""} />
          </button>
        </div>
        <span className="sql-preview" title={result?.sql}>
          {message || result?.sql}
        </span>
        {active && columns[active.c] && (
          <span className="muted">
            {columns[active.c].name} · {columns[active.c].type?.toLowerCase()}
          </span>
        )}
        <span>
          {total === null ? "…" : total.toLocaleString()} records
          {dirty && <span className="dirty-dot"> · unsaved changes</span>}
        </span>
        <select value={pageSize} onChange={async (e) => (await guardDirty()) && (setPage(0), setPageSize(Number(e.target.value)))}>
          {PAGE_SIZES.map((n) => (
            <option key={n} value={n}>
              {n.toLocaleString()} / page
            </option>
          ))}
        </select>
        <div className="pager">
          <button disabled={page === 0} onClick={() => goPage(0)}>
            <ChevronsLeft size={13} />
          </button>
          <button disabled={page === 0} onClick={() => goPage(page - 1)}>
            <ChevronLeft size={13} />
          </button>
          <span>
            {page + 1}
            {pages ? ` / ${pages}` : ""}
          </span>
          <button disabled={pages !== null && page >= pages - 1} onClick={() => goPage(page + 1)}>
            <ChevronRight size={13} />
          </button>
          <button disabled={!pages || page >= pages - 1} onClick={() => pages && goPage(pages - 1)}>
            <ChevronsRight size={13} />
          </button>
        </div>
      </div>
    </div>
  );
}
