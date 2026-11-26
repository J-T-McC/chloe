// Copy and Paste of tables between databases and schemas of one connection. The
// copied tables live in memory only: Paste never reads the system clipboard.
import { create } from "zustand";
import { DBType, Design, ObjectKind, ObjectRef, arr, copyText, errorText } from "./api";
import { alert } from "./components/Dialogs";
import { invalidateCatalog } from "./editor/completion";
import { connById, useApp } from "./store";
import { refreshFolder } from "./tableActions";
import type { TreeNode } from "./tree";

/** Where a paste creates its copies: a database, or a schema of one. */
export interface PasteSpot {
  connId: string;
  database: string;
  schema?: string;
}

interface TableClipboard {
  copied: { connId: string; refs: ObjectRef[] } | null;
  /** Tree rows showing the working spinner, by tree key. */
  busy: Record<string, true>;
}

export const useTableClipboard = create<TableClipboard>(() => ({ copied: null, busy: {} }));

/** Targets with a paste running, so a second Paste on one does nothing. */
const running = new Set<string>();

const typeOf = (connId: string) => connById(connId)?.type;

/** Remember tables for Paste and put their names on the clipboard, one per line. */
export function copyTables(connId: string, refs: ObjectRef[]) {
  useTableClipboard.setState({ copied: { connId, refs } });
  return copyText(refs.map((r) => r.name).join("\n"));
}

/** The database or schema a Paste on this tree row goes to, or null when the row takes none. */
export function pasteTargetOf(n: TreeNode): PasteSpot | null {
  const type = typeOf(n.connId);
  if (type === DBType.MongoDB || type === DBType.Redis) return null;
  switch (n.type) {
    case "db":
      return type === DBType.PostgreSQL || n.count !== undefined ? null : { connId: n.connId, database: n.database };
    case "schema":
      return { connId: n.connId, database: n.database, schema: n.schema };
    case "folder":
    case "object":
      return n.kind === ObjectKind.KindTable ? { connId: n.connId, database: n.database, schema: n.schema || n.info?.schema || undefined } : null;
  }
  return null;
}

/** The target of the object list showing kind (undefined lists tables) in database or schema. */
export function listTargetOf(connId: string, database: string, schema: string | undefined, kind: ObjectKind | undefined): PasteSpot | null {
  return kind === ObjectKind.KindTable || kind === undefined ? { connId, database, schema } : null;
}

export function canPaste(target: PasteSpot | null): target is PasteSpot {
  const copied = useTableClipboard.getState().copied;
  if (!target || !copied || copied.connId !== target.connId) return false;
  return typeOf(target.connId) !== DBType.PostgreSQL || !!target.schema;
}

const spotKey = (t: PasteSpot) => [t.connId, t.database, t.schema ?? ""].join("\u0000");

/** Paste the copied tables into target, showing the spinner on tree row busyKey while it runs. */
export async function pasteTables(target: PasteSpot, busyKey: string) {
  const copied = useTableClipboard.getState().copied;
  const key = spotKey(target);
  if (!copied || !canPaste(target) || running.has(key)) return;
  running.add(key);
  setBusy(busyKey, true);
  try {
    const results = arr(await Design.PasteTables(target.connId, copied.refs, { database: target.database, schema: target.schema }));
    const failed = results.filter((r) => r.error);
    if (failed.length > 0)
      alert(
        failed.length === 1 ? "A table could not be pasted" : `${failed.length} tables could not be pasted`,
        failed.map((r) => `${r.source.name}: ${r.error}${r.created ? " (the copy was created without its rows)" : ""}`).join("\n"),
      );
  } catch (e) {
    alert("Paste failed", errorText(e));
  } finally {
    running.delete(key);
    setBusy(busyKey, false);
    refreshTarget(target);
  }
}

function refreshTarget(t: PasteSpot) {
  refreshFolder(t.connId, t.database, t.schema, ObjectKind.KindTable);
  invalidateCatalog(t.connId, t.database);
  useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 }));
}

function setBusy(key: string, on: boolean) {
  useTableClipboard.setState((s) => {
    const busy = { ...s.busy };
    if (on) busy[key] = true;
    else delete busy[key];
    return { busy };
  });
}
