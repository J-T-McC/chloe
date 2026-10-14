// View designer: the SELECT definition in a SQL editor with a result preview, plus
// view options (MySQL algorithm/definer/security, PostgreSQL barrier/invoker,
// materialized view tablespace), comment and SQL preview.
import { Eye, Play, Save, SaveAll, Sparkles } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { D, DBType, Design, ObjectKind, Query, arr, errorText } from "../../api";
import { autoTrigger, clearEditorContext, invalidateCatalog, preloadCatalog, registerSqlCompletion, setEditorContext } from "../../editor/completion";
import { beautifyEditor, registerFormatter } from "../../editor/format";
import { Monaco, editorTheme, monaco } from "../../editor/monaco";
import { Tab, connById, useApp } from "../../store";
import { useTree } from "../../tree";
import { connDialect, editorLanguage } from "../../dialect";
import { confirm, prompt } from "../Dialogs";
import { CodeBlock } from "../ObjectsPane";
import { Messages, ResultGrid, RunState } from "../QueryEditor";
import { Split } from "../Split";

type Section = "definition" | "advanced" | "comment" | "sql";

const same = (a: D.View | null, b: D.View | null) => JSON.stringify(a) === JSON.stringify(b);

export function ViewDesigner({ tab }: { tab: Tab }) {
  const connId = tab.connId!;
  const pg = connById(connId)?.type === DBType.PostgreSQL;
  const sqlite = connById(connId)?.type === DBType.SQLite;
  const isNew = !tab.ref?.name;
  const [original, setOriginal] = useState<D.View | null>(null);
  const [draft, setDraft] = useState<D.View | null>(null);
  const [section, setSection] = useState<Section>("definition");
  const [preview, setPreview] = useState<{ statements: string[]; warnings: string[]; error?: string }>({ statements: [], warnings: [] });
  const [error, setError] = useState<{ text: string; sql?: string } | null>(null);
  const [saving, setSaving] = useState(false);
  const [run, setRun] = useState<RunState>({ results: [], running: false });
  const host = useRef<HTMLDivElement>(null);
  const editorRef = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null);
  const session = useRef<Promise<string> | null>(null);
  const saveRef = useRef<() => void>(() => {});
  const previewRef = useRef<() => void>(() => {});

  // Load (or start) the view.
  useEffect(() => {
    (async () => {
      try {
        const v: D.View = isNew
          ? { database: tab.database!, schema: pg ? (tab.schema ?? "public") : undefined, name: "", definition: "SELECT\n  \nFROM ", materialized: tab.ref?.kind === ObjectKind.KindMatView }
          : await Design.LoadView(connId, tab.ref!);
        setOriginal(isNew ? null : v);
        setDraft(v);
      } catch (e) {
        setError({ text: errorText(e) });
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connId]);

  // The definition editor, created once the view is loaded.
  const loaded = !!draft;
  useEffect(() => {
    if (!loaded || !host.current) return;
    registerSqlCompletion();
    registerFormatter();
    const model = monaco.editor.createModel(draft!.definition, editorLanguage(connDialect(connId)));
    const editor = monaco.editor.create(host.current, {
      model,
      theme: editorTheme(),
      automaticLayout: true,
      minimap: { enabled: false },
      fontSize: 13,
      fontFamily: "SF Mono, Menlo, Consolas, monospace",
      lineNumbersMinChars: 3,
      scrollBeyondLastLine: false,
      tabSize: 2,
      wordBasedSuggestions: "off",
      quickSuggestions: { other: true, comments: false, strings: true },
      suggest: { showWords: false, preview: true, showStatusBar: true },
      suggestSelection: "first",
      fixedOverflowWidgets: true,
    });
    editorRef.current = editor;
    const ctx = { connId, database: tab.database, schema: pg ? (draft!.schema ?? "public") : undefined };
    setEditorContext(model, ctx);
    preloadCatalog(ctx);
    const trig = autoTrigger(editor);
    const sub = editor.onDidChangeModelContent(() => setDraft((d) => (d ? { ...d, definition: model.getValue() } : d)));
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => saveRef.current());
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyR, () => previewRef.current());
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyMod.Shift | monaco.KeyCode.KeyF, () => beautifyEditor(editor));
    if (isNew) {
      editor.setPosition({ lineNumber: 2, column: 3 });
      editor.focus();
    }
    return () => {
      trig.dispose();
      sub.dispose();
      clearEditorContext(model);
      editor.dispose();
      model.dispose();
      editorRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loaded]);

  // Close the preview session with the tab.
  useEffect(
    () => () => {
      session.current?.then((id) => Query.CloseSession(id)).catch(() => {});
    },
    [],
  );

  // Live SQL preview.
  useEffect(() => {
    if (!draft) return;
    const t = setTimeout(() => {
      Design.PreviewView(connId, original, draft)
        .then((p) => setPreview({ statements: arr(p.statements), warnings: arr(p.warnings) }))
        .catch((e) => setPreview({ statements: [], warnings: [], error: errorText(e) }));
    }, 200);
    return () => clearTimeout(t);
  }, [draft, original, connId]);

  const dirty = useMemo(() => (isNew ? !!draft?.name || (draft?.definition.trim() ?? "") !== "SELECT\n  \nFROM".trim() : !same(original, draft)), [isNew, draft, original]);
  const kindLabel = draft?.materialized ? "materialized view" : "view";
  useEffect(() => {
    const base = draft?.name || "Untitled";
    useApp.getState().updateTab(tab.id, { title: `${base}${dirty && !isNew ? " •" : ""} (${kindLabel})` });
  }, [dirty, draft?.name, tab.id, isNew, kindLabel]);

  const update = (patch: Partial<D.View>) => setDraft((d) => (d ? { ...d, ...patch } : d));

  const apply = useCallback(
    async (old: D.View | null, v: D.View) => {
      const p = await Design.PreviewView(connId, old, v).catch((e) => {
        setError({ text: errorText(e) });
        return null;
      });
      if (!p || !arr(p.statements).length) return null;
      const warnings = arr(p.warnings);
      if (warnings.length && !(await confirm(`Save ${kindLabel}?`, { message: warnings.join("\n") + "\n\n" + arr(p.statements).join(";\n"), okLabel: "Save" }))) return null;
      setSaving(true);
      setError(null);
      try {
        const res = await Design.ApplyView(connId, old, v);
        if (res.error) {
          setError({ text: res.error, sql: res.failedStatement });
          return null;
        }
        invalidateCatalog(connId, v.database);
        refreshViews(connId, v.database, v.schema);
        useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 }));
        return res.view ?? null;
      } catch (e) {
        setError({ text: errorText(e) });
        return null;
      } finally {
        setSaving(false);
      }
    },
    [connId, kindLabel],
  );

  const save = async () => {
    if (!draft) return;
    let v = draft;
    if (!v.name.trim()) {
      const name = await prompt(`Save ${kindLabel}`, { message: "View name" });
      if (!name?.trim()) return;
      v = { ...v, name: name.trim() };
    }
    const saved = await apply(original, v);
    if (!saved) return;
    setOriginal(saved);
    setDraft(saved);
    editorRef.current?.setValue(saved.definition);
    const kind = saved.materialized ? ObjectKind.KindMatView : ObjectKind.KindView;
    useApp.getState().updateTab(tab.id, { ref: { database: saved.database, schema: saved.schema, name: saved.name, kind } });
  };
  saveRef.current = save;

  const saveAs = async () => {
    if (!draft) return;
    const name = await prompt(`Save ${kindLabel} as`, { message: "New view name", defaultValue: draft.name ? `${draft.name}_copy` : "" });
    if (!name?.trim()) return;
    const saved = await apply(null, { ...draft, name: name.trim(), origName: undefined });
    if (saved) viewDesign(connId, { database: saved.database, schema: saved.schema, name: saved.name, kind: saved.materialized ? ObjectKind.KindMatView : ObjectKind.KindView });
  };

  const runPreview = async () => {
    if (!draft?.definition.trim() || run.running) return;
    setSection("definition");
    setRun({ results: [], running: true, startedAt: Date.now() });
    try {
      session.current ??= Query.OpenSession(connId, draft.database, draft.schema ?? "");
      const sid = await session.current;
      const sql = draft.definition.trim().replace(/;+\s*$/, "");
      setRun({ results: arr(await Query.Execute(sid, sql, { continueOnError: false })), running: false });
    } catch (e) {
      session.current = null;
      setRun({ results: [], running: false, error: errorText(e) });
    }
  };
  previewRef.current = runPreview;

  if (!draft) return <div className="center-hint">{error ? <span className="err">{error.text}</span> : "Loading…"}</div>;

  const result = run.results.find((r) => r.resultId);
  const sections: Array<[Section, string, boolean]> = [
    ["definition", "Definition", true],
    ["advanced", "Advanced", !sqlite],
    ["comment", "Comment", pg],
    ["sql", `SQL Preview${preview.statements.length ? ` (${preview.statements.length})` : ""}`, true],
  ];

  return (
    <div className="designer view-designer">
      <div className="toolbar">
        <button className={dirty ? "accent" : ""} disabled={saving || (!dirty && !isNew)} onClick={save} title="Save (⌘S)">
          <Save size={14} /> {saving ? "Saving…" : "Save"}
        </button>
        <button disabled={saving} onClick={saveAs} title="Save as a new view">
          <SaveAll size={14} /> Save As
        </button>
        <span className="tb-sep" />
        <button onClick={runPreview} disabled={run.running} title="Run the definition (⌘R)">
          <Play size={14} /> Preview
        </button>
        <button onClick={() => editorRef.current && beautifyEditor(editorRef.current)} title="Beautify SQL (⇧⌘F)">
          <Sparkles size={14} /> Beautify
        </button>
        <span className="spacer" />
        <span className="crumbs">
          <Eye size={13} /> {[connById(connId)?.name, draft.database, draft.schema].filter(Boolean).join(" › ")} ›{" "}
          <input className="name-input" placeholder={`${kindLabel} name`} value={draft.name} onChange={(e) => update({ name: e.target.value })} />
        </span>
      </div>
      <div className="seg-tabs designer-tabs">
        {sections
          .filter(([, , on]) => on)
          .map(([k, label]) => (
            <button key={k} className={section === k ? "active" : ""} onClick={() => setSection(k)}>
              {label}
            </button>
          ))}
      </div>
      {error && (
        <div className="error-banner" onClick={() => setError(null)}>
          {error.text}
          {error.sql && <pre>{error.sql}</pre>}
        </div>
      )}
      <div className="designer-body">
        <div style={{ display: section === "definition" ? "flex" : "none", flex: 1, minHeight: 0 }}>
          <Split
            id="view-designer"
            vertical
            className="qe-split"
            firstSize="60"
            minFirst="15"
            minSecond="10"
            first={<div className="monaco-host" ref={host} />}
            second={
              <div className="results">
                <div className="result-tabs">
                  <button className="active">{result ? "Preview" : "Message"}</button>
                  <span className="spacer" />
                  <span className="muted">{run.running ? "Running…" : ""}</span>
                </div>
                <div className="result-body">
                  {result ? <ResultGrid result={result} connId={connId} database={draft.database} schema={draft.schema} /> : run.results.length || run.error || run.running ? <Messages run={run} /> : <div className="messages muted">Preview runs the definition and shows its rows.</div>}
                </div>
              </div>
            }
          />
        </div>
        {section === "advanced" && <AdvancedTab view={draft} pg={pg} isNew={isNew} update={update} />}
        {section === "comment" && <textarea className="designer-comment" placeholder="Comment" value={draft.comment ?? ""} onChange={(e) => update({ comment: e.target.value || undefined })} />}
        {section === "sql" && (
          <div className="designer-sql">
            {preview.error && <div className="badge warn block">{preview.error}</div>}
            {preview.warnings.map((w, i) => (
              <div key={i} className="badge warn block">
                {w}
              </div>
            ))}
            {preview.statements.length ? <CodeBlock code={preview.statements.join(";\n\n") + ";"} /> : !preview.error && <div className="center-hint">No changes.</div>}
          </div>
        )}
      </div>
    </div>
  );
}

