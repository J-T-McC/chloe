// Statement analysis for autocomplete: finds the statement under the cursor, the
// clause the cursor is in, any "qualifier." typed before it, and every table
// reference (with aliases, CTEs and derived tables) visible at the cursor.

import { DialectName, Token, isIdent, significant, tokenize } from "./lexer";

export type Clause =
  | "start" // beginning of a statement
  | "select" // select list
  | "from" // after FROM / JOIN, expecting a table
  | "afterTable" // just after a table reference (alias, JOIN, WHERE...)
  | "on" // join condition
  | "where" // WHERE / HAVING / any boolean expression
  | "groupBy"
  | "orderBy"
  | "set" // UPDATE ... SET
  | "insertTable" // INSERT INTO <table>
  | "insertColumns" // INSERT INTO t ( <cols> )
  | "values"
  | "updateTable"
  | "deleteTable"
  | "ddlTable" // ALTER/DROP/TRUNCATE TABLE <name>
  | "limit"
  | "other";

export interface TableRef {
  schema?: string;
  name: string;
  alias?: string;
  kind: "table" | "cte" | "derived";
  /** Columns known from the query text itself (CTE column lists, derived table select lists). */
  columns?: string[];
  /** Token index range of the reference, for "the table just typed" lookups. */
  at: number;
}

export interface Analysis {
  /** Text of the identifier fragment being typed (may be empty). */
  prefix: string;
  /** Offset where the fragment starts (for the replace range). */
  prefixStart: number;
  /** Whether the fragment began with an opening identifier quote. */
  prefixQuoted: boolean;
  /** Identifiers before the dot(s) immediately preceding the cursor, e.g. ["public","orders"]. */
  qualifier: string[];
  clause: Clause;
  /** Tables visible at the cursor, innermost query first. */
  tables: TableRef[];
  /** CTE names defined by the statement. */
  ctes: TableRef[];
  /** Upper-cased significant keyword before the cursor (e.g. "JOIN", "ON", "SELECT"). */
  prevKeyword: string;
  /** The table reference directly before the cursor when the cursor follows "JOIN x [alias] ON". */
  joinTarget?: TableRef;
  /** True when the cursor sits right after "SELECT *" or "t.*". */
  afterStar: boolean;
  /**
   * True when the cursor follows a complete operand (a column, literal or closing
   * parenthesis), so an operator or clause keyword is expected rather than a value:
   * "WHERE email |" vs "WHERE email = |".
   */
  afterOperand: boolean;
  /** Statement start offset in the full text. */
  statementStart: number;
}

const CLAUSE_KEYWORDS: Record<string, Clause> = {
  SELECT: "select",
  FROM: "from",
  JOIN: "from",
  WHERE: "where",
  HAVING: "where",
  ON: "on",
  SET: "set",
  VALUES: "values",
  LIMIT: "limit",
  OFFSET: "limit",
  USING: "on",
  RETURNING: "select",
};

// Words that terminate a table reference (so they're never taken as an alias).
const REF_TERMINATORS = new Set([
  "WHERE", "JOIN", "INNER", "LEFT", "RIGHT", "FULL", "OUTER", "CROSS", "NATURAL", "ON", "USING", "GROUP", "ORDER",
  "HAVING", "LIMIT", "OFFSET", "UNION", "INTERSECT", "EXCEPT", "WINDOW", "SET", "VALUES", "RETURNING", "FOR", "LATERAL",
  "STRAIGHT_JOIN", "FETCH", "AS", "SELECT", "WITH", "INTO", "PARTITION", "USE", "FORCE", "IGNORE", "TABLESAMPLE", "DEFAULT",
]);

/** Split points: statement boundaries on top-level semicolons. */
function statementBounds(tokens: Token[], offset: number): [number, number] {
  let start = 0;
  let end = tokens.length;
  for (let i = 0; i < tokens.length; i++) {
    const t = tokens[i];
    if (t.type === "punct" && t.text === ";") {
      if (t.end <= offset) start = i + 1;
      else {
        end = i;
        break;
      }
    }
  }
  return [start, end];
}

