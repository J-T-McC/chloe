import { useVirtualizer } from "@tanstack/react-virtual";
import { ChevronDown, ChevronRight, Loader2, Search, TriangleAlert } from "lucide-react";
import { useEffect, useMemo, useRef } from "react";
import { Connections, ObjectKind, ObjectRef } from "../api";
import { connById, useApp } from "../store";
import { TreeNode, flatten, useTree } from "../tree";
import { openMenu } from "./ContextMenu";
import { confirm } from "./Dialogs";
import { DBIcon, KindIcon } from "./icons";

const ROW = 24;

export function Sidebar() {
  const connections = useApp((s) => s.connections);
  const open = useApp((s) => s.open);
  const selection = useApp((s) => s.selection);
  const { expanded, children, loading, errors, filter } = useTree();
  const scroller = useRef<HTMLDivElement>(null);

  const roots: TreeNode[] = useMemo(
    () => connections.map((c) => ({ key: c.id, type: "conn", label: c.name, connId: c.id, depth: 0, expandable: true })),
    [connections],
  );
  const rows = useMemo(() => flatten(roots, expanded, children, filter), [roots, expanded, children, filter]);
  const v = useVirtualizer({ count: rows.length, getScrollElement: () => scroller.current, estimateSize: () => ROW, overscan: 20 });

  const selectedKey = selection ? selectionKey(selection) : "";

  return (
    <div className="sidebar">
      <div className="sidebar-search">
        <Search size={13} />
        <input placeholder="Search objects" value={filter} onChange={(e) => useTree.getState().setFilter(e.target.value)} />
      </div>
      <div className="tree" ref={scroller} tabIndex={0}>
        {connections.length === 0 && (
          <div className="empty-hint">
            No connections yet.
            <button className="link" onClick={() => useApp.getState().editConnection("new")}>
              Create a connection
            </button>
          </div>
        )}
        <div style={{ height: v.getTotalSize(), position: "relative" }}>
          {v.getVirtualItems().map((vi) => {
            const n = rows[vi.index];
            return (
              <TreeRow
                key={n.key}
                node={n}
                top={vi.start}
                expanded={!!expanded[n.key]}
                loading={!!loading[n.key]}
                error={errors[n.key]}
                connected={!!open[n.connId]}
                selected={selectedKey === n.key}
              />
            );
          })}
        </div>
      </div>
    </div>
  );
}

function selectionKey(s: { connId: string; database?: string; schema?: string; kind?: ObjectKind }) {
  let k = s.connId;
  if (s.database) k += "/" + s.database;
  if (s.schema) k += "/" + s.schema;
  if (s.kind) k += "/#" + s.kind;
  return k;
}

function refOf(n: TreeNode): ObjectRef {
  return { database: n.database!, schema: n.schema, name: n.info!.name, kind: n.kind!, signature: n.info?.signature };
}

function TreeRow(props: { node: TreeNode; top: number; expanded: boolean; loading: boolean; error?: string; connected: boolean; selected: boolean }) {
  const { node: n, top, expanded, loading, error, connected, selected } = props;
  const conn = connById(n.connId);

  const onClick = () => {
    const app = useApp.getState();
    if (n.type === "object") {
      app.select({ connId: n.connId, database: n.database, schema: n.schema, kind: n.kind, object: refOf(n) });
    } else {
      app.select({ connId: n.connId, database: n.database, schema: n.schema, kind: n.kind });
    }
    if (app.activeTab !== "objects" && n.type !== "object") app.setActive("objects");
  };

  const onDouble = () => {
    if (n.type === "object") openObject(n);
    else useTree.getState().toggle(n);
  };

  return (
    <div
      className={"tree-row" + (selected ? " selected" : "")}
      style={{ top, height: ROW, paddingLeft: 6 + n.depth * 16 }}
      onClick={onClick}
      onDoubleClick={onDouble}
      onContextMenu={(e) => openMenu(e, menuFor(n, connected))}
      title={error || n.info?.comment || undefined}
    >
      <span
        className="twisty"
        onClick={(e) => {
          e.stopPropagation();
          if (n.expandable) useTree.getState().toggle(n);
        }}
      >
        {n.expandable ? loading ? <Loader2 size={12} className="spin" /> : expanded ? <ChevronDown size={12} /> : <ChevronRight size={12} /> : null}
      </span>
      {n.type === "conn" && conn ? (
        <>
          <DBIcon type={conn.type} dim={!connected} />
          {conn.color && <span className="conn-color" style={{ background: conn.color }} />}
        </>
      ) : n.type === "db" ? (
        <KindIcon kind={ObjectKind.KindDatabase} />
      ) : n.type === "schema" ? (
        <KindIcon kind={ObjectKind.KindSchema} />
      ) : (
        <KindIcon kind={n.kind!} />
      )}
      <span className={"tree-label" + (n.type === "conn" && !connected ? " dim" : "")}>{n.label}</span>
      {error && <TriangleAlert size={12} className="tree-error" />}
    </div>
  );
}

