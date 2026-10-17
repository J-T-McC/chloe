// Context menus shared by the connection tree and the object list, following
// Navicat's table / objects-area menus.
import { DBType, Design, Explorer, MaintainOp, ObjectKind, ObjectRef, TableAction, Transfer, arr, errorText, copyText } from "./api";
import { newView, viewDesign } from "./components/designer/ViewDesigner";
import { openSetPrivileges } from "./components/users/SetPrivilegesDialog";
import { folderKey, useTree } from "./tree";
import { MenuItem } from "./components/ContextMenu";
import { alert, confirm } from "./components/Dialogs";
import { newQuery, openDDL, openObject, selectAllSQL } from "./actions/tabs";
import { openExecuteFile, openExport, openImport, track } from "./components/transfer/state";
import { connById, useApp } from "./store";
import { designTable, newTable, runTableAction } from "./tableActions";
import { reverseToModel } from "./components/model/actions";
import { isPG, qualifiedName } from "./dialect";
import { deleteSavedQueries, duplicateSavedQuery, openSavedQuery, renameSavedQuery } from "./queries";

const isSQLite = (connId: string) => connById(connId)?.type === DBType.SQLite;

/** Dump SQL File for a whole database/schema (tables empty) or chosen tables. */
export async function dumpSQL(connId: string, database: string, schema: string | undefined, tables: string[], data: boolean) {
  const base = tables.length === 1 ? tables[0] : schema ? `${database}.${schema}` : database;
  const path = await Transfer.SaveFileDialog(`${base}.sql`, "SQL", "*.sql");
  if (!path) return;
  try {
    await track(Transfer.Dump(connId, { database, schema, tables, data, path }));
  } catch (e) {
    alert("Dump failed", errorText(e));
  }
}

async function maintain(connId: string, refs: ObjectRef[], op: MaintainOp, label: string) {
  try {
    const res = arr(await Design.Maintain(connId, refs, op));
    alert(`${label} — ${refs.length === 1 ? refs[0].name : `${refs.length} tables`}`, res.map((r) => `${r.table}: ${r.status} ${r.message}`).join("\n") || "Done");
  } catch (e) {
    alert(`${label} failed`, errorText(e));
  }
}

function maintainMenu(connId: string, refs: ObjectRef[]): MenuItem[] {
  const items: Array<[MaintainOp, string]> = isSQLite(connId)
    ? [
        [MaintainOp.MaintainAnalyze, "Analyze Table"],
        [MaintainOp.MaintainCheck, "Check Integrity"],
        [MaintainOp.MaintainReindex, "Reindex Table"],
        [MaintainOp.MaintainVacuum, "Vacuum Database"],
      ]
    : isPG(connId)
    ? [
        [MaintainOp.MaintainAnalyze, "Analyze Table"],
        [MaintainOp.MaintainVacuum, "Vacuum Table"],
        [MaintainOp.MaintainVacuumAnalyz, "Vacuum Analyze Table"],
        [MaintainOp.MaintainVacuumFull, "Vacuum Full Table"],
        [MaintainOp.MaintainReindex, "Reindex Table"],
      ]
    : [
        [MaintainOp.MaintainAnalyze, "Analyze Table"],
        [MaintainOp.MaintainCheck, "Check Table"],
        [MaintainOp.MaintainOptimize, "Optimize Table"],
        [MaintainOp.MaintainRepair, "Repair Table"],
      ];
  return items.map(([op, label]) => ({ label, onClick: () => maintain(connId, refs, op, label) }));
}

const later = (label: string): MenuItem => ({ label, disabled: true });