export function analyze(text: string, offset: number, dialect: DialectName): Analysis {
  const all = tokenize(text, dialect);
  const [s, e] = statementBounds(all, offset);
  const stmtTokens = all.slice(s, e);
  const statementStart = stmtTokens[0]?.start ?? offset;

  // The fragment being typed: an identifier token that touches the cursor.
  let prefix = "";
  let prefixStart = offset;
  let prefixQuoted = false;
  const touching = stmtTokens.find((t) => t.start < offset && offset <= t.end && (t.type === "word" || t.type === "quoted"));
  if (touching) {
    prefixStart = touching.start;
    if (touching.type === "quoted") {
      prefixQuoted = true;
      prefix = text.slice(touching.start + 1, offset);
    } else {
      prefix = text.slice(touching.start, offset);
    }
  }

  const sig = significant(stmtTokens);
  // Index of the first significant token at/after the fragment (i.e. "the cursor position").
  let cur = sig.findIndex((t) => t.start >= prefixStart);
  if (cur < 0) cur = sig.length;

  // Qualifier chain: ident . ident . <cursor>
  const qualifier: string[] = [];
  let q = cur - 1;
  while (q >= 1 && sig[q].text === "." && isIdent(sig[q - 1]) && sig[q].end <= prefixStart) {
    qualifier.unshift(sig[q - 1].value);
    q -= 2;
  }

  const depthAt = parenDepths(sig);
  // Depth just after the token preceding the cursor.
  const cursorDepth = cur > 0 ? depthAt[cur - 1] + (sig[cur - 1].type === "punct" && sig[cur - 1].text === "(" ? 1 : 0) : 0;

  const ctes = findCTEs(sig, dialect);
  const tables = visibleTables(sig, cur, depthAt, cursorDepth, ctes, dialect);

  const before = sig.slice(0, q + 1).filter((_, i) => i < cur);
  const prevKeyword = lastKeyword(before);
  const clause = detectClause(sig, cur - qualifier.length * 2, depthAt, cursorDepth);

  let joinTarget: TableRef | undefined;
  if (prevKeyword === "ON" || prevKeyword === "USING" || clause === "on") {
    // the reference immediately preceding ON at this depth
    let onIdx = -1;
    for (let i = cur - 1; i >= 0; i--) {
      if (depthAt[i] !== cursorDepth) continue;
      if (sig[i].upper === "ON") {
        onIdx = i;
        break;
      }
      if (sig[i].upper === "JOIN" || sig[i].upper === "FROM") break;
    }
    if (onIdx >= 0) joinTarget = [...tables].filter((t) => t.at < onIdx).sort((a, b) => b.at - a.at)[0];
  }

  const prevTok = sig[cur - 1];
  const afterStar = !!prevTok && prevTok.text === "*" && prefix === "";
  const afterOperand =
    !!prevTok &&
    qualifier.length === 0 &&
    (prevTok.type === "quoted" ||
      prevTok.type === "number" ||
      prevTok.type === "string" ||
      prevTok.type === "param" ||
      (prevTok.type === "punct" && prevTok.text === ")") ||
      // "*" as a select item (SELECT *, t.*, a, *) is complete; elsewhere it multiplies.
      (prevTok.text === "*" && (!sig[cur - 2] || ["SELECT", "DISTINCT", "ALL"].includes(sig[cur - 2].upper) || [",", "."].includes(sig[cur - 2].text))) ||
      (prevTok.type === "word" && (!OPERAND_BREAKERS.has(prevTok.upper) || ["NULL", "TRUE", "FALSE"].includes(prevTok.upper))));

  return { prefix, prefixStart, prefixQuoted, qualifier, clause, tables, ctes, prevKeyword, joinTarget, afterStar, afterOperand, statementStart };
}

function parenDepths(sig: Token[]): number[] {
  const depths: number[] = [];
  let d = 0;
  for (const t of sig) {
    if (t.text === ")" && t.type === "punct") d = Math.max(0, d - 1);
    depths.push(d);
    if (t.text === "(" && t.type === "punct") d++;
  }
  return depths;
}

function lastKeyword(tokens: Token[]): string {
  for (let i = tokens.length - 1; i >= 0; i--) {
    if (tokens[i].type === "word" && KEYWORD_SET.has(tokens[i].upper)) return tokens[i].upper;
  }
  return "";
}

