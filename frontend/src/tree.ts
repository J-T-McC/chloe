// Object tree model: lazily loaded nodes cached by key, flattened for rendering.
import { create } from "zustand";
import { DBType, Explorer, Mongo, ObjectInfo, ObjectKind, Redis, arr, errorText } from "./api";
import { KIND_LABEL } from "./components/icons";
import { connById, useApp } from "./store";
import { isPG } from "./dialect";

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
  /** Redis databases: number of keys */
  count?: number;
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
  /** Key of a node the sidebar should scroll to and select (set by reveal). */
  revealKey: string | null;
  /** Expand the tree down to a connection, database, schema, folder or object. */
  reveal(t: RevealTarget): Promise<void>;
}

export interface RevealTarget {
  connId: string;
  database?: string;
  schema?: string;
  kind?: ObjectKind;
  name?: string;
  signature?: string;
}

const MYSQL_FOLDERS = [ObjectKind.KindTable, ObjectKind.KindView, ObjectKind.KindFunction, ObjectKind.KindProcedure, ObjectKind.KindTrigger, ObjectKind.KindEvent, ObjectKind.KindQuery];
const PG_FOLDERS = [ObjectKind.KindTable, ObjectKind.KindView, ObjectKind.KindMatView, ObjectKind.KindFunction, ObjectKind.KindProcedure, ObjectKind.KindSequence, ObjectKind.KindTrigger, ObjectKind.KindType, ObjectKind.KindQuery];

const SQLITE_FOLDERS = [ObjectKind.KindTable, ObjectKind.KindView, ObjectKind.KindIndex, ObjectKind.KindTrigger, ObjectKind.KindQuery];
const MONGO_FOLDERS = [ObjectKind.KindCollection, ObjectKind.KindView];

export const isMongo = (connId: string) => connById(connId)?.type === DBType.MongoDB;


// ---- shared folder listings -------------------------------------------------------

// Clicking a folder in the tree both expands it and shows it in the Objects list,
// which asked the server for the same listing twice (slow on MySQL's
// information_schema). A listing is shared while it loads and for a moment after;
// a change to its folder, or a refresh, drops it.
const LISTING_TTL = 2000;
const listings = new Map<string, { at: number; p: Promise<ObjectInfo[]> }>();

export function listObjects(connId: string, database: string, schema: string | undefined, kind: ObjectKind): Promise<ObjectInfo[]> {
  const k = folderKey(connId, database, schema, kind);
  const hit = listings.get(k);
  if (hit && Date.now() - hit.at < LISTING_TTL) return hit.p;
  const p = Explorer.ListObjects(connId, database, schema ?? "", kind).then(arr);
  listings.set(k, { at: Date.now(), p });
  p.catch(() => listings.delete(k));
  return p;
}

/** Drop shared listings of a folder, or of every folder under a tree key. */
export function forgetListings(prefix: string) {
  for (const k of [...listings.keys()]) if (k === prefix || k.startsWith(prefix + "/")) listings.delete(k);
}

useApp.subscribe((s, prev) => {
  if (s.objectsVersion !== prev.objectsVersion) listings.clear();
  else if (s.folderVersions !== prev.folderVersions) for (const k of Object.keys(s.folderVersions)) if (s.folderVersions[k] !== prev.folderVersions[k]) forgetListings(k);
});

