import { useVirtualizer } from "@tanstack/react-virtual";
import { ChevronDown, ChevronRight, Folder, Loader2, Search, TriangleAlert } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Connection, Connections, ObjectKind, ObjectRef, errorText } from "../api";
import { connById, dbKey, useApp } from "../store";
import { TreeNode, flatten, matchParts, useTree } from "../tree";
import { openMenu } from "./ContextMenu";
import { alert } from "./Dialogs";
import { ConnIcon, KindIcon } from "./icons";
import { restoreDrafts } from "../queries";
import { Drag, Drop, DropTarget, resolveDrop } from "../connDrag";
import { openObject, refOf } from "../actions/tabs";
import { emptyAreaMenu, menuFor, refreshSelected, reload } from "./treeMenus";

const ROW = 24;


export function Sidebar() {
  const connections = useApp((s) => s.connections);
  const groups = useApp((s) => s.groups);
  const open = useApp((s) => s.open);
  const openDbs = useApp((s) => s.openDbs);
  const selection = useApp((s) => s.selection);
  const { expanded, children, loading, errors, filter } = useTree();
  const scroller = useRef<HTMLDivElement>(null);

  // Groups are folders whose children are the connections in them.
  const { roots, groupChildren } = useMemo(() => {
    const connNode = (c: Connection, depth: number): TreeNode => ({ key: c.id, type: "conn", label: c.name, connId: c.id, depth, expandable: true });
    const groupChildren: Record<string, TreeNode[]> = {};
    const roots: TreeNode[] = groups.map((g) => {
      const key = "group:" + g;
      groupChildren[key] = connections.filter((c) => c.group === g).map((c) => connNode(c, 1));
      return { key, type: "group", group: g, label: g, connId: "", depth: 0, expandable: true };
    });
    roots.push(...connections.filter((c) => !c.group).map((c) => connNode(c, 0)));
    return { roots, groupChildren };
  }, [connections, groups]);

  const rows = useMemo(() => flatten(roots, expanded, { ...children, ...groupChildren }, filter), [roots, expanded, children, groupChildren, filter]);
  const v = useVirtualizer({ count: rows.length, getScrollElement: () => scroller.current, estimateSize: () => ROW, overscan: 20 });
  const selectedKey = selection ? selectionKey(selection) : "";

  // Dragging connections and groups to reorder them or move them between groups.
  const drag = useRef<Drag | null>(null);
  const [drop, setDrop] = useState<{ key: string; pos: Drop["pos"] } | null>(null);
  const dnd = useMemo<RowDnd>(() => {
    const resolve = (e: React.DragEvent, n?: TreeNode) => {
      const target: DropTarget | null = !n ? { type: "end" } : n.type === "conn" ? { type: "conn", id: n.connId } : n.type === "group" ? { type: "group", name: n.group } : null;
      if (!drag.current || !target) return null;
      const r = e.currentTarget.getBoundingClientRect();
      return resolveDrop(drag.current, target, (e.clientY - r.top) / r.height, connections, groups);
    };
    const end = () => {
      drag.current = null;
      setDrop(null);
    };
    return {
      start(e, n) {
        drag.current = n.type === "conn" ? { type: "conn", id: n.connId } : { type: "group", name: n.group! };
        e.dataTransfer.effectAllowed = "move";
        e.dataTransfer.setData("text/plain", n.label);
      },
      over(e, n) {
        e.stopPropagation();
        const d = resolve(e, n);
        setDrop((cur) => (!d ? null : cur?.key === (n?.key ?? "") && cur.pos === d.pos ? cur : { key: n?.key ?? "", pos: d.pos }));
        if (!d) return;
        e.preventDefault();
        e.dataTransfer.dropEffect = "move";
      },
      async drop(e, n) {
        e.preventDefault();
        e.stopPropagation();
        const d = resolve(e, n);
        end();
        if (!d) return;
        try {
          if ("conn" in d.move) await Connections.Move(d.move.conn, d.move.group, d.move.before);
          else await Connections.MoveGroup(d.move.group, d.move.before);
        } catch (err) {
          alert("Couldn't move", errorText(err));
        }
        await reload();
      },
      end,
    };
  }, [connections, groups]);

  // Reveal in Navigation Pane: scroll to the node and select it.
  const revealKey = useTree((s) => s.revealKey);
  useEffect(() => {
    if (!revealKey) return;
    const i = rows.findIndex((r) => r.key === revealKey);
    if (i < 0) return;
    v.scrollToIndex(i, { align: "center" });
    selectNode(rows[i], false);
    useTree.setState({ revealKey: null });
  }, [revealKey, rows, v]);

  return (
    <div className="sidebar">
      <div className="sidebar-search">
        <Search size={13} />
        <input placeholder="Search objects" value={filter} onChange={(e) => useTree.getState().setFilter(e.target.value)} />
      </div>
      <div
        className={"tree" + (drop?.pos === "end" ? " drop-end" : "")}
        ref={scroller}
        tabIndex={0}
        onContextMenu={(e) => openMenu(e, emptyAreaMenu())}
        onDragOver={(e) => dnd.over(e)}
        onDragLeave={(e) => {
          if (!e.currentTarget.contains(e.relatedTarget as Node)) setDrop(null);
        }}
        onDrop={(e) => dnd.drop(e)}
        onKeyDown={(e) => {
          if ((e.metaKey || e.ctrlKey) && e.key === "r") {
            e.preventDefault();
            refreshSelected();
          }
        }}
      >
        {connections.length > 0 && filter.trim() !== "" && rows.length === 0 && (
          <div className="empty-hint">No matches. Only opened databases are searched.</div>
        )}
        {connections.length === 0 && (
          <div className="empty-hint">
            No connections yet.
            <button className="link" onClick={() => useApp.getState().editConnection("new")}>
              Create a connection
            </button>
          </div>
        )}
        <div className="tree-rows" style={{ height: v.getTotalSize(), position: "relative" }}>
          {v.getVirtualItems().map((vi) => {
            const n = rows[vi.index];
            return (
              <TreeRow
                key={n.key}
                node={n}
                top={vi.start}
                expanded={n.type === "group" ? expanded[n.key] !== false : !!expanded[n.key]}
                loading={!!loading[n.key]}
                error={errors[n.key]}
                connected={!!open[n.connId]}
                dbOpen={n.type === "db" ? (n.count !== undefined ? n.count > 0 : !!openDbs[dbKey(n.connId, n.database)]) : true}
                selected={selectedKey === n.key}
                drop={drop?.key === n.key ? drop.pos : undefined}
                dnd={filter.trim() === "" ? dnd : undefined}
              />
            );
          })}
        </div>
      </div>
    </div>
  );
}

