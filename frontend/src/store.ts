import { create } from "zustand";
import { Connection, Connections, Explorer, ObjectKind, ObjectRef, arr, errorText } from "./api";
import { alert, prompt } from "./components/Dialogs";

interface TabBase {
  id: string;
  title: string;
  connId?: string;
  database?: string;
  schema?: string;
  /** the object the tab shows (table, view, designer, collection), or the account a user tab edits */
  ref?: ObjectRef;
  /** initial SQL for query tabs */
  sql?: string;
  /** file of a model tab */
  modelPath?: string;
  /** BI workspace file of a bi / bisource / bichart / bidash tab */
  biPath?: string;
  /** the data source, chart or dashboard the tab edits ("" for a new one) */
  biId?: string;
  /** saved query a query tab edits */
  queryId?: string;
  /** the text as last saved (or opened); the tab is dirty once it differs */
  savedSql?: string;
  dirty?: boolean;
}

type OnConn = { connId: string };
type OnObject = OnConn & { ref: ObjectRef };

/** What each kind of tab always has. */
interface TabNeeds {
  objects: object;
  history: object;
  mcp: object;
  query: OnConn;
  mongoquery: OnConn;
  monitor: OnConn;
  users: OnConn;
  redisconsole: OnConn;
  mongoshell: OnConn;
  mongousers: OnConn;
  table: OnObject;
  ddl: OnObject;
  design: OnObject;
  viewdesign: OnObject;
  collection: OnObject;
  mongodesign: OnObject;
  user: OnObject;
  erd: OnConn & { database: string };
  model: object;
  bi: { biPath: string };
  bisource: { biPath: string };
  bichart: { biPath: string };
  bidash: { biPath: string };
}

export type TabKind = keyof TabNeeds;
export type TabOf<K extends TabKind> = TabBase & TabNeeds[K] & { kind: K };
export type Tab = { [K in TabKind]: TabOf<K> }[TabKind];
/** A tab to open: its id is made up unless given. */
export type NewTab = { [K in TabKind]: Omit<TabOf<K>, "id"> & { id?: string } }[TabKind];

const refKey = (r: ObjectRef) => [r.database, r.schema ?? "", r.name, r.kind, r.signature ?? ""].join("\u0000");

/** Opening a tab with the key of an open one shows that one instead; null: always a new tab. */
function tabKey(t: NewTab): string | null {
  switch (t.kind) {
    case "objects":
    case "history":
    case "mcp":
      return t.kind;
    case "query":
    case "mongoquery":
    case "mongoshell":
    case "redisconsole":
      return null;
    case "monitor":
    case "users":
      return `${t.kind}:${t.connId}`;
    case "mongousers":
      return `${t.kind}:${t.connId}:${t.database ?? ""}`;
    case "erd":
      return `erd:${t.connId}:${t.database}:${t.schema ?? ""}`;
    case "model":
      return t.modelPath ? `model:${t.modelPath}` : null;
    case "bi":
      return `bi:${t.biPath}`;
    case "bisource":
    case "bichart":
    case "bidash":
      return t.biId ? `${t.kind}:${t.biPath}:${t.biId}` : null;
    default:
      // An object; a new designer (no name yet) gets its own tab.
      return t.ref.name ? `${t.kind}:${t.connId}:${refKey(t.ref)}` : null;
  }
}

/** What the tree has selected; drives the Objects tab. */
export interface Selection {
  /** Set when a group folder (not a connection) is selected. */
  group?: string;
  connId: string;
  database?: string;
  schema?: string;
  kind?: ObjectKind;
  object?: ObjectRef;
}

interface ConnState {
  version: string;
}

