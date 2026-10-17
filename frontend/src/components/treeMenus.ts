// Context menus of the connection tree: the empty area, groups, connections, databases,
// schemas, folders and objects.
import { Connection, Connections, DBType, ObjectKind, Redis, errorText, copyText } from "../api";
import { openRedisConsole } from "./redis/actions";
import { collectionMenu, designCollection, mongoConnMenu, mongoDbMenu, newMongoQuery } from "./mongo/actions";
import { openUsers } from "./users/actions";
import { connById, useApp } from "../store";
import { TreeNode, useTree } from "../tree";
import { MenuItem } from "./ContextMenu";
import { alert, confirm, prompt } from "./Dialogs";
import { newTable } from "../tableActions";
import { databaseTools, objectMenu, objectsAreaMenu } from "../menus";
import { deleteDatabase, openDatabaseDialog } from "./DatabaseDialog";
import { openExecuteFile } from "./transfer/state";
import { openDataTransfer } from "./transfer/DataTransferWizard";
import { openStructureSync } from "./transfer/StructureSyncWizard";
import { openDataSync } from "./transfer/DataSyncWizard";
import { isPG } from "../dialect";
import { newQuery, openMonitor, refOf } from "../actions/tabs";

const COLORS: Array<[string, string]> = [
  ["", "None"],
  ["#e5534b", "Red"],
  ["#e8963c", "Orange"],
  ["#d6b63a", "Yellow"],
  ["#4fae5a", "Green"],
  ["#3b8eea", "Blue"],
  ["#9a6bd8", "Purple"],
  ["#8b8f98", "Gray"],
];


const copy = (s: string) => copyText(s);

