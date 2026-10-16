import { Copy, Download, FilePlus2, Network, PencilRuler, Plus, RefreshCw, Table2, Trash2, Upload } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { DBType, Explorer, ObjectInfo, ObjectKind, ObjectRef, TableAction, arr, errorText, copyText } from "../api";
import { RedisBrowser } from "./redis/RedisBrowser";
import { RedisDatabases } from "./redis/RedisDatabases";
import { MongoCollections } from "./mongo/MongoCollections";
import { ModelsList } from "./model/ModelsList";
import { BIList } from "./bi/BIList";
import { designTable, newTable, runTableAction } from "../tableActions";
import { newView, viewDesign } from "./designer/ViewDesigner";
import { objectsAreaMenu, openERD, tableChildMenu, tableMenu, viewMenu } from "../menus";
import { openExport, openImport } from "./transfer/state";
import { editorTheme, monaco } from "../editor/monaco";
import { connById, dbKey, useApp } from "../store";
import { TreeNode, useTree } from "../tree";
import { openMenu } from "./ContextMenu";
import { Split } from "./Split";
import { KIND_LABEL, KindIcon } from "./icons";
import { newQuery, openObject, savedQueryMenu, selectAllSQL } from "./Sidebar";
import { deleteSavedQueries } from "../queries";
import { Toolbar } from "./Toolbar";

export function ObjectsPane() {
  const sel = useApp((s) => s.selection);
  const showingModels = useApp((s) => s.showingModels);
  const showingBI = useApp((s) => s.showingBI);
  const connected = useApp((s) => !!(sel?.connId && s.open[sel.connId]));
  if (showingModels) return <ModelsList />;
  if (showingBI) return <BIList />;
  if (sel?.connId && connById(sel.connId)?.type === DBType.Redis) {
    if (!connected) return <div className="center-hint">Open the connection to browse its keys.</div>;
    return sel.database !== undefined ? <RedisBrowser key={`${sel.connId}/${sel.database}`} connId={sel.connId} db={Number(sel.database)} /> : <RedisDatabases connId={sel.connId} />;
  }
  if (sel?.connId && connById(sel.connId)?.type === DBType.MongoDB) {
    if (!connected) return <div className="center-hint">Open the connection to browse its collections.</div>;
    if (!sel.database) return <div className="center-hint">Select a database in the connection tree.</div>;
    const kind = sel.kind === ObjectKind.KindView || sel.object?.kind === ObjectKind.KindView ? ObjectKind.KindView : ObjectKind.KindCollection;
    return <MongoCollections key={`${sel.connId}/${sel.database}`} connId={sel.connId} db={sel.database} kind={kind} />;
  }
  return <SqlObjectsPane />;
}