function AdvancedTab({ view: v, pg, isNew, update }: { view: D.View; pg: boolean; isNew: boolean; update(p: Partial<D.View>): void }) {
  const select = (label: string, value: string | undefined, options: string[], onChange: (v: string) => void, empty = "(default)") => (
    <div className="field">
      <label>{label}</label>
      <div className="field-input">
        <select value={(value ?? "").toUpperCase()} onChange={(e) => onChange(e.target.value)}>
          <option value="">{empty}</option>
          {options.map((o) => (
            <option key={o}>{o}</option>
          ))}
        </select>
      </div>
    </div>
  );
  const check = (label: string, value: boolean | undefined, onChange: (v: boolean) => void, hint?: string) => (
    <div className="field">
      <label />
      <div className="field-input">
        <label className="check" title={hint}>
          <input type="checkbox" checked={!!value} onChange={(e) => onChange(e.target.checked)} /> {label}
        </label>
      </div>
    </div>
  );
  return (
    <div className="form designer-form">
      {pg && check("Materialized view", v.materialized, (m) => update({ materialized: m, checkOption: m ? undefined : v.checkOption }), "Store the query result; refresh it with Refresh Materialized View")}
      {!pg && (
        <>
          {select("Algorithm", v.algorithm, ["UNDEFINED", "MERGE", "TEMPTABLE"], (x) => update({ algorithm: x || undefined }))}
          <div className="field">
            <label>Definer</label>
            <div className="field-input">
              <input placeholder="current user (e.g. app@%)" value={v.definer ?? ""} onChange={(e) => update({ definer: e.target.value || undefined })} />
            </div>
          </div>
          {select("Security", v.security, ["DEFINER", "INVOKER"], (x) => update({ security: x || undefined }))}
        </>
      )}
      {!v.materialized && select("Check Option", v.checkOption, ["CASCADED", "LOCAL"], (x) => update({ checkOption: x || undefined }), "(none)")}
      {pg && !v.materialized && (
        <>
          {check("Security barrier", v.securityBarrier, (b) => update({ securityBarrier: b }), "Evaluate the view's own conditions before user-supplied functions")}
          {check("Security invoker", v.securityInvoker, (b) => update({ securityInvoker: b }), "Check permissions on underlying tables as the querying user (PostgreSQL 15+)")}
        </>
      )}
      {pg && v.materialized && (
        <>
          <div className="field">
            <label>Tablespace</label>
            <div className="field-input">
              <input placeholder="(default)" value={v.tablespace ?? ""} onChange={(e) => update({ tablespace: e.target.value || undefined })} />
            </div>
          </div>
          {check(isNew ? "Populate when created (WITH DATA)" : "Populated (WITH DATA)", !v.noData, (b) => update({ noData: !b }))}
        </>
      )}
    </div>
  );
}

/** Open the view designer for an existing view or materialized view. */
export function viewDesign(connId: string, ref: { database: string; schema?: string; name: string; kind: ObjectKind }) {
  useApp.getState().openTab({ kind: "viewdesign", title: `${ref.name} (view)`, connId, database: ref.database, schema: ref.schema, ref });
}

/** Open the view designer for a new view. */
export function newView(connId: string, database: string, schema?: string, materialized = false) {
  useApp.getState().openTab({
    kind: "viewdesign",
    title: "Untitled (view)",
    connId,
    database,
    schema,
    ref: { database, schema, name: "", kind: materialized ? ObjectKind.KindMatView : ObjectKind.KindView, signature: String(Date.now()) },
  });
}

function refreshViews(connId: string, database: string, schema?: string) {
  const tree = useTree.getState();
  for (const kind of [ObjectKind.KindView, ObjectKind.KindMatView]) {
    const key = [connId, database, schema, "#" + kind].filter(Boolean).join("/");
    if (tree.children[key]) tree.refresh({ key, type: "folder", label: "", connId, database, schema, kind, depth: 0, expandable: true });
  }
}