export async function reload() {
  await useApp.getState().loadConnections();
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

export function emptyAreaMenu(): MenuItem[] {
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

export function menuFor(n: TreeNode, connected: boolean, dbOpen: boolean): MenuItem[] {
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
      if (c.type === DBType.MongoDB) return mongoConnMenu(n, c, connected, { info: () => showInfo(n.connId), monitor: () => openMonitor(n.connId), remove: () => deleteConnection(c) });
      const lite = c.type === DBType.SQLite;
      return [
        connected
          ? { label: "Close Connection", onClick: () => { app.disconnect(n.connId); tree.collapseConnection(n.connId); } }
          : { label: "Open Connection", onClick: () => tree.expand(n) },
        { label: "Edit Connection…", onClick: () => app.editConnection(c) },
        { label: "New Connection…", onClick: () => app.editConnection("new") },
        { label: "Delete Connection…", onClick: () => deleteConnection(c) },
        { label: "Duplicate Connection", onClick: async () => { await Connections.Duplicate(c.id); await reload(); } },
        { separator: true },
        { label: lite ? "Attach Database…" : "New Database…", disabled: !connected, onClick: () => openDatabaseDialog(n.connId) },
        { label: "New Query", disabled: !connected, shortcut: "⌘T", onClick: () => newQuery(n.connId) },
        { separator: true },
        { label: "Execute SQL File…", disabled: !connected, onClick: () => openExecuteFile(n.connId, lite ? "main" : "") },
        { label: "Data Transfer…", onClick: () => openDataTransfer({ connId: n.connId, database: lite ? "main" : "" }) },
        ...(lite ? [] : [{ label: "Structure Synchronization…", onClick: () => openStructureSync({ connId: n.connId }) }]),
        { label: "Data Synchronization…", onClick: () => openDataSync({ connId: n.connId, database: lite ? "main" : "" }) },
        ...(lite
          ? []
          : [
              { label: c.type === "postgres" ? "Roles" : "Users", disabled: !connected, onClick: () => openUsers(n.connId) },
              { label: "Server Monitor", disabled: !connected, onClick: () => openMonitor(n.connId) },
            ]),
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
      if (connById(n.connId)?.type === DBType.MongoDB) return mongoDbMenu(n, dbOpen);
      if (connById(n.connId)?.type === DBType.SQLite) return sqliteDbMenu(n, dbOpen);
      return [
        dbOpen
          ? { label: "Close Database", onClick: () => tree.closeDatabase(n) }
          : { label: "Open Database", onClick: () => tree.expand(n) },
        { label: "New Database…", onClick: () => openDatabaseDialog(n.connId) },
        { label: "Edit Database…", onClick: () => openDatabaseDialog(n.connId, n.database) },
        { label: "Delete Database", onClick: () => deleteDatabase(n.connId, n.database!) },
        { separator: true },
        { label: "New Query", onClick: () => newQuery(n.connId, n.database) },
        ...(isPG(n.connId) ? [] : [{ label: "New Table", onClick: () => newTable(n.connId, n.database!) }]),
        { separator: true },
        ...(isPG(n.connId) ? [{ label: "Execute SQL File…", onClick: () => openExecuteFile(n.connId, n.database!) }] : databaseTools(n.connId, n.database!)),
        { label: "Data Transfer…", onClick: () => openDataTransfer({ connId: n.connId, database: n.database }) },
        ...(isPG(n.connId) ? [] : [{ label: "Structure Synchronization…", onClick: () => openStructureSync({ connId: n.connId, database: n.database }) }]),
        ...(isPG(n.connId) ? [] : [{ label: "Data Synchronization…", onClick: () => openDataSync({ connId: n.connId, database: n.database }) }]),
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
        { label: "Data Transfer…", onClick: () => openDataTransfer({ connId: n.connId, database: n.database, schema: n.schema }) },
        { label: "Structure Synchronization…", onClick: () => openStructureSync({ connId: n.connId, database: n.database, schema: n.schema }) },
        { label: "Data Synchronization…", onClick: () => openDataSync({ connId: n.connId, database: n.database, schema: n.schema }) },
        { separator: true },
        { label: "Copy Name", onClick: () => copy(n.label) },
        { label: "Refresh", shortcut: "⌘R", onClick: () => tree.refresh(n) },
      ];
    case "folder":
      if (connById(n.connId)?.type === DBType.MongoDB)
        return [
          { label: n.kind === ObjectKind.KindView ? "New View" : "New Collection", onClick: () => designCollection(n.connId, n.database!, undefined, n.kind) },
          { label: "New Query", onClick: () => newMongoQuery(n.connId, n.database) },
          { separator: true },
          { label: "Refresh", shortcut: "⌘R", onClick: () => tree.refresh(n) },
        ];
      if (n.kind === ObjectKind.KindQuery)
        return [
          { label: "New Query", onClick: () => newQuery(n.connId, n.database, n.schema) },
          { label: "Refresh", shortcut: "⌘R", onClick: () => tree.refresh(n) },
        ];
      if (n.kind === ObjectKind.KindTable || n.kind === ObjectKind.KindView || n.kind === ObjectKind.KindMatView) return [...objectsAreaMenu(n.connId, n.database!, n.schema, n.kind).slice(0, -1), { label: "Refresh", shortcut: "⌘R", onClick: () => tree.refresh(n) }];
      return [
        { label: "New Query", onClick: () => newQuery(n.connId, n.database, n.schema) },
        { label: "Refresh", shortcut: "⌘R", onClick: () => tree.refresh(n) },
      ];
    case "object":
      if (connById(n.connId)?.type === DBType.MongoDB) return collectionMenu(n.connId, n.database!, [n.label], n.kind);
      return objectMenu(n.connId, [refOf(n)], n.info?.comment);
  }
}



function sqliteDbMenu(n: TreeNode, dbOpen: boolean): MenuItem[] {
  const tree = useTree.getState();
  return [
    dbOpen ? { label: "Close Database", onClick: () => tree.closeDatabase(n) } : { label: "Open Database", onClick: () => tree.expand(n) },
    { label: "Attach Database…", onClick: () => openDatabaseDialog(n.connId) },
    ...(n.database === "main" ? [] : [{ label: "Detach Database", onClick: () => deleteDatabase(n.connId, n.database!) }]),
    { separator: true },
    { label: "New Query", onClick: () => newQuery(n.connId, n.database) },
    { label: "New Table", onClick: () => newTable(n.connId, n.database!) },
    { separator: true },
    ...databaseTools(n.connId, n.database!),
    { label: "Data Transfer…", onClick: () => openDataTransfer({ connId: n.connId, database: n.database }) },
    { label: "Data Synchronization…", onClick: () => openDataSync({ connId: n.connId, database: n.database }) },
    { separator: true },
    { label: "Copy Name", onClick: () => copy(n.label) },
    { label: "Refresh", shortcut: "⌘R", disabled: !dbOpen, onClick: () => tree.refresh(n) },
  ];
}

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
