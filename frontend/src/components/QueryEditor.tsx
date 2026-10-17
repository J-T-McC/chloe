import { Check, CircleStop, History, Minus, PanelBottom, Play, PlayCircle, Plus, Save, Sparkles, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { DBType, Events, Explorer, Query, ResultEdit, StatementResult, arr, errorText } from "../api";
import { invalidateCatalog, preloadCatalog, setEditorContext } from "../editor/completion";
import { beautifyEditor } from "../editor/format";
import { Monaco, monaco } from "../editor/monaco";
import { TabOf, connById, useApp } from "../store";
import { Cell, DataGrid, GridEdits, MAX_CELL_CHARS, emptyEdits } from "./DataGrid";
import { confirm } from "./Dialogs";
import { buildRowChanges } from "../gridChanges";
import { Split } from "./Split";
import { useStable } from "./useStable";
import { ValuePanel } from "./ValuePanel";
import { readFlag, writeFlag } from "../prefs";
import { connDialect } from "../dialect";
import { openExport } from "./transfer/state";
import { createChartFrom } from "./bi/actions";
import { queryTextChanged, registerQueryEditor, saveQueryTab } from "../queries";
import { openHistoryLog } from "./HistoryLog";
import { Toolbar } from "./Toolbar";
import { createSqlEditor } from "../editor/sqlEditor";

/** Shown for a result nobody has edited; edits replace it rather than change it. */
const NO_EDITS = emptyEdits();

export interface RunState {
  results: StatementResult[];
  running: boolean;
  startedAt?: number;
  error?: string;
}

export function QueryEditor({ tab }: { tab: TabOf<"query"> }) {
  const connId = tab.connId;
  const conn = connById(connId);
  const isPG = conn?.type === DBType.PostgreSQL;
  const dialect = connDialect(connId);
  const host = useRef<HTMLDivElement>(null);
  const editorRef = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null);
  const decorations = useRef<Monaco.editor.IEditorDecorationsCollection | null>(null);
  const [session, setSession] = useState<string | null>(null);
  const [sessionError, setSessionError] = useState("");
  const [dbs, setDbs] = useState<string[]>([]);
  const [schemas, setSchemas] = useState<string[]>([]);
  const [database, setDatabase] = useState(tab.database ?? (dialect === "sqlite" ? "main" : ""));
  const [schema, setSchema] = useState(tab.schema ?? (isPG ? "public" : ""));
  const [run, setRun] = useState<RunState>({ results: [], running: false });
  const [resultTab, setResultTab] = useState(0);
  // Pending edits per result, kept here so switching result tabs doesn't lose them.
  const [edits, setEdits] = useState<Record<string, GridEdits>>({});
  const hasEdits = Object.values(edits).some(isDirty);
  const guardEdits = async () => !hasEdits || (await confirm("Discard unsaved changes to the results?", { danger: true, okLabel: "Discard" }));
  const [elapsed, setElapsed] = useState(0);
  const runRef = useRef<() => void>(() => {});
  const runCurrentRef = useRef<() => void>(() => {});
  const beautifyRef = useRef<() => void>(() => {});

  // ---- editor --------------------------------------------------------------------
  useEffect(() => {
    const { editor, model, dispose } = createSqlEditor(host.current!, tab.sql ?? "", dialect);
    editorRef.current = editor;
    decorations.current = editor.createDecorationsCollection();
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, () => runCurrentRef.current());
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyR, () => runRef.current());
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.Enter, () => runRef.current());
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.KeyF, () => beautifyRef.current());
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => saveQueryTab(tab.id));
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.KeyS, () => saveQueryTab(tab.id, true));
    const unregister = registerQueryEditor(tab.id, () => model.getValue(), () => model.getValueLength());
    const save = editor.onDidChangeModelContent(() => {
      decorations.current?.clear();
      queryTextChanged(tab.id);
    });
    editor.focus();
    return () => {
      // Keep the text when the tab re-mounts.
      useApp.getState().updateTab(tab.id, { sql: model.getValue() });
      unregister();
      save.dispose();
      dispose();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ---- session ---------------------------------------------------------------------
  // A query tab outlives its connection being closed. It connects when first shown
  // (tabs restored at startup) and otherwise offers to reconnect.
  const connected = useApp((s) => !!s.open[connId]);
  const triedConnect = useRef(false);
  const where = useRef({ database, schema });
  where.current = { database, schema };
  const reconnect = () => {
    const db = where.current.database;
    return db ? useApp.getState().openDatabase(connId, db) : useApp.getState().connect(connId);
  };
  useEffect(() => {
    if (!connected) {
      setSession(null);
      if (!triedConnect.current) {
        triedConnect.current = true;
        reconnect();
      }
      return;
    }
    triedConnect.current = true;
    setSessionError("");
    let sid: string | null = null;
    let cancelled = false;
    Query.OpenSession(connId, where.current.database, where.current.schema)
      .then((id) => {
        if (cancelled) Query.CloseSession(id);
        else {
          sid = id;
          setSession(id);
        }
      })
      .catch((e) => setSessionError(errorText(e)));
    Explorer.ListDatabases(connId).then((d) => setDbs(arr(d))).catch(() => {});
    return () => {
      cancelled = true;
      if (sid) Query.CloseSession(sid);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connId, connected]);

  useEffect(() => {
    if (isPG && database) Explorer.ListSchemas(connId, database).then((s) => setSchemas(arr(s))).catch(() => {});
  }, [connId, database, isPG]);

  // Completion context follows the selected database/schema.
  useEffect(() => {
    const model = editorRef.current?.getModel();
    if (!model) return;
    const ctx = { connId, database: database || undefined, schema: schema || undefined };
    setEditorContext(model, ctx);
    preloadCatalog(ctx);
  }, [connId, database, schema]);

  const changeDb = async (db: string, sc: string) => {
    if (!session || !(await guardEdits())) return;
    try {
      await Query.UseDatabase(session, db, sc);
      setDatabase(db);
      setSchema(sc);
      const saved = useApp.getState().tabs.find((t) => t.id === tab.id)?.queryId;
      useApp.getState().updateTab(tab.id, { database: db, schema: sc, ...(saved ? {} : { title: `Query — ${db}` }) });
    } catch (e) {
      setRun((r) => ({ ...r, error: errorText(e) }));
    }
  };

  // ---- results streaming ----------------------------------------------------------
  useEffect(() => {
    return Events.On("result:progress", (ev) => {
      const p = ev.data;
      setRun((r) => {
        if (!r.results.some((x) => x.resultId === p.resultId)) return r;
        return { ...r, results: r.results.map((x) => (x.resultId === p.resultId ? { ...x, rowCount: p.rows, complete: p.complete } : x)) };
      });
    });
  }, []);

  useEffect(() => {
    if (!run.running) return;
    const t = setInterval(() => setElapsed(Date.now() - (run.startedAt ?? Date.now())), 100);
    return () => clearInterval(t);
  }, [run.running, run.startedAt]);

  const execute = useCallback(
    async (sql: string, lineOffset: number) => {
      if (!session || run.running || !sql.trim()) return;
      if (!(await guardEdits())) return;
      setEdits({});
      decorations.current?.clear();
      setRun({ results: [], running: true, startedAt: Date.now() });
      try {
        const results = arr(await Query.Execute(session, sql, { continueOnError: false }));
        setRun({ results, running: false });
        const firstData = results.findIndex((r) => r.resultId);
        setResultTab(firstData >= 0 ? firstData + 1 : 0);
        const bad = results.find((r) => r.error);
        if (bad) {
          setResultTab(0);
          const line = (bad.errorLine || 1) + lineOffset;
          decorations.current?.set([{ range: new monaco.Range(line, 1, line, 1), options: { isWholeLine: true, className: "error-line", glyphMarginClassName: "error-glyph" } }]);
        }
        // DDL may have changed the schema: refresh autocomplete metadata.
        if (results.some((r) => /^\s*(create|alter|drop|rename|truncate)\b/i.test(r.sql))) invalidateCatalog(connId, database);
      } catch (e) {
        setRun({ results: [], running: false, error: errorText(e) });
      }
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [session, run.running, connId, database, hasEdits],
  );

  runRef.current = () => {
    const ed = editorRef.current!;
    const model = ed.getModel()!;
    const sel = ed.getSelection();
    if (sel && !sel.isEmpty()) execute(model.getValueInRange(sel), sel.startLineNumber - 1);
    else execute(model.getValue(), 0);
  };

  // Run the statement under the cursor (or the selection).
  runCurrentRef.current = () => {
    const ed = editorRef.current!;
    const model = ed.getModel()!;
    const sel = ed.getSelection();
    if (sel && !sel.isEmpty()) return execute(model.getValueInRange(sel), sel.startLineNumber - 1);
    const text = model.getValue();
    const offset = model.getOffsetAt(ed.getPosition()!);
    const { start, end } = statementRange(text, offset);
    const startLine = model.getPositionAt(start).lineNumber;
    ed.setSelection(monaco.Selection.fromPositions(model.getPositionAt(start), model.getPositionAt(end)));
    execute(text.slice(start, end), startLine - 1);
  };

  const stop = () => session && Query.Cancel(session);

  beautifyRef.current = () => {
    const err = beautifyEditor(editorRef.current!);
    if (err) setRun((r) => ({ ...r, error: `Beautify failed: ${err}` }));
  };

  const dataResults = run.results.filter((r) => r.resultId);

  return (
    <div className="query-editor">
      <Toolbar>
        <button className="accent" onClick={() => runRef.current()} disabled={!session || run.running} title="Run (⌘R)">
          <Play size={14} /> Run
        </button>
        <button onClick={() => runCurrentRef.current()} disabled={!session || run.running} title="Run current statement (⌘↩)">
          <PlayCircle size={14} /> Run Current
        </button>
        <button onClick={stop} disabled={!run.running} title="Stop">
          <CircleStop size={14} /> Stop
        </button>
        <button onClick={() => beautifyRef.current()} title="Beautify SQL (⇧⌘F)">
          <Sparkles size={14} /> Beautify
        </button>
        <button onClick={() => saveQueryTab(tab.id)} title="Save (⌘S); Save As is ⇧⌘S">
          <Save size={14} /> Save
        </button>
        <button onClick={() => openHistoryLog(connId)} title="History Log">
          <History size={14} /> History
        </button>
        <span className="tb-sep" />
        <span className="muted">{conn?.name}</span>
        <select value={database} onChange={(e) => changeDb(e.target.value, isPG ? "public" : "")} disabled={!session}>
          {!database && <option value="">(no database)</option>}
          {dbs.map((d) => (
            <option key={d}>{d}</option>
          ))}
        </select>
        {isPG && (
          <select value={schema} onChange={(e) => changeDb(database, e.target.value)} disabled={!session}>
            {schemas.map((s) => (
              <option key={s}>{s}</option>
            ))}
          </select>
        )}
        <span className="spacer" />
        {sessionError && <span className="err">{sessionError}</span>}
      </Toolbar>
      {!connected && (
        <div className="qe-offline">
          Not connected to {conn?.name ?? "the server"}.
          <button onClick={reconnect}>Connect</button>
        </div>
      )}
      <Split
        id="query-editor"
        vertical
        className="qe-split"
        firstSize="55"
        minFirst="15"
        minSecond="10"
        first={<div className="monaco-host" ref={host} />}
        second={
          <div className="results">
            <div className="result-tabs">
              <button className={resultTab === 0 ? "active" : ""} onClick={() => setResultTab(0)}>
                Message
              </button>
              {run.results.map((r, i) =>
                r.resultId ? (
                  <button key={r.resultId} className={resultTab === i + 1 ? "active" : ""} onClick={() => setResultTab(i + 1)}>
                    Result {dataResults.indexOf(r) + 1}
                  </button>
                ) : null,
              )}
              <span className="spacer" />
              <span className="muted">{run.running ? `Running… ${(elapsed / 1000).toFixed(1)}s` : ""}</span>
            </div>
            <div className="result-body">
              {resultTab === 0 || !run.results[resultTab - 1]?.resultId ? (
                <Messages run={run} />
              ) : (
                <ResultGrid
                  result={run.results[resultTab - 1]}
                  connId={connId}
                  database={database}
                  schema={schema}
                  edits={edits[run.results[resultTab - 1].resultId!] ?? NO_EDITS}
                  onEdits={(id, fn) => setEdits((m) => ({ ...m, [id]: fn(m[id] ?? emptyEdits()) }))}
                  onPatched={(st) =>
                    setRun((r) => ({ ...r, results: r.results.map((x) => (x.resultId === st.resultId ? { ...x, rowCount: st.rowCount, complete: st.complete } : x)) }))
                  }
                />
              )}
            </div>
          </div>
        }
      />
    </div>
  );
}

export function Messages({ run }: { run: RunState }) {
  if (run.error) return <div className="messages"><div className="msg err">{run.error}</div></div>;
  if (!run.results.length) return <div className="messages muted">{run.running ? "Executing…" : "Run a query to see results."}</div>;
  return (
    <div className="messages">
      {run.results.map((r, i) => (
        <div key={i} className={"msg" + (r.error ? " err" : "")}>
          <pre>{r.sql.length > 400 ? r.sql.slice(0, 400) + "…" : r.sql}</pre>
          <div>
            {r.error
              ? `Error${r.errorLine ? ` (line ${r.errorLine})` : ""}: ${r.error}`
              : r.resultId
                ? `OK, ${r.rowCount.toLocaleString()} row${r.rowCount === 1 ? "" : "s"}${r.complete ? "" : "+"} returned${r.truncated ? " (truncated)" : ""}`
                : `OK, ${r.rowsAffected.toLocaleString()} row${r.rowsAffected === 1 ? "" : "s"} affected`}
            <span className="muted"> · {r.durationMs} ms</span>
          </div>
        </div>
      ))}
    </div>
  );
}

const isDirty = (e: GridEdits) => e.updated.size > 0 || e.inserted.size > 0 || e.deleted.size > 0;

export function ResultGrid({
  result,
  connId,
  database,
  schema,
  edits,
  onEdits,
  onPatched,
}: {
  result: StatementResult;
  connId: string;
  database?: string;
  schema?: string;
  /** With edits and onEdits the grid can be edited when the query allows it. */
  edits?: GridEdits;
  onEdits?(resultId: string, fn: (e: GridEdits) => GridEdits): void;
  onPatched?(status: StatementResult): void;
}) {
  const id = result.resultId!;
  const fetchPage = useCallback(async (offset: number, limit: number) => {
    const p = await Query.FetchRows(id, offset, limit, MAX_CELL_CHARS);
    return { rows: arr(p.rows) as Cell[][], cut: p.cut };
  }, [id]);
  const fetchFull = useCallback(async (offset: number, limit: number) => arr((await Query.FetchRows(id, offset, limit, 0)).rows) as Cell[][], [id]);
  const [panel, setPanel] = useState(() => readFlag("valuePanel"));
  const [active, setActive] = useState<{ r: number; c: number; value?: Cell } | null>(null);
  const onActiveCell = useCallback((r: number, c: number, value?: Cell) => setActive({ r, c, value }), []);
  const [fetched, setFetched] = useState<{ key: string; value: Cell } | null>(null);
  const [target, setTarget] = useState<ResultEdit | null>(null);
  const [revision, setRevision] = useState(0);
  const [error, setError] = useState("");
  const [message, setMessage] = useState("");
  const activeKey = active ? `${id}:${active.r}:${active.c}` : "";
  useEffect(() => {
    if (!panel || !active || active.value !== undefined) return;
    Query.FetchRows(id, active.r, 1, 0)
      .then((p) => setFetched({ key: activeKey, value: (arr(p.rows)[0] as Cell[] | undefined)?.[active.c] ?? null }))
      .catch(() => {});
  }, [panel, active, activeKey, id]);

  // Whether (and how) the result maps onto a table; asked once all rows are in.
  useEffect(() => {
    setTarget(null);
    setError("");
    setMessage("");
    if (!onEdits || !result.complete) return;
    let live = true;
    Query.ResultEditTarget(id)
      .then((t) => live && setTarget(t))
      .catch(() => {});
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, result.complete]);

  const toggle = (on: boolean) => (setPanel(on), writeFlag("valuePanel", on));
  const columns = arr(result.columns);
  const editable = !!target?.editable && !!edits && !!onEdits;
  const mapped = arr(target?.columns);
  const readOnlyColumns = useMemo(() => (editable ? columns.map((_, c) => !mapped[c]) : undefined), [editable, columns, mapped]);
  const e = edits ?? NO_EDITS;
  const dirty = isDirty(e);
  const update = (fn: (e: GridEdits) => GridEdits) => onEdits?.(id, fn);

  const onEditCell = useStable((row: number, col: number, value: Cell) =>
    update((x) => {
      const updated = new Map(x.updated);
      const m = new Map(updated.get(row) ?? []);
      m.set(col, value);
      updated.set(row, m);
      return { ...x, updated };
    }),
  );
  const onInsertRow = useStable(() =>
    update((x) => {
      const inserted = new Set(x.inserted);
      inserted.add(result.rowCount + inserted.size);
      return { ...x, inserted };
    }),
  );
  const onDeleteRows = useStable((rows: number[]) =>
    update((x) => {
      const deleted = new Set(x.deleted);
      const inserted = new Set(x.inserted);
      for (const r of rows) {
        if (inserted.has(r)) inserted.delete(r);
        else deleted.add(r);
      }
      return { ...x, deleted, inserted };
    }),
  );

  const apply = async () => {
    if (!editable || !target || !dirty) return;
    setError("");
    const original = async (r: number) => arr((await Query.FetchRows(id, r, 1, 0)).rows)[0] as Cell[];
    const { changes, rows } = await buildRowChanges(e, (c) => mapped[c] || undefined, arr(target.key), original, columns.length);
    if (!changes.length) return update(() => emptyEdits());
    const res = await Query.ApplyChanges(connId, target.ref, changes);
    const results = arr(res.results);
    if (res.error) {
      const bad = results.findIndex((r) => r.error);
      setError(bad >= 0 ? `Row ${rows[bad] + 1}: ${results[bad].error}\n\n${results[bad].sql}` : res.error);
      return;
    }
    // Show the applied values without re-running the query.
    const updated: Record<number, Record<number, Cell>> = {};
    for (const [r, m] of e.updated) {
      if (e.deleted.has(r) || e.inserted.has(r)) continue;
      updated[r] = Object.fromEntries([...m].filter(([c]) => mapped[c]));
    }
    const inserted = [...e.inserted].sort((a, b) => a - b).map((r) => columns.map((_, c) => (mapped[c] ? (e.updated.get(r)?.get(c) ?? null) : null)));
    try {
      const st = await Query.PatchResult(id, { updated, deleted: [...e.deleted], inserted } as never);
      onPatched?.(st);
    } catch {
      /* the rows were written; the grid just shows the old values until re-run */
    }
    update(() => emptyEdits());
    setRevision((n) => n + 1);
    const n = results.reduce((a, r) => a + r.rowsAffected, 0);
    setMessage(`${changes.length} change${changes.length === 1 ? "" : "s"} applied to ${target.ref.name} (${n} row${n === 1 ? "" : "s"} affected)`);
  };

  const activeValue = !active
    ? undefined
    : e.updated.get(active.r)?.has(active.c)
      ? e.updated.get(active.r)!.get(active.c)!
      : active.value !== undefined
        ? active.value
        : fetched?.key === activeKey
          ? fetched.value
          : undefined;

  return (
    <div
      className="result-grid"
      onKeyDown={(ev) => {
        if (editable && (ev.metaKey || ev.ctrlKey) && ev.key === "s") {
          ev.preventDefault();
          ev.stopPropagation();
          apply();
        }
      }}
    >
      {error && (
        <div className="error-banner" onClick={() => setError("")}>
          {error}
        </div>
      )}
      <Split
        id="result-value-panel"
        vertical
        className="value-split"
        firstSize="65"
        minFirst="20"
        minSecond="12"
        collapsed={!panel}
        first={
          <DataGrid
            columns={columns}
            rowCount={result.rowCount}
            dataKey={id}
            revision={revision}
            fetchPage={fetchPage}
            fetchFull={fetchFull}
            editable={editable}
            readOnlyColumns={readOnlyColumns}
            edits={editable ? e : undefined}
            onEditCell={onEditCell}
            onInsertRow={onInsertRow}
            onDeleteRows={onDeleteRows}
            onActiveCell={onActiveCell}
          />
        }
        second={
          <ValuePanel
            cellId={activeKey}
            column={active ? columns[active.c] : undefined}
            value={activeValue}
            readOnly={!editable || !active || !mapped[active.c] || e.deleted.has(active.r)}
            onChange={(v) => active && onEditCell(active.r, active.c, v)}
            onClose={() => toggle(false)}
          />
        }
      />
      <div className="statusbar">
        {editable && (
          <div className="tb-group">
            <button title="Add record" onClick={onInsertRow}>
              <Plus size={14} />
            </button>
            <button title="Delete record (⌘⌫)" disabled={!active} onClick={() => active && onDeleteRows([active.r])}>
              <Minus size={14} />
            </button>
            <button title="Apply changes (⌘S)" className={dirty ? "accent" : ""} disabled={!dirty} onClick={apply}>
              <Check size={14} />
            </button>
            <button title="Discard changes" disabled={!dirty} onClick={() => update(() => emptyEdits())}>
              <X size={14} />
            </button>
          </div>
        )}
        <span className="sql-preview" title={result.sql}>
          {message || result.sql}
        </span>
        <span className="spacer" />
        {onEdits && target && !target.editable && (
          <span className="badge" title={`Read-only: ${target.reason}`}>
            read-only
          </span>
        )}
        {dirty && <span className="dirty-dot">unsaved changes</span>}
        <button className={"small-btn" + (panel ? " on" : "")} title="Show the active cell's full value" onClick={() => toggle(!panel)}>
          <PanelBottom size={12} /> Value
        </button>
        <button className="small-btn" title="Export this result (re-runs the query)" onClick={() => openExport({ connId, database: database ?? "", schema, tables: [], sql: result.sql, name: "query_result" })}>
          Export…
        </button>
        <button className="small-btn" title="Create a chart on this query in a BI workspace" onClick={() => createChartFrom({ connId, database: database ?? "", schema, sql: result.sql, name: "Query result" })}>
          Create Chart…
        </button>
        <span>
          {result.rowCount.toLocaleString()}
          {result.complete ? "" : "+"} rows{result.truncated ? " (truncated)" : ""} · {result.durationMs} ms
        </span>
      </div>
    </div>
  );
}

/** Bounds of the statement under the cursor (top-level semicolons, quotes and comments respected). */
export function statementRange(text: string, offset: number): { start: number; end: number } {
  let start = 0;
  let i = 0;
  const n = text.length;
  const bounds: Array<[number, number]> = [];
  while (i < n) {
    const c = text[i];
    if (c === "'" || c === '"' || c === "`") {
      const q = c;
      i++;
      while (i < n && text[i] !== q) i += text[i] === "\\" ? 2 : 1;
      i++;
      continue;
    }
    if (c === "-" && text[i + 1] === "-") {
      while (i < n && text[i] !== "\n") i++;
      continue;
    }
    if (c === "/" && text[i + 1] === "*") {
      const e = text.indexOf("*/", i + 2);
      i = e < 0 ? n : e + 2;
      continue;
    }
    if (c === "$") {
      const m = /^\$[A-Za-z_]*\$/.exec(text.slice(i));
      if (m) {
        const e = text.indexOf(m[0], i + m[0].length);
        i = e < 0 ? n : e + m[0].length;
        continue;
      }
    }
    if (c === ";") {
      bounds.push([start, i]);
      start = i + 1;
    }
    i++;
  }
  bounds.push([start, n]);
  // Statement containing the cursor; if the cursor is right after a ";", the one before it.
  let pick = bounds.find(([s, e]) => offset >= s && offset <= e) ?? bounds[bounds.length - 1];
  if (!text.slice(pick[0], pick[1]).trim()) {
    const idx = bounds.indexOf(pick);
    if (idx > 0) pick = bounds[idx - 1];
  }
  // Trim surrounding whitespace.
  let [s, e] = pick;
  while (s < e && /\s/.test(text[s])) s++;
  while (e > s && /\s/.test(text[e - 1])) e--;
  return { start: s, end: e };
}