/** Menu for one or more selected tables. */
export function tableMenu(connId: string, refs: ObjectRef[]): MenuItem[] {
  const ref = refs[0];
  const one = refs.length === 1;
  const { database, schema } = ref;
  const lite = isSQLite(connId);
  const node = { key: "", type: "object" as const, label: ref.name, connId, database, schema, kind: ObjectKind.KindTable, info: { ...ref, rows: 0, dataSize: 0 }, depth: 0, expandable: false };
  return [
    { label: "Open Table", shortcut: "⌘O", disabled: !one, onClick: () => openObject(node) },
    { label: "Design Table", shortcut: "⌘D", disabled: !one, onClick: () => designTable(connId, ref) },
    { label: "Query Table", shortcut: "⌘T", disabled: !one, onClick: () => newQuery(connId, database, schema, selectAllSQL(connId, ref)) },
    { label: "New Table", shortcut: "⌘N", onClick: () => newTable(connId, database, schema) },
    { label: "Delete Table", disabled: !one, onClick: () => runTableAction(connId, ref, TableAction.ActionDrop) },
    { label: "Empty Table", disabled: !one, onClick: () => runTableAction(connId, ref, TableAction.ActionEmpty) },
    ...(lite ? [] : [{ label: "Truncate Table", disabled: !one, onClick: () => runTableAction(connId, ref, TableAction.ActionTruncate) }]),
    {
      label: "Duplicate Table",
      disabled: !one,
      submenu: [
        { label: "Structure and Data", onClick: () => runTableAction(connId, ref, TableAction.ActionCopy) },
        { label: "Structure Only", onClick: () => runTableAction(connId, ref, TableAction.ActionDuplicate) },
      ],
    },
    ...(lite ? [] : [{ label: "Set Privileges…", disabled: !one, onClick: () => openSetPrivileges(connId, { level: "table", database, schema, object: ref.name }, `${schema ? schema + "." : database + "."}${ref.name}`) }]),
    { separator: true },
    { label: "Import Wizard…", disabled: !one, onClick: () => openImport({ connId, database, schema, table: ref }) },
    { label: "Export Wizard…", onClick: () => openExport({ connId, database, schema, tables: refs }) },
    later("Data Dictionary…"),
    later("Data Generation…"),
    { separator: true },
    {
      label: "Dump SQL File",
      submenu: [
        { label: "Structure and Data…", onClick: () => dumpSQL(connId, database, schema, refs.map((r) => r.name), true) },
        { label: "Structure Only…", onClick: () => dumpSQL(connId, database, schema, refs.map((r) => r.name), false) },
      ],
    },
    { label: "Maintain", submenu: maintainMenu(connId, refs) },
    ...(lite ? [] : [{ label: "Reverse Tables to Model…", onClick: () => reverseToModel(connId, database, schema, refs.map((r) => r.name)) }]),
    { separator: true },
    { label: "Copy", shortcut: "⌘C", onClick: () => copyText(refs.map((r) => r.name).join("\n")) },
    later("Paste"),
    { label: "Rename", disabled: !one, onClick: () => runTableAction(connId, ref, TableAction.ActionRename) },
    { separator: true },
    { label: "Refresh", shortcut: "⌘R", onClick: () => useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 })) },
  ];
}

/** Menu for views (and materialized views). */
export function viewMenu(connId: string, ref: ObjectRef): MenuItem[] {
  const node = { key: "", type: "object" as const, label: ref.name, connId, database: ref.database, schema: ref.schema, kind: ref.kind, info: { ...ref, rows: 0, dataSize: 0 }, depth: 0, expandable: false };
  const mat = ref.kind === ObjectKind.KindMatView;
  return [
    { label: mat ? "Open Materialized View" : "Open View", shortcut: "⌘O", onClick: () => openObject(node) },
    { label: mat ? "Design Materialized View" : "Design View", shortcut: "⌘D", onClick: () => viewDesign(connId, ref) },
    { label: "New View", shortcut: "⌘N", onClick: () => newView(connId, ref.database, ref.schema) },
    ...(isPG(connId) ? [{ label: "New Materialized View", onClick: () => newView(connId, ref.database, ref.schema, true) }] : []),
    { label: mat ? "Delete Materialized View" : "Delete View", onClick: () => runTableAction(connId, ref, TableAction.ActionDrop) },
    { label: mat ? "Duplicate Materialized View" : "Duplicate View", shortcut: "⇧⌘D", onClick: () => duplicateView(connId, ref) },
    ...(mat
      ? [
          {
            label: "Refresh Materialized View",
            submenu: [
              { label: "Refresh", onClick: () => refreshMatView(connId, ref, false) },
              { label: "Refresh Concurrently", onClick: () => refreshMatView(connId, ref, true) },
            ],
          },
        ]
      : []),
    ...(isSQLite(connId)
      ? []
      : [{ label: "Set Privileges…", onClick: () => openSetPrivileges(connId, { level: "table", database: ref.database, schema: ref.schema, object: ref.name }, `${ref.schema ? ref.schema + "." : ref.database + "."}${ref.name}`) }]),
    { separator: true },
    { label: "Query View", shortcut: "⌘T", onClick: () => newQuery(connId, ref.database, ref.schema, selectAllSQL(connId, ref)) },
    { label: "Show DDL", onClick: () => openDDL(connId, ref) },
    { label: "Export Wizard…", onClick: () => openExport({ connId, database: ref.database, schema: ref.schema, tables: [], sql: `SELECT * FROM ${qualifiedName(connId, ref, true)}`, name: ref.name }) },
    later("Data Dictionary…"),
    { separator: true },
    { label: "Copy", shortcut: "⌘C", onClick: () => copyText(ref.name) },
    { label: "Rename", onClick: () => runTableAction(connId, ref, TableAction.ActionRename) },
    { separator: true },
    { label: "Refresh", shortcut: "⌘R", onClick: () => useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 })) },
  ];
}

