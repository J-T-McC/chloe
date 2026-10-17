// MongoDB actions shared by the tree, the objects list and the tabs.
import { Connection, Connections, Mongo, ObjectKind, errorText, copyText } from "../../api";
import { invalidateMongoCatalog } from "../../editor/mongo";
import { connById, useApp } from "../../store";
import { TreeNode, useTree } from "../../tree";
import { MenuItem } from "../ContextMenu";
import { alert, confirm, prompt } from "../Dialogs";
import { exportCollection, importCollection } from "./transferState";

const kindOf = (k?: string) => (k === ObjectKind.KindView ? ObjectKind.KindView : ObjectKind.KindCollection);

export function openCollection(connId: string, db: string, name: string, kind?: string) {
  useApp.getState().openTab({ kind: "collection", title: `${name} @${db}`, connId, database: db, ref: { database: db, name, kind: kindOf(kind) } });
}

/** Open the designer of a collection or view; without a name, for a new one. */
export function designCollection(connId: string, db: string, name: string | undefined, kind?: string) {
  const k = kindOf(kind);
  const what = k === ObjectKind.KindView ? "View" : "Collection";
  useApp.getState().openTab({
    kind: "mongodesign",
    title: name ? `${name} @${db} (${what})` : `Untitled @${db} (${what})`,
    connId,
    database: db,
    // A new designer gets a unique signature so each opens its own tab.
    ref: { database: db, name: name ?? "", kind: k, signature: name ? undefined : String(Date.now()) },
  });
}

export function newMongoQuery(connId: string, db?: string, text = "") {
  const c = connById(connId);
  const database = db ?? c?.initialDatabase ?? "test";
  useApp.getState().openTab({ kind: "mongoquery", title: `Query — ${database}`, connId, database, sql: text, ref: { database, name: "query", kind: ObjectKind.$zero, signature: String(Date.now()) } });
}

export function openMongoUsers(connId: string, db?: string) {
  useApp.getState().openTab({ kind: "mongousers", title: `Users — ${connById(connId)?.name ?? ""}`, connId, database: db, ref: { database: db ?? "", name: "users", kind: ObjectKind.$zero } });
}

export function openMongoShell(connId: string, db?: string) {
  const database = db ?? connById(connId)?.initialDatabase ?? "test";
  useApp.getState().openTab({ kind: "mongoshell", title: `MongoShell — ${connById(connId)?.name ?? ""}`, connId, database, ref: { database, name: "shell", kind: ObjectKind.$zero, signature: String(Date.now()) } });
}

/** Reload the tree folders and object lists of a database after its collections changed. */
export function collectionsChanged(connId: string, db: string) {
  invalidateMongoCatalog(connId);
  useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 }));
  const tree = useTree.getState();
  for (const k of [ObjectKind.KindCollection, ObjectKind.KindView]) {
    const key = `${connId}/${db}/#${k}`;
    if (tree.children[key]) tree.refresh({ key, type: "folder", label: "", connId, database: db, kind: k, depth: 2, expandable: true });
  }
}

function databasesChanged(connId: string) {
  invalidateMongoCatalog(connId);
  const tree = useTree.getState();
  tree.refresh({ key: connId, type: "conn", label: "", connId, depth: 0, expandable: true });
}

export async function newDatabase(connId: string) {
  const db = (await prompt("New Database", { message: "Database name" }))?.trim();
  if (!db) return;
  // MongoDB keeps a database only once it holds a collection.
  const coll = (await prompt("New Database", { message: `First collection in “${db}”`, defaultValue: "collection1" }))?.trim();
  if (!coll) return;
  try {
    await Mongo.CreateDatabase(connId, db, coll);
    databasesChanged(connId);
  } catch (e) {
    alert("Couldn't create the database", errorText(e));
  }
}

export async function dropDatabase(connId: string, db: string) {
  if (!(await confirm(`Delete database “${db}”?`, { message: "All its collections, views and indexes are dropped. This can't be undone.", okLabel: "Delete" }))) return;
  try {
    await Mongo.DropDatabase(connId, db);
    await useApp.getState().closeDatabase(connId, db);
    databasesChanged(connId);
  } catch (e) {
    alert("Couldn't delete the database", errorText(e));
  }
}

export async function dropCollections(connId: string, db: string, names: string[], kind?: string) {
  const what = kindOf(kind) === ObjectKind.KindView ? "view" : "collection";
  const title = names.length === 1 ? `Delete ${what} “${names[0]}”?` : `Delete ${names.length} ${what}s?`;
  if (!(await confirm(title, { message: what === "view" ? undefined : "Its documents and indexes are dropped. This can't be undone.", okLabel: "Delete" }))) return;
  for (const n of names) {
    try {
      await Mongo.DropCollection(connId, db, n);
    } catch (e) {
      alert(`Couldn't delete ${n}`, errorText(e));
      break;
    }
  }
  const app = useApp.getState();
  app.closeTabs(app.tabs.filter((t) => t.connId === connId && t.ref?.database === db && names.includes(t.ref?.name ?? "") && (t.kind === "collection" || t.kind === "mongodesign")).map((t) => t.id));
  collectionsChanged(connId, db);
}

export async function emptyCollection(connId: string, db: string, name: string) {
  if (!(await confirm(`Empty collection “${name}”?`, { message: "Every document is deleted; indexes and validation stay.", okLabel: "Empty" }))) return;
  try {
    const n = await Mongo.EmptyCollection(connId, db, name);
    collectionsChanged(connId, db);
    alert("Collection emptied", `${n.toLocaleString()} documents deleted.`);
  } catch (e) {
    alert("Couldn't empty the collection", errorText(e));
  }
}

