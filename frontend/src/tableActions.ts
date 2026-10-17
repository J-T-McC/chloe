// Table operations from the tree and object list. Every destructive action shows the
// SQL it will run before running it.
import { Design, ObjectKind, ObjectRef, TableAction, arr, errorText } from "./api";
import { alert, confirm, prompt } from "./components/Dialogs";
import { invalidateCatalog } from "./editor/completion";
import { useApp } from "./store";
import { folderKey, useTree } from "./tree";

export function designTable(connId: string, ref: ObjectRef) {
  useApp.getState().openTab({ kind: "design", title: `${ref.name} (design)`, connId, database: ref.database, schema: ref.schema, ref: { ...ref, kind: ObjectKind.KindTable } });
}

export function newTable(connId: string, database: string, schema?: string) {
  // A fresh id each time, so several new-table designers can be open at once.
  useApp.getState().openTab({ kind: "design", title: "Untitled (design)", connId, database, schema, ref: { database, schema, name: "", kind: ObjectKind.KindTable, signature: String(Date.now()) } });
}

const LABEL: Record<string, { title: string; ok: string; danger: boolean; needsName?: boolean }> = {
  [TableAction.ActionDrop]: { title: "Delete", ok: "Delete", danger: true },
  [TableAction.ActionTruncate]: { title: "Truncate", ok: "Truncate", danger: true },
  [TableAction.ActionEmpty]: { title: "Empty", ok: "Empty", danger: true },
  [TableAction.ActionRename]: { title: "Rename", ok: "Rename", danger: false, needsName: true },
  [TableAction.ActionDuplicate]: { title: "Duplicate (structure only)", ok: "Duplicate", danger: false, needsName: true },
  [TableAction.ActionCopy]: { title: "Duplicate (structure and data)", ok: "Copy", danger: false, needsName: true },
};

export async function runTableAction(connId: string, ref: ObjectRef, action: TableAction) {
  const l = LABEL[action];
  let newName = "";
  if (l.needsName) {
    const suggested = action === TableAction.ActionRename ? ref.name : `${ref.name}_copy`;
    const n = await prompt(`${l.title} “${ref.name}”`, { message: "New table name", defaultValue: suggested });
    if (!n?.trim() || n.trim() === ref.name) return;
    newName = n.trim();
  }
  let sql: string[];
  try {
    sql = arr(await Design.TableActionSQL(connId, ref, action, newName));
  } catch (e) {
    return alert(`${l.title} failed`, errorText(e));
  }
  const what = ref.kind === ObjectKind.KindView ? "view" : "table";
  const note =
    action === TableAction.ActionEmpty
      ? "Deletes every row (triggers fire; auto-increment keeps counting).\n\n"
      : action === TableAction.ActionTruncate
        ? "Removes every row quickly and resets auto-increment.\n\n"
        : action === TableAction.ActionDrop
          ? `The ${what} and all its data are permanently deleted.\n\n`
          : "";
  if (!(await confirm(`${l.title} ${what} “${ref.name}”?`, { message: note + sql.join(";\n") + ";", danger: l.danger, okLabel: l.ok }))) return;
  const res = await Design.RunTableAction(connId, ref, action, newName);
  if (res.error) return alert(`${l.title} failed`, res.error + (res.failedStatement ? `\n\n${res.failedStatement}` : ""));

  // Keep the UI in step with the change.
  const app = useApp.getState();
  if (action === TableAction.ActionDrop || action === TableAction.ActionRename) {
    for (const t of app.tabs) if (t.connId === connId && t.ref?.name === ref.name && t.ref.database === ref.database && (t.ref.schema ?? "") === (ref.schema ?? "")) app.closeTab(t.id);
  }
  invalidateCatalog(connId, ref.database);
  refreshFolder(connId, ref.database, ref.schema, ref.kind);
  useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 }));
}

export function refreshFolder(connId: string, database: string, schema: string | undefined, kind: ObjectKind) {
  const tree = useTree.getState();
  const key = folderKey(connId, database, schema, kind);
  if (tree.children[key]) tree.refresh({ key, type: "folder", label: "", connId, database, schema, kind, depth: 0, expandable: true });
}