async function duplicateView(connId: string, ref: ObjectRef) {
  try {
    const v = await Design.LoadView(connId, ref);
    const names = new Set(arr(await Explorer.ListObjects(connId, ref.database, ref.schema ?? "", ref.kind)).map((o) => o.name));
    let name = `${ref.name}_copy`;
    for (let i = 2; names.has(name); i++) name = `${ref.name}_copy${i}`;
    const res = await Design.ApplyView(connId, null, { ...v, name, origName: undefined });
    if (res.error) return alert("Duplicate failed", res.error);
    refreshFolder(connId, ref);
  } catch (e) {
    alert("Duplicate failed", errorText(e));
  }
}

async function refreshMatView(connId: string, ref: ObjectRef, concurrently: boolean) {
  const res = await Design.RefreshMaterializedView(connId, ref, concurrently);
  if (res.error) alert("Refresh failed", res.error + (concurrently ? "\n\nRefresh Concurrently needs a unique index on the materialized view." : ""));
  else useApp.getState().bumpFolder(folderKey(connId, ref.database, ref.schema, ref.kind));
}

function refreshFolder(connId: string, ref: ObjectRef) {
  const tree = useTree.getState();
  const key = folderKey(connId, ref.database, ref.schema, ref.kind);
  useApp.getState().bumpFolder(key);
  if (tree.children[key]) tree.refresh({ key, type: "folder", label: "", connId, database: ref.database, schema: ref.schema, kind: ref.kind, depth: 0, expandable: true });
}

/** Menu for the empty area of the object list (and the Tables folder). */
export function objectsAreaMenu(connId: string, database: string, schema: string | undefined, kind: ObjectKind | undefined): MenuItem[] {
  const tables = kind === ObjectKind.KindTable || kind === undefined;
  const views = kind === ObjectKind.KindView || kind === ObjectKind.KindMatView;
  return [
    ...(tables ? [{ label: "New Table", shortcut: "⌘N", onClick: () => newTable(connId, database, schema) }] : []),
    ...(kind === ObjectKind.KindView ? [{ label: "New View", shortcut: "⌘N", onClick: () => newView(connId, database, schema) }] : []),
    ...(views && isPG(connId) ? [{ label: "New Materialized View", onClick: () => newView(connId, database, schema, true) }] : []),
    ...(isSQLite(connId)
      ? []
      : [
          {
            label: "Set Privileges…",
            onClick: () =>
              schema
                ? openSetPrivileges(connId, { level: "schema", database, schema }, `schema ${schema}`)
                : openSetPrivileges(connId, { level: "database", database }, `database ${database}`),
          },
        ]),
    { separator: true },
    { label: "Import Wizard…", onClick: () => openImport({ connId, database, schema }) },
    { label: "Export Wizard…", onClick: () => openExport({ connId, database, schema, tables: [] }) },
    { separator: true },
    { label: "Execute SQL File…", onClick: () => openExecuteFile(connId, database, schema) },
    later("Find in Database…"),
    { separator: true },
    { label: "New Query", onClick: () => newQuery(connId, database, schema) },
    later("Paste"),
    { separator: true },
    { label: "Refresh", shortcut: "⌘R", onClick: () => useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 })) },
  ];
}

