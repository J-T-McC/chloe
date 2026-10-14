// Context menus shared by the connection tree and the object list, following
// Navicat's table / objects-area menus.
import { DBType, Design, Explorer, MaintainOp, ObjectKind, ObjectRef, TableAction, Transfer, arr, errorText } from "./api";
import { newView, viewDesign } from "./components/designer/ViewDesigner";
import { openSetPrivileges } from "./components/users/SetPrivilegesDialog";
import { useTree } from "./tree";
import { MenuItem } from "./components/ContextMenu";
import { alert } from "./components/Dialogs";
import { newQuery, openObject, selectAllSQL } from "./components/Sidebar";
import { openExecuteFile, openExport, openImport, track } from "./components/transfer/state";
import { connById, useApp } from "./store";
import { designTable, newTable, runTableAction } from "./tableActions";
import { reverseToModel } from "./components/model/actions";

const isPG = (connId: string) => connById(connId)?.type === DBType.PostgreSQL;

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
  const items: Array<[MaintainOp, string]> = isPG(connId)
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
  const node = { key: "", type: "object" as const, label: ref.name, connId, database, schema, kind: ObjectKind.KindTable, info: { ...ref, rows: 0, dataSize: 0 }, depth: 0, expandable: false };
  return [
    { label: "Open Table", shortcut: "⌘O", disabled: !one, onClick: () => openObject(node) },
    { label: "Design Table", shortcut: "⌘D", disabled: !one, onClick: () => designTable(connId, ref) },
    { label: "Query Table", shortcut: "⌘T", disabled: !one, onClick: () => newQuery(connId, database, schema, selectAllSQL(connId, ref)) },
    { label: "New Table", shortcut: "⌘N", onClick: () => newTable(connId, database, schema) },
    { label: "Delete Table", disabled: !one, onClick: () => runTableAction(connId, ref, TableAction.ActionDrop) },
    { label: "Empty Table", disabled: !one, onClick: () => runTableAction(connId, ref, TableAction.ActionEmpty) },
    { label: "Truncate Table", disabled: !one, onClick: () => runTableAction(connId, ref, TableAction.ActionTruncate) },
    {
      label: "Duplicate Table",
      disabled: !one,
      submenu: [
        { label: "Structure and Data", onClick: () => runTableAction(connId, ref, TableAction.ActionCopy) },
        { label: "Structure Only", onClick: () => runTableAction(connId, ref, TableAction.ActionDuplicate) },
      ],
    },
    { label: "Set Privileges…", disabled: !one, onClick: () => openSetPrivileges(connId, { level: "table", database, schema, object: ref.name }, `${schema ? schema + "." : database + "."}${ref.name}`) },
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
    { label: "Reverse Tables to Model…", onClick: () => reverseToModel(connId, database, schema, refs.map((r) => r.name)) },
    { separator: true },
    { label: "Copy", shortcut: "⌘C", onClick: () => navigator.clipboard.writeText(refs.map((r) => r.name).join("\n")) },
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
    { label: "Set Privileges…", onClick: () => openSetPrivileges(connId, { level: "table", database: ref.database, schema: ref.schema, object: ref.name }, `${ref.schema ? ref.schema + "." : ref.database + "."}${ref.name}`) },
    { separator: true },
    { label: "Query View", shortcut: "⌘T", onClick: () => newQuery(connId, ref.database, ref.schema, selectAllSQL(connId, ref)) },
    { label: "Show DDL", onClick: () => useApp.getState().openTab({ kind: "ddl", title: ref.name, connId, database: ref.database, schema: ref.schema, ref }) },
    { label: "Export Wizard…", onClick: () => openExport({ connId, database: ref.database, schema: ref.schema, tables: [], sql: `SELECT * FROM ${quoted(connId, ref)}`, name: ref.name }) },
    later("Data Dictionary…"),
    { separator: true },
    { label: "Copy", shortcut: "⌘C", onClick: () => navigator.clipboard.writeText(ref.name) },
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
    useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 }));
    refreshFolder(connId, ref);
  } catch (e) {
    alert("Duplicate failed", errorText(e));
  }
}

async function refreshMatView(connId: string, ref: ObjectRef, concurrently: boolean) {
  const res = await Design.RefreshMaterializedView(connId, ref, concurrently);
  if (res.error) alert("Refresh failed", res.error + (concurrently ? "\n\nRefresh Concurrently needs a unique index on the materialized view." : ""));
  else useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 }));
}

function refreshFolder(connId: string, ref: ObjectRef) {
  const tree = useTree.getState();
  const key = [connId, ref.database, ref.schema, "#" + ref.kind].filter(Boolean).join("/");
  if (tree.children[key]) tree.refresh({ key, type: "folder", label: "", connId, database: ref.database, schema: ref.schema, kind: ref.kind, depth: 0, expandable: true });
}

function quoted(connId: string, ref: ObjectRef) {
  const q = isPG(connId) ? '"' : "`";
  const qi = (s: string) => q + s.split(q).join(q + q) + q;
  return isPG(connId) ? `${qi(ref.schema || "public")}.${qi(ref.name)}` : `${qi(ref.database)}.${qi(ref.name)}`;
}

/** Menu for the empty area of the object list (and the Tables folder). */
export function objectsAreaMenu(connId: string, database: string, schema: string | undefined, kind: ObjectKind | undefined): MenuItem[] {
  const tables = kind === ObjectKind.KindTable || kind === undefined;
  const views = kind === ObjectKind.KindView || kind === ObjectKind.KindMatView;
  return [
    ...(tables ? [{ label: "New Table", shortcut: "⌘N", onClick: () => newTable(connId, database, schema) }] : []),
    ...(kind === ObjectKind.KindView ? [{ label: "New View", shortcut: "⌘N", onClick: () => newView(connId, database, schema) }] : []),
    ...(views && isPG(connId) ? [{ label: "New Materialized View", onClick: () => newView(connId, database, schema, true) }] : []),
    {
      label: "Set Privileges…",
      onClick: () =>
        schema
          ? openSetPrivileges(connId, { level: "schema", database, schema }, `schema ${schema}`)
          : openSetPrivileges(connId, { level: "database", database }, `database ${database}`),
    },
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

export function openERD(connId: string, database: string, schema?: string) {
  const title = `ER — ${schema ?? database}`;
  useApp.getState().openTab({ kind: "erd", title, connId, database, schema, ref: { database, schema, name: "erd", kind: ObjectKind.$zero } });
}

/** Extra database / schema menu items (dump, execute, import/export). */
export function databaseTools(connId: string, database: string, schema?: string): MenuItem[] {
  return [
    { label: "ER Diagram", onClick: () => openERD(connId, database, schema) },
    { label: `Reverse ${schema ? "Schema" : "Database"} to Model…`, onClick: () => reverseToModel(connId, database, schema) },
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
