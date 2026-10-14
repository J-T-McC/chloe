// JSON key completion inside path expressions:
//   MySQL  JSON_EXTRACT(attrs, '$.a.b|'), attrs->'$.a|', attrs->>'$.a|', JSON_SET(...)
//   PG     attrs->'a'->>'b|', attrs #> '{a,b|}', jsonb_path_query(attrs, '$.a|'),
//          jsonb_extract_path(attrs, 'a', 'b|')
// Keys come from a sample of the column's values (merged into one key tree) plus
// paths already written against the same column in the editor.

import { Analysis } from "./analyze";
import { CatalogIndex, CompletionCatalog, Suggestion, indexOf } from "./complete";
import { DialectName, Token, isIdent, significant, tokenize } from "./lexer";

/** Path segment: an object key, or ARRAY for "any element". */
export const ARRAY = "[]";

export type PathStyle =
  | "jsonpath" // '$.a.b' (MySQL paths, PG SQL/JSON path)
  | "key" // one key per string: PG -> / ->> chains, jsonb_extract_path args
  | "array"; // PG text-array path '{a,b}'

export interface JsonPathContext {
  /** Column the path applies to, as written: [qualifier?, column]. */
  column: string[];
  /** Segments before the one being typed. */
  path: string[];
  /** Partial key being typed. */
  prefix: string;
  /** Offset where the partial key starts (replace range). */
  replaceStart: number;
  style: PathStyle;
  /** jsonpath: the key was opened with a double quote. */
  quoted?: boolean;
  /** jsonpath: the string is empty, so suggestions start with "$.". */
  root?: boolean;
}

// Function → which argument positions (0-based) hold a path.
const MYSQL_PATH_FUNCS: Record<string, (i: number) => boolean> = {
  JSON_EXTRACT: (i) => i >= 1,
  JSON_VALUE: (i) => i === 1,
  JSON_KEYS: (i) => i === 1,
  JSON_LENGTH: (i) => i === 1,
  JSON_CONTAINS: (i) => i === 2,
  JSON_CONTAINS_PATH: (i) => i >= 2,
  JSON_REMOVE: (i) => i >= 1,
  JSON_SET: (i) => i % 2 === 1,
  JSON_INSERT: (i) => i % 2 === 1,
  JSON_REPLACE: (i) => i % 2 === 1,
  JSON_ARRAY_APPEND: (i) => i % 2 === 1,
  JSON_ARRAY_INSERT: (i) => i % 2 === 1,
  JSON_SEARCH: (i) => i >= 4,
  JSON_TABLE: (i) => i === 1,
};

const PG_JSONPATH_FUNCS = new Set(
  ["path_exists", "path_match", "path_query", "path_query_array", "path_query_first"].flatMap((f) => [`JSONB_${f}`, `JSONB_${f}_TZ`].map((x) => x.toUpperCase())),
);
const PG_KEY_FUNCS = new Set(["JSON_EXTRACT_PATH", "JSON_EXTRACT_PATH_TEXT", "JSONB_EXTRACT_PATH", "JSONB_EXTRACT_PATH_TEXT"]);
const PG_ARRAY_FUNCS = new Set(["JSONB_SET", "JSONB_SET_LAX", "JSONB_INSERT"]);

/** The string literal containing offset (cursor after the opening quote, before any closing one). */
function stringAt(tokens: Token[], offset: number): { tok: Token; index: number } | null {
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (t.type !== "string" || t.text[0] === "$") continue;
    const closed = t.text.length > 1 && t.text.endsWith(t.text[0]);
    if (t.start < offset && (closed ? offset < t.end : offset <= t.end)) return { tok: t, index: i };
  }
  return null;
}

/** Whether offset sits inside a string literal. */
export const inString = (text: string, offset: number, dialect: DialectName) => !!stringAt(tokenize(text, dialect), offset);

const stringValue = (t: Token) => {
  const q = t.text[0];
  const closed = t.text.length > 1 && t.text.endsWith(q);
  return t.text.slice(1, closed ? -1 : undefined).split(q + q).join(q);
};

/** A column reference ending at sig[end]: ident or ident.ident. */
function columnBefore(sig: Token[], end: number): string[] | null {
  if (!isIdent(sig[end])) return null;
  if (end >= 2 && sig[end - 1].text === "." && isIdent(sig[end - 2])) return [sig[end - 2].value, sig[end].value];
  return [sig[end].value];
}