export function openObject(n: TreeNode) {
  const app = useApp.getState();
  const ref = refOf(n);
  if (n.kind === ObjectKind.KindTable || n.kind === ObjectKind.KindView || n.kind === ObjectKind.KindMatView) {
    app.openTab({ kind: "table", title: n.info!.name, connId: n.connId, database: n.database, schema: n.schema, ref });
  } else {
    app.openTab({ kind: "ddl", title: n.info!.name, connId: n.connId, database: n.database, schema: n.schema, ref });
  }
}

export function newQuery(connId: string, database?: string, schema?: string, sql = "") {
  const app = useApp.getState();
  const conn = connById(connId);
  app.openTab({ kind: "query", title: `Query — ${database ?? conn?.name ?? ""}`, connId, database, schema, sql });
}

function menuFor(n: TreeNode, connected: boolean) {
  const tree = useTree.getState();
  const app = useApp.getState();
  const copy = (s: string) => navigator.clipboard.writeText(s);
  switch (n.type) {
    case "conn":
      return [
        connected
          ? { label: "Close Connection", onClick: () => { app.disconnect(n.connId); tree.collapseConnection(n.connId); } }
          : { label: "Open Connection", onClick: () => tree.expand(n) },
        { label: "Edit Connection…", onClick: () => app.editConnection(app.connections.find((c) => c.id === n.connId)) },
        { label: "New Query", disabled: !connected, onClick: () => newQuery(n.connId) },
        { separator: true },
        { label: "Refresh", disabled: !connected, onClick: () => tree.refresh(n) },
        { separator: true },
        {
          label: "Delete Connection…",
          danger: true,
          onClick: async () => {
            if (await confirm(`Delete connection “${n.label}”?`, { message: "The saved connection and its stored passwords will be removed.", danger: true, okLabel: "Delete" })) {
              await Connections.Delete(n.connId);
              tree.collapseConnection(n.connId);
              await app.loadConnections();
            }
          },
        },
      ];
    case "db":
    case "schema":
      return [
        { label: "New Query", onClick: () => newQuery(n.connId, n.database, n.schema) },
        { label: "Refresh", onClick: () => tree.refresh(n) },
        { separator: true },
        { label: "Copy Name", onClick: () => copy(n.label) },
      ];
    case "folder":
      return [
        { label: "New Query", onClick: () => newQuery(n.connId, n.database, n.schema) },
        { label: "Refresh", onClick: () => tree.refresh(n) },
      ];
    case "object": {
      const isData = n.kind === ObjectKind.KindTable || n.kind === ObjectKind.KindView || n.kind === ObjectKind.KindMatView;
      return [
        ...(isData ? [{ label: n.kind === ObjectKind.KindTable ? "Open Table" : "Open View", onClick: () => openObject(n) }] : []),
        { label: "Show DDL", onClick: () => app.openTab({ kind: "ddl", title: n.info!.name, connId: n.connId, database: n.database, schema: n.schema, ref: refOf(n) }) },
        { label: "New Query", onClick: () => newQuery(n.connId, n.database, n.schema) },
        { separator: true },
        { label: "Copy Name", onClick: () => copy(n.info!.name) },
      ];
    }
  }
}

/** Load the connection list once on startup. */
export function useInitialLoad() {
  useEffect(() => {
    useApp.getState().loadConnections();
  }, []);
}
