// Data source designer: a table or view, or a query, on one connection; Live or
// Archive mode; Filter, Sort and Projection panes; and the preview grid
// ("Apply and Refresh Data"), like Navicat's data source tab.
import { ArrowUpDown, Calculator, Columns3, Combine, Database, Filter, Play, Plus, RefreshCw, Save, X } from "lucide-react";
import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { B, BI, BIPreview, DBType, Explorer, ObjectKind, Query, SavedQuery, arr, errorText } from "../../api";
import { connDialect } from "../../dialect";
import { Monaco, monaco } from "../../editor/monaco";
import { connById, useApp, type TabOf } from "../../store";
import { Split } from "../Split";
import { biChanged, openObject } from "./actions";
import { CustomFieldsPane } from "./CustomFields";
import { JoinsPane } from "./Joins";
import { FilterList, cleanFilters } from "./FilterList";
import { useWorkspace } from "./WorkspaceView";
import { Toolbar } from "../Toolbar";
import { useSavableTab } from "../useSavableTab";
import { Field } from "../form";
import { createSqlEditor } from "../../editor/sqlEditor";

const SQL_ENGINES = [DBType.MySQL, DBType.MariaDB, DBType.PostgreSQL, DBType.SQLite];

/** IANA zones the webview knows, for the time zone field's suggestions. */
const ZONES: string[] = (() => {
  try {
    return (Intl as unknown as { supportedValuesOf(k: string): string[] }).supportedValuesOf("timeZone");
  } catch {
    return ["UTC", "America/Los_Angeles", "America/New_York", "Europe/London", "Europe/Berlin", "Asia/Tokyo", "Australia/Sydney"];
  }
})();

const sameDef = (a: B.DataSource, b: B.DataSource) => a.connId === b.connId && a.database === b.database && (a.schema ?? "") === (b.schema ?? "") && (a.table ?? "") === (b.table ?? "") && (a.sql ?? "") === (b.sql ?? "");
const comparable = (d: B.DataSource | null) => (d ? JSON.stringify({ ...d, fields: undefined, modified: undefined, refreshedAt: undefined, archiveRows: undefined, filters: cleanFilters(arr(d.filters)) }) : "");