function selectionKey(s: { group?: string; connId: string; database?: string; schema?: string; kind?: ObjectKind; object?: ObjectRef }) {
  if (s.group !== undefined && !s.connId) return "group:" + s.group;
  let k = s.connId;
  if (s.database && connById(s.connId)?.type === "redis") return `${k}/db${s.database}`;
  if (s.database) k += "/" + s.database;
  if (s.schema) k += "/" + s.schema;
  if (s.kind) k += "/#" + s.kind;
  if (s.object) k += "/" + s.object.name + (s.object.signature ? `(${s.object.signature})` : "");
  return k;
}

/** Select a tree node (what a click does); showObjects switches to the Objects tab. */
function selectNode(n: TreeNode, showObjects = true) {
  const app = useApp.getState();
  if (n.type === "group") app.select({ group: n.group, connId: "" });
  else if (n.type === "object") app.select({ connId: n.connId, database: n.database, schema: n.schema, kind: n.kind, object: refOf(n) });
  else app.select({ connId: n.connId, database: n.database, schema: n.schema, kind: n.kind });
  if (showObjects && app.activeTab !== "objects" && n.type !== "object") app.setActive("objects");
}

type RowDnd = {
  start(e: React.DragEvent<HTMLElement>, n: TreeNode): void;
  over(e: React.DragEvent<HTMLElement>, n?: TreeNode): void;
  drop(e: React.DragEvent<HTMLElement>, n?: TreeNode): void;
  end(): void;
};