/** Detect a JSON path being typed at offset. */
export function jsonPathContext(text: string, offset: number, dialect: DialectName): JsonPathContext | null {
  const tokens = tokenize(text, dialect);
  const hit = stringAt(tokens, offset);
  if (!hit) return null;
  const inner = text.slice(hit.tok.start + 1, offset);
  const sig = significant(tokens);
  const si = sig.indexOf(hit.tok);
  const prev = sig[si - 1];
  if (!prev) return null;

  // PG "@?" lexes as "@" followed by a "?" parameter.
  if (dialect === "postgres" && prev.text === "?" && sig[si - 2]?.text === "@" && sig[si - 2].end === prev.start) {
    const column = columnBefore(sig, si - 3);
    return column ? jsonpath(column, [], inner, offset) : null;
  }
  // Operators: col->'..', col->>'..', col #> '{..}', PG chains.
  if (prev.type === "op") {
    if (prev.text === "->" || prev.text === "->>") {
      if (dialect === "mysql") {
        const column = columnBefore(sig, si - 2);
        return column ? jsonpath(column, [], inner, offset) : null;
      }
      const chain = pgChain(sig, si - 1);
      return chain && { column: chain.column, path: chain.path, prefix: inner, replaceStart: offset - inner.length, style: "key" };
    }
    if (dialect === "postgres" && (prev.text === "#>" || prev.text === "#>>" || prev.text === "#-")) {
      const chain = pgChain(sig, si - 1);
      return chain ? arrayPath(chain.column, chain.path, inner, offset) : null;
    }
    if (dialect === "postgres" && (prev.text === "@?" || prev.text === "@@")) {
      const column = columnBefore(sig, si - 2);
      return column ? jsonpath(column, [], inner, offset) : null;
    }
    return null;
  }

  // Function arguments.
  const call = enclosingCall(sig, si);
  if (!call || call.argIndex < 1) return null;
  const fn = call.name;
  const column = call.args[0].length ? columnBefore(call.args[0], call.args[0].length - 1) : null;
  if (!column || call.args[0].length > 3) return null;
  if (dialect === "mysql") {
    const isPath = MYSQL_PATH_FUNCS[fn];
    if (!isPath || !isPath(call.argIndex)) return null;
    return jsonpath(column, [], inner, offset);
  }
  if (PG_JSONPATH_FUNCS.has(fn) && call.argIndex === 1) return jsonpath(column, [], inner, offset);
  if (PG_ARRAY_FUNCS.has(fn) && call.argIndex === 1) return arrayPath(column, [], inner, offset);
  if (PG_KEY_FUNCS.has(fn)) {
    // Earlier arguments are the path so far.
    const path: string[] = [];
    for (const a of call.args.slice(1, call.argIndex)) {
      if (a.length !== 1 || a[0].type !== "string") return null;
      const v = stringValue(a[0]);
      path.push(/^\d+$/.test(v) ? ARRAY : v);
    }
    return { column, path, prefix: inner, replaceStart: offset - inner.length, style: "key" };
  }
  return null;
}

/** Walk back over a PG -> chain ending at the operator sig[op]: col->'a'->0->'b'. */
function pgChain(sig: Token[], op: number): { column: string[]; path: string[] } | null {
  const path: string[] = [];
  let i = op - 1;
  while (i >= 0) {
    const t = sig[i];
    if ((t.type === "string" || t.type === "number") && sig[i - 1]?.type === "op" && (sig[i - 1].text === "->" || sig[i - 1].text === "->>")) {
      const v = t.type === "string" ? stringValue(t) : t.text;
      path.unshift(t.type === "number" || /^\d+$/.test(v) ? ARRAY : v);
      i -= 2;
      continue;
    }
    const column = columnBefore(sig, i);
    return column ? { column, path } : null;
  }
  return null;
}