async function loadChildren(n: TreeNode): Promise<TreeNode[]> {
  const d = n.depth + 1;
  switch (n.type) {
    case "conn": {
      if (!(await useApp.getState().connect(n.connId))) throw new Error(useApp.getState().errors[n.connId] || "connection failed");
      if (connById(n.connId)?.type === DBType.Redis) {
        const dbs = arr(await Redis.Databases(n.connId));
        return dbs.map((x) => ({ key: `${n.key}/db${x.index}`, type: "db", label: `db${x.index}`, connId: n.connId, database: String(x.index), count: x.keys, depth: d, expandable: false }));
      }
      if (isMongo(n.connId)) {
        const dbs = arr(await Mongo.Databases(n.connId));
        return dbs.map((x) => ({ key: `${n.key}/${x.name}`, type: "db", label: x.name, connId: n.connId, database: x.name, depth: d, expandable: true }));
      }
      const dbs = arr(await Explorer.ListDatabases(n.connId));
      return dbs.map((db) => ({ key: `${n.key}/${db}`, type: "db", label: db, connId: n.connId, database: db, depth: d, expandable: true }));
    }
    case "db":
      if (isMongo(n.connId)) return folders(n, MONGO_FOLDERS);
      if (isPG(n.connId)) {
        const schemas = arr(await Explorer.ListSchemas(n.connId, n.database!));
        return schemas.map((s) => ({ key: `${n.key}/${s}`, type: "schema", label: s, connId: n.connId, database: n.database, schema: s, depth: d, expandable: true }));
      }
      return folders(n, connById(n.connId)?.type === DBType.SQLite ? SQLITE_FOLDERS : MYSQL_FOLDERS);
    case "schema":
      return folders(n, PG_FOLDERS);
    case "folder": {
      if (isMongo(n.connId)) {
        const view = n.kind === ObjectKind.KindView;
        const colls = arr(await Mongo.Collections(n.connId, n.database!, false)).filter((c) => (c.type === "view") === view);
        return colls.map((c) => ({
          key: `${n.key}/${c.name}`,
          type: "object",
          label: c.name,
          connId: n.connId,
          database: n.database,
          kind: n.kind,
          info: { database: n.database!, name: c.name, kind: n.kind! },
          depth: d,
          expandable: false,
        }));
      }
      const objs = await listObjects(n.connId, n.database!, n.schema, n.kind!);
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

/** The tree key of an object folder, such as conn/db/schema/#table (the same as folders() builds). */
export function folderKey(connId: string, database: string | undefined, schema: string | undefined, kind: string): string {
  return [connId, database, schema, "#" + kind].filter(Boolean).join("/");
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
  revealKey: null,

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
    // Callers may pass a placeholder for the node; its children's indent comes
    // from the node's real depth in the tree.
    const real = Object.values(get().children)
      .flat()
      .find((c) => c.key === node.key);
    if (real) node = { ...node, depth: real.depth };
    forgetListings(node.key);
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

  async reveal(t) {
    const conn = connById(t.connId);
    if (!conn) return;
    const node = (key: string, type: NodeType, extra: Partial<TreeNode> = {}): TreeNode => ({ key, type, label: "", connId: t.connId, depth: 0, expandable: true, ...extra });
    // Hidden by a search? Clear it so the target can show.
    set((s) => ({ filter: "", expanded: conn.group ? { ...s.expanded, ["group:" + conn.group]: true } : s.expanded }));
    let key = t.connId;
    await get().expand(node(key, "conn"));
    const redis = conn.type === DBType.Redis;
    if (t.database !== undefined && t.database !== "") {
      key = redis ? `${t.connId}/db${t.database}` : `${t.connId}/${t.database}`;
      if (!redis) await get().expand(node(key, "db", { database: t.database }));
      if (!redis && t.schema && isPG(t.connId)) {
        key = `${key}/${t.schema}`;
        await get().expand(node(key, "schema", { database: t.database, schema: t.schema }));
      }
      if (!redis && t.kind) {
        key = `${key}/#${t.kind}`;
        await get().expand(node(key, "folder", { database: t.database, schema: t.schema, kind: t.kind }));
        if (t.name) {
          const objKey = `${key}/${t.name}${t.signature ? `(${t.signature})` : ""}`;
          if (get().children[key]?.some((c) => c.key === objKey)) key = objKey;
        }
      }
    }
    set({ revealKey: key });
  },

  setFilter(filter) {
    set({ filter });
  },
}));

/**
 * Flatten the visible part of the tree for virtualized rendering. With a filter, only
 * nodes that match or contain a (loaded) match are shown, and the path to every match
 * opens; the contents of a matching node show unfiltered.
 */
export function flatten(roots: TreeNode[], expanded: Record<string, boolean>, children: Record<string, TreeNode[]>, filter: string): TreeNode[] {
  const out: TreeNode[] = [];
  const f = filter.trim().toLowerCase();
  const hit = new Map<string, boolean>();
  const contains = (n: TreeNode): boolean => {
    let v = hit.get(n.key);
    if (v === undefined) {
      v = n.label.toLowerCase().includes(f) || (children[n.key] ?? []).some(contains);
      hit.set(n.key, v);
    }
    return v;
  };
  // Groups are expanded unless explicitly collapsed.
  const isOpen = (n: TreeNode) => (n.type === "group" ? expanded[n.key] !== false : !!expanded[n.key]);
  const walk = (nodes: TreeNode[], filtering: boolean) => {
    for (const n of nodes) {
      if (filtering && !contains(n)) continue;
      out.push(n);
      const kids = children[n.key];
      if (!kids) continue;
      if (!filtering) {
        if (isOpen(n)) walk(kids, false);
      } else if (n.label.toLowerCase().includes(f)) {
        if (isOpen(n)) walk(kids, false);
      } else {
        walk(kids, true); // reveal the matches inside
      }
    }
  };
  walk(roots, !!f);
  return out;
}

/** Split a label around the first match of the filter, for highlighting. */
export function matchParts(label: string, filter: string): [string, string, string] | null {
  const f = filter.trim().toLowerCase();
  const i = f ? label.toLowerCase().indexOf(f) : -1;
  return i < 0 ? null : [label.slice(0, i), label.slice(i, i + f.length), label.slice(i + f.length)];
}
