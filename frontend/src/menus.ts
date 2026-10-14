// Context menus shared by the connection tree and the object list, following
// Navicat's table / objects-area menus.
import { DBType, Design, MaintainOp, ObjectKind, ObjectRef, TableAction, Transfer, arr, errorText } from "./api";
import { MenuItem } from "./components/ContextMenu";
import { alert } from "./components/Dialogs";
import { newQuery, openObject } from "./components/Sidebar";
import { openExecuteFile, openExport, openImport, track } from "./components/transfer/state";
import { connById, useApp } from "./store";
import { designTable, newTable, runTableAction } from "./tableActions";

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
    later("Set Privileges"),
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
    later("Reverse Tables to Model…"),
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
  return [
    { label: "Open View", onClick: () => openObject(node) },
    { label: "Show DDL", onClick: () => useApp.getState().openTab({ kind: "ddl", title: ref.name, connId, database: ref.database, schema: ref.schema, ref }) },
    { separator: true },
    { label: "Export Wizard…", onClick: () => openExport({ connId, database: ref.database, schema: ref.schema, tables: [], sql: `SELECT * FROM ${quoted(connId, ref)}`, name: ref.name }) },
    { label: "Rename", onClick: () => runTableAction(connId, ref, TableAction.ActionRename) },
    { label: "Delete View", onClick: () => runTableAction(connId, ref, TableAction.ActionDrop) },
    { separator: true },
    { label: "Copy", onClick: () => navigator.clipboard.writeText(ref.name) },
    { label: "Refresh", shortcut: "⌘R", onClick: () => useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 })) },
  ];
}

function quoted(connId: string, ref: ObjectRef) {
  const q = isPG(connId) ? '"' : "`";
  const qi = (s: string) => q + s.split(q).join(q + q) + q;
  return isPG(connId) ? `${qi(ref.schema || "public")}.${qi(ref.name)}` : `${qi(ref.database)}.${qi(ref.name)}`;
}

/** Menu for the empty area of the object list (and the Tables folder). */
export function objectsAreaMenu(connId: string, database: string, schema: string | undefined, kind: ObjectKind | undefined): MenuItem[] {
  const tables = kind === ObjectKind.KindTable || kind === undefined;
  return [
    ...(tables ? [{ label: "New Table", shortcut: "⌘N", onClick: () => newTable(connId, database, schema) }] : []),
    later("Set Privileges"),
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

/** Extra database / schema menu items (dump, execute, import/export). */
export function databaseTools(connId: string, database: string, schema?: string): MenuItem[] {
  return [
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
