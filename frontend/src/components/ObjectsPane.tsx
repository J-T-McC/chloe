import { Copy, FilePlus2, PencilRuler, Plus, RefreshCw, Table2, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Explorer, ObjectInfo, ObjectKind, ObjectRef, TableAction, arr, errorText } from "../api";
import { designTable, newTable, runTableAction } from "../tableActions";
import { editorTheme, monaco } from "../editor/monaco";
import { connById, dbKey, useApp } from "../store";
import { TreeNode, useTree } from "../tree";
import { openMenu } from "./ContextMenu";
import { KIND_LABEL, KindIcon } from "./icons";
import { newQuery, openObject } from "./Sidebar";

export function ObjectsPane() {
  const sel = useApp((s) => s.selection);
  const open = useApp((s) => s.open);
  const [objects, setObjects] = useState<ObjectInfo[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  // The picked object remembers which listing it came from, so a stale pick is never
  // shown (or queried) against a different connection or database.
  const [pick, setPick] = useState<{ key: string; obj: ObjectInfo } | null>(null);
  const [filter, setFilter] = useState("");
  const [sortKey, setSortKey] = useState<"name" | "rows" | "dataSize" | "modified">("name");
  const [reload, setReload] = useState(0);
  const objectsVersion = useApp((s) => s.objectsVersion);

  const kind = sel?.kind ?? (sel?.database ? ObjectKind.KindTable : undefined);
  const connected = sel?.connId ? !!open[sel.connId] : false;
  const dbOpen = useApp((s) => !!(sel?.database && s.openDbs[dbKey(sel.connId, sel.database)]));
  const needsSchema = connById(sel?.connId)?.type === "postgres" && !sel?.schema;
  const listKey = [sel?.connId, sel?.database, sel?.schema, kind].join("/");
  const picked = pick?.key === listKey ? pick.obj : null;
  const setPicked = (o: ObjectInfo | null) => setPick(o ? { key: listKey, obj: o } : null);

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
  const showRows = kind === ObjectKind.KindTable || kind === ObjectKind.KindMatView;

  return (
    <div className="objects-pane">
      <div className="toolbar">
        <button disabled={!picked} onClick={() => picked && openObject(asNode(picked))}>
          <Table2 size={14} /> {isData ? "Open" : "View DDL"}
        </button>
        {kind === ObjectKind.KindTable && (
          <>
            <button disabled={!picked} onClick={() => picked && designTable(sel.connId, { ...picked, database: sel.database!, schema: picked.schema || sel.schema })}>
              <PencilRuler size={14} /> Design Table
            </button>
            <button onClick={() => newTable(sel.connId, sel.database!, sel.schema)}>
              <Plus size={14} /> New Table
            </button>
            <button disabled={!picked} onClick={() => picked && runTableAction(sel.connId, { ...picked, database: sel.database!, schema: picked.schema || sel.schema }, TableAction.ActionDrop)}>
              <Trash2 size={14} /> Delete Table
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
      </div>
      <div className="objects-split">
        <div className="object-list">
          {error && <div className="error-banner">{error}</div>}
          <table>
            <thead>
              <tr>
                <th onClick={() => setSortKey("name")}>Name</th>
                {showRows && <th className="num" onClick={() => setSortKey("rows")}>Rows</th>}
                {showRows && <th className="num" onClick={() => setSortKey("dataSize")}>Size</th>}
                {kind === ObjectKind.KindTable && sel.schema === undefined && <th>Engine</th>}
                {(kind === ObjectKind.KindFunction || kind === ObjectKind.KindProcedure) && <th>Returns</th>}
                <th onClick={() => setSortKey("modified")}>{sel.schema === undefined ? "Modified" : "Owner"}</th>
                <th>Comment</th>
              </tr>
            </thead>
            <tbody>
              {shown.map((o) => (
                <tr
                  key={o.name + (o.signature ?? "")}
                  className={picked === o ? "selected" : ""}
                  onClick={() => setPicked(o)}
                  onDoubleClick={() => openObject(asNode(o))}
                  onContextMenu={(e) => {
                    setPicked(o);
                    openMenu(e, [
                      ...(isData ? [{ label: "Open", onClick: () => openObject(asNode(o)) }] : []),
                      { label: "Show DDL", onClick: () => useApp.getState().openTab({ kind: "ddl", title: o.name, connId: sel.connId, database: sel.database, schema: o.schema || sel.schema, ref: o }) },
                      { label: "New Query", onClick: () => newQuery(sel.connId, sel.database, sel.schema) },
                      { separator: true },
                      { label: "Copy Name", onClick: () => navigator.clipboard.writeText(o.name) },
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
                  <td>{sel.schema === undefined ? o.modified || o.created : o.owner}</td>
                  <td className="muted">{o.comment || (o.engine && kind === ObjectKind.KindTrigger ? o.engine : "")}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {!loading && !shown.length && !error && <div className="center-hint">No {KIND_LABEL[kind!]?.toLowerCase()}.</div>}
        </div>
        <InfoPanel key={listKey} connId={sel.connId} object={picked} database={sel.database} schema={sel.schema} />
      </div>
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
        {object.comment && (
          <>
            <dt>Comment</dt>
            <dd>{object.comment}</dd>
          </>
        )}
      </dl>
      <div className="info-ddl-head">
        DDL
        <button className="icon" title="Copy DDL" onClick={() => navigator.clipboard.writeText(ddl)} disabled={!ddl}>
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
      <div className="toolbar">
        <button onClick={() => navigator.clipboard.writeText(ddl)} disabled={!ddl}>
          <Copy size={14} /> Copy
        </button>
        <button onClick={() => newQuery(connId, r.database, r.schema, ddl)} disabled={!ddl}>
          <FilePlus2 size={14} /> Open in Query Editor
        </button>
      </div>
      {err ? <div className="error-banner">{err}</div> : <CodeBlock code={ddl} />}
    </div>
  );
}

function Welcome() {
  return (
    <div className="welcome">
      <h1>Chloe</h1>
      <p>Connect to MySQL, MariaDB and PostgreSQL. MongoDB, Redis and more are on the way.</p>
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
