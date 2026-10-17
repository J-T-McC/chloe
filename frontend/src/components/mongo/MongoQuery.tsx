// A MongoDB query tab: a shell script editor and its results as documents, values or text.
import { ChevronLeft, ChevronRight, Play, PlayCircle, Square } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Mongo, arr, errorText, type MG } from "../../api";
import { editorTheme, monaco, type Monaco } from "../../editor/monaco";
import { clearMongoContext, invalidateMongoCatalog, mongoEditorOptions, registerMongoLanguage, setMongoContext } from "../../editor/mongo";
import { Val, field, formatVal } from "../../mongo/docs";
import { useApp, type TabOf } from "../../store";
import { alert } from "../Dialogs";
import { Split } from "../Split";
import { DocsView, type DocsMode } from "./DocsView";
import { ModeSwitch, readMode, writeMode } from "./CollectionViewer";
import { editDocument } from "./DocEditor";
import { Toolbar } from "../Toolbar";

const PAGE = 100;

interface Shown extends MG.Result {
  /** documents of the current page (edits update them in place) */
  docs: Val[];
  skip: number;
  revision: number;
}

export function MongoQuery({ tab }: { tab: TabOf<"mongoquery"> }) {
  const connId = tab.connId;
  const host = useRef<HTMLDivElement>(null);
  const editorRef = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null);
  const [shell, setShell] = useState<string | null>(null);
  const [db, setDb] = useState(tab.database ?? "test");
  const dbRef = useRef(db);
  dbRef.current = db;
  const [dbs, setDbs] = useState<string[]>([]);
  const [running, setRunning] = useState(false);
  const [out, setOut] = useState<MG.RunOutput | null>(null);
  const [results, setResults] = useState<Shown[]>([]);
  const [view, setView] = useState(0); // 0 = Message, n = result n
  const [mode, setMode] = useState<DocsMode>(readMode);
  const runRef = useRef<(current: boolean) => void>(() => {});

  // ---- editor -----------------------------------------------------------------------
  useEffect(() => {
    registerMongoLanguage();
    const model = monaco.editor.createModel(tab.sql ?? "", "mongo");
    setMongoContext(model, { connId, database: () => dbRef.current });
    const ed = monaco.editor.create(host.current!, { ...mongoEditorOptions, model, theme: editorTheme() });
    editorRef.current = ed;
    ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyR, () => runRef.current(false));
    ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.Enter, () => runRef.current(false));
    ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.Enter, () => runRef.current(true));
    ed.focus();
    return () => {
      useApp.getState().updateTab(tab.id, { sql: model.getValue() });
      clearMongoContext(model);
      ed.dispose();
      model.dispose();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // ---- shell ------------------------------------------------------------------------
  useEffect(() => {
    let id: string | null = null;
    let cancelled = false;
    Mongo.OpenShell(connId, db)
      .then((s) => {
        if (cancelled) Mongo.CloseShell(s);
        else setShell((id = s));
      })
      .catch((e) => setOut({ results: [], output: "", error: errorText(e), db, elapsedMs: 0 }));
    Mongo.Databases(connId)
      .then((d) => setDbs(arr(d).map((x) => x.name)))
      .catch(() => {});
    return () => {
      cancelled = true;
      if (id) Mongo.CloseShell(id);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connId]);

  const changeDb = async (name: string) => {
    if (!shell) return;
    await Mongo.Run(shell, `use ${name}`, 1).catch(() => {});
    setDb(name);
    useApp.getState().updateTab(tab.id, { database: name, title: `Query — ${name}` });
  };

  /** The statement block around the cursor: the selection, or the lines between blank lines. */
  const currentText = (): string => {
    const ed = editorRef.current!;
    const model = ed.getModel()!;
    const sel = ed.getSelection();
    if (sel && !sel.isEmpty()) return model.getValueInRange(sel);
    const line = ed.getPosition()?.lineNumber ?? 1;
    let a = line;
    let b = line;
    while (a > 1 && model.getLineContent(a - 1).trim() !== "") a--;
    while (b < model.getLineCount() && model.getLineContent(b + 1).trim() !== "") b++;
    return model.getValueInRange(new monaco.Range(a, 1, b, model.getLineMaxColumn(b)));
  };

  const run = async (current: boolean) => {
    if (!shell || running) return;
    const script = current ? currentText() : editorRef.current!.getValue();
    if (!script.trim()) return;
    setRunning(true);
    try {
      const o = await Mongo.Run(shell, script, PAGE);
      setOut(o);
      const rs = arr(o.results).map((r) => ({ ...r, docs: arr(r.page?.docs), skip: 0, revision: 0 }));
      setResults(rs);
      setView(o.error || !rs.length ? 0 : 1);
      if (o.db && o.db !== dbRef.current) {
        setDb(o.db);
        useApp.getState().updateTab(tab.id, { database: o.db, title: `Query — ${o.db}` });
      }
      // Scripts can create or drop collections.
      if (/\b(create|drop|rename|insert|use)\w*/i.test(script)) invalidateMongoCatalog(connId);
    } catch (e) {
      setOut({ results: [], output: "", error: errorText(e), db, elapsedMs: 0 });
      setView(0);
    } finally {
      setRunning(false);
    }
  };
  runRef.current = run;

  const page = async (i: number, skip: number) => {
    const r = results[i];
    if (!shell || !r.cursorId) return;
    try {
      const f = await Mongo.Fetch(shell, r.cursorId, skip, PAGE);
      setResults((rs) => rs.map((x, j) => (j === i ? { ...x, page: f.page, docs: arr(f.page.docs), hasMore: f.hasMore, skip, revision: x.revision + 1 } : x)));
    } catch (e) {
      alert("Couldn't fetch more documents", errorText(e));
    }
  };

  const shown = view > 0 ? results[view - 1] : undefined;
  // Only a find's own documents can be edited; aggregation and map() results are computed.
  const editable = !!shown?.editable && !!shown.collection && !!shown.cursorId;

  return (
    <div className="query-editor mongo-query">
      <Toolbar>
        <button className="accent" disabled={!shell || running} onClick={() => run(false)} title="Run (⌘R)">
          <Play size={14} /> Run
        </button>
        <button disabled={!shell || running} onClick={() => run(true)} title="Run the statement at the cursor or the selection (⌘↩)">
          <PlayCircle size={14} /> Run Current
        </button>
        <button disabled={!running} onClick={() => shell && Mongo.Stop(shell)} title="Stop">
          <Square size={14} /> Stop
        </button>
        <span className="tb-sep" />
        <select value={db} onChange={(e) => changeDb(e.target.value)} title="Database">
          {[...new Set([db, ...dbs])].map((d) => (
            <option key={d} value={d}>
              {d}
            </option>
          ))}
        </select>
      </Toolbar>
      <Split
        id="mongo-query-results"
        vertical
        className="qe-split"
        firstSize="55"
        minFirst="15"
        minSecond="15"
        first={<div className="monaco-host" ref={host} />}
        second={
          <div className="results">
            <div className="result-tabs">
              <button className={view === 0 ? "active" : ""} onClick={() => setView(0)}>
                Message
              </button>
              {results.map((r, i) => (
                <button key={i} className={view === i + 1 ? "active" : ""} onClick={() => setView(i + 1)} title={r.statement}>
                  Result {i + 1}
                </button>
              ))}
              <span className="spacer" />
              {shown?.kind === "documents" && <ModeSwitch mode={mode} onChange={(m) => (setMode(m), writeMode(m))} />}
            </div>
            <div className="result-body">
              {view === 0 && <Message out={out} running={running} />}
              {shown && shown.kind === "documents" && shown.page && (
                <DocsView
                  mode={mode}
                  docs={shown.docs}
                  fields={arr(shown.page.fields)}
                  dataKey={`${view}/${shown.cursorId ?? ""}/${shown.skip}`}
                  revision={shown.revision}
                  rowOffset={shown.skip}
                  onSetField={
                    editable
                      ? async (i, path, value) => {
                          const id = field(shown.docs[i], "_id");
                          if (!id) throw new Error("the document has no _id");
                          await Mongo.EditField(connId, shown.db!, shown.collection!, { id, path, unset: value === null, value: value ?? { t: "null" } });
                          // Re-read the page: the find may project or sort on what changed.
                          await page(view - 1, shown.skip);
                        }
                      : undefined
                  }
                  onEditDoc={
                    editable
                      ? async (i) => {
                          const id = field(shown.docs[i], "_id");
                          // Edit the stored document, not the (possibly projected) result.
                          const full = id ? await Mongo.GetDoc(connId, shown.db!, shown.collection!, id).catch((e) => (alert("Couldn't load the document", errorText(e)), null)) : null;
                          if (full && (await editDocument({ connId, db: shown.db!, coll: shown.collection!, doc: full }))) page(view - 1, shown.skip);
                        }
                      : undefined
                  }
                />
              )}
              {shown && shown.kind === "value" && shown.value && <pre className="code-block result-value">{formatVal(shown.value, "  ")}</pre>}
              {shown && shown.kind === "text" && <pre className="code-block result-value">{shown.text}</pre>}
            </div>
            {shown && (
              <div className="statusbar">
                <span className="sql-preview" title={shown.statement}>
                  {shown.statement}
                </span>
                {shown.kind === "documents" && (
                  <>
                    <span>
                      {shown.cursorId ? `${shown.skip + 1}–${shown.skip + shown.docs.length}` : shown.docs.length} documents{editable ? "" : " · read-only"}
                    </span>
                    {shown.cursorId && (
                      <div className="pager">
                        <button disabled={shown.skip === 0} onClick={() => page(view - 1, Math.max(0, shown.skip - PAGE))}>
                          <ChevronLeft size={13} />
                        </button>
                        <button disabled={!shown.hasMore} onClick={() => page(view - 1, shown.skip + PAGE)}>
                          <ChevronRight size={13} />
                        </button>
                      </div>
                    )}
                  </>
                )}
                <span className="muted">{shown.elapsedMs.toFixed(1)} ms</span>
              </div>
            )}
          </div>
        }
      />
    </div>
  );
}

function Message({ out, running }: { out: MG.RunOutput | null; running: boolean }) {
  if (running) return <div className="messages muted">Executing…</div>;
  if (!out) return <div className="messages muted">Run a script: db.collection.find({"{}"}), aggregate([…]), insertOne({"{}"})…</div>;
  return (
    <div className="messages">
      {arr(out.results).map((r, i) => (
        <div key={i} className="msg">
          <pre>{r.statement.length > 400 ? r.statement.slice(0, 400) + "…" : r.statement}</pre>
          <div>
            {r.kind === "documents" ? `OK, ${arr(r.page?.docs).length.toLocaleString()}${r.hasMore ? "+" : ""} documents` : r.kind === "text" ? r.text : "OK"}
            <span className="muted"> · {r.elapsedMs.toFixed(1)} ms</span>
          </div>
        </div>
      ))}
      {out.output && (
        <div className="msg">
          <pre className="output">{out.output}</pre>
        </div>
      )}
      {out.error && (
        <div className="msg err">
          <div>Error: {out.error}</div>
        </div>
      )}
      {!out.error && <div className="muted small">Finished in {out.elapsedMs.toFixed(1)} ms</div>}
    </div>
  );
}
