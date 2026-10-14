// Context-aware SQL completion. Pure logic: the Monaco adapter maps Suggestions to
// editor completion items.

import { Analysis, TableRef, analyze } from "./analyze";
import { enumContext, sqlString } from "./enums";
import { DialectName } from "./lexer";

export interface CatColumn {
  name: string;
  type: string;
  pk?: boolean;
  /** Allowed values of enum columns. */
  enumValues?: string[];
}

export interface CatTable {
  schema?: string;
  name: string;
  kind: "table" | "view" | "materialized_view";
  comment?: string;
  columns: CatColumn[];
}

export interface CatFK {
  schema?: string;
  table: string;
  columns: string[];
  refSchema?: string;
  refTable: string;
  refColumns: string[];
}

export interface CatFunction {
  name: string;
  schema?: string;
  signature?: string;
  returnType?: string;
  builtin: boolean;
}

export interface CompletionCatalog {
  dialect: DialectName;
  /** PG current schema / MySQL current database. */
  defaultSchema?: string;
  /** PG schemas in the database / MySQL databases on the server. */
  schemas: string[];
  tables: CatTable[];
  fks: CatFK[];
  functions: CatFunction[];
}

export type SuggestionKind = "keyword" | "schema" | "table" | "view" | "column" | "alias" | "function" | "join" | "snippet" | "cte" | "property" | "enum";

export interface Suggestion {
  label: string;
  kind: SuggestionKind;
  insertText: string;
  /** insertText uses Monaco snippet syntax ($1, ${1:x}). */
  snippet?: boolean;
  detail?: string;
  documentation?: string;
  /** Lower sorts first. */
  rank: number;
  /** Override the start of the replaced range (absolute offset). */
  rangeStart?: number;
  filterText?: string;
  /** Re-trigger suggestions after accepting (e.g. after inserting "schema."). */
  retrigger?: boolean;
}

export interface CompleteOptions {
  keywordCase?: "upper" | "lower";
  /** Insert "alias.column" even when the column is unambiguous. */
  alwaysQualify?: boolean;
}

export interface CompletionResult {
  suggestions: Suggestion[];
  /** Absolute offset where the replaced fragment starts. */
  replaceStart: number;
  analysis: Analysis;
}

// ---- catalog index -----------------------------------------------------------

export class CatalogIndex {
  byName = new Map<string, CatTable[]>();
  bySchema = new Map<string, CatTable[]>();
  schemaSet = new Set<string>();
  constructor(public cat: CompletionCatalog) {
    for (const t of cat.tables) {
      const k = t.name.toLowerCase();
      (this.byName.get(k) ?? this.byName.set(k, []).get(k)!).push(t);
      // Tables without a schema belong to the default one (MySQL's current database).
      const s = (t.schema ?? cat.defaultSchema ?? "").toLowerCase();
      (this.bySchema.get(s) ?? this.bySchema.set(s, []).get(s)!).push(t);
    }
    for (const s of cat.schemas) this.schemaSet.add(s.toLowerCase());
  }

  /** Resolve a table reference the way the server would (default schema first). */
  resolve(name: string, schema?: string): CatTable | undefined {
    const cands = this.byName.get(name.toLowerCase());
    if (!cands?.length) return undefined;
    const def = (this.cat.defaultSchema ?? "").toLowerCase();
    if (schema) {
      const s = schema.toLowerCase();
      return cands.find((t) => (t.schema ?? "").toLowerCase() === s) ?? (s === def ? cands.find((t) => !t.schema) : undefined);
    }
    return (
      cands.find((t) => (t.schema ?? "").toLowerCase() === def) ??
      cands.find((t) => (t.schema ?? "").toLowerCase() === "public") ??
      cands[0]
    );
  }

  isSchema(name: string) {
    return this.schemaSet.has(name.toLowerCase());
  }
}

const indexCache = new WeakMap<CompletionCatalog, CatalogIndex>();
export function indexOf(cat: CompletionCatalog): CatalogIndex {
  let ix = indexCache.get(cat);
  if (!ix) {
    ix = new CatalogIndex(cat);
    indexCache.set(cat, ix);
  }
  return ix;
}

// ---- identifiers --------------------------------------------------------------

