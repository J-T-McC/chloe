import { useVirtualizer } from "@tanstack/react-virtual";
import { ChevronDown, ChevronRight, Folder, Loader2, Search, TriangleAlert } from "lucide-react";
import { useEffect, useMemo, useRef } from "react";
import { Connection, Connections, DBType, ObjectKind, ObjectRef, Redis, errorText } from "../api";
import { openRedisConsole } from "./redis/actions";
import { connById, dbKey, useApp } from "../store";
import { TreeNode, flatten, matchParts, useTree } from "../tree";
import { MenuItem, openMenu } from "./ContextMenu";
import { alert, confirm, prompt } from "./Dialogs";
import { ConnIcon, KindIcon } from "./icons";
import { newTable } from "../tableActions";
import { databaseTools, objectsAreaMenu, tableMenu, viewMenu } from "../menus";
import { deleteDatabase, openDatabaseDialog } from "./DatabaseDialog";
import { openExecuteFile } from "./transfer/state";

const ROW = 24;
export const COLORS: Array<[string, string]> = [
  ["", "None"],
  ["#e5534b", "Red"],
  ["#e8963c", "Orange"],
  ["#d6b63a", "Yellow"],
  ["#4fae5a", "Green"],
  ["#3b8eea", "Blue"],
  ["#9a6bd8", "Purple"],
  ["#8b8f98", "Gray"],
];

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
        className="tree"
        ref={scroller}
        tabIndex={0}
        onContextMenu={(e) => openMenu(e, emptyAreaMenu())}
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
        <div style={{ height: v.getTotalSize(), position: "relative" }}>
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
                dbOpen={n.type === "db" ? (n.count !== undefined ? n.count > 0 : !!openDbs[dbKey(n.connId, n.database!)]) : true}
                selected={selectedKey === n.key}
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

function refOf(n: TreeNode): ObjectRef {
  return { database: n.database!, schema: n.schema, name: n.info!.name, kind: n.kind!, signature: n.info?.signature };
}

function TreeRow(props: { node: TreeNode; top: number; expanded: boolean; loading: boolean; error?: string; connected: boolean; dbOpen: boolean; selected: boolean }) {
  const { node: n, top, expanded, loading, error, connected, dbOpen, selected } = props;
  const conn = n.type === "conn" ? connById(n.connId) : undefined;

  const onClick = () => selectNode(n);

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
  const db = database ?? (conn?.initialDatabase || undefined);
  // A query tab works in its database, so that database counts as open.
  if (db) app.openDatabase(connId, db);
  app.openTab({ kind: "query", title: `Query — ${db ?? conn?.name ?? ""}`, connId, database: db, schema, sql });
}

/** "SELECT * FROM table;" with the table quoted for the connection's dialect. */
export function selectAllSQL(connId: string, ref: ObjectRef) {
  const pg = connById(connId)?.type === "postgres";
  const q = pg ? '"' : "`";
  const qi = (s: string) => (/^[a-z_][a-z0-9_]*$/.test(s) ? s : q + s.split(q).join(q + q) + q);
  const name = pg && ref.schema && ref.schema !== "public" ? `${qi(ref.schema)}.${qi(ref.name)}` : qi(ref.name);
  return `SELECT * FROM ${name};`;
}

export function openMonitor(connId: string) {
  const conn = connById(connId);
  useApp.getState().openTab({ kind: "monitor", title: `Server Monitor — ${conn?.name ?? ""}`, connId, ref: { database: "", name: "monitor", kind: ObjectKind.$zero } });
}

// ---- actions ---------------------------------------------------------------------

const copy = (s: string) => navigator.clipboard.writeText(s);

async function reload() {
  await useApp.getState().loadConnections();
}

function connNode(c: Connection): TreeNode {
  return { key: c.id, type: "conn", label: c.name, connId: c.id, depth: 0, expandable: true };
}

export async function newGroup() {
  const name = await prompt("New Group", { message: "Group name" });
  if (!name?.trim()) return;
  try {
    await Connections.AddGroup(name.trim());
    await reload();
  } catch (e) {
    alert("Couldn't create group", errorText(e));
  }
}

export async function refreshSelected() {
  const app = useApp.getState();
  const sel = app.selection;
  const tree = useTree.getState();
  if (!sel?.connId) return reload();
  let key = sel.connId;
  if (sel.database) key += "/" + sel.database;
  if (sel.schema) key += "/" + sel.schema;
  if (sel.kind) key += "/#" + sel.kind;
  const type = sel.kind ? "folder" : sel.schema ? "schema" : sel.database ? "db" : "conn";
  await tree.refresh({ key, type, label: "", connId: sel.connId, database: sel.database, schema: sel.schema, kind: sel.kind, depth: 0, expandable: true });
}

async function showInfo(connId: string) {
  try {
    const i = await Connections.Info(connId);
    const c = connById(connId);
    alert(c?.name ?? "Connection", [`Server version: ${i.version}`, `Host: ${i.host}`, `User: ${i.user}`, `Route: ${i.via}`, `SSL: ${i.ssl ? "yes" : "no"}`, `Open server sessions: ${i.sessions}`].join("\n"));
  } catch (e) {
    alert("Connection info", errorText(e));
  }
}