const KEYWORD_SET = new Set([
  "SELECT", "FROM", "JOIN", "WHERE", "AND", "OR", "ON", "BY", "SET", "INTO", "VALUES", "UPDATE", "DELETE", "INSERT",
  "HAVING", "LIMIT", "OFFSET", "AS", "TABLE", "USING", "RETURNING", "WITH", "NOT", "IN", "IS", "LIKE", "BETWEEN", "CASE",
  "WHEN", "THEN", "ELSE", "END", "DISTINCT", "UNION", "ALL", "ALTER", "DROP", "TRUNCATE", "CREATE", "INDEX", "VIEW",
  "DESCRIBE", "DESC", "EXPLAIN", "CALL", "USE", "SHOW",
]);

// Words after which a value (not an operator) comes next.
const OPERAND_BREAKERS = new Set([
  ...KEYWORD_SET, "WHERE", "HAVING", "ON", "SET", "VALUES", "SELECT", "BY", "AND", "OR", "NOT", "LIKE", "ILIKE", "IN", "IS",
  "BETWEEN", "REGEXP", "RLIKE", "SIMILAR", "TO", "WHEN", "THEN", "ELSE", "CASE", "DISTINCT", "FROM", "JOIN", "INTO", "UPDATE",
  "RETURNING", "LIMIT", "OFFSET", "ALL", "ANY", "SOME", "EXISTS", "INTERVAL", "AS", "SOUNDS", "ESCAPE", "WITH",
]);

/** Walk backwards from the cursor at the cursor's paren depth to find the governing clause. */
function detectClause(sig: Token[], cur: number, depths: number[], depth: number): Clause {
  if (cur <= 0) return "start";
  // Inside parentheses directly after INSERT INTO t ( ... -> column list
  for (let i = cur - 1; i >= 0; i--) {
    const t = sig[i];
    if (depths[i] < depth) {
      // We're inside a paren group opened at i. What precedes it?
      if (t.text === "(") {
        const p = sig[i - 1];
        const pp = sig[i - 2];
        if (isIdent(p) && pp && (pp.upper === "INTO" || (isIdent(pp) && sig[i - 3]?.text === "." && sig[i - 4] && ["INTO"].includes(sig[i - 5]?.upper ?? "")))) {
          return "insertColumns";
        }
        if (p?.upper === "VALUES") return "values";
        if (p?.upper === "USING") return "on";
        if (p && (p.upper === "IN" || p.upper === "EXISTS" || p.upper === "ANY" || p.upper === "ALL")) {
          // subquery start or value list
          const next = sig[i + 1];
          if (next && next.upper === "SELECT") break; // handled below at this depth
          return "where";
        }
        // function call arguments or expression grouping: treat like the enclosing expression
        if (p && (isIdent(p) || p.type === "word")) {
          const outer = detectClause(sig, i, depths, depths[i]);
          return outer === "from" || outer === "afterTable" ? "where" : outer;
        }
      }
      break;
    }
    if (depths[i] !== depth) continue;
    if (t.type !== "word") continue;
    const u = t.upper;
    if (u === "BY") {
      const p = sig[i - 1]?.upper;
      if (p === "GROUP") return "groupBy";
      if (p === "ORDER") return "orderBy";
      if (p === "PARTITION") return "orderBy";
    }
    if (u === "INTO") {
      const p = sig[i - 1]?.upper;
      if (p === "INSERT" || p === "REPLACE" || p === "IGNORE") return afterTableName(sig, i, cur) ? "other" : "insertTable";
    }
    if (u === "UPDATE") return afterTableName(sig, i, cur) ? "afterTable" : "updateTable";
    if (u === "DELETE") continue;
    if (u === "TABLE") {
      const p = sig[i - 1]?.upper;
      if (p === "ALTER" || p === "DROP" || p === "TRUNCATE" || p === "LOCK") return afterTableName(sig, i, cur) ? "other" : "ddlTable";
    }
    if (u === "DESCRIBE" || (u === "DESC" && i === 0) || u === "EXPLAIN") {
      if (sig[i + 1]?.upper !== "SELECT") return afterTableName(sig, i, cur) ? "other" : "ddlTable";
    }
    if (u === "FROM" || u === "JOIN") {
      if (u === "FROM" && sig[i - 1]?.upper === "DELETE") return afterTableName(sig, i, cur) ? "afterTable" : "deleteTable";
      return fromState(sig, i, cur, depths, depth);
    }
    const c = CLAUSE_KEYWORDS[u];
    if (c) return c;
  }
  return depth > 0 ? "other" : "start";
}