const RESERVED = new Set(
  ("all alter and any as asc between by case check column constraint create cross current_date current_time " +
    "current_timestamp current_user database default delete desc distinct drop else end exists false fetch for foreign " +
    "from full grant group having in index inner insert interval into is join key left like limit natural not null " +
    "offset on or order outer primary references right select set table then to true union unique update user using " +
    "values when where with window").split(" "),
);

export function quoteIdent(name: string, d: DialectName, force = false): string {
  const plain = d === "postgres" ? /^[a-z_][a-z0-9_$]*$/ : /^[A-Za-z_][A-Za-z0-9_$]*$/;
  if (!force && plain.test(name) && !RESERVED.has(name.toLowerCase())) return name;
  const q = d === "mysql" ? "`" : '"';
  return q + name.split(q).join(q + q) + q;
}

/** Short alias from a table name: customer_orders -> co, OrderItems -> oi. */
export function makeAlias(name: string, taken: Set<string>): string {
  const words = name
    .replace(/([a-z])([A-Z])/g, "$1_$2")
    .split(/[_\s\-$]+/)
    .filter(Boolean);
  let base = words.map((w) => w[0]).join("").toLowerCase() || "t";
  if (RESERVED.has(base)) base = base + "1";
  let alias = base;
  for (let n = 2; taken.has(alias.toLowerCase()); n++) alias = base + n;
  return alias;
}

// ---- keywords -------------------------------------------------------------------

const STATEMENT_STARTERS: Array<[string, string?]> = [
  ["SELECT"],
  ["SELECT * FROM", "SELECT * FROM $0"],
  ["INSERT INTO"],
  ["UPDATE"],
  ["DELETE FROM"],
  ["WITH"],
  ["CREATE TABLE"],
  ["CREATE VIEW"],
  ["CREATE INDEX"],
  ["ALTER TABLE"],
  ["DROP TABLE"],
  ["TRUNCATE TABLE"],
  ["EXPLAIN"],
  ["BEGIN"],
  ["COMMIT"],
  ["ROLLBACK"],
];
const MYSQL_STARTERS = ["SHOW TABLES", "SHOW DATABASES", "SHOW CREATE TABLE", "SHOW PROCESSLIST", "SHOW VARIABLES LIKE", "DESCRIBE", "USE", "CALL", "REPLACE INTO"];
const SQLITE_STARTERS = ["PRAGMA", "ATTACH DATABASE", "DETACH DATABASE", "VACUUM", "ANALYZE", "REINDEX", "EXPLAIN QUERY PLAN", "REPLACE INTO"];
const PG_STARTERS = ["SET search_path TO", "VACUUM", "ANALYZE", "COPY", "CREATE SCHEMA", "CREATE EXTENSION", "DO", "LISTEN", "NOTIFY"];