/** Menu for SQLite indexes and triggers, which belong to a table. */
export function tableChildMenu(connId: string, ref: ObjectRef, table: string): MenuItem[] {
  const word = ref.kind === ObjectKind.KindIndex ? "Index" : "Trigger";
  return [
    { label: "Show DDL", onClick: () => openDDL(connId, ref) },
    ...(table ? [{ label: "Design Table", shortcut: "⌘D", onClick: () => designTable(connId, { database: ref.database, name: table, kind: ObjectKind.KindTable }) }] : []),
    {
      label: `Delete ${word}`,
      onClick: async () => {
        if (!(await confirm(`Delete ${word.toLowerCase()} ${ref.name}?`, { okLabel: "Delete" }))) return;
        const res = await Design.RunTableAction(connId, ref, TableAction.ActionDrop, "");
        if (res.error) return alert("Delete failed", res.error);
        refreshFolder(connId, ref);
      },
    },
    { separator: true },
    { label: "New Query", onClick: () => newQuery(connId, ref.database) },
    { label: "Copy Name", onClick: () => copyText(ref.name) },
  ];
}

export function openERD(connId: string, database: string, schema?: string) {
  const title = `ER — ${schema ?? database}`;
  useApp.getState().openTab({ kind: "erd", title, connId, database, schema, ref: { database, schema, name: "erd", kind: ObjectKind.$zero } });
}

/** Extra database / schema menu items (dump, execute, import/export). */
export function databaseTools(connId: string, database: string, schema?: string): MenuItem[] {
  return [
    { label: "ER Diagram", onClick: () => openERD(connId, database, schema) },
    ...(isSQLite(connId) ? [] : [{ label: `Reverse ${schema ? "Schema" : "Database"} to Model…`, onClick: () => reverseToModel(connId, database, schema) }]),
    { separator: true },
    { label: "Execute SQL File…", onClick: () => openExecuteFile(connId, database, schema) },
    {
      label: "Dump SQL File",
      submenu: [
        { label: "Structure and Data…", onClick: () => dumpSQL(connId, database, schema, [], true) },
        { label: "Structure Only…", onClick: () => dumpSQL(connId, database, schema, [], false) },
      ],
    },
    { label: "Import Wizard…", onClick: () => openImport({ connId, database, schema }) },
    { label: "Export Wizard…", onClick: () => openExport({ connId, database, schema, tables: [] }) },
    later("Find in Database…"),
  ];
}

/** Menu of saved queries in a Queries folder or the Objects list. */
export function savedQueryMenu(connId: string, database: string, schema: string | undefined, names: string[]): MenuItem[] {
  const one = names.length === 1;
  return [
    { label: "Open Query", disabled: !one, onClick: () => openSavedQuery(connId, database, schema, names[0]) },
    { label: "New Query", onClick: () => newQuery(connId, database, schema) },
    { separator: true },
    { label: "Rename…", disabled: !one, onClick: () => renameSavedQuery(connId, database, schema, names[0]) },
    { label: "Duplicate", disabled: !one, onClick: () => duplicateSavedQuery(connId, database, schema, names[0]) },
    { label: one ? "Delete Query" : `Delete ${names.length} Queries`, onClick: () => deleteSavedQueries(connId, database, schema, names) },
    { separator: true },
    { label: "Copy Name", disabled: !one, onClick: () => copyText(names[0]) },
  ];
}

/** The menu of picked objects (one kind) in the tree or the object list; comment is the first one's. */
export function objectMenu(connId: string, refs: ObjectRef[], comment = ""): MenuItem[] {
  const ref = refs[0];
  switch (ref.kind) {
    case ObjectKind.KindQuery:
      return savedQueryMenu(connId, ref.database, ref.schema, refs.map((r) => r.name));
    case ObjectKind.KindTable:
      return tableMenu(connId, refs);
    case ObjectKind.KindView:
    case ObjectKind.KindMatView:
      return viewMenu(connId, ref);
  }
  if (ref.kind === ObjectKind.KindIndex || (ref.kind === ObjectKind.KindTrigger && isSQLite(connId))) return tableChildMenu(connId, ref, comment.replace(/^ON /, ""));
  return [
    { label: "Show DDL", onClick: () => openDDL(connId, ref) },
    { label: "New Query", onClick: () => newQuery(connId, ref.database, ref.schema) },
    { separator: true },
    { label: "Copy Name", onClick: () => copyText(ref.name) },
  ]
}