/** True if a complete table name has been typed after keyword index k (before cur). */
function afterTableName(sig: Token[], k: number, cur: number): boolean {
  let i = k + 1;
  if (i >= cur) return false;
  if (!isIdent(sig[i])) return false;
  i++;
  while (i + 1 < cur && sig[i].text === "." && isIdent(sig[i + 1])) i += 2;
  return i < cur && !(sig[i].text === "." && i === cur - 1);
}

/** After FROM/JOIN: either expecting a table, or past it (alias, more joins, comma). */
function fromState(sig: Token[], k: number, cur: number, depths: number[], depth: number): Clause {
  let last = sig[cur - 1];
  if (cur - 1 === k) return "from";
  // walk the comma separated list at this depth
  let i = cur - 1;
  while (i > k && depths[i] !== depth) i--;
  last = sig[i];
  if (last.text === ",") return "from";
  if (last.text === ".") return "from";
  return "afterTable";
}

/** WITH name [(cols)] AS ( ... ), ... */
function findCTEs(sig: Token[], _d: DialectName): TableRef[] {
  const out: TableRef[] = [];
  if (sig[0]?.upper !== "WITH") return out;
  let i = 1;
  if (sig[i]?.upper === "RECURSIVE") i++;
  while (i < sig.length && isIdent(sig[i])) {
    const name = sig[i].value;
    const at = i;
    i++;
    let columns: string[] | undefined;
    if (sig[i]?.text === "(") {
      columns = [];
      i++;
      while (i < sig.length && sig[i].text !== ")") {
        if (isIdent(sig[i])) columns.push(sig[i].value);
        i++;
      }
      i++;
    }
    if (sig[i]?.upper !== "AS") break;
    i++;
    if (sig[i]?.upper === "MATERIALIZED" || sig[i]?.upper === "NOT") i += sig[i].upper === "NOT" ? 2 : 1;
    if (sig[i]?.text !== "(") break;
    const close = matchParen(sig, i);
    if (!columns) columns = selectListNames(sig.slice(i + 1, close));
    out.push({ name, kind: "cte", columns, at });
    i = close + 1;
    if (sig[i]?.text !== ",") break;
    i++;
  }
  return out;
}

function matchParen(sig: Token[], open: number): number {
  let d = 0;
  for (let i = open; i < sig.length; i++) {
    if (sig[i].text === "(") d++;
    else if (sig[i].text === ")") {
      d--;
      if (d === 0) return i;
    }
  }
  return sig.length;
}

/** Output column names of a SELECT list (aliases or trailing identifiers). */
export function selectListNames(sig: Token[]): string[] {
  const sel = sig.findIndex((t) => t.upper === "SELECT");
  if (sel < 0) return [];
  const names: string[] = [];
  let depth = 0;
  let item: Token[] = [];
  const flush = () => {
    if (!item.length) return;
    const last = item[item.length - 1];
    if (isIdent(last) && !(item.length >= 2 && item[item.length - 2].text === "." && last.text === "*")) {
      names.push(last.value);
    }
    item = [];
  };
  for (let i = sel + 1; i < sig.length; i++) {
    const t = sig[i];
    if (t.text === "(") depth++;
    if (t.text === ")") depth--;
    if (depth === 0 && (t.upper === "FROM" || t.upper === "INTO" || t.upper === "WHERE")) break;
    if (depth === 0 && t.text === ",") {
      flush();
      continue;
    }
    if (depth === 0 && (t.upper === "DISTINCT" || t.upper === "ALL") && !item.length) continue;
    item.push(t);
  }
  flush();
  return names;
}

/**
 * Collect table references from FROM/JOIN/UPDATE/INTO/DELETE clauses that are visible
 * at the cursor: those at the cursor's depth inside its enclosing query, plus those of
 * enclosing queries (for correlated subqueries).
 */