interface AppState {
  connections: Connection[];
  groups: string[];
  /** Group of the selected tree node, used as the default for new connections. */
  selectedGroup?: string;
  open: Record<string, ConnState>;
  /** Opened databases, keyed "connId/database". */
  openDbs: Record<string, true>;
  connecting: Record<string, boolean>;
  errors: Record<string, string>;
  selection?: Selection;
  tabs: Tab[];
  activeTab: string;
  editing?: Connection | "new";
  managing: boolean;
  /** Bumped when objects change (create/drop/rename) so lists reload. */
  objectsVersion: number;
  /** Bumped per object folder (folderKey) when only that folder changed: a saved query doesn't reload the tables. */
  folderVersions: Record<string, number>;
  bumpFolder(key: string): void;
  /** The Objects tab shows Chloe's model library (the Model toolbar button) instead of the selection's objects. */
  showingModels: boolean;
  showModels(): void;
  /** The Objects tab shows the BI workspace library (the BI toolbar button). */
  showingBI: boolean;
  showBI(): void;

  loadConnections(): Promise<void>;
  connect(id: string): Promise<boolean>;
  disconnect(id: string): void;
  disconnectAll(): void;
  openDatabase(connId: string, db: string): Promise<boolean>;
  closeDatabase(connId: string, db: string): Promise<void>;
  isDbOpen(connId: string, db: string): boolean;
  setManaging(v: boolean): void;
  select(sel: Selection): void;
  openTab(tab: NewTab): string;
  closeTab(id: string): void;
  /** Close several tabs at once (the Objects tab always stays). */
  closeTabs(ids: string[]): void;
  setActive(id: string): void;
  /** Changes a tab's fields (never its kind). */
  updateTab(id: string, patch: Partial<Omit<TabBase, "id">>): void;
  editConnection(c?: Connection | "new"): void;
}

let seq = 0;
export const newId = (p: string) => `${p}-${++seq}-${Date.now().toString(36)}`;

const OBJECTS_TAB: Tab = { id: "objects", kind: "objects", title: "Objects" };

export const dbKey = (connId: string, db: string) => `${connId}/${db}`;

let lockedShown = false;

