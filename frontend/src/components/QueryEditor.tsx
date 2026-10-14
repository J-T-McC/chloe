import { CircleStop, Play, PlayCircle } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Group, Panel, Separator } from "react-resizable-panels";
import { DBType, Events, Explorer, Query, StatementResult, arr, errorText } from "../api";
import { autoTrigger, clearEditorContext, invalidateCatalog, preloadCatalog, registerSqlCompletion, setEditorContext } from "../editor/completion";
import { Monaco, editorTheme, monaco } from "../editor/monaco";
import { Tab, connById, useApp } from "../store";
import { Cell, DataGrid } from "./DataGrid";

interface RunState {
  results: StatementResult[];
  running: boolean;
  startedAt?: number;
  error?: string;
}

export function QueryEditor({ tab }: { tab: Tab }) {
  const connId = tab.connId!;
  const conn = connById(connId);
  const isPG = conn?.type === DBType.PostgreSQL;
  const host = useRef<HTMLDivElement>(null);
  const editorRef = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null);
  const decorations = useRef<Monaco.editor.IEditorDecorationsCollection | null>(null);
  const [session, setSession] = useState<string | null>(null);
  const [sessionError, setSessionError] = useState("");
  const [dbs, setDbs] = useState<string[]>([]);
  const [schemas, setSchemas] = useState<string[]>([]);
  const [database, setDatabase] = useState(tab.database ?? "");
  const [schema, setSchema] = useState(tab.schema ?? (isPG ? "public" : ""));
  const [run, setRun] = useState<RunState>({ results: [], running: false });
  const [resultTab, setResultTab] = useState(0);
  const [elapsed, setElapsed] = useState(0);
  const runRef = useRef<() => void>(() => {});
  const runCurrentRef = useRef<() => void>(() => {});

  // ---- editor --------------------------------------------------------------------
  useEffect(() => {
    registerSqlCompletion();
    const model = monaco.editor.createModel(tab.sql ?? "", isPG ? "pgsql" : "mysql");
    const editor = monaco.editor.create(host.current!, {
      model,
      theme: editorTheme(),
      automaticLayout: true,
      minimap: { enabled: false },
      fontSize: 13,
      fontFamily: "SF Mono, Menlo, Consolas, monospace",
      lineNumbersMinChars: 3,
      scrollBeyondLastLine: false,
      renderLineHighlight: "line",
      tabSize: 2,
      wordBasedSuggestions: "off",
      quickSuggestions: { other: true, comments: false, strings: false },
      suggest: { showWords: false, snippetsPreventQuickSuggestions: false, preview: true, showStatusBar: true },
      suggestSelection: "first",
      fixedOverflowWidgets: true,
    });
    editorRef.current = editor;
    decorations.current = editor.createDecorationsCollection();
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, () => runCurrentRef.current());
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyR, () => runRef.current());
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.Enter, () => runRef.current());
    const trig = autoTrigger(editor);
    const save = editor.onDidChangeModelContent(() => {
      decorations.current?.clear();
    });
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const onTheme = () => monaco.editor.setTheme(editorTheme());
    media.addEventListener("change", onTheme);
    editor.focus();
    return () => {
      // Keep the text when the tab re-mounts.
      useApp.getState().updateTab(tab.id, { sql: model.getValue() });
      media.removeEventListener("change", onTheme);
      trig.dispose();
      save.dispose();
      clearEditorContext(model);
      editor.dispose();
      model.dispose();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ---- session ---------------------------------------------------------------------
  useEffect(() => {
    let sid: string | null = null;
    let cancelled = false;
    Query.OpenSession(connId, database, schema)
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
  }, [connId]);

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
    if (!session) return;
    try {
      await Query.UseDatabase(session, db, sc);
      setDatabase(db);
      setSchema(sc);
      useApp.getState().updateTab(tab.id, { database: db, schema: sc, title: `Query — ${db}` });
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
    [session, run.running, connId, database],
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

  const dataResults = run.results.filter((r) => r.resultId);

  return (
    <div className="query-editor">
      <div className="toolbar">
        <button className="accent" onClick={() => runRef.current()} disabled={!session || run.running} title="Run (⌘R)">
          <Play size={14} /> Run
        </button>
        <button onClick={() => runCurrentRef.current()} disabled={!session || run.running} title="Run current statement (⌘↩)">
          <PlayCircle size={14} /> Run Current
        </button>
        <button onClick={stop} disabled={!run.running} title="Stop">
          <CircleStop size={14} /> Stop
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
      </div>
      <Group orientation="vertical" className="qe-split">
        <Panel defaultSize="55" minSize="15">
          <div className="monaco-host" ref={host} />
        </Panel>
        <Separator className="split-handle horizontal" />
        <Panel minSize="10">
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
                <ResultGrid result={run.results[resultTab - 1]} />
              )}
            </div>
          </div>
        </Panel>
      </Group>
    </div>
  );
}

function Messages({ run }: { run: RunState }) {
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

function ResultGrid({ result }: { result: StatementResult }) {
  const fetchPage = useCallback(
    async (offset: number, limit: number) => arr((await Query.FetchRows(result.resultId!, offset, limit)).rows) as Cell[][],
    [result.resultId],
  );
  return (
    <div className="result-grid">
      <DataGrid columns={arr(result.columns)} rowCount={result.rowCount} dataKey={result.resultId!} fetchPage={fetchPage} />
      <div className="statusbar">
        <span className="sql-preview">{result.sql}</span>
        <span className="spacer" />
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
