// Saved queries (each database's Queries folder), the Save prompt when closing an
// edited query tab, and drafts: the text of open query tabs, kept so it survives
// a quit or crash and comes back on the next start.
import { ObjectKind, Query, errorText, type Draft, type MCPQueryTab, type SavedQuery } from "./api";
import { alert, choose, confirm, prompt } from "./components/Dialogs";
import { Tab, connById, useApp } from "./store";
import { folderKey, useTree } from "./tree";

// ---- editor text --------------------------------------------------------------------

// The live text of each mounted query editor; tab.sql lags behind it.
const editors = new Map<string, () => string>();
const lengths = new Map<string, () => number>();

export function registerQueryEditor(tabId: string, text: () => string, length?: () => number) {
  editors.set(tabId, text);
  if (length) lengths.set(tabId, length);
  return () => {
    editors.delete(tabId);
    lengths.delete(tabId);
  };
}

export const tabText = (t: Tab) => editors.get(t.id)?.() ?? t.sql ?? "";

/** Called by a query editor as its text changes. */
export function queryTextChanged(tabId: string) {
  const t = useApp.getState().tabs.find((x) => x.id === tabId);
  if (!t) return;
  // Typing nearly always changes the length, which Monaco knows without building the text.
  const saved = t.savedSql ?? "";
  const length = lengths.get(tabId)?.();
  const dirty = (length !== undefined && length !== saved.length) || tabText(t) !== saved;
  if (dirty !== !!t.dirty) useApp.getState().updateTab(tabId, { dirty });
  scheduleDrafts();
}

// ---- saved queries -------------------------------------------------------------------

function queriesKey(connId: string, database: string, schema?: string) {
  return folderKey(connId, database, schema, ObjectKind.KindQuery);
}

/** Reload the Queries folder (tree and Objects list) of a database. */
export function refreshFolder(connId: string, database: string, schema?: string) {
  const tree = useTree.getState();
  const key = queriesKey(connId, database, schema);
  if (tree.children[key]) tree.refresh({ key, type: "folder", label: "", connId, database, schema, kind: ObjectKind.KindQuery, depth: 0, expandable: true });
  useApp.getState().bumpFolder(key); // only the Queries folder: not every table list
}

export async function openSavedQuery(connId: string, database: string, schema: string | undefined, name: string) {
  const app = useApp.getState();
  let q: SavedQuery;
  try {
    q = await Query.SavedQueryByName(connId, database, schema ?? "", name);
  } catch (e) {
    return alert("Couldn't open query", errorText(e));
  }
  const open = app.tabs.find((t) => t.kind === "query" && t.queryId === q.id);
  if (open) return app.setActive(open.id);
  app.openDatabase(connId, database);
  app.openTab({ kind: "query", title: q.name, connId, database, schema, sql: q.sql, queryId: q.id, savedSql: q.sql });
}

/** A tab an agent opened over MCP: one of the saved queries, or new unsaved SQL. Nothing runs. */
export function openAgentQuery(t: MCPQueryTab) {
  const schema = t.schema || undefined;
  if (t.queryName) return openSavedQuery(t.connId, t.database, schema, t.queryName);
  const app = useApp.getState();
  app.openDatabase(t.connId, t.database);
  app.openTab({ kind: "query", title: t.title, connId: t.connId, database: t.database, schema, sql: t.sql, savedSql: "", dirty: true });
  scheduleDrafts();
}

/** Save a query tab, asking for a name when it's new (or for Save As). False when cancelled or failed. */
export async function saveQueryTab(tabId: string, saveAs = false): Promise<boolean> {
  const app = useApp.getState();
  const t = app.tabs.find((x) => x.id === tabId);
  if (!t?.connId) return false;
  const sql = tabText(t);
  const database = t.database ?? "";
  const schema = t.schema ?? "";
  let q: SavedQuery | null = null;
  if (t.queryId && !saveAs) q = await Query.SavedQuery(t.queryId).catch(() => null);
  if (q) {
    q = { ...q, sql };
  } else {
    if (!database) {
      await alert("Choose a database first", "A saved query lives in a database's Queries folder. Pick the database in the query's toolbar, then save.");
      return false;
    }
    const base = saveAs && t.queryId ? `${t.title} copy` : "Untitled";
    const name = await prompt(saveAs ? "Save Query As" : "Save Query", {
      message: `Query name (saved in ${[database, schema].filter(Boolean).join(".")})`,
      defaultValue: await Query.UniqueQueryName(t.connId, database, schema, base),
    });
    if (name === null) return false;
    q = { id: "", connId: t.connId, database, schema, name, sql, created: "", modified: "" };
  }
  try {
    const saved = await Query.SaveQuery(q);
    app.updateTab(tabId, { queryId: saved.id, savedSql: sql, sql, title: saved.name, dirty: false });
    refreshFolder(t.connId, database, schema || undefined);
    scheduleDrafts();
    return true;
  } catch (e) {
    await alert("Couldn't save the query", errorText(e));
    return false;
  }
}