async function deleteConnection(c: Connection) {
  if (!(await confirm(`Delete connection “${c.name}”?`, { message: "The saved connection and its stored passwords will be removed.", danger: true, okLabel: "Delete" }))) return;
  useApp.getState().disconnect(c.id);
  useTree.getState().collapseConnection(c.id);
  await Connections.Delete(c.id);
  await reload();
}

function emptyAreaMenu(): MenuItem[] {
  const app = useApp.getState();
  return [
    { label: "New Connection…", onClick: () => app.editConnection("new") },
    { label: "Close All Connections", disabled: !Object.keys(app.open).length, onClick: () => { app.disconnectAll(); for (const c of app.connections) useTree.getState().collapseConnection(c.id); } },
    { label: "Manage Connections…", onClick: () => app.setManaging(true) },
    { separator: true },
    { label: "New Group…", onClick: newGroup },
    { separator: true },
    { label: "Refresh", shortcut: "⌘R", onClick: reload },
  ];
}

function menuFor(n: TreeNode, connected: boolean, dbOpen: boolean): MenuItem[] {
  const tree = useTree.getState();
  const app = useApp.getState();
  switch (n.type) {
    case "group":
      return [
        { label: "New Connection…", onClick: () => app.editConnection("new") },
        { separator: true },
        {
          label: "Rename Group…",
          onClick: async () => {
            const name = await prompt("Rename Group", { defaultValue: n.group });
            if (name?.trim() && name.trim() !== n.group) {
              await Connections.RenameGroup(n.group!, name.trim()).catch((e) => alert("Couldn't rename group", errorText(e)));
              await reload();
            }
          },
        },
        {
          label: "Delete Group",
          danger: true,
          onClick: async () => {
            if (await confirm(`Delete group “${n.group}”?`, { message: "Its connections move to the top level; nothing else is deleted.", okLabel: "Delete Group" })) {
              await Connections.DeleteGroup(n.group!);
              await reload();
            }
          },
        },
        { separator: true },
        { label: "New Group…", onClick: newGroup },
      ];
    case "conn": {
      const c = connById(n.connId)!;
      if (c.type === DBType.Redis) return redisConnMenu(n, c, connected);
      return [
        connected
          ? { label: "Close Connection", onClick: () => { app.disconnect(n.connId); tree.collapseConnection(n.connId); } }
          : { label: "Open Connection", onClick: () => tree.expand(n) },
        { label: "Edit Connection…", onClick: () => app.editConnection(c) },
        { label: "New Connection…", onClick: () => app.editConnection("new") },
        { label: "Delete Connection…", danger: true, onClick: () => deleteConnection(c) },
        { label: "Duplicate Connection", onClick: async () => { await Connections.Duplicate(c.id); await reload(); } },
        { separator: true },
        { label: "New Database…", disabled: !connected, onClick: () => openDatabaseDialog(n.connId) },
        { label: "New Query", disabled: !connected, shortcut: "⌘T", onClick: () => newQuery(n.connId) },
        { separator: true },
        { label: "Execute SQL File…", disabled: !connected, onClick: () => openExecuteFile(n.connId, "") },
        { label: "Server Monitor", disabled: !connected, onClick: () => openMonitor(n.connId) },
        { label: "Connection Info…", disabled: !connected, onClick: () => showInfo(n.connId) },
        { separator: true },
        {
          label: "Connection Color",
          submenu: COLORS.map(([color, name]) => ({
            label: name,
            swatch: color,
            checked: (c.color ?? "") === color,
            onClick: async () => {
              const full = await Connections.Get(c.id);
              await Connections.Save({ ...full, color });
              await reload();
            },
          })),
        },
        {
          label: "Move to Group",
          submenu: [
            { label: "(none)", checked: !c.group, onClick: async () => { await Connections.MoveToGroup(c.id, ""); await reload(); } },
            ...app.groups.map((g) => ({ label: g, checked: c.group === g, onClick: async () => { await Connections.MoveToGroup(c.id, g); await reload(); } })),
            { separator: true },
            {
              label: "New Group…",
              onClick: async () => {
                const g = await prompt("New Group", { message: "Group name" });
                if (g?.trim()) {
                  await Connections.MoveToGroup(c.id, g.trim());
                  await reload();
                }
              },
            },
          ],
        },
        { label: "Copy Connection URL", onClick: async () => copy(await Connections.URL(c.id)) },
        { separator: true },
        { label: "Refresh", shortcut: "⌘R", disabled: !connected, onClick: () => tree.refresh(n) },
      ];
    }
    case "db":
      if (n.count !== undefined) return redisDbMenu(n);
      return [
        dbOpen
          ? { label: "Close Database", onClick: () => tree.closeDatabase(n) }
          : { label: "Open Database", onClick: () => tree.expand(n) },
        { label: "New Database…", onClick: () => openDatabaseDialog(n.connId) },
        { label: "Edit Database…", onClick: () => openDatabaseDialog(n.connId, n.database) },
        { label: "Delete Database", onClick: () => deleteDatabase(n.connId, n.database!) },
        { separator: true },
        { label: "New Query", onClick: () => newQuery(n.connId, n.database) },
        ...(isPGConn(n.connId) ? [] : [{ label: "New Table", onClick: () => newTable(n.connId, n.database!) }]),
        { separator: true },
        ...(isPGConn(n.connId) ? [{ label: "Execute SQL File…", onClick: () => openExecuteFile(n.connId, n.database!) }] : databaseTools(n.connId, n.database!)),
        { separator: true },
        { label: "Copy Name", onClick: () => copy(n.label) },
        { label: "Refresh", shortcut: "⌘R", disabled: !dbOpen, onClick: () => tree.refresh(n) },
      ];
    case "schema":
      return [
        { label: "New Query", onClick: () => newQuery(n.connId, n.database, n.schema) },
        { label: "New Table", onClick: () => newTable(n.connId, n.database!, n.schema) },
        { separator: true },
        ...databaseTools(n.connId, n.database!, n.schema),
        { separator: true },
        { label: "Copy Name", onClick: () => copy(n.label) },
        { label: "Refresh", shortcut: "⌘R", onClick: () => tree.refresh(n) },
      ];
    case "folder":
      if (n.kind === ObjectKind.KindTable || n.kind === ObjectKind.KindView || n.kind === ObjectKind.KindMatView) return [...objectsAreaMenu(n.connId, n.database!, n.schema, n.kind).slice(0, -1), { label: "Refresh", shortcut: "⌘R", onClick: () => tree.refresh(n) }];
      return [
        { label: "New Query", onClick: () => newQuery(n.connId, n.database, n.schema) },
        { label: "Refresh", shortcut: "⌘R", onClick: () => tree.refresh(n) },
      ];
    case "object": {
      const ref = refOf(n);
      if (n.kind === ObjectKind.KindTable) return tableMenu(n.connId, [ref]);
      if (n.kind === ObjectKind.KindView || n.kind === ObjectKind.KindMatView) return viewMenu(n.connId, ref);
      return [
        { label: "Show DDL", onClick: () => app.openTab({ kind: "ddl", title: n.info!.name, connId: n.connId, database: n.database, schema: n.schema, ref }) },
        { label: "New Query", onClick: () => newQuery(n.connId, n.database, n.schema) },
        { separator: true },
        { label: "Copy Name", onClick: () => copy(n.info!.name) },
      ];
    }
  }
}

