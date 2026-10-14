// Object tree model: lazily loaded nodes cached by key, flattened for rendering.
import { create } from "zustand";
import { DBType, Explorer, ObjectInfo, ObjectKind, arr, errorText } from "./api";
import { KIND_LABEL } from "./components/icons";
import { connById, useApp } from "./store";

export type NodeType = "group" | "conn" | "db" | "schema" | "folder" | "object";

export interface TreeNode {
  key: string;
  /** group nodes: the group name */
  group?: string;
  type: NodeType;
  label: string;
  connId: string;
  database?: string;
  schema?: string;
  kind?: ObjectKind;
  info?: ObjectInfo;
  depth: number;
  expandable: boolean;
}

interface TreeState {
  expanded: Record<string, boolean>;
  children: Record<string, TreeNode[]>;
  loading: Record<string, boolean>;
  errors: Record<string, string>;
  filter: string;
  toggle(node: TreeNode): Promise<void>;
  expand(node: TreeNode): Promise<void>;
  refresh(node: TreeNode): Promise<void>;
  collapseConnection(connId: string): void;
  closeDatabase(node: TreeNode): Promise<void>;
  setFilter(f: string): void;
}

const MYSQL_FOLDERS = [ObjectKind.KindTable, ObjectKind.KindView, ObjectKind.KindFunction, ObjectKind.KindProcedure, ObjectKind.KindTrigger, ObjectKind.KindEvent];
const PG_FOLDERS = [ObjectKind.KindTable, ObjectKind.KindView, ObjectKind.KindMatView, ObjectKind.KindFunction, ObjectKind.KindProcedure, ObjectKind.KindSequence, ObjectKind.KindTrigger, ObjectKind.KindType];

export const isPG = (connId: string) => connById(connId)?.type === DBType.PostgreSQL;

async function loadChildren(n: TreeNode): Promise<TreeNode[]> {
  const d = n.depth + 1;
  switch (n.type) {
    case "conn": {
      if (!(await useApp.getState().connect(n.connId))) throw new Error(useApp.getState().errors[n.connId] || "connection failed");
      const dbs = arr(await Explorer.ListDatabases(n.connId));
      return dbs.map((db) => ({ key: `${n.key}/${db}`, type: "db", label: db, connId: n.connId, database: db, depth: d, expandable: true }));
    }
    case "db":
      if (isPG(n.connId)) {
        const schemas = arr(await Explorer.ListSchemas(n.connId, n.database!));
        return schemas.map((s) => ({ key: `${n.key}/${s}`, type: "schema", label: s, connId: n.connId, database: n.database, schema: s, depth: d, expandable: true }));
      }
      return folders(n, MYSQL_FOLDERS);
    case "schema":
      return folders(n, PG_FOLDERS);
    case "folder": {
      const objs = arr(await Explorer.ListObjects(n.connId, n.database!, n.schema ?? "", n.kind!));
      return objs.map((o) => ({
        key: `${n.key}/${o.name}${o.signature ? `(${o.signature})` : ""}`,
        type: "object",
        label: o.signature !== undefined && o.signature !== "" ? `${o.name}(${o.signature})` : o.name,
        connId: n.connId,
        database: n.database,
        schema: o.schema || n.schema,
        kind: n.kind,
        info: o,
        depth: d,
        expandable: false,
      }));
    }
  }
  return [];
}

function folders(n: TreeNode, kinds: ObjectKind[]): TreeNode[] {
  return kinds.map((k) => ({
    key: `${n.key}/#${k}`,
    type: "folder",
    label: KIND_LABEL[k],
    connId: n.connId,
    database: n.database,
    schema: n.schema,
    kind: k,
    depth: n.depth + 1,
    expandable: true,
  }));
}

export const useTree = create<TreeState>((set, get) => ({
  expanded: {},
  children: {},
  loading: {},
  errors: {},
  filter: "",

  async toggle(node) {
    if (node.type === "group") {
      set((s) => ({ expanded: { ...s.expanded, [node.key]: s.expanded[node.key] === false } }));
      return;
    }
    if (get().expanded[node.key]) {
      set((s) => ({ expanded: { ...s.expanded, [node.key]: false } }));
      return;
    }
    await get().expand(node);
  },

  async expand(node) {
    // Expanding a closed database opens it first (Navicat's "Open Database").
    if (node.type === "db" && !useApp.getState().isDbOpen(node.connId, node.database!)) {
      if (!(await useApp.getState().openDatabase(node.connId, node.database!))) return;
    }
    set((s) => ({ expanded: { ...s.expanded, [node.key]: true } }));
    if (get().children[node.key] || get().loading[node.key]) return;
    set((s) => ({ loading: { ...s.loading, [node.key]: true }, errors: { ...s.errors, [node.key]: "" } }));
    try {
      const kids = await loadChildren(node);
      set((s) => ({ children: { ...s.children, [node.key]: kids } }));
    } catch (e) {
      set((s) => ({ errors: { ...s.errors, [node.key]: errorText(e) }, expanded: { ...s.expanded, [node.key]: node.type !== "conn" } }));
    } finally {
      set((s) => ({ loading: { ...s.loading, [node.key]: false } }));
    }
  },

  async refresh(node) {
    // Drop cached children of this node and everything below it.
    set((s) => {
      const children = { ...s.children };
      for (const k of Object.keys(children)) if (k === node.key || k.startsWith(node.key + "/")) delete children[k];
      return { children };
    });
    if (get().expanded[node.key]) {
      set((s) => ({ expanded: { ...s.expanded, [node.key]: false } }));
      await get().expand(node);
    }
  },

  closeDatabase(node) {
    set((s) => {
      const strip = <T,>(m: Record<string, T>) => Object.fromEntries(Object.entries(m).filter(([k]) => k !== node.key && !k.startsWith(node.key + "/")));
      return { expanded: strip(s.expanded), children: strip(s.children) };
    });
    return useApp.getState().closeDatabase(node.connId, node.database!);
  },

  collapseConnection(connId) {
    set((s) => {
      const strip = <T,>(m: Record<string, T>) => Object.fromEntries(Object.entries(m).filter(([k]) => k !== connId && !k.startsWith(connId + "/")));
      return { expanded: strip(s.expanded), children: strip(s.children) };
    });
  },

  setFilter(filter) {
    set({ filter });
  },
}));

/** Flatten the visible part of the tree for virtualized rendering. */
export function flatten(roots: TreeNode[], expanded: Record<string, boolean>, children: Record<string, TreeNode[]>, filter: string): TreeNode[] {
  const out: TreeNode[] = [];
  const f = filter.trim().toLowerCase();
  const walk = (nodes: TreeNode[]) => {
    for (const n of nodes) {
      // The filter applies to leaf objects; containers always show.
      if (f && n.type === "object" && !n.label.toLowerCase().includes(f)) continue;
      out.push(n);
      // Groups are expanded unless explicitly collapsed.
      const open = n.type === "group" ? expanded[n.key] !== false : expanded[n.key];
      if (open && children[n.key]) walk(children[n.key]);
    }
  };
  walk(roots);
  return out;
}