export const useApp = create<AppState>((set, get) => ({
  connections: [],
  groups: [],
  open: {},
  openDbs: {},
  managing: false,
  objectsVersion: 0,
  folderVersions: {},
  bumpFolder: (key) => set((s) => ({ folderVersions: { ...s.folderVersions, [key]: (s.folderVersions[key] ?? 0) + 1 } })),
  showingModels: false,
  showingBI: false,
  connecting: {},
  errors: {},
  tabs: [OBJECTS_TAB],
  activeTab: "objects",

  async loadConnections() {
    const [connections, groups] = await Promise.all([Connections.List(), Connections.Groups()]);
    set({ connections: arr(connections), groups: arr(groups) });
    // Tabs of a deleted connection go with it.
    const ids = new Set(arr(connections).map((c) => c.id));
    const gone = get().tabs.filter((t) => t.connId && !ids.has(t.connId)).map((t) => t.id);
    if (gone.length) get().closeTabs(gone);
    if (!lockedShown) {
      lockedShown = true;
      const why = await Connections.Locked().catch(() => "");
      if (why) alert("Saved connections are locked", why);
    }
  },

  async connect(id) {
    if (get().open[id]) return true;
    set((s) => ({ connecting: { ...s.connecting, [id]: true }, errors: { ...s.errors, [id]: "" } }));
    try {
      let password = "";
      const conn = get().connections.find((c) => c.id === id);
      if (conn && !conn.savePassword) {
        const p = await prompt(`Connect to ${conn.name}`, { message: `Password for ${conn.user}@${conn.host}`, password: true });
        if (p === null) return false;
        password = p;
      }
      const version = await Connections.Connect(id, password);
      set((s) => ({ open: { ...s.open, [id]: { version } } }));
      // Databases marked "Auto Open" (and the initial database) open with the connection.
      if (conn) {
        const auto = arr(conn.databases?.list).filter((d) => d.autoOpen).map((d) => d.name);
        if (conn.initialDatabase && !auto.includes(conn.initialDatabase)) auto.push(conn.initialDatabase);
        set((s) => ({ openDbs: { ...s.openDbs, ...Object.fromEntries(auto.map((d) => [dbKey(id, d), true as const])) } }));
      }
      return true;
    } catch (e) {
      set((s) => ({ errors: { ...s.errors, [id]: errorText(e) } }));
      return false;
    } finally {
      set((s) => ({ connecting: { ...s.connecting, [id]: false } }));
    }
  },

  disconnect(id) {
    Connections.Disconnect(id);
    set((s) => {
      const open = { ...s.open };
      delete open[id];
      const openDbs = Object.fromEntries(Object.entries(s.openDbs).filter(([k]) => !k.startsWith(id + "/")));
      // Close tabs that belonged to the connection. Query tabs stay: they hold the
      // user's text and reconnect when run again.
      const tabs = s.tabs.filter((t) => t.connId !== id || t.kind === "objects" || t.kind === "query");
      const activeTab = tabs.some((t) => t.id === s.activeTab) ? s.activeTab : "objects";
      return { open, openDbs, tabs, activeTab, selection: s.selection?.connId === id ? undefined : s.selection };
    });
  },

  disconnectAll() {
    for (const id of Object.keys(get().open)) get().disconnect(id);
  },

  async openDatabase(connId, db) {
    if (!(await get().connect(connId))) return false;
    set((s) => ({ openDbs: { ...s.openDbs, [dbKey(connId, db)]: true } }));
    return true;
  },

  async closeDatabase(connId, db) {
    await Explorer.CloseDatabase(connId, db).catch(() => {});
    set((s) => {
      const openDbs = { ...s.openDbs };
      delete openDbs[dbKey(connId, db)];
      // Tabs working in that database close with it.
      const tabs = s.tabs.filter((t) => t.kind === "objects" || t.kind === "query" || t.connId !== connId || (t.database ?? t.ref?.database) !== db);
      const activeTab = tabs.some((t) => t.id === s.activeTab) ? s.activeTab : "objects";
      const selection = s.selection?.connId === connId && s.selection.database === db ? { connId, database: db } : s.selection;
      return { openDbs, tabs, activeTab, selection };
    });
  },

  isDbOpen(connId, db) {
    return !!get().openDbs[dbKey(connId, db)];
  },

  setManaging(managing) {
    set({ managing });
  },

  select(selection) {
    const conn = get().connections.find((c) => c.id === selection.connId);
    set({ selection, selectedGroup: selection.group ?? conn?.group ?? undefined, showingModels: false, showingBI: false });
  },

  showModels() {
    set({ showingModels: true, showingBI: false, activeTab: "objects" });
  },

  showBI() {
    set({ showingBI: true, showingModels: false, activeTab: "objects" });
  },

  openTab(tab) {
    // Re-use an open tab for the same object.
    const key = tabKey(tab);
    const existing = key === null ? undefined : get().tabs.find((t) => tabKey(t) === key);
    if (existing) {
      set({ activeTab: existing.id });
      return existing.id;
    }
    const id = tab.id ?? newId(tab.kind);
    set((s) => ({ tabs: [...s.tabs, { ...tab, id } as Tab], activeTab: id }));
    return id;
  },

  closeTab(id) {
    if (id === "objects") return;
    set((s) => {
      const idx = s.tabs.findIndex((t) => t.id === id);
      const tabs = s.tabs.filter((t) => t.id !== id);
      const activeTab = s.activeTab === id ? (tabs[Math.max(0, idx - 1)]?.id ?? "objects") : s.activeTab;
      return { tabs, activeTab };
    });
  },

  closeTabs(ids) {
    const gone = new Set(ids.filter((id) => id !== "objects"));
    set((s) => {
      const tabs = s.tabs.filter((t) => !gone.has(t.id));
      let activeTab = s.activeTab;
      if (gone.has(activeTab)) {
        const idx = s.tabs.findIndex((t) => t.id === activeTab);
        // The nearest surviving tab to the left, else the first one.
        activeTab = [...s.tabs.slice(0, idx)].reverse().find((t) => !gone.has(t.id))?.id ?? tabs[0]?.id ?? "objects";
      }
      return { tabs, activeTab };
    });
  },

  setActive(activeTab) {
    set({ activeTab });
  },

  updateTab(id, patch) {
    set((s) => ({ tabs: s.tabs.map((t) => (t.id === id ? ({ ...t, ...patch } as Tab) : t)) }));
  },

  editConnection(editing) {
    set({ editing });
  },
}));

export function connById(id?: string) {
  return useApp.getState().connections.find((c) => c.id === id);
}