function SqlObjectsPane() {
  const sel = useApp((s) => s.selection);
  const open = useApp((s) => s.open);
  const [objects, setObjects] = useState<ObjectInfo[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  // The picked object remembers which listing it came from, so a stale pick is never
  // shown (or queried) against a different connection or database.
  const [pick, setPick] = useState<{ key: string; names: string[]; anchor: string } | null>(null);
  const [filter, setFilter] = useState("");
  const [sortKey, setSortKey] = useState<"name" | "rows" | "dataSize" | "modified">("name");
  const [reload, setReload] = useState(0);
  const objectsVersion = useApp((s) => s.objectsVersion);

  const kind = sel?.kind ?? (sel?.database ? ObjectKind.KindTable : undefined);
  const connected = sel?.connId ? !!open[sel.connId] : false;
  const dbOpen = useApp((s) => !!(sel?.database && s.openDbs[dbKey(sel.connId, sel.database)]));
  const needsSchema = connById(sel?.connId)?.type === "postgres" && !sel?.schema;
  const listKey = [sel?.connId, sel?.database, sel?.schema, kind].join("/");
  // Multi-selection by name (⌘-click toggles, ⇧-click extends); "picked" is the last one.
  const pickedNames = pick?.key === listKey ? pick.names : [];
  const pickedList = objects.filter((o) => pickedNames.includes(o.name));
  const picked = pickedList.find((o) => o.name === pickedNames[pickedNames.length - 1]) ?? null;
  const setPicked = (o: ObjectInfo | null) => setPick(o ? { key: listKey, names: [o.name], anchor: o.name } : null);

  useEffect(() => {
    setPicked(null);
    if (!sel || !sel.database || !kind || !connected || !dbOpen || needsSchema) {
      setObjects([]);
      return;
    }
    setLoading(true);
    setError("");
    Explorer.ListObjects(sel.connId, sel.database, sel.schema ?? "", kind)
      .then((o) => setObjects(arr(o)))
      .catch((e) => setError(errorText(e)))
      .finally(() => setLoading(false));
  }, [sel?.connId, sel?.database, sel?.schema, kind, connected, dbOpen, needsSchema, reload, objectsVersion]);

  // Selecting an object in the tree selects it here too.
  useEffect(() => {
    if (sel?.object) setPicked(objects.find((o) => o.name === sel.object!.name) ?? null);
  }, [sel?.object, objects]);

  const shown = useMemo(() => {
    const f = filter.toLowerCase();
    const list = objects.filter((o) => !f || o.name.toLowerCase().includes(f));
    return [...list].sort((a, b) => (sortKey === "name" ? a.name.localeCompare(b.name) : ((b[sortKey] as number) ?? 0) > ((a[sortKey] as number) ?? 0) ? 1 : -1));
  }, [objects, filter, sortKey]);

  if (!sel || (!sel.connId && sel.group === undefined)) return <Welcome />;
  if (!sel.connId) return <div className="center-hint">Group “{sel.group}” — select a connection.</div>;
  if (!connected) return <div className="center-hint">Open the connection to browse its objects.</div>;
  if (sel.database && !dbOpen)
    return (
      <div className="center-hint">
        Database “{sel.database}” is closed.
        <div>
          <button onClick={() => useTree.getState().expand({ key: `${sel.connId}/${sel.database}`, type: "db", label: sel.database!, connId: sel.connId, database: sel.database, depth: 1, expandable: true })}>
            Open Database
          </button>
        </div>
      </div>
    );
  if (!sel.database || needsSchema) return <div className="center-hint">Select a {needsSchema && sel.database ? "schema" : "database"} in the connection tree.</div>;

  const asNode = (o: ObjectInfo): TreeNode => ({
    key: "",
    type: "object",
    label: o.name,
    connId: sel.connId,
    database: sel.database,
    schema: o.schema || sel.schema,
    kind: o.kind,
    info: o,
    depth: 0,
    expandable: false,
  });
  const isData = kind === ObjectKind.KindTable || kind === ObjectKind.KindView || kind === ObjectKind.KindMatView;
  const isView = kind === ObjectKind.KindView || kind === ObjectKind.KindMatView;
  const isQueries = kind === ObjectKind.KindQuery;
  // Saved queries are Chloe's: they have a modified time even where server objects show an owner.
  const showModified = sel.schema === undefined || isQueries;
  const refOfObj = (o: ObjectInfo): ObjectRef => ({ database: sel.database!, schema: o.schema || sel.schema, name: o.name, kind: o.kind, signature: o.signature });
  const clickRow = (e: React.MouseEvent, o: ObjectInfo) => {
    if (e.metaKey || e.ctrlKey) {
      const names = pickedNames.includes(o.name) ? pickedNames.filter((n) => n !== o.name) : [...pickedNames, o.name];
      return setPick({ key: listKey, names, anchor: o.name });
    }
    if (e.shiftKey && pick?.anchor) {
      const a = shown.findIndex((x) => x.name === pick.anchor);
      const b = shown.findIndex((x) => x.name === o.name);
      if (a >= 0 && b >= 0) {
        const [lo, hi] = a < b ? [a, b] : [b, a];
        return setPick({ key: listKey, names: shown.slice(lo, hi + 1).map((x) => x.name), anchor: pick.anchor });
      }
    }
    setPicked(o);
  };
  const tableRefs = pickedList.map((o) => ({ database: sel.database!, schema: o.schema || sel.schema, name: o.name, kind: o.kind }));
  const showRows = kind === ObjectKind.KindTable || kind === ObjectKind.KindMatView;

  return (
    <div className="objects-pane">
      <Toolbar>
        <button disabled={!picked} onClick={() => picked && openObject(asNode(picked))}>
          <Table2 size={14} /> {isData ? "Open" : isQueries ? "Open Query" : "View DDL"}
        </button>
        {isQueries && (
          <button disabled={!pickedList.length} onClick={() => deleteSavedQueries(sel.connId, sel.database!, sel.schema, pickedNames)}>
            <Trash2 size={14} /> Delete Query
          </button>
        )}
        {isView && (
          <>
            <button disabled={!picked} onClick={() => picked && viewDesign(sel.connId, refOfObj(picked))}>
              <PencilRuler size={14} /> Design View
            </button>
            <button onClick={() => newView(sel.connId, sel.database!, sel.schema, kind === ObjectKind.KindMatView)}>
              <Plus size={14} /> New {kind === ObjectKind.KindMatView ? "Materialized View" : "View"}
            </button>
            <button disabled={pickedList.length !== 1} onClick={() => picked && runTableAction(sel.connId, refOfObj(picked), TableAction.ActionDrop)}>
              <Trash2 size={14} /> Delete View
            </button>
          </>
        )}
        {kind === ObjectKind.KindTable && (
          <>
            <button disabled={!picked} onClick={() => picked && designTable(sel.connId, { ...picked, database: sel.database!, schema: picked.schema || sel.schema })}>
              <PencilRuler size={14} /> Design Table
            </button>
            <button onClick={() => newTable(sel.connId, sel.database!, sel.schema)}>
              <Plus size={14} /> New Table
            </button>
            <button disabled={pickedList.length !== 1} onClick={() => picked && runTableAction(sel.connId, refOfObj(picked), TableAction.ActionDrop)}>
              <Trash2 size={14} /> Delete Table
            </button>
            <span className="tb-sep" />
            <button title="Import Wizard" onClick={() => openImport({ connId: sel.connId, database: sel.database!, schema: sel.schema, table: pickedList.length === 1 ? tableRefs[0] : undefined })}>
              <Download size={14} /> Import
            </button>
            <button title="Export Wizard" onClick={() => openExport({ connId: sel.connId, database: sel.database!, schema: sel.schema, tables: tableRefs })}>
              <Upload size={14} /> Export
            </button>
            <button title="ER Diagram" onClick={() => openERD(sel.connId, sel.database!, sel.schema)}>
              <Network size={14} /> ER Diagram
            </button>
          </>
        )}
        <button onClick={() => newQuery(sel.connId, sel.database, sel.schema)}>
          <FilePlus2 size={14} /> New Query
        </button>
        <button
          onClick={() => {
            setReload((x) => x + 1);
            // keep the tree in sync
            const tree = useTree.getState();
            const key = [sel.connId, sel.database, sel.schema, "#" + kind].filter(Boolean).join("/");
            if (tree.children[key]) tree.refresh({ key, type: "folder", label: "", connId: sel.connId, database: sel.database, schema: sel.schema, kind, depth: 0, expandable: true });
          }}
        >
          <RefreshCw size={14} className={loading ? "spin" : ""} />
        </button>
        <span className="tb-sep" />
        <span className="crumbs">
          {[connById(sel.connId)?.name, sel.database, sel.schema, KIND_LABEL[kind!]].filter(Boolean).join(" › ")}
        </span>
        <span className="spacer" />
        <input className="search" placeholder="Filter" value={filter} onChange={(e) => setFilter(e.target.value)} />
      </Toolbar>
      <Split
        id="objects-info"
        className="objects-split"
        secondSize={320}
        minFirst={240}
        minSecond={180}
        maxSecond="70"
        first={
        <div
          className="object-list"
          tabIndex={0}
          onContextMenu={(e) => openMenu(e, objectsAreaMenu(sel.connId, sel.database!, sel.schema, kind))}
          onMouseDown={(e) => e.target === e.currentTarget && setPicked(null)}
          onKeyDown={(e) => {
            const meta = e.metaKey || e.ctrlKey;
            if (meta && e.key === "a") {
              e.preventDefault();
              setPick({ key: listKey, names: shown.map((o) => o.name), anchor: shown[0]?.name ?? "" });
            } else if (meta && e.key === "o" && picked) {
              e.preventDefault();
              openObject(asNode(picked));
            } else if (meta && e.key === "d" && picked && kind === ObjectKind.KindTable) {
              e.preventDefault();
              designTable(sel.connId, refOfObj(picked));
            } else if (meta && e.key === "d" && picked && isView) {
              e.preventDefault();
              viewDesign(sel.connId, refOfObj(picked));
            } else if (meta && e.key === "n" && kind === ObjectKind.KindTable) {
              e.preventDefault();
              newTable(sel.connId, sel.database!, sel.schema);
            } else if (meta && e.key === "n" && isView) {
              e.preventDefault();
              newView(sel.connId, sel.database!, sel.schema, kind === ObjectKind.KindMatView);
            } else if (meta && (e.key === "t" || (e.key === "q" && !/Mac/i.test(navigator.platform)))) {
              // Handled here (not globally) so the picked table becomes the SELECT.
              e.preventDefault();
              e.stopPropagation();
              const isData = picked && (picked.kind === ObjectKind.KindTable || picked.kind === ObjectKind.KindView || picked.kind === ObjectKind.KindMatView);
              newQuery(sel.connId, sel.database, sel.schema, isData ? selectAllSQL(sel.connId, refOfObj(picked!)) : "");
            } else if (meta && e.key === "r") {
              e.preventDefault();
              setReload((x) => x + 1);
            } else if (e.key === "Enter" && picked) {
              openObject(asNode(picked));
            }
          }}
        >
          {error && <div className="error-banner">{error}</div>}
          <table>
            <thead>
              <tr>
                <th onClick={() => setSortKey("name")}>Name</th>
                {showRows && <th className="num" onClick={() => setSortKey("rows")}>Rows</th>}
                {showRows && <th className="num" onClick={() => setSortKey("dataSize")}>Size</th>}
                {kind === ObjectKind.KindTable && sel.schema === undefined && <th>Engine</th>}
                {(kind === ObjectKind.KindFunction || kind === ObjectKind.KindProcedure) && <th>Returns</th>}
                <th onClick={() => setSortKey("modified")}>{showModified ? "Modified" : "Owner"}</th>
                <th>{isQueries ? "SQL" : "Comment"}</th>
              </tr>
            </thead>
            <tbody>
              {shown.map((o) => (
                <tr
                  key={o.name + (o.signature ?? "")}
                  className={pickedNames.includes(o.name) ? "selected" : ""}
                  onClick={(e) => clickRow(e, o)}
                  onDoubleClick={() => openObject(asNode(o))}
                  onContextMenu={(e) => {
                    const names = pickedNames.includes(o.name) ? pickedNames : [o.name];
                    if (!pickedNames.includes(o.name)) setPicked(o);
                    const refs = objects.filter((x) => names.includes(x.name)).map(refOfObj);
                    if (o.kind === ObjectKind.KindQuery) return openMenu(e, savedQueryMenu(sel.connId, sel.database!, sel.schema, names));
                    if (o.kind === ObjectKind.KindTable) return openMenu(e, tableMenu(sel.connId, refs));
                    if (o.kind === ObjectKind.KindView || o.kind === ObjectKind.KindMatView) return openMenu(e, viewMenu(sel.connId, refOfObj(o)));
                    if (o.kind === ObjectKind.KindIndex || (o.kind === ObjectKind.KindTrigger && connById(sel.connId)?.type === DBType.SQLite))
                      return openMenu(e, tableChildMenu(sel.connId, refOfObj(o), (o.comment ?? "").replace(/^ON /, "")));
                    openMenu(e, [
                      { label: "Show DDL", onClick: () => useApp.getState().openTab({ kind: "ddl", title: o.name, connId: sel.connId, database: sel.database, schema: o.schema || sel.schema, ref: refOfObj(o) }) },
                      { label: "New Query", onClick: () => newQuery(sel.connId, sel.database, sel.schema) },
                      { separator: true },
                      { label: "Copy Name", onClick: () => copyText(o.name) },
                    ]);
                  }}
                >
                  <td>
                    <KindIcon kind={o.kind} /> {o.name}
                    {o.signature ? <span className="muted">({o.signature})</span> : null}
                  </td>
                  {showRows && <td className="num">{(o.rows ?? 0).toLocaleString()}</td>}
                  {showRows && <td className="num">{formatBytes(o.dataSize ?? 0)}</td>}
                  {kind === ObjectKind.KindTable && sel.schema === undefined && <td>{o.engine}</td>}
                  {(kind === ObjectKind.KindFunction || kind === ObjectKind.KindProcedure) && <td>{o.returnType}</td>}
                  <td>{showModified ? o.modified || o.created : o.owner}</td>
                  <td className="muted">{o.comment || (o.engine && kind === ObjectKind.KindTrigger ? o.engine : "")}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {!loading && !shown.length && !error && <div className="center-hint">No {KIND_LABEL[kind!]?.toLowerCase()}.</div>}
        </div>
        }
        second={
          pickedList.length > 1 ? (
          <div className="info-panel empty">{pickedList.length} objects selected</div>
        ) : (
          <InfoPanel key={listKey} connId={sel.connId} object={picked} database={sel.database} schema={sel.schema} />
        )
        }
      />
    </div>
  );
}

function InfoPanel({ connId, object, database, schema }: { connId: string; object: ObjectInfo | null; database: string; schema?: string }) {
  const [ddl, setDdl] = useState("");
  const [err, setErr] = useState("");
  useEffect(() => {
    setDdl("");
    setErr("");
    if (!object) return;
    Explorer.DDL(connId, { ...object, database, schema: object.schema || schema })
      .then(setDdl)
      .catch((e) => setErr(errorText(e)));
  }, [connId, object, database, schema]);

  if (!object) return <div className="info-panel empty">Select an object to see its details.</div>;
  return (
    <div className="info-panel">
      <div className="info-head">
        <KindIcon kind={object.kind} size={16} />
        <strong>{object.name}</strong>
      </div>
      <dl>
        {!!object.rows && (
          <>
            <dt>Rows (est.)</dt>
            <dd>{object.rows.toLocaleString()}</dd>
          </>
        )}
        {!!object.dataSize && (
          <>
            <dt>Size</dt>
            <dd>{formatBytes(object.dataSize)}</dd>
          </>
        )}
        {object.engine && (
          <>
            <dt>{object.kind === ObjectKind.KindTrigger ? "Table" : object.kind === ObjectKind.KindType ? "Category" : "Engine"}</dt>
            <dd>{object.engine}</dd>
          </>
        )}
        {object.owner && (
          <>
            <dt>Owner</dt>
            <dd>{object.owner}</dd>
          </>
        )}
        {object.created && (
          <>
            <dt>Created</dt>
            <dd>{object.created}</dd>
          </>
        )}
        {object.modified && (
          <>
            <dt>Modified</dt>
            <dd>{object.modified}</dd>
          </>
        )}
        {object.comment && object.kind !== ObjectKind.KindQuery && (
          <>
            <dt>Comment</dt>
            <dd>{object.comment}</dd>
          </>
        )}
      </dl>
      <div className="info-ddl-head">
        {object.kind === ObjectKind.KindQuery ? "SQL" : "DDL"}
        <button className="icon" title="Copy DDL" onClick={() => copyText(ddl)} disabled={!ddl}>
          <Copy size={12} />
        </button>
      </div>
      {err ? <div className="err small">{err}</div> : <CodeBlock code={ddl} />}
    </div>
  );
}

/** Read-only syntax-highlighted SQL using Monaco's colorizer (no editor instance). */
export function CodeBlock({ code, language = "sql" }: { code: string; language?: string }) {
  const [html, setHtml] = useState("");
  useEffect(() => {
    let live = true;
    monaco.editor.setTheme(editorTheme());
    monaco.editor.colorize(code, language, { tabSize: 2 }).then((h) => live && setHtml(h));
    return () => {
      live = false;
    };
  }, [code, language]);
  return <pre className="code-block" dangerouslySetInnerHTML={{ __html: html }} />;
}

export function DDLView({ connId, objRef: r }: { connId: string; objRef: ObjectRef }) {
  const [ddl, setDdl] = useState("");
  const [err, setErr] = useState("");
  useEffect(() => {
    Explorer.DDL(connId, r).then(setDdl).catch((e) => setErr(errorText(e)));
  }, [connId, r]);
  return (
    <div className="ddl-view">
      <Toolbar>
        <button onClick={() => copyText(ddl)} disabled={!ddl}>
          <Copy size={14} /> Copy
        </button>
        <button onClick={() => newQuery(connId, r.database, r.schema, ddl)} disabled={!ddl}>
          <FilePlus2 size={14} /> Open in Query Editor
        </button>
      </Toolbar>
      {err ? <div className="error-banner">{err}</div> : <CodeBlock code={ddl} />}
    </div>
  );
}

function Welcome() {
  return (
    <div className="welcome">
      <h1>Chloe</h1>
      <p>Connect to MySQL, MariaDB, PostgreSQL, SQLite, MongoDB and Redis.</p>
      <button className="primary" onClick={() => useApp.getState().editConnection("new")}>
        New Connection
      </button>
    </div>
  );
}

export function formatBytes(n: number) {
  if (!n) return "";
  const u = ["B", "KB", "MB", "GB", "TB"];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) {
    n /= 1024;
    i++;
  }
  return `${n.toFixed(i ? 1 : 0)} ${u[i]}`;
}