export function SourceEditor({ tab }: { tab: TabOf<"bisource"> }) {
  const path = tab.biPath;
  const { w } = useWorkspace(path);
  const connections = useApp((s) => s.connections).filter((c) => SQL_ENGINES.includes(c.type));
  const [saved, setSaved] = useState<B.DataSource | null>(null);
  const [draft, setDraft] = useState<B.DataSource | null>(null);
  const [kind, setKind] = useState<"table" | "query">("table");
  const [databases, setDatabases] = useState<string[]>([]);
  const [schemas, setSchemas] = useState<string[]>([]);
  const [objects, setObjects] = useState<{ name: string; view: boolean }[]>([]);
  const [queries, setQueries] = useState<SavedQuery[]>([]);
  const [fields, setFields] = useState<B.Field[]>([]);
  const [preview, setPreview] = useState<BIPreview | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const [panes, setPanes] = useState({ filter: false, sort: false, projection: false, custom: false, joins: false });

  // Load the source (or start one) once the workspace is read; reload it when it
  // changes elsewhere and isn't being edited here.
  const savedText = useMemo(() => comparable(saved), [saved]);
  const dirty = !!draft && comparable(draft) !== savedText;
  useEffect(() => {
    if (!w) return;
    const found = tab.biId ? arr(w.dataSources).find((s) => s.id === tab.biId) : null;
    if (found) {
      if (!dirty) {
        setSaved(found);
        setDraft(found);
        if (tab.title !== found.name) useApp.getState().updateTab(tab.id, { title: found.name });
        setKind(found.sql ? "query" : "table");
        if (!fields.length) setFields(arr(found.fields));
      }
      return;
    }
    if (draft) return;
    const sel = useApp.getState().selection;
    const conn = connById(sel?.connId) && SQL_ENGINES.includes(connById(sel?.connId)!.type) ? connById(sel!.connId)! : connections[0];
    let n = arr(w.dataSources).length + 1;
    while (arr(w.dataSources).some((s) => s.name === `Data Source ${n}`)) n++;
    const isPG = conn?.type === DBType.PostgreSQL;
    setDraft({
      id: "",
      name: `Data Source ${n}`,
      connId: conn?.id ?? "",
      database: conn?.type === DBType.SQLite ? "main" : (sel?.connId === conn?.id ? sel?.database : conn?.initialDatabase) || "",
      schema: isPG ? (sel?.connId === conn?.id && sel?.schema) || "public" : "",
      table: sel?.object && sel.connId === conn?.id ? sel.object.name : "",
      sql: "",
      mode: "live",
    });
  }, [w]); // eslint-disable-line react-hooks/exhaustive-deps

  const update = (patch: Partial<B.DataSource>) => setDraft((d) => (d ? { ...d, ...patch } : d));
  const conn = connById(draft?.connId);
  const pg = conn?.type === DBType.PostgreSQL;

  // Databases, schemas, tables and saved queries of the chosen connection.
  useEffect(() => {
    if (!draft?.connId) return;
    let live = true;
    (async () => {
      if (!(await useApp.getState().connect(draft.connId))) return;
      const dbs = arr(await Explorer.ListDatabases(draft.connId).catch(() => null));
      if (live) setDatabases(dbs);
    })();
    return () => {
      live = false;
    };
  }, [draft?.connId]);
  useEffect(() => {
    if (!draft?.connId || !draft.database) return;
    let live = true;
    (async () => {
      if (!(await useApp.getState().connect(draft.connId))) return;
      if (pg) {
        const sc = arr(await Explorer.ListSchemas(draft.connId, draft.database).catch(() => null));
        if (live) setSchemas(sc);
      }
      const schema = pg ? draft.schema || "public" : "";
      const [tables, views, qs] = await Promise.all([
        Explorer.ListObjects(draft.connId, draft.database, schema, ObjectKind.KindTable).catch(() => null),
        Explorer.ListObjects(draft.connId, draft.database, schema, ObjectKind.KindView).catch(() => null),
        Query.SavedQueries(draft.connId, draft.database, schema).catch(() => null),
      ]);
      if (!live) return;
      setObjects([...arr(tables).map((o) => ({ name: o.name, view: false })), ...arr(views).map((o) => ({ name: o.name, view: true }))]);
      setQueries(arr(qs));
    })();
    return () => {
      live = false;
    };
  }, [draft?.connId, draft?.database, draft?.schema, pg]);

  /** The draft as it should run: archive previews read the snapshot only while it matches. */
  const runnable = useCallback(
    (d: B.DataSource): B.DataSource => {
      const clean = { ...d, filters: cleanFilters(arr(d.filters)) };
      return d.mode === "archive" && saved?.refreshedAt && sameDef(saved, d) ? clean : { ...clean, mode: "live" };
    },
    [saved],
  );

  const refresh = useCallback(async () => {
    if (!draft) return;
    setBusy("Reading…");
    setError("");
    try {
      const f = arr(await BI.Fields(path, runnable(draft)));
      setFields(f);
      setPreview(await BI.Preview(path, { ...runnable(draft), fields: f }, 200));
    } catch (e) {
      setError(errorText(e));
      setPreview(null);
    } finally {
      setBusy("");
    }
  }, [draft, path, runnable]);

  const save = useCallback(async (): Promise<boolean> => {
    if (!draft) return false;
    setBusy(draft.mode === "archive" ? "Saving and reading the snapshot…" : "Saving…");
    setError("");
    try {
      const s = await BI.SaveSource(path, { ...draft, filters: cleanFilters(arr(draft.filters)) });
      setSaved(s);
      setDraft(s);
      setFields(arr(s.fields));
      useApp.getState().updateTab(tab.id, { biId: s.id, title: s.name, dirty: false });
      biChanged(path);
      return true;
    } catch (e) {
      setError(errorText(e));
      return false;
    } finally {
      setBusy("");
    }
  }, [draft, path, tab.id]);

  const refreshSnapshot = async () => {
    if (!saved?.id) return;
    setBusy("Reading the snapshot…");
    try {
      const s = await BI.RefreshSource(path, saved.id);
      setSaved(s);
      setDraft((d) => (d ? { ...d, refreshedAt: s.refreshedAt, archiveRows: s.archiveRows, fields: s.fields } : d));
      biChanged(path);
      refresh();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy("");
    }
  };

  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;
  const saveRef = useSavableTab(tab.id, dirty, save);
  // Preview the source when it opens.
  const opened = !!draft && (!!saved || !!draft.table);
  useEffect(() => {
    if (opened) refresh();
  }, [opened]); // eslint-disable-line react-hooks/exhaustive-deps

  const usedBy = useMemo(() => arr(w?.charts).filter((c) => c.dataSource === saved?.id), [w, saved?.id]);

  if (!draft) return <div className="center-hint">Loading…</div>;
  const projection = draft.projection ?? { exclude: false, fields: [] };
  const allFields = fields.length ? fields : arr(draft.fields);

  return (
    <div
      className="bi-source"
      onKeyDown={(e) => {
        const meta = e.metaKey || e.ctrlKey;
        if (meta && e.key.toLowerCase() === "s") (e.preventDefault(), save());
        if (meta && e.key.toLowerCase() === "r") (e.preventDefault(), refresh());
      }}
    >
      <Toolbar>
        <button onClick={save} disabled={!!busy} className={dirty ? "accent" : ""} title="Save (⌘S)">
          <Save size={14} /> Save
        </button>
        <button onClick={refresh} disabled={!!busy || !draft.connId} title="Apply and Refresh Data (⌘R)">
          <Play size={14} /> Apply and Refresh Data
        </button>
        <span className="tb-sep" />
        <div className="seg" role="group" aria-label="Mode">
          <button className={draft.mode !== "archive" ? "on" : ""} onClick={() => update({ mode: "live" })} title="Read from the server whenever a chart is shown">
            Live
          </button>
          <button className={draft.mode === "archive" ? "on" : ""} onClick={() => update({ mode: "archive" })} title="Read once into a snapshot kept with the workspace">
            Archive
          </button>
        </div>
        {draft.mode === "archive" && saved?.id && (
          <button onClick={refreshSnapshot} disabled={!!busy || dirty} title={dirty ? "Save first" : "Read the rows again"}>
            <RefreshCw size={14} /> Refresh Snapshot
          </button>
        )}
        <span className="tb-sep" />
        {kind === "table" && (
          <button className={panes.joins || arr(draft.joins).length ? "on" : ""} onClick={() => setPanes((p) => ({ ...p, joins: !p.joins }))}>
            <Combine size={14} /> Joins
          </button>
        )}
        <button className={panes.filter || arr(draft.filters).length ? "on" : ""} onClick={() => setPanes((p) => ({ ...p, filter: !p.filter }))}>
          <Filter size={14} /> Filter
        </button>
        <button className={panes.sort || arr(draft.sorts).length ? "on" : ""} onClick={() => setPanes((p) => ({ ...p, sort: !p.sort }))}>
          <ArrowUpDown size={14} /> Sort
        </button>
        <button className={panes.projection || projection.fields?.length ? "on" : ""} onClick={() => setPanes((p) => ({ ...p, projection: !p.projection }))}>
          <Columns3 size={14} /> Projection
        </button>
        <button className={panes.custom || arr(draft.customFields).length ? "on" : ""} onClick={() => setPanes((p) => ({ ...p, custom: !p.custom }))}>
          <Calculator size={14} /> Custom Fields
        </button>
        <span className="spacer" />
        {saved?.id && (
          <button onClick={() => openObject(path, "chart", "", "")} title="A new chart on this data source">
            <Plus size={14} /> New Chart
          </button>
        )}
      </Toolbar>
      {error && <div className="error-banner">{error}</div>}
      <Split
        id="bi-source-preview"
        vertical
        className="objects-split"
        firstSize="50"
        minFirst={120}
        minSecond={80}
        first={
          <div className="bi-source-design">
            <div className="form bi-source-form">
              <Field label="Name">
                <input aria-label="Data source name" value={draft.name} onChange={(e) => update({ name: e.target.value })} />
              </Field>
              <div className="field">
                <label>Connection</label>
                <div className="field-input bi-row">
                  <select aria-label="Connection" value={draft.connId} onChange={(e) => {
                    const c = connById(e.target.value);
                    update({ connId: e.target.value, database: c?.type === DBType.SQLite ? "main" : c?.initialDatabase || "", schema: c?.type === DBType.PostgreSQL ? "public" : "", table: "" });
                  }}>
                    {!draft.connId && <option value="">(choose a connection)</option>}
                    {connections.map((c) => (
                      <option key={c.id} value={c.id}>
                        {c.name}
                      </option>
                    ))}
                  </select>
                  <select aria-label="Database" value={draft.database} onChange={(e) => update({ database: e.target.value, table: "" })}>
                    {!databases.includes(draft.database) && <option value={draft.database}>{draft.database || "(database)"}</option>}
                    {databases.map((d) => (
                      <option key={d}>{d}</option>
                    ))}
                  </select>
                  {pg && (
                    <select aria-label="Schema" value={draft.schema || "public"} onChange={(e) => update({ schema: e.target.value, table: "" })}>
                      {!schemas.includes(draft.schema || "public") && <option>{draft.schema || "public"}</option>}
                      {schemas.map((d) => (
                        <option key={d}>{d}</option>
                      ))}
                    </select>
                  )}
                </div>
              </div>
              <div className="field">
                <label>Reads</label>
                <div className="field-input bi-row">
                  <label className="check">
                    <input type="radio" checked={kind === "table"} onChange={() => (setKind("table"), update({ sql: "" }))} /> Table or view
                  </label>
                  <label className="check">
                    <input type="radio" checked={kind === "query"} onChange={() => (setKind("query"), update({ table: "", joins: [] }))} /> Query
                  </label>
                  {kind === "table" ? (
                    <select aria-label="Table" value={draft.table ?? ""} onChange={(e) => update({ table: e.target.value })}>
                      <option value="">(choose a table or view)</option>
                      {!!draft.table && !objects.some((o) => o.name === draft.table) && <option>{draft.table}</option>}
                      {objects.map((o) => (
                        <option key={(o.view ? "v:" : "t:") + o.name} value={o.name}>
                          {o.name}
                          {o.view ? " (view)" : ""}
                        </option>
                      ))}
                    </select>
                  ) : (
                    queries.length > 0 && (
                      <select aria-label="Import saved query" value="" onChange={(e) => update({ sql: queries.find((q) => q.id === e.target.value)?.sql ?? draft.sql })}>
                        <option value="">Import a saved query…</option>
                        {queries.map((q) => (
                          <option key={q.id} value={q.id}>
                            {q.name}
                          </option>
                        ))}
                      </select>
                    )
                  )}
                </div>
              </div>
              <div className="field">
                <label>Time zone</label>
                <div className="field-input bi-row">
                  <input
                    aria-label="Time zone"
                    list="bi-timezones"
                    placeholder="As stored"
                    value={draft.timezone ?? ""}
                    onChange={(e) => update({ timezone: e.target.value })}
                    title="Bucket and filter date/time fields in this zone (values without a zone are taken as UTC)"
                  />
                  <datalist id="bi-timezones">
                    {ZONES.map((z) => (
                      <option key={z} value={z} />
                    ))}
                  </datalist>
                  <span className="muted">Dates are grouped and filtered in this zone; stored values without one count as UTC.</span>
                </div>
              </div>
              {draft.mode === "archive" && (
                <div className="field">
                  <label>Snapshot</label>
                  <div className="field-input muted">
                    {saved?.refreshedAt && sameDef(saved, draft) ? `${(saved.archiveRows ?? 0).toLocaleString()} rows, read ${new Date(saved.refreshedAt).toLocaleString()}` : "Read when you save."}
                  </div>
                </div>
              )}
              <Field label="Remarks">
                <input aria-label="Remarks" value={draft.remarks ?? ""} onChange={(e) => update({ remarks: e.target.value })} />
              </Field>
            </div>
            {kind === "query" && (
              <SqlBox
                key={draft.connId + "/" + draft.database}
                connId={draft.connId}
                database={draft.database}
                schema={pg ? draft.schema || "public" : undefined}
                value={draft.sql ?? ""}
                onChange={(sql) => update({ sql })}
                onSave={() => saveRef.current()}
                onRun={() => refreshRef.current()}
              />
            )}
            {panes.joins && kind === "table" && (
              <section className="bi-pane">
                <div className="bi-pane-title">Joins</div>
                <JoinsPane src={draft} tables={objects.map((o) => o.name)} fields={allFields.filter((f) => !f.name.includes(".")).map((f) => f.name)} onChange={(joins) => update({ joins })} />
              </section>
            )}
            {panes.custom && (
              <section className="bi-pane">
                <div className="bi-pane-title">Custom Fields</div>
                <CustomFieldsPane fields={allFields} custom={arr(draft.customFields)} onChange={(customFields) => update({ customFields })} />
              </section>
            )}
            {panes.filter && (
              <section className="bi-pane">
                <div className="bi-pane-title">Filter</div>
                <FilterList fields={[...allFields, ...arr(draft.customFields).map((c) => ({ name: c.name, type: c.type || "string" }) as B.Field)]} filters={arr(draft.filters)} onChange={(filters) => update({ filters })} />
              </section>
            )}
            {panes.sort && (
              <section className="bi-pane">
                <div className="bi-pane-title">Sort</div>
                {arr(draft.sorts).map((s, i) => (
                  <div key={i} className="bi-filter">
                    <select aria-label="Sort field" value={s.field} onChange={(e) => update({ sorts: arr(draft.sorts).map((x, j) => (j === i ? { ...x, field: e.target.value } : x)) })}>
                      {allFields.map((f) => (
                        <option key={f.name}>{f.name}</option>
                      ))}
                    </select>
                    <select aria-label="Sort order" value={s.desc ? "desc" : "asc"} onChange={(e) => update({ sorts: arr(draft.sorts).map((x, j) => (j === i ? { ...x, desc: e.target.value === "desc" } : x)) })}>
                      <option value="asc">ASC</option>
                      <option value="desc">DESC</option>
                    </select>
                    <button className="icon-btn" title="Remove" onClick={() => update({ sorts: arr(draft.sorts).filter((_, j) => j !== i) })}>
                      <X size={13} />
                    </button>
                  </div>
                ))}
                <button className="small-btn" disabled={!allFields.length} onClick={() => update({ sorts: [...arr(draft.sorts), { field: allFields[0].name }] })}>
                  <Plus size={12} /> Add Sort
                </button>
              </section>
            )}
            {panes.projection && (
              <section className="bi-pane">
                <div className="bi-pane-title">
                  Projection{" "}
                  <select aria-label="Projection type" value={projection.exclude ? "exclude" : "include"} onChange={(e) => update({ projection: { ...projection, exclude: e.target.value === "exclude" } })}>
                    <option value="include">Include only</option>
                    <option value="exclude">Exclude</option>
                  </select>
                </div>
                <div className="bi-projection">
                  {allFields.map((f) => (
                    <label key={f.name} className="check">
                      <input
                        type="checkbox"
                        checked={arr(projection.fields).includes(f.name)}
                        onChange={(e) => update({ projection: { ...projection, fields: e.target.checked ? [...arr(projection.fields), f.name] : arr(projection.fields).filter((x) => x !== f.name) } })}
                      />
                      {f.name}
                    </label>
                  ))}
                  {!allFields.length && <span className="muted">Apply and Refresh Data to list the fields.</span>}
                </div>
              </section>
            )}
            {usedBy.length > 0 && <div className="bi-usedby muted">Used by {usedBy.map((c) => c.name).join(", ")}</div>}
          </div>
        }
        second={<PreviewGrid preview={preview} fields={allFields} busy={busy} />}
      />
    </div>
  );
}