// Only keywords the dialect accepts: MySQL has no FULL OUTER JOIN or NULLS FIRST,
// SQLite no TRUNCATE or INTERVAL, and only MySQL has WITH ROLLUP.
const JOINS = (d: DialectName) => ["INNER JOIN", "LEFT JOIN", "RIGHT JOIN", "CROSS JOIN", "JOIN", ...(d === "mysql" ? [] : ["FULL OUTER JOIN"])];
const AFTER_TABLE = (d: DialectName) => ["WHERE", ...JOINS(d), "GROUP BY", "ORDER BY", "LIMIT", "AS", "ON", "USING", "UNION", "UNION ALL", "HAVING"];
// What can follow a join modifier ("LEFT |").
const JOIN_REST = (d: DialectName): Record<string, string[]> => ({
  INNER: ["JOIN"],
  CROSS: ["JOIN"],
  LEFT: ["JOIN", "OUTER JOIN"],
  RIGHT: ["JOIN", "OUTER JOIN"],
  ...(d === "mysql" ? {} : { FULL: ["OUTER JOIN", "JOIN"] }),
  NATURAL: ["JOIN", "LEFT JOIN", "RIGHT JOIN", "INNER JOIN"],
  OUTER: ["JOIN"],
});
/** Whether the last non-blank character before at is ch. */
const prevTokenIs = (text: string, at: number, ch: string) => text.slice(0, at).trimEnd().endsWith(ch);
/** The word directly before the prefix being typed, upper-cased. */
const wordBefore = (text: string, at: number) => /([A-Za-z_]+)\s+$/.exec(text.slice(0, at))?.[1].toUpperCase();
// Keywords that start a value or expression.
const VALUE_KEYWORDS = (d: DialectName) => ["NOT", "EXISTS", "CASE", "WHEN", "THEN", "ELSE", "END", "NULL", "TRUE", "FALSE", "DISTINCT", ...(d === "sqlite" ? [] : ["INTERVAL"])];
// Operators that follow an operand ("email |").
const OPERATORS_COMMON = ["AND", "OR", "LIKE", "NOT LIKE", "IN", "NOT IN", "BETWEEN", "NOT BETWEEN", "IS NULL", "IS NOT NULL", "IS TRUE", "IS FALSE"];
const OPERATORS: Record<DialectName, string[]> = {
  mysql: [...OPERATORS_COMMON, "REGEXP", "NOT REGEXP", "RLIKE", "NOT RLIKE", "SOUNDS LIKE", "MEMBER OF", "DIV", "MOD", "XOR", "COLLATE"],
  // REGEXP needs an application-defined function and MATCH a full-text table, so neither is offered.
  sqlite: [...OPERATORS_COMMON, "GLOB", "NOT GLOB", "IS", "IS NOT", "IS DISTINCT FROM", "IS NOT DISTINCT FROM", "COLLATE"],
  postgres: [...OPERATORS_COMMON, "ILIKE", "NOT ILIKE", "SIMILAR TO", "NOT SIMILAR TO", "IS DISTINCT FROM", "IS NOT DISTINCT FROM", "COLLATE", "AT TIME ZONE"],
};
const EXPR_KEYWORDS = (d: DialectName) => [...VALUE_KEYWORDS(d), "AND", "OR", "IN", "IS NULL", "IS NOT NULL", "LIKE", "BETWEEN", "AS"];
const AFTER_WHERE = ["GROUP BY", "ORDER BY", "LIMIT", "HAVING", "UNION", "UNION ALL"];
const AFTER_ORDER = (d: DialectName) => ["ASC", "DESC", "LIMIT", ...(d === "mysql" ? [] : ["NULLS FIRST", "NULLS LAST"])];
const AFTER_GROUP = (d: DialectName) => ["HAVING", "ORDER BY", "LIMIT", ...(d === "mysql" ? ["WITH ROLLUP"] : [])];

function kw(list: string[], rank: number, opts: CompleteOptions): Suggestion[] {
  const lower = opts.keywordCase === "lower";
  return list.map((k) => {
    const text = lower ? k.toLowerCase() : k;
    return { label: text, kind: "keyword" as const, insertText: text + " ", rank, retrigger: true };
  });
}

// ---- main ----------------------------------------------------------------------

