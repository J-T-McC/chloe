import { create } from "zustand";
import { Connection, Connections, DBType, Explorer, ObjectKind, ObjectRef, arr, errorText } from "./api";
import { prompt } from "./components/Dialogs";

export type TabKind = "objects" | "query" | "table" | "ddl" | "monitor" | "design" | "erd" | "model";

export interface Tab {
  id: string;
  kind: TabKind;
  title: string;
  connId?: string;
  database?: string;
  schema?: string;
  /** table / view being viewed (table and ddl tabs) */
  ref?: ObjectRef;
  /** initial SQL for query tabs */
  sql?: string;
  /** file of a model tab */
  modelPath?: string;
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

  loadConnections(): Promise<void>;
  connect(id: string): Promise<boolean>;
  disconnect(id: string): void;
  disconnectAll(): void;
  openDatabase(connId: string, db: string): Promise<boolean>;
  closeDatabase(connId: string, db: string): Promise<void>;
  isDbOpen(connId: string, db: string): boolean;
  setManaging(v: boolean): void;
  select(sel: Selection): void;
  openTab(tab: Omit<Tab, "id"> & { id?: string }): string;
  closeTab(id: string): void;
  setActive(id: string): void;
  updateTab(id: string, patch: Partial<Tab>): void;
  editConnection(c?: Connection | "new"): void;
}

let seq = 0;
export const newId = (p: string) => `${p}-${++seq}-${Date.now().toString(36)}`;

const OBJECTS_TAB: Tab = { id: "objects", kind: "objects", title: "Objects" };

export const dbKey = (connId: string, db: string) => `${connId}/${db}`;

export const useApp = create<AppState>((set, get) => ({
  connections: [],
  groups: [],
  open: {},
  openDbs: {},
  managing: false,
  objectsVersion: 0,
  connecting: {},
  errors: {},
  tabs: [OBJECTS_TAB],
  activeTab: "objects",

  async loadConnections() {
    const [connections, groups] = await Promise.all([Connections.List(), Connections.Groups()]);
    set({ connections: arr(connections), groups: arr(groups) });
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
      // Close tabs that belonged to the connection.
      const tabs = s.tabs.filter((t) => t.connId !== id || t.kind === "objects");
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
      const tabs = s.tabs.filter((t) => t.kind === "objects" || t.connId !== connId || (t.database ?? t.ref?.database) !== db);
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
    set({ selection, selectedGroup: selection.group ?? conn?.group ?? undefined });
  },

  openTab(tab) {
    // Re-use an existing tab for the same object.
    const existing = get().tabs.find(
      (t) =>
        t.kind === tab.kind &&
        (tab.kind === "model" ? !!tab.modelPath && t.modelPath === tab.modelPath : tab.kind !== "query" && t.connId === tab.connId && sameRef(t.ref, tab.ref)),
    );
    if (existing) {
      set({ activeTab: existing.id });
      return existing.id;
    }
    const id = tab.id ?? newId(tab.kind);
    set((s) => ({ tabs: [...s.tabs, { ...tab, id }], activeTab: id }));
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

  setActive(activeTab) {
    set({ activeTab });
  },

  updateTab(id, patch) {
    set((s) => ({ tabs: s.tabs.map((t) => (t.id === id ? { ...t, ...patch } : t)) }));
  },

  editConnection(editing) {
    set({ editing });
  },
}));

function sameRef(a?: ObjectRef, b?: ObjectRef) {
  if (!a || !b) return a === b;
  return a.database === b.database && (a.schema ?? "") === (b.schema ?? "") && a.name === b.name && a.kind === b.kind && (a.signature ?? "") === (b.signature ?? "");
}

export const isSchemaEngine = (t?: DBType) => t === DBType.PostgreSQL;

export function connById(id?: string) {
  return useApp.getState().connections.find((c) => c.id === id);
}