const TYPE_MARK: Record<string, string> = { number: "#", datetime: "◷", string: "Aa" };

// Memoised: the SQL box re-renders the editor on every keystroke.
const PreviewGrid = memo(function PreviewGrid({ preview, fields, busy }: { preview: BIPreview | null; fields: B.Field[]; busy: string }) {
  if (busy) return <div className="center-hint">{busy}</div>;
  if (!preview) return <div className="center-hint">Apply and Refresh Data to preview the rows.</div>;
  const cols = arr(preview.columns);
  const rows = arr(preview.rows);
  return (
    <div className="bi-preview">
      <table>
        <thead>
          <tr>
            {cols.map((c) => {
              const t = fields.find((f) => f.name === c.name)?.type ?? "string";
              return (
                <th key={c.name} title={`${c.name} — ${t}${c.type ? ` (${c.type})` : ""}`}>
                  <span className={"bi-type " + t}>{TYPE_MARK[t]}</span> {c.name}
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i}>
              {arr(r).map((v, j) => (
                <td key={j} className={v === null ? "null" : ""}>
                  {v === null ? "NULL" : v}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      <div className="statusbar">
        <Database size={12} /> {rows.length === 200 ? "First 200 rows" : `${rows.length} rows`}
      </div>
    </div>
  );
});

function SqlBox({ connId, database, schema, value, onChange, onSave, onRun }: { connId: string; database: string; schema?: string; value: string; onChange(v: string): void; onSave(): void; onRun(): void }) {
  const host = useRef<HTMLDivElement>(null);
  const ed = useRef<Monaco.editor.IStandaloneCodeEditor | null>(null);
  const change = useRef(onChange);
  change.current = onChange;
  useEffect(() => {
    if (!host.current) return;
    const { editor, model, dispose } = createSqlEditor(host.current, value, connDialect(connId), { connId, database, schema });
    ed.current = editor;
    const sub = editor.onDidChangeModelContent(() => change.current(model.getValue()));
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, onSave);
    editor.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyR, onRun);
    return () => {
      sub.dispose();
      dispose();
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps
  // An imported saved query replaces the text.
  useEffect(() => {
    const m = ed.current?.getModel();
    if (m && m.getValue() !== value) m.setValue(value);
  }, [value]);
  return <div className="bi-sql" ref={host} data-testid="bi-sql" />;
}