/** The function call whose argument list contains sig[at]. */
function enclosingCall(sig: Token[], at: number): { name: string; argIndex: number; args: Token[][] } | null {
  let depth = 0;
  let open = -1;
  for (let i = at - 1; i >= 0; i--) {
    const t = sig[i].text;
    if (t === ")") depth++;
    else if (t === "(") {
      if (depth === 0) {
        open = i;
        break;
      }
      depth--;
    }
  }
  if (open < 1 || sig[open - 1].type !== "word") return null;
  const args: Token[][] = [[]];
  depth = 0;
  for (let i = open + 1; i < at; i++) {
    const t = sig[i];
    if (t.text === "(") depth++;
    else if (t.text === ")") depth--;
    if (depth === 0 && t.text === ",") args.push([]);
    else args[args.length - 1].push(t);
  }
  return { name: sig[open - 1].upper, argIndex: args.length - 1, args };
}

const IDENT_KEY = /^[A-Za-z_$][A-Za-z0-9_$]*$/;

/** Parse '$.a."b c"[0].d' up to the cursor. */
function jsonpath(column: string[], base: string[], inner: string, offset: number): JsonPathContext | null {
  if (inner === "") return { column, path: base, prefix: "", replaceStart: offset, style: "jsonpath", root: true };
  if (inner[0] !== "$") return null;
  const path = [...base];
  let i = 1;
  while (i < inner.length) {
    const c = inner[i];
    if (c === "[") {
      const close = inner.indexOf("]", i);
      if (close < 0) return null; // inside an array index
      path.push(ARRAY);
      i = close + 1;
    } else if (c === ".") {
      if (inner[i + 1] === "*") return null;
      if (inner[i + 1] === '"') {
        const close = inner.indexOf('"', i + 2);
        if (close < 0) {
          const prefix = inner.slice(i + 2);
          return { column, path, prefix, replaceStart: offset - prefix.length, style: "jsonpath", quoted: true };
        }
        path.push(inner.slice(i + 2, close));
        i = close + 1;
      } else {
        let j = i + 1;
        while (j < inner.length && /[A-Za-z0-9_$]/.test(inner[j])) j++;
        if (j === inner.length) {
          const prefix = inner.slice(i + 1);
          return { column, path, prefix, replaceStart: offset - prefix.length, style: "jsonpath" };
        }
        path.push(inner.slice(i + 1, j));
        i = j;
      }
    } else if (c === " " ) {
      return null; // "lax $.a" / "$.a ? (...)" filters: not handled
    } else {
      return null;
    }
  }
  // Cursor right after "$" or "]": nothing to complete until "." is typed.
  return null;
}

