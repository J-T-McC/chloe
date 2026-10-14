import { create } from "zustand";
import { Connection, Connections, DBType, ObjectKind, ObjectRef, arr, errorText } from "./api";
import { prompt } from "./components/Dialogs";

export type TabKind = "objects" | "query" | "table" | "ddl";

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
}

/** What the tree has selected; drives the Objects tab. */
export interface Selection {
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
  open: Record<string, ConnState>;
  connecting: Record<string, boolean>;
  errors: Record<string, string>;
  selection?: Selection;
  tabs: Tab[];
  activeTab: string;
  editing?: Connection | "new";

  loadConnections(): Promise<void>;
  connect(id: string): Promise<boolean>;
  disconnect(id: string): void;
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

export const useApp = create<AppState>((set, get) => ({
  connections: [],
  open: {},
  connecting: {},
  errors: {},
  tabs: [OBJECTS_TAB],
  activeTab: "objects",

  async loadConnections() {
    const connections = arr(await Connections.List());
    set({ connections });
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
      // Close tabs that belonged to the connection.
      const tabs = s.tabs.filter((t) => t.connId !== id || t.kind === "objects");
      const activeTab = tabs.some((t) => t.id === s.activeTab) ? s.activeTab : "objects";
      return { open, tabs, activeTab, selection: s.selection?.connId === id ? undefined : s.selection };
    });
  },

  select(selection) {
    set({ selection });
  },

  openTab(tab) {
    // Re-use an existing tab for the same object.
    const existing = get().tabs.find(
      (t) => t.kind === tab.kind && tab.kind !== "query" && t.connId === tab.connId && sameRef(t.ref, tab.ref),
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
  return a.database === b.database && (a.schema ?? "") === (b.schema ?? "") && a.name === b.name && a.kind === b.kind;
}

export const isSchemaEngine = (t?: DBType) => t === DBType.PostgreSQL;

export function connById(id?: string) {
  return useApp.getState().connections.find((c) => c.id === id);
}