function visibleTables(sig: Token[], cur: number, depths: number[], cursorDepth: number, ctes: TableRef[], d: DialectName): TableRef[] {
  // Determine the token range of each query level containing the cursor.
  const ranges: Array<[number, number, number]> = []; // [start, end, depth]
  let depth = cursorDepth;
  let lo = cur;
  while (depth >= 0) {
    let start = 0;
    for (let i = Math.min(lo, sig.length) - 1; i >= 0; i--) {
      if (depths[i] < depth) {
        start = i + 1;
        break;
      }
    }
    let end = sig.length;
    for (let i = Math.max(start, Math.min(cur, sig.length)); i < sig.length; i++) {
      if (depths[i] < depth || (sig[i].text === ")" && depths[i] === depth - 1)) {
        end = i;
        break;
      }
    }
    ranges.push([start, end, depth]);
    if (depth === 0) break;
    lo = start - 1;
    depth--;
  }

  const out: TableRef[] = [];
  for (const [start, end, dep] of ranges) {
    // Skip CTE bodies when collecting the main query's refs at depth 0.
    out.push(...refsInRange(sig, start, end, dep, depths, ctes, d));
  }
  return out;
}

function refsInRange(sig: Token[], start: number, end: number, depth: number, depths: number[], ctes: TableRef[], _d: DialectName): TableRef[] {
  const out: TableRef[] = [];
  const cteNames = new Map(ctes.map((c) => [c.name.toLowerCase(), c]));
  let i = start;
  while (i < end) {
    const t = sig[i];
    if (depths[i] !== depth || t.type !== "word") {
      i++;
      continue;
    }
    const u = t.upper;
    const startsRefs =
      u === "FROM" || u === "JOIN" || u === "UPDATE" || u === "STRAIGHT_JOIN" ||
      (u === "INTO" && ["INSERT", "REPLACE", "IGNORE"].includes(sig[i - 1]?.upper ?? "")) ||
      (u === "USING" && sig[i - 2]?.upper === "DELETE");
    if (!startsRefs) {
      i++;
      continue;
    }
    i++;
    // comma separated list of references (FROM a, b) — JOINs restart the loop
    for (;;) {
      if (i >= end) break;
      if (sig[i].upper === "LATERAL" || sig[i].upper === "ONLY") i++;
      if (sig[i]?.text === "(") {
        // derived table
        const close = matchParen(sig, i);
        const inner = sig.slice(i + 1, close);
        i = close + 1;
        const alias = readAlias(sig, i, end);
        if (alias) {
          out.push({ name: alias.name, alias: alias.name, kind: "derived", columns: selectListNames(inner), at: i });
          i = alias.next;
        }
      } else if (isIdent(sig[i])) {
        const at = i;
        const parts = [sig[i].value];
        i++;
        while (i + 1 < end && sig[i].text === "." && isIdent(sig[i + 1])) {
          parts.push(sig[i + 1].value);
          i += 2;
        }
        // "schema." still being typed: not a reference yet.
        if (sig[i]?.text === ".") break;
        // function call in FROM (e.g. generate_series(...)): skip arguments
        if (sig[i]?.text === "(") i = matchParen(sig, i) + 1;
        const name = parts[parts.length - 1];
        const schema = parts.length > 1 ? parts[parts.length - 2] : undefined;
        const alias = readAlias(sig, i, end);
        const cte = !schema ? cteNames.get(name.toLowerCase()) : undefined;
        const ref: TableRef = cte
          ? { name, kind: "cte", columns: cte.columns, at }
          : { schema, name, kind: "table", at };
        if (alias) {
          ref.alias = alias.name;
          i = alias.next;
        }
        out.push(ref);
      } else break;
      if (sig[i]?.text === ",") {
        i++;
        continue;
      }
      break;
    }
  }
  return out;
}

function readAlias(sig: Token[], i: number, end: number): { name: string; next: number } | undefined {
  if (i >= end) return undefined;
  if (sig[i].upper === "AS") i++;
  const t = sig[i];
  if (!t || i >= end) return undefined;
  if (t.type === "quoted") return { name: t.value, next: i + 1 };
  if (t.type === "word" && !REF_TERMINATORS.has(t.upper) && !KEYWORD_SET.has(t.upper)) {
    // column alias list for derived tables: AS x(a, b)
    let next = i + 1;
    if (sig[next]?.text === "(") next = matchParen(sig, next) + 1;
    return { name: t.value, next };
  }
  return undefined;
}