/** Parse '{a,b,c' up to the cursor. */
function arrayPath(column: string[], base: string[], inner: string, offset: number): JsonPathContext | null {
  if (inner === "") return { column, path: base, prefix: "", replaceStart: offset, style: "array" };
  if (inner[0] !== "{" || inner.includes("}")) return null;
  const parts = inner.slice(1).split(",");
  const prefix = parts.pop()!.replace(/^\s*"?/, "");
  const path = [...base, ...parts.map((p) => p.trim().replace(/^"|"$/g, "")).map((p) => (/^-?\d+$/.test(p) ? ARRAY : p))];
  return { column, path, prefix, replaceStart: offset - prefix.length, style: "array" };
}

// ---- key tree ----------------------------------------------------------------------

export interface KeyNode {
  /** How many times this value was seen. */
  count: number;
  types: Set<string>;
  children: Map<string, KeyNode>;
}

const node = (): KeyNode => ({ count: 0, types: new Set(), children: new Map() });

const typeOf = (v: unknown) => (v === null ? "null" : Array.isArray(v) ? "array" : typeof v);

function addValue(n: KeyNode, v: unknown, depth: number) {
  n.count++;
  n.types.add(typeOf(v));
  if (depth > 12 || v === null || typeof v !== "object") return;
  if (Array.isArray(v)) {
    const el = n.children.get(ARRAY) ?? n.children.set(ARRAY, node()).get(ARRAY)!;
    for (const x of v.slice(0, 50)) addValue(el, x, depth + 1);
    return;
  }
  for (const [k, x] of Object.entries(v as Record<string, unknown>)) {
    const c = n.children.get(k) ?? n.children.set(k, node()).get(k)!;
    addValue(c, x, depth + 1);
  }
}

/** Merge JSON documents (as text) into one tree. Unparseable values are skipped. */
export function buildKeyTree(values: string[]): KeyNode {
  const root = node();
  for (const s of values) {
    let v: unknown;
    try {
      v = JSON.parse(s);
    } catch {
      continue;
    }
    addValue(root, v, 0);
  }
  return root;
}

/** Add a path written in the query (count 0: known, but not seen in the sample). */
export function addPath(root: KeyNode, path: string[]) {
  let n = root;
  for (const seg of path) {
    if (!seg) return;
    n = n.children.get(seg) ?? n.children.set(seg, node()).get(seg)!;
  }
}

/** The node a path leads to; key segments step through arrays transparently. */
export function lookup(root: KeyNode, path: string[]): KeyNode | undefined {
  let n: KeyNode | undefined = root;
  for (const seg of path) {
    if (!n) return undefined;
    n = n.children.get(seg) ?? (seg !== ARRAY ? n.children.get(ARRAY)?.children.get(seg) : undefined);
  }
  return n;
}

/** Every complete path written against each column in the text, keyed by column name (lowercase). */
export function pathsInText(text: string, dialect: DialectName): Map<string, string[][]> {
  const out = new Map<string, string[][]>();
  for (const t of tokenize(text, dialect)) {
    if (t.type !== "string" || t.text.length < 2) continue;
    const ctx = jsonPathContext(text, t.end - 1, dialect);
    if (!ctx) continue;
    const full = ctx.prefix ? [...ctx.path, ctx.prefix] : ctx.path;
    if (!full.length) continue;
    const k = ctx.column[ctx.column.length - 1].toLowerCase();
    (out.get(k) ?? out.set(k, []).get(k)!).push(full);
  }
  return out;
}

// ---- resolution and suggestions ----------------------------------------------------

export interface ColumnTarget {
  schema?: string;
  table: string;
  column: string;
  type: string;
}

/** Find the table column a path's column reference points at. */
export function resolveColumn(ctx: JsonPathContext, a: Analysis, cat: CompletionCatalog): ColumnTarget | null {
  const ix: CatalogIndex = indexOf(cat);
  const name = ctx.column[ctx.column.length - 1].toLowerCase();
  const qual = ctx.column.length > 1 ? ctx.column[0].toLowerCase() : undefined;
  for (const r of a.tables) {
    if (r.kind !== "table") continue;
    if (qual && (r.alias ?? r.name).toLowerCase() !== qual) continue;
    const t = ix.resolve(r.name, r.schema);
    const c = t?.columns.find((x) => x.name.toLowerCase() === name);
    if (t && c) return { schema: t.schema, table: t.name, column: c.name, type: c.type };
  }
  return null;
}

const describe = (n: KeyNode, parent: KeyNode) => {
  const types = [...n.types].join("|") || "used in query";
  if (!n.count || !parent.count) return types;
  return `${types} · ${Math.round((Math.min(n.count, parent.count) / parent.count) * 100)}%`;
};

/** Suggestions for the keys under the context's path. */
export function jsonKeySuggestions(ctx: JsonPathContext, root: KeyNode, quoteChar = "'"): Suggestion[] {
  let parent = lookup(root, ctx.path);
  if (!parent) return [];
  // Keys of array elements are offered when the path stops at an array (PG -> chains, jsonpath after "[*]").
  if (!parent.children.size || (parent.children.size === 1 && parent.children.has(ARRAY) && ctx.style !== "array")) parent = parent.children.get(ARRAY) ?? parent;
  const keys = [...parent.children.entries()].filter(([k]) => k !== ARRAY);
  keys.sort((a, b) => b[1].count - a[1].count || a[0].localeCompare(b[0]));
  return keys.map(([k, n], i) => {
    let insert = k;
    if (ctx.style === "jsonpath") {
      if (!IDENT_KEY.test(k) || ctx.quoted) insert = ctx.quoted ? k.replace(/"/g, '\\"') + '"' : `"${k.replace(/"/g, '\\"')}"`;
      if (ctx.root) insert = "$." + insert;
    } else if (ctx.style === "array" && /[,{}"\s\\]/.test(k)) {
      insert = `"${k.replace(/["\\]/g, "\\$&")}"`;
    }
    if (quoteChar) insert = insert.split(quoteChar).join(quoteChar + quoteChar);
    return {
      label: k,
      kind: "property" as const,
      insertText: insert,
      filterText: k,
      detail: describe(n, parent!),
      rank: -5 + i / 1000,
      rangeStart: ctx.replaceStart,
    };
  });
}