const isPGConn = (id: string) => connById(id)?.type === "postgres";

function redisConnMenu(n: TreeNode, c: Connection, connected: boolean): MenuItem[] {
  const app = useApp.getState();
  const tree = useTree.getState();
  return [
    connected
      ? { label: "Close Connection", onClick: () => { app.disconnect(n.connId); tree.collapseConnection(n.connId); } }
      : { label: "Open Connection", onClick: () => tree.expand(n) },
    { label: "Edit Connection…", onClick: () => app.editConnection(c) },
    { label: "New Connection…", onClick: () => app.editConnection("new") },
    { label: "Delete Connection…", onClick: () => deleteConnection(c) },
    { label: "Duplicate Connection", onClick: async () => { await Connections.Duplicate(c.id); await useApp.getState().loadConnections(); } },
    { separator: true },
    { label: "Console", disabled: !connected, onClick: () => openRedisConsole(n.connId, 0) },
    { label: "Server Monitor", disabled: !connected, onClick: () => openMonitor(n.connId) },
    { label: "Connection Info…", disabled: !connected, onClick: () => showInfo(n.connId) },
    { separator: true },
    { label: "Copy Connection URL", onClick: async () => copy(await Connections.URL(c.id)) },
    { separator: true },
    { label: "Refresh", shortcut: "⌘R", disabled: !connected, onClick: () => tree.refresh(n) },
  ];
}

function redisDbMenu(n: TreeNode): MenuItem[] {
  const db = Number(n.database);
  const refreshConn = () => useTree.getState().refresh({ key: n.connId, type: "conn", label: "", connId: n.connId, depth: 0, expandable: true });
  return [
    { label: "Open Database", onClick: () => useApp.getState().select({ connId: n.connId, database: n.database }) },
    { label: "Console", onClick: () => openRedisConsole(n.connId, db) },
    { separator: true },
    {
      label: "Flush Database…",
      disabled: !n.count,
      onClick: async () => {
        if (!(await confirm(`Flush db${db}?`, { message: `All ${n.count?.toLocaleString()} keys in db${db} are deleted. This can't be undone.`, okLabel: "Flush" }))) return;
        try {
          await Redis.FlushDB(n.connId, db);
          useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 }));
          await refreshConn();
        } catch (e) {
          alert("Flush failed", errorText(e));
        }
      },
    },
    { separator: true },
    { label: "Copy Name", onClick: () => copy(n.label) },
    { label: "Refresh", shortcut: "⌘R", onClick: refreshConn },
  ];
}

/** Load connections on startup and open those marked "Auto connect". */
export function useInitialLoad() {
  useEffect(() => {
    (async () => {
      await useApp.getState().loadConnections();
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