function TreeRow(props: {
  node: TreeNode;
  top: number;
  expanded: boolean;
  loading: boolean;
  error?: string;
  connected: boolean;
  dbOpen: boolean;
  selected: boolean;
  drop?: Drop["pos"];
  /** Set when connections and groups can be dragged (not while filtering). */
  dnd?: RowDnd;
}) {
  const { node: n, top, expanded, loading, error, connected, dbOpen, selected, drop, dnd } = props;
  const movable = !!dnd && (n.type === "conn" || n.type === "group");
  const conn = n.type === "conn" ? connById(n.connId) : undefined;

  const onClick = () => selectNode(n);

  const onDouble = () => {
    if (n.type === "object") openObject(n);
    else useTree.getState().toggle(n);
  };

  return (
    <div
      className={"tree-row" + (selected ? " selected" : "") + (drop ? " drop-" + drop : "")}
      style={{ top, height: ROW, paddingLeft: 6 + n.depth * 16 }}
      draggable={movable}
      onDragStart={movable ? (e) => dnd!.start(e, n) : undefined}
      onDragEnd={dnd?.end}
      onDragOver={dnd && ((e) => dnd.over(e, n))}
      onDrop={dnd && ((e) => dnd.drop(e, n))}
      onClick={onClick}
      onDoubleClick={onDouble}
      onContextMenu={(e) => {
        onClick();
        openMenu(e, menuFor(n, connected, dbOpen));
      }}
      title={error || n.info?.comment || conn?.remark || undefined}
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
      {n.type === "group" ? (
        <Folder size={14} className="kind-icon kind-group" />
      ) : n.type === "conn" && conn ? (
        <>
          <ConnIcon type={conn.type} preset={conn.preset} dim={!connected} />
          {conn.color && <span className="conn-color" style={{ background: conn.color }} />}
        </>
      ) : n.type === "db" ? (
        <span className={dbOpen ? "" : "dim-icon"}>
          <KindIcon kind={ObjectKind.KindDatabase} />
        </span>
      ) : n.type === "schema" ? (
        <KindIcon kind={ObjectKind.KindSchema} />
      ) : (
        <KindIcon kind={n.kind!} />
      )}
      <span className={"tree-label" + ((n.type === "conn" && !connected) || (n.type === "db" && !dbOpen) ? " dim" : "")}>
        <Highlighted label={n.label} />
      </span>
      {n.count !== undefined && <span className="tree-count">{n.count.toLocaleString()}</span>}
      {error && <TriangleAlert size={12} className="tree-error" />}
    </div>
  );
}

function connNode(c: Connection): TreeNode {
  return { key: c.id, type: "conn", label: c.name, connId: c.id, depth: 0, expandable: true };
}

/** Load connections on startup and open those marked "Auto connect". */
export function useInitialLoad() {
  useEffect(() => {
    (async () => {
      await useApp.getState().loadConnections();
      await restoreDrafts();
      for (const c of useApp.getState().connections) {
        if (c.advanced?.autoConnect) useTree.getState().expand(connNode(c));
      }
    })();
  }, []);
}

function Highlighted({ label }: { label: string }) {
  const filter = useTree((s) => s.filter);
  const parts = matchParts(label, filter);
  if (!parts) return <>{label}</>;
  return (
    <>
      {parts[0]}
      <mark className="tree-match">{parts[1]}</mark>
      {parts[2]}
    </>
  );
}