export async function renameSavedQuery(connId: string, database: string, schema: string | undefined, name: string) {
  const q = await Query.SavedQueryByName(connId, database, schema ?? "", name).catch(() => null);
  if (!q) return;
  const next = await prompt("Rename Query", { message: "New name", defaultValue: q.name });
  if (next === null || next.trim() === q.name) return;
  try {
    const saved = await Query.RenameQuery(q.id, next);
    for (const t of useApp.getState().tabs) if (t.queryId === q.id) useApp.getState().updateTab(t.id, { title: saved.name });
    refreshFolder(connId, database, schema);
  } catch (e) {
    alert("Couldn't rename the query", errorText(e));
  }
}

export async function duplicateSavedQuery(connId: string, database: string, schema: string | undefined, name: string) {
  const q = await Query.SavedQueryByName(connId, database, schema ?? "", name).catch(() => null);
  if (!q) return;
  try {
    const copy = await Query.UniqueQueryName(connId, database, schema ?? "", `${q.name} copy`);
    await Query.SaveQuery({ ...q, id: "", name: copy });
    refreshFolder(connId, database, schema);
  } catch (e) {
    alert("Couldn't duplicate the query", errorText(e));
  }
}

export async function deleteSavedQueries(connId: string, database: string, schema: string | undefined, names: string[]) {
  if (!names.length) return;
  const what = names.length === 1 ? `query “${names[0]}”` : `${names.length} queries`;
  if (!(await confirm(`Delete ${what}?`, { message: "Saved queries can't be restored once deleted.", okLabel: "Delete" }))) return;
  for (const name of names) {
    const q = await Query.SavedQueryByName(connId, database, schema ?? "", name).catch(() => null);
    if (!q) continue;
    try {
      await Query.DeleteQuery(q.id);
    } catch (e) {
      alert("Couldn't delete the query", errorText(e));
      break;
    }
    // An open tab keeps its text as a new, unsaved query.
    for (const t of useApp.getState().tabs) if (t.queryId === q.id) useApp.getState().updateTab(t.id, { queryId: undefined, savedSql: "", dirty: true });
  }
  refreshFolder(connId, database, schema);
}

// ---- closing ------------------------------------------------------------------------------

/** Close tabs, first asking whether to save each edited query. False when the user cancelled. */
/** Save handlers of other editing tabs (BI designers), by tab id; they resolve false when saving failed. */
export const tabSavers = new Map<string, () => Promise<boolean>>();

export async function requestCloseTabs(ids: string[]): Promise<boolean> {
  const app = useApp.getState();
  for (const id of ids) {
    const t = app.tabs.find((x) => x.id === id);
    const saver = t?.kind === "query" ? () => saveQueryTab(id) : tabSavers.get(id);
    if (!t?.dirty || !saver) continue;
    app.setActive(id);
    const answer = await choose(`Save changes to “${t.title}”?`, {
      message: "Your changes will be lost if you don't save them.",
      buttons: ["Don't Save", "Cancel", "Save"],
    });
    if (answer === null || answer === "Cancel") return false;
    if (answer === "Save" && !(await saver())) return false;
  }
  useApp.getState().closeTabs(ids);
  return true;
}

// ---- drafts -------------------------------------------------------------------------------

let draftTimer: ReturnType<typeof setTimeout> | undefined;
let restored = false;

function drafts(): Draft[] {
  return useApp
    .getState()
    .tabs.filter((t) => t.kind === "query" && t.connId)
    .map((t) => ({ tabId: t.id, connId: t.connId!, database: t.database ?? "", schema: t.schema ?? "", title: t.title, queryId: t.queryId ?? "", sql: tabText(t), kind: t.kind }))
    // An untouched new tab isn't worth bringing back.
    .filter((d) => d.queryId || d.sql.trim());
}

/** Write the open query tabs soon (batched while typing). */
export function scheduleDrafts() {
  if (!restored) return; // don't overwrite the drafts before they're read back
  clearTimeout(draftTimer);
  draftTimer = setTimeout(() => Query.SetDrafts(drafts()).catch(() => {}), 800);
}

/** Reopen the query tabs open when Chloe last quit (or crashed). They connect when first shown. */
export async function restoreDrafts() {
  if (restored) return;
  try {
    const app = useApp.getState();
    const active = app.activeTab;
    for (const d of (await Query.Drafts()) ?? []) {
      if (!connById(d.connId)) continue;
      // A draft of a saved query compares against the query as saved now.
      const q = d.queryId ? await Query.SavedQuery(d.queryId).catch(() => null) : null;
      const savedSql = q?.sql ?? "";
      const title = q?.name ?? d.title;
      const queryId = q?.id;
      app.openTab({ kind: "query", title, connId: d.connId, database: d.database || undefined, schema: d.schema || undefined, sql: d.sql, queryId, savedSql, dirty: d.sql !== savedSql });
    }
    app.setActive(active);
  } finally {
    restored = true;
    // Closing or opening tabs changes what's worth keeping.
    useApp.subscribe((s, prev) => {
      if (s.tabs !== prev.tabs) scheduleDrafts();
    });
  }
}
