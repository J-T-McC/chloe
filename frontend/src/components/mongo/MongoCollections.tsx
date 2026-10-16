// The Objects tab for a MongoDB database: its collections or views with sizes.
import { Download, FilePlus2, PencilRuler, Plus, RefreshCw, Table2, TerminalSquare, Trash2, Upload } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Mongo, ObjectKind, arr, errorText, type MG } from "../../api";
import { formatVal, humanBytes } from "../../mongo/docs";
import { connById, dbKey, useApp } from "../../store";
import { useTree } from "../../tree";
import { openMenu } from "../ContextMenu";
import { KindIcon } from "../icons";
import { Split } from "../Split";
import { CodeBlock } from "../ObjectsPane";
import { exportCollection, importCollection } from "./TransferDialog";
import { collectionMenu, collectionsChanged, designCollection, dropCollections, newMongoQuery, openCollection, openMongoShell } from "./actions";
import { Toolbar } from "../Toolbar";

type SortKey = "name" | "count" | "size" | "storageSize" | "indexes";

export function MongoCollections({ connId, db, kind }: { connId: string; db: string; kind: ObjectKind }) {
  const [list, setList] = useState<MG.CollInfo[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const [picked, setPicked] = useState<string[]>([]);
  const [filter, setFilter] = useState("");
  const [sortKey, setSortKey] = useState<SortKey>("name");
  const objectsVersion = useApp((s) => s.objectsVersion);
  const dbOpen = useApp((s) => !!s.openDbs[dbKey(connId, db)]);
  const view = kind === ObjectKind.KindView;

  useEffect(() => {
    setPicked([]);
    if (!dbOpen) return;
    setLoading(true);
    setError("");
    Mongo.Collections(connId, db, true)
      .then((l) => setList(arr(l)))
      .catch((e) => setError(errorText(e)))
      .finally(() => setLoading(false));
  }, [connId, db, dbOpen, objectsVersion]);

  const shown = useMemo(() => {
    const f = filter.toLowerCase();
    return list
      .filter((c) => (c.type === "view") === view && (!f || c.name.toLowerCase().includes(f)))
      .sort((a, b) => (sortKey === "name" ? a.name.localeCompare(b.name) : (b[sortKey] as number) - (a[sortKey] as number)));
  }, [list, filter, sortKey, view]);
  const one = picked.length === 1 ? list.find((c) => c.name === picked[0]) : undefined;

  if (!dbOpen)
    return (
      <div className="center-hint">
        Database “{db}” is closed.
        <div>
          <button onClick={() => useTree.getState().expand({ key: `${connId}/${db}`, type: "db", label: db, connId, database: db, depth: 1, expandable: true })}>Open Database</button>
        </div>
      </div>
    );

  const what = view ? "View" : "Collection";
  const click = (e: React.MouseEvent, name: string) => {
    if (e.metaKey || e.ctrlKey) setPicked((p) => (p.includes(name) ? p.filter((x) => x !== name) : [...p, name]));
    else setPicked([name]);
  };

  return (
    <div className="objects-pane">
      <Toolbar>
        <button disabled={!one} onClick={() => one && openCollection(connId, db, one.name, kind)}>
          <Table2 size={14} /> Open {what}
        </button>
        <button disabled={!one} onClick={() => one && designCollection(connId, db, one.name, kind)}>
          <PencilRuler size={14} /> Design {what}
        </button>
        <button onClick={() => designCollection(connId, db, undefined, kind)}>
          <Plus size={14} /> New {what}
        </button>
        <button disabled={!picked.length} onClick={() => dropCollections(connId, db, picked, kind)}>
          <Trash2 size={14} /> Delete {what}
        </button>
        <span className="tb-sep" />
        {!view && (
          <button title="Import Wizard" disabled={!one} onClick={() => one && importCollection(connId, db, one.name)}>
            <Download size={14} /> Import
          </button>
        )}
        <button title="Export Wizard" disabled={!one} onClick={() => one && exportCollection(connId, db, one.name)}>
          <Upload size={14} /> Export
        </button>
        <button onClick={() => newMongoQuery(connId, db, one ? `db.getCollection(${JSON.stringify(one.name)}).find({})` : "")}>
          <FilePlus2 size={14} /> New Query
        </button>
        <button onClick={() => openMongoShell(connId, db)}>
          <TerminalSquare size={14} /> MongoShell
        </button>
        <button title="Refresh" onClick={() => collectionsChanged(connId, db)}>
          <RefreshCw size={14} className={loading ? "spin" : ""} />
        </button>
        <span className="tb-sep" />
        <span className="crumbs">{[connById(connId)?.name, db, view ? "Views" : "Collections"].join(" › ")}</span>
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
            onMouseDown={(e) => e.target === e.currentTarget && setPicked([])}
            onContextMenu={(e) => {
              if (e.target === e.currentTarget)
                openMenu(e, [
                  { label: `New ${what}`, onClick: () => designCollection(connId, db, undefined, kind) },
                  { label: "New Query", onClick: () => newMongoQuery(connId, db) },
                  { label: "Refresh", onClick: () => collectionsChanged(connId, db) },
                ]);
            }}
            onKeyDown={(e) => {
              if (e.key === "Enter" && one) openCollection(connId, db, one.name, kind);
              if ((e.metaKey || e.ctrlKey) && e.key === "a") {
                e.preventDefault();
                setPicked(shown.map((c) => c.name));
              }
            }}
          >
            {error && <div className="error-banner">{error}</div>}
            <table>
              <thead>
                <tr>
                  <th onClick={() => setSortKey("name")}>Name</th>
                  {!view && (
                    <>
                      <th className="num" onClick={() => setSortKey("count")}>Documents</th>
                      <th className="num" onClick={() => setSortKey("size")}>Data Size</th>
                      <th className="num" onClick={() => setSortKey("storageSize")}>Storage Size</th>
                      <th className="num">Avg Doc Size</th>
                      <th className="num" onClick={() => setSortKey("indexes")}>Indexes</th>
                      <th>Type</th>
                    </>
                  )}
                  {view && <th>View On</th>}
                </tr>
              </thead>
              <tbody>
                {shown.map((c) => (
                  <tr
                    key={c.name}
                    className={picked.includes(c.name) ? "selected" : ""}
                    onClick={(e) => click(e, c.name)}
                    onDoubleClick={() => openCollection(connId, db, c.name, kind)}
                    onContextMenu={(e) => {
                      const names = picked.includes(c.name) ? picked : [c.name];
                      if (!picked.includes(c.name)) setPicked([c.name]);
                      openMenu(e, collectionMenu(connId, db, names, kind));
                    }}
                  >
                    <td>
                      <KindIcon kind={kind} /> {c.name}
                    </td>
                    {!view && (
                      <>
                        <td className="num">{c.count.toLocaleString()}</td>
                        <td className="num">{humanBytes(c.size)}</td>
                        <td className="num">{humanBytes(c.storageSize)}</td>
                        <td className="num">{humanBytes(c.avgObjSize)}</td>
                        <td className="num">{c.indexes}</td>
                        <td>{c.type === "timeseries" ? "time series" : c.capped ? "capped" : ""}</td>
                      </>
                    )}
                    {view && <td>{c.viewOn}</td>}
                  </tr>
                ))}
              </tbody>
            </table>
            {!loading && !shown.length && !error && <div className="center-hint">No {view ? "views" : "collections"}.</div>}
          </div>
        }
        second={
          one ? (
            <div className="info-panel">
              <div className="info-head">
                <KindIcon kind={kind} size={16} />
                <strong>{one.name}</strong>
              </div>
              <dl>
                {!view && (
                  <>
                    <dt>Documents</dt>
                    <dd>{one.count.toLocaleString()}</dd>
                    <dt>Data Size</dt>
                    <dd>{humanBytes(one.size)}</dd>
                    <dt>Storage Size</dt>
                    <dd>{humanBytes(one.storageSize)}</dd>
                    <dt>Indexes</dt>
                    <dd>
                      {one.indexes} ({humanBytes(one.indexSize)})
                    </dd>
                  </>
                )}
                {view && (
                  <>
                    <dt>View On</dt>
                    <dd>{one.viewOn}</dd>
                  </>
                )}
              </dl>
              <div className="info-ddl-head">Options</div>
              <CodeBlock code={formatVal(one.options, "  ")} language="mongo" />
            </div>
          ) : (
            <div className="info-panel empty">{picked.length > 1 ? `${picked.length} ${view ? "views" : "collections"} selected` : ""}</div>
          )
        }
      />
    </div>
  );
}