export function complete(text: string, offset: number, cat: CompletionCatalog, opts: CompleteOptions = {}): CompletionResult {
  const a = analyze(text, offset, cat.dialect);
  const ix = indexOf(cat);
  const d = cat.dialect;
  const out: Suggestion[] = [];

  // Enum values after "col = " / inside "col = '…" / in "col IN (…".
  const ec = enumContext(text, offset, d);
  if (ec) {
    const col = resolveColumnRef(ec.column, a, cat);
    if (col?.column.enumValues?.length) {
      const values = col.column.enumValues.map(
        (v, i): Suggestion => ({
          label: v,
          kind: "enum",
          insertText: ec.inString ? v.replace(/'/g, "''") : sqlString(v),
          filterText: ec.inString ? v : `${v} '${v}'`,
          detail: `${col.table.name}.${col.column.name}`,
          rank: -6 + i / 1000, // keep the enum's declared order
          rangeStart: ec.replaceStart,
        }),
      );
      if (ec.inString) return { suggestions: values, replaceStart: ec.replaceStart, analysis: a };
      out.push(...values);
    }
  }
  const q = (name: string) => quoteIdent(name, d, a.prefixQuoted);

  // Visible references, resolved against the catalog.
  const scope = a.tables.map((r) => ({ ref: r, table: r.kind === "table" ? ix.resolve(r.name, r.schema) : undefined }));
  const qualName = (r: TableRef) => r.alias ?? r.name;
  // Columns of a reference; "*" and "x.*" in a derived table or CTE expand to its sources.
  const columnsOf = (r: TableRef, t?: CatTable, depth = 0): CatColumn[] => {
    if (t) return t.columns;
    const out: CatColumn[] = [];
    for (const name of r.columns ?? []) {
      if (!name.endsWith("*")) {
        out.push({ name, type: r.kind === "cte" ? "cte column" : "derived column" });
        continue;
      }
      if (depth > 8) continue;
      const qual = name === "*" ? undefined : name.slice(0, -2).toLowerCase();
      for (const src of r.sources ?? []) {
        if (qual && (src.alias ?? src.name).toLowerCase() !== qual) continue;
        out.push(...columnsOf(src, src.kind === "table" ? ix.resolve(src.name, src.schema) : undefined, depth + 1));
      }
    }
    // The same name twice (SELECT * over a join) is one output column for completion.
    const seen = new Set<string>();
    return out.filter((c) => !seen.has(c.name.toLowerCase()) && !!seen.add(c.name.toLowerCase()));
  };

  const addTables = (rank: number, schemaFilter?: string, withSchemas = true) => {
    for (const c of a.ctes) out.push({ label: c.name, kind: "cte", insertText: q(c.name), detail: "CTE", rank: rank - 1 });
    const list = schemaFilter !== undefined ? ix.bySchema.get(schemaFilter.toLowerCase()) ?? [] : cat.tables;
    const def = (cat.defaultSchema ?? "").toLowerCase();
    for (const t of list) {
      const inDefault = !t.schema || t.schema.toLowerCase() === def || schemaFilter !== undefined;
      // Tables outside the default schema are offered schema-qualified.
      const insert = inDefault ? q(t.name) : quoteIdent(t.schema!, d) + "." + q(t.name);
      out.push({
        label: inDefault ? t.name : `${t.schema}.${t.name}`,
        filterText: t.name,
        kind: t.kind === "table" ? "table" : "view",
        insertText: insert,
        detail: t.kind === "table" ? "table" : t.kind.replace("_", " "),
        documentation: t.comment || undefined,
        rank: inDefault ? rank : rank + 2,
      });
    }
    if (withSchemas && schemaFilter === undefined) {
      for (const s of cat.schemas) {
        out.push({ label: s, kind: "schema", insertText: quoteIdent(s, d) + ".", detail: d === "postgres" ? "schema" : "database", rank: rank + 3, retrigger: true });
      }
    }
  };

  const addColumns = (rank: number, onlyRefs?: typeof scope) => {
    const refs = onlyRefs ?? scope;
    const counts = new Map<string, number>();
    for (const s of refs) for (const c of columnsOf(s.ref, s.table)) counts.set(c.name.toLowerCase(), (counts.get(c.name.toLowerCase()) ?? 0) + 1);
    for (const s of refs) {
      const owner = qualName(s.ref);
      for (const c of columnsOf(s.ref, s.table)) {
        const ambiguous = (counts.get(c.name.toLowerCase()) ?? 0) > 1;
        const qualify = (ambiguous || opts.alwaysQualify) && refs.length > 1;
        out.push({
          label: c.name,
          kind: "column",
          insertText: qualify ? quoteIdent(owner, d) + "." + q(c.name) : q(c.name),
          detail: `${c.type}${c.pk ? " · PK" : ""} — ${owner}`,
          filterText: c.name,
          rank: c.pk ? rank : rank + 0.5,
        });
      }
    }
  };

  const addRefNames = (rank: number) => {
    for (const s of scope) {
      const name = qualName(s.ref);
      out.push({ label: name, kind: "alias", insertText: quoteIdent(name, d) + ".", detail: s.ref.alias ? `alias of ${s.ref.name}` : s.ref.kind, rank, retrigger: true });
    }
  };

  const addFunctions = (rank: number) => {
    const lower = opts.keywordCase === "lower" || d === "postgres";
    for (const f of cat.functions) {
      const name = f.builtin ? (lower ? f.name.toLowerCase() : f.name.toUpperCase()) : f.name;
      const qualified = !f.builtin && f.schema && f.schema !== cat.defaultSchema ? quoteIdent(f.schema, d) + "." + quoteIdent(f.name, d) : quoteIdent(name, d);
      out.push({
        label: f.builtin ? name : f.signature !== undefined ? `${f.name}(${f.signature})` : f.name,
        filterText: f.name,
        kind: "function",
        insertText: `${qualified}($0)`,
        snippet: true,
        detail: f.returnType ? `→ ${f.returnType}` : f.builtin ? "built-in function" : "function",
        rank: f.builtin ? rank + 1 : rank,
      });
    }
  };

  /** FK-based join conditions between `target` and the other tables in scope. */
  const joinConditions = (target: TableRef, others: typeof scope): string[] => {
    const tt = target.kind === "table" ? ix.resolve(target.name, target.schema) : undefined;
    if (!tt) return [];
    const tq = quoteIdent(qualName(target), d);
    const conds: string[] = [];
    for (const o of others) {
      if (!o.table || o.ref === target) continue;
      const oq = quoteIdent(qualName(o.ref), d);
      for (const fk of cat.fks) {
        const same = (s: string | undefined, n: string, t: CatTable) =>
          n.toLowerCase() === t.name.toLowerCase() && (!s || !t.schema || s.toLowerCase() === t.schema.toLowerCase());
        if (same(fk.schema, fk.table, tt) && same(fk.refSchema, fk.refTable, o.table)) {
          conds.push(fk.columns.map((c, i) => `${tq}.${quoteIdent(c, d)} = ${oq}.${quoteIdent(fk.refColumns[i], d)}`).join(" AND "));
        } else if (same(fk.schema, fk.table, o.table) && same(fk.refSchema, fk.refTable, tt)) {
          conds.push(fk.columns.map((c, i) => `${tq}.${quoteIdent(fk.refColumns[i], d)} = ${oq}.${quoteIdent(c, d)}`).join(" AND "));
        }
      }
    }
    return conds;
  };

  // ---- qualified: "x." -------------------------------------------------------
  if (a.qualifier.length) {
    const qual = a.qualifier;
    const last = qual[qual.length - 1];
    const inScope = scope.find((s) => qualName(s.ref).toLowerCase() === last.toLowerCase() && qual.length === 1);
    if (inScope) {
      addColumns(0, [inScope]);
      out.forEach((s) => (s.insertText = q(s.label)));
      out.push({ label: "*", kind: "column", insertText: "*", detail: "all columns", rank: 1 });
    } else if (qual.length === 1 && ix.isSchema(last)) {
      const tableCtx = ["from", "updateTable", "insertTable", "deleteTable", "ddlTable"].includes(a.clause);
      addTables(0, last, false);
      if (!tableCtx) {
        for (const f of cat.functions.filter((f) => !f.builtin && f.schema?.toLowerCase() === last.toLowerCase())) {
          out.push({ label: f.name, kind: "function", insertText: `${quoteIdent(f.name, d)}($0)`, snippet: true, detail: f.returnType ? `→ ${f.returnType}` : "function", rank: 2 });
        }
      }
    } else {
      const t = ix.resolve(last, qual.length > 1 ? qual[qual.length - 2] : undefined);
      if (t) for (const c of t.columns) out.push({ label: c.name, kind: "column", insertText: q(c.name), detail: c.type, rank: c.pk ? 0 : 1 });
    }
    return finish(out, a);
  }

  // ---- by clause ---------------------------------------------------------------
  switch (a.clause) {
    case "start":
      const starters = STATEMENT_STARTERS.filter(([k]) => d !== "sqlite" || k !== "TRUNCATE TABLE");
      out.push(...kw(starters.map(([k]) => k), 0, opts));
      for (const [label, snip] of starters) {
        if (snip) out.push({ label: label + "…", kind: "snippet", insertText: snip, snippet: true, rank: 0.5, filterText: label, retrigger: true });
      }
      out.push(...kw(d === "mysql" ? MYSQL_STARTERS : d === "sqlite" ? SQLITE_STARTERS : PG_STARTERS, 1, opts));
      break;

    case "from": {
      // CROSS and NATURAL joins take no condition, so no "ON" suggestions for them.
      const joinKind = a.prevKeyword === "JOIN" ? /([A-Za-z_]+)\s+JOIN\s+$/i.exec(text.slice(0, a.prefixStart))?.[1].toUpperCase() : undefined;
      const isJoin = a.prevKeyword === "JOIN" && joinKind !== "CROSS" && joinKind !== "NATURAL";
      if (isJoin && scope.length) {
        // FK-aware join suggestions: "orders o ON o.customer_id = c.id"
        const taken = new Set(scope.map((s) => qualName(s.ref).toLowerCase()));
        for (const t of cat.tables) {
          const alias = makeAlias(t.name, taken);
          const fake: TableRef = { name: t.name, schema: t.schema, alias, kind: "table", at: -1 };
          const conds = joinConditions(fake, scope);
          const def = !t.schema || t.schema.toLowerCase() === (cat.defaultSchema ?? "").toLowerCase();
          const tname = def ? quoteIdent(t.name, d) : quoteIdent(t.schema!, d) + "." + quoteIdent(t.name, d);
          for (const cond of conds) {
            out.push({ label: `${t.name} ${alias} ON ${cond}`, filterText: t.name, kind: "join", insertText: `${tname} ${alias} ON ${cond}`, detail: "join on foreign key", rank: -1 });
          }
        }
      }
      addTables(0);
      break;
    }

    case "selectStart": {
      // SQLite only accepts UNION ALL; MySQL and PostgreSQL take ALL or DISTINCT after each operator.
      const op = wordBefore(text, a.prefixStart);
      if (prevTokenIs(text, a.prefixStart, "(")) {
        out.push(...kw(["SELECT", "WITH", ...(d === "mysql" ? [] : ["VALUES"])], 0, opts));
        break;
      }
      const quant = op === "UNION" ? (d === "sqlite" ? ["ALL"] : ["ALL", "DISTINCT"]) : op === "INTERSECT" || op === "EXCEPT" ? (d === "sqlite" ? [] : ["ALL", "DISTINCT"]) : [];
      out.push(...kw(["SELECT", ...quant], 0, opts));
      break;
    }

    case "using": {
      // JOIN t USING ( <columns both sides have> )
      const target = [...scope].filter((s) => s.ref.at >= 0 && s.ref.at < a.prefixStart).sort((x, y) => y.ref.at - x.ref.at)[0] ?? scope[scope.length - 1];
      if (target) {
        const others = new Set(scope.filter((s) => s !== target).flatMap((s) => columnsOf(s.ref, s.table).map((c) => c.name.toLowerCase())));
        for (const c of columnsOf(target.ref, target.table)) {
          const shared = others.has(c.name.toLowerCase());
          out.push({ label: c.name, kind: "column", insertText: q(c.name), detail: shared ? "in both tables" : `${c.type} — ${qualName(target.ref)}`, filterText: c.name, rank: shared ? -1 : 1 });
        }
      }
      break;
    }

    case "updateTable":
    case "insertTable":
    case "deleteTable":
    case "ddlTable":
      addTables(0);
      break;

    case "afterTable": {
      const last = scope.filter((s) => s.ref.at >= 0).sort((x, y) => y.ref.at - x.ref.at)[0];
      // Offer an alias right after a bare table name.
      if (last && !last.ref.alias && a.prefix === "") {
        const taken = new Set(scope.map((s) => qualName(s.ref).toLowerCase()));
        const alias = makeAlias(last.ref.name, taken);
        out.push({ label: alias, kind: "alias", insertText: alias + " ", detail: `alias for ${last.ref.name}`, rank: -0.5 });
      }
      const rest = JOIN_REST(d)[wordBefore(text, a.prefixStart) ?? ""];
      out.push(...kw(rest ?? AFTER_TABLE(d), 0, opts));
      break;
    }

    case "on": {
      const target = a.joinTarget ?? scope[scope.length - 1]?.ref;
      if (target) {
        for (const cond of joinConditions(target, scope)) {
          out.push({ label: cond, kind: "join", insertText: cond, detail: "foreign key", rank: -1 });
        }
      }
      if (a.afterOperand) out.push(...kw(OPERATORS[d], -0.5, opts));
      addRefNames(1);
      addColumns(2);
      out.push(...kw(["AND", "OR", "WHERE", ...JOINS(d), ...AFTER_WHERE], 3, opts));
      break;
    }

    case "insertColumns": {
      const target = scope.find((s) => s.ref.kind === "table");
      if (target?.table) {
        const cols = target.table.columns;
        out.push({ label: "(all columns)", kind: "snippet", insertText: cols.map((c) => q(c.name)).join(", "), rank: -1, filterText: "" });
        for (const c of cols) out.push({ label: c.name, kind: "column", insertText: q(c.name), detail: c.type, rank: 0 });
      }
      break;
    }

    case "select":
    case "where":
    case "groupBy":
    case "orderBy":
    case "set":
    case "values":
    case "limit":
    case "other": {
      if (a.afterStar && a.clause === "select" && scope.length) {
        // Expand * into the explicit column list.
        const multi = scope.length > 1;
        const cols = scope.flatMap((s) => columnsOf(s.ref, s.table).map((c) => (multi ? quoteIdent(qualName(s.ref), d) + "." : "") + quoteIdent(c.name, d)));
        out.push({ label: "Expand * to column list", kind: "snippet", insertText: cols.join(", "), rangeStart: offset - 1, rank: -2, filterText: "*" });
      }
      if (a.clause === "limit") break;
      if (a.clause === "values") {
        out.push(...kw(["NULL", "DEFAULT", "TRUE", "FALSE"], 0, opts));
        addFunctions(1);
        break;
      }
      // After a complete operand an operator or clause keyword comes next, so those
      // rank first ("WHERE email REG" -> REGEXP before REGEXP_LIKE()).
      const conditional = a.clause === "where" || a.clause === "set" || a.clause === "other";
      if (a.afterOperand && conditional) {
        out.push(...kw(OPERATORS[d], -1, opts));
        if (a.clause === "where") out.push(...kw(AFTER_WHERE, 0, opts));
        addColumns(2);
        addFunctions(3);
        break;
      }
      if (a.afterOperand && a.clause === "select") {
        out.push(...kw(["FROM", "AS", ...OPERATORS[d].filter((o) => o !== "AND" && o !== "OR")], -1, opts));
      }
      if (a.clause === "orderBy" && a.afterSetOp) {
        // ORDER BY after a UNION sorts the combined result: only its output columns exist.
        for (const n of a.outputColumns) out.push({ label: n, kind: "column", insertText: q(n), detail: "result column", filterText: n, rank: 0 });
        out.push(...kw(AFTER_ORDER(d), 3, opts));
        break;
      }
      if (a.clause === "orderBy" || a.clause === "groupBy") {
        // Select-list aliases ("SELECT price * qty AS total ... ORDER BY total").
        const cols = new Set(scope.flatMap((s) => columnsOf(s.ref, s.table).map((c) => c.name.toLowerCase())));
        for (const n of a.outputColumns) {
          if (!cols.has(n.toLowerCase())) out.push({ label: n, kind: "column", insertText: q(n), detail: "select list", filterText: n, rank: -0.5 });
        }
      }
      if (a.clause === "where" && /\b(IN|EXISTS|ANY|SOME|ALL)\s*\(\s*$/i.test(text.slice(0, a.prefixStart))) {
        out.push(...kw(["SELECT"], -1, opts));
      }
      addColumns(0);
      addRefNames(1);
      if (a.clause !== "set") addFunctions(2);
      const kws = a.clause === "orderBy" ? AFTER_ORDER(d) : a.clause === "where" ? [...VALUE_KEYWORDS(d), ...AFTER_WHERE] : a.clause === "groupBy" ? AFTER_GROUP(d) : EXPR_KEYWORDS(d);
      out.push(...kw(a.clause === "select" ? [...kws, "FROM"] : kws, 3, opts));
      // Without any FROM yet, tables are useful too (SELECT t. ...)
      if (a.clause === "select" && !scope.length) addTables(4, undefined, false);
      break;
    }
  }
  return finish(out, a);
}

/** Resolve a column written as [qualifier?, name] against the tables in scope. */
export function resolveColumnRef(column: string[], a: Analysis, cat: CompletionCatalog): { table: CatTable; column: CatColumn } | null {
  const ix = indexOf(cat);
  const name = column[column.length - 1].toLowerCase();
  const qual = column.length > 1 ? column[0].toLowerCase() : undefined;
  for (const r of a.tables) {
    if (r.kind !== "table") continue;
    if (qual && (r.alias ?? r.name).toLowerCase() !== qual) continue;
    const t = ix.resolve(r.name, r.schema);
    const c = t?.columns.find((x) => x.name.toLowerCase() === name);
    if (t && c) return { table: t, column: c };
  }
  return null;
}

function finish(out: Suggestion[], a: Analysis): CompletionResult {
  // De-duplicate identical insertions, keeping the best rank. Order of insertion is
  // kept as the tie-breaker so columns stay in table order.
  const seen = new Map<string, Suggestion>();
  for (const s of out) {
    const k = s.kind + "\u0000" + s.label + "\u0000" + s.insertText;
    const prev = seen.get(k);
    if (!prev || s.rank < prev.rank) seen.set(k, s);
  }
  return { suggestions: [...seen.values()], replaceStart: a.prefixStart, analysis: a };
}