export async function renameCollection(connId: string, db: string, name: string) {
  const to = (await prompt("Rename", { defaultValue: name }))?.trim();
  if (!to || to === name) return;
  try {
    await Mongo.RenameCollection(connId, db, name, to);
    collectionsChanged(connId, db);
  } catch (e) {
    alert("Couldn't rename", errorText(e));
  }
}

export async function duplicateCollection(connId: string, db: string, name: string, data: boolean) {
  const to = (await prompt(data ? "Duplicate Collection" : "Duplicate Collection Structure", { defaultValue: `${name}_copy` }))?.trim();
  if (!to) return;
  try {
    await Mongo.DuplicateCollection(connId, db, name, to, data);
    collectionsChanged(connId, db);
  } catch (e) {
    alert("Couldn't duplicate", errorText(e));
  }
}

const copy = (s: string) => copyText(s);

/** Right-click menu of collections or views (several when names has more than one). */
export function collectionMenu(connId: string, db: string, names: string[], kind?: string): MenuItem[] {
  const one = names.length === 1;
  const name = names[0];
  const view = kindOf(kind) === ObjectKind.KindView;
  return [
    { label: view ? "Open View" : "Open Collection", disabled: !one, onClick: () => openCollection(connId, db, name, kind) },
    { label: view ? "Design View" : "Design Collection", disabled: !one, onClick: () => designCollection(connId, db, name, kind) },
    { label: view ? "New View" : "New Collection", onClick: () => designCollection(connId, db, undefined, kind) },
    { label: view ? "Delete View" : "Delete Collection", onClick: () => dropCollections(connId, db, names, kind) },
    ...(view
      ? []
      : [
          { label: "Empty Collection", disabled: !one, onClick: () => emptyCollection(connId, db, name) },
          {
            label: "Duplicate Collection",
            disabled: !one,
            submenu: [
              { label: "Structure and Data", onClick: () => duplicateCollection(connId, db, name, true) },
              { label: "Structure Only", onClick: () => duplicateCollection(connId, db, name, false) },
            ],
          },
        ]),
    { separator: true },
    ...(view ? [] : [{ label: "Import Wizard…", disabled: !one, onClick: () => importCollection(connId, db, name) }]),
    { label: "Export Wizard…", disabled: !one, onClick: () => exportCollection(connId, db, name) },
    { separator: true },
    { label: "New Query", onClick: () => newMongoQuery(connId, db, one ? `db.getCollection(${JSON.stringify(name)}).find({})` : "") },
    { label: "MongoShell", onClick: () => openMongoShell(connId, db) },
    { separator: true },
    { label: "Rename", disabled: !one, onClick: () => renameCollection(connId, db, name) },
    { label: "Copy Name", disabled: !one, onClick: () => copy(name) },
    { label: "Refresh", shortcut: "⌘R", onClick: () => collectionsChanged(connId, db) },
  ];
}

export function mongoDbMenu(n: TreeNode, dbOpen: boolean): MenuItem[] {
  const tree = useTree.getState();
  const db = n.database!;
  return [
    dbOpen ? { label: "Close Database", onClick: () => tree.closeDatabase(n) } : { label: "Open Database", onClick: () => tree.expand(n) },
    { label: "New Database…", onClick: () => newDatabase(n.connId) },
    { label: "Delete Database", onClick: () => dropDatabase(n.connId, db) },
    { separator: true },
    { label: "New Collection", onClick: () => designCollection(n.connId, db, undefined) },
    { label: "New Query", onClick: () => newMongoQuery(n.connId, db) },
    { label: "MongoShell", onClick: () => openMongoShell(n.connId, db) },
    { separator: true },
    { label: "Copy Name", onClick: () => copy(db) },
    { label: "Refresh", shortcut: "⌘R", disabled: !dbOpen, onClick: () => tree.refresh(n) },
  ];
}

export function mongoConnMenu(n: TreeNode, c: Connection, connected: boolean, extra: { info(): void; monitor(): void; remove(): void }): MenuItem[] {
  const app = useApp.getState();
  const tree = useTree.getState();
  return [
    connected
      ? { label: "Close Connection", onClick: () => { app.disconnect(n.connId); tree.collapseConnection(n.connId); } }
      : { label: "Open Connection", onClick: () => tree.expand(n) },
    { label: "Edit Connection…", onClick: () => app.editConnection(c) },
    { label: "New Connection…", onClick: () => app.editConnection("new") },
    { label: "Delete Connection…", onClick: extra.remove },
    { label: "Duplicate Connection", onClick: async () => { await Connections.Duplicate(c.id); await app.loadConnections(); } },
    { separator: true },
    { label: "New Database…", disabled: !connected, onClick: () => newDatabase(n.connId) },
    { label: "New Query", disabled: !connected, shortcut: "⌘T", onClick: () => newMongoQuery(n.connId) },
    { label: "MongoShell", disabled: !connected, onClick: () => openMongoShell(n.connId) },
    { separator: true },
    { label: "Users", disabled: !connected, onClick: () => openMongoUsers(n.connId) },
    { label: "Server Monitor", disabled: !connected, onClick: extra.monitor },
    { label: "Connection Info…", disabled: !connected, onClick: extra.info },
    { separator: true },
    { label: "Copy Connection URL", onClick: async () => copy(await Connections.URL(c.id)) },
    { separator: true },
    { label: "Refresh", shortcut: "⌘R", disabled: !connected, onClick: () => { invalidateMongoCatalog(n.connId); tree.refresh(n); } },
  ];
}
