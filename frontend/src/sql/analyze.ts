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
  | "using" // JOIN ... USING ( <columns> )
  | "where" // WHERE / HAVING / any boolean expression
  | "groupBy"
  | "orderBy"
  | "set" // UPDATE ... SET
  | "insertTable" // INSERT INTO <table>
  | "insertColumns" // INSERT INTO t ( <cols> )
  | "afterInsertTable" // INSERT INTO t | or INSERT INTO t (cols) |
  | "indexColumns" // CREATE INDEX i ON t ( <cols> ), ON CONFLICT ( <cols> )
  | "alterTable" // ALTER TABLE t |
  | "alterColumn" // ALTER TABLE t DROP COLUMN | (and MODIFY, CHANGE, ALTER, RENAME)
  | "values"
  | "updateTable"
  | "deleteTable"
  | "ddlTable" // ALTER/DROP/TRUNCATE TABLE <name>
  | "limit"
  | "selectStart" // a SELECT is expected: after UNION / INTERSECT / EXCEPT, or "FROM (" 
  | "other";

export interface TableRef {
  schema?: string;
  name: string;
  alias?: string;
  kind: "table" | "cte" | "derived";
  /**
   * Columns known from the query text itself (CTE column lists, derived table select lists).
   * "*" and "x.*" entries stand for the columns of `sources`.
   */
  columns?: string[];
  /** Tables of a derived table's or CTE's (first) SELECT, which "*" columns expand to. */
  sources?: TableRef[];
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
  /** Output column names of the query at the cursor (select-list aliases and columns). */
  outputColumns: string[];
  /** The cursor follows a UNION / INTERSECT / EXCEPT of its query (ORDER BY then sorts the whole result). */
  afterSetOp: boolean;
  /** Kind of the statement under the cursor (after any WITH). */
  statement: StatementKind;
  /** Parenthesis depth of the cursor. */
  depth: number;
  /** In SET, where a column to assign goes ("SET |", "SET a = 1, |"), not its value. */
  assignTarget: boolean;
  /** Top-level keywords of the statement before the cursor, upper-cased. */
  seen: Set<string>;
  /** Columns listed in INSERT INTO t ( ... ). */
  insertColumns: string[];
}

export type StatementKind = "select" | "insert" | "update" | "delete" | "alter" | "createIndex" | "other";

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
  UNION: "selectStart",
  INTERSECT: "selectStart",
  EXCEPT: "selectStart",
};

// Words that terminate a table reference (so they're never taken as an alias).
const REF_TERMINATORS = new Set([
  "WHERE", "JOIN", "INNER", "LEFT", "RIGHT", "FULL", "OUTER", "CROSS", "NATURAL", "ON", "USING", "GROUP", "ORDER",
  "HAVING", "LIMIT", "OFFSET", "UNION", "INTERSECT", "EXCEPT", "WINDOW", "SET", "VALUES", "RETURNING", "FOR", "LATERAL",
  "STRAIGHT_JOIN", "FETCH", "AS", "SELECT", "WITH", "INTO", "PARTITION", "USE", "FORCE", "IGNORE", "TABLESAMPLE", "DEFAULT",
  "ADD", "MODIFY", "CHANGE", "RENAME", "OWNER", "ENGINE",
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

  const statement = statementKind(sig, depthAt);
  const ctes = findCTEs(sig, depthAt);
  const tables = visibleTables(sig, cur, depthAt, cursorDepth, ctes, dialect, statement);

  const before = sig.slice(0, q + 1).filter((_, i) => i < cur);
  const prevKeyword = lastKeyword(before);
  const clause = detectClause(sig, cur - qualifier.length * 2, depthAt, cursorDepth, statement);

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

  // The query level around the cursor; after a UNION its first SELECT names the columns.
  let lvlStart = 0;
  for (let i = Math.min(cur, sig.length) - 1; i >= 0; i--) if (depthAt[i] < cursorDepth) { lvlStart = i + 1; break; }
  let lvlEnd = sig.length;
  for (let i = lvlStart; i < sig.length; i++) if (depthAt[i] < cursorDepth) { lvlEnd = i; break; }
  if (sig[lvlStart]?.upper === "WITH") {
    // skip the CTE definitions to the main SELECT
    for (let i = lvlStart; i < lvlEnd; i++) if (depthAt[i] === cursorDepth && sig[i].upper === "SELECT" && i > lvlStart) { lvlStart = i; break; }
  }
  const outputColumns = selectListNames(sig.slice(lvlStart, firstBranchEnd(sig, lvlStart, lvlEnd, depthAt))).filter((n) => !n.endsWith("*"));

  let afterSetOp = false;
  for (let i = lvlStart; i < cur; i++) if (depthAt[i] === cursorDepth && SET_OPS.has(sig[i].upper)) afterSetOp = true;

  const seen = new Set<string>();
  for (let i = 0; i < Math.min(cur, sig.length); i++) if (depthAt[i] === 0 && sig[i].type === "word") seen.add(sig[i].upper);

  // SET a = 1, | : the last SET (or ON DUPLICATE KEY UPDATE) at the cursor's depth.
  let assignTarget = false;
  if (clause === "set" && qualifier.length === 0) {
    for (let i = cur - 1; i >= 0; i--) {
      if (depthAt[i] < cursorDepth) break;
      if (depthAt[i] !== cursorDepth) continue;
      if (isAssignStart(sig, i)) {
        assignTarget = i === cur - 1 || sig[cur - 1].text === ",";
        break;
      }
    }
  }

  const insertColumns: string[] = [];
  if (statement === "insert") {
    const into = sig.findIndex((t, i) => depthAt[i] === 0 && t.upper === "INTO");
    let i = into + 1;
    while (isIdent(sig[i]) && sig[i + 1]?.text === ".") i += 2;
    if (into >= 0 && sig[i + 1]?.text === "(") {
      for (let j = i + 2; j < sig.length && depthAt[j] > 0; j++) if (depthAt[j] === 1 && isIdent(sig[j])) insertColumns.push(sig[j].value);
    }
  }

  return {
    prefix, prefixStart, prefixQuoted, qualifier, clause, tables, ctes, prevKeyword, joinTarget, afterStar, afterOperand, statementStart, outputColumns, afterSetOp,
    statement, depth: cursorDepth, assignTarget, seen, insertColumns,
  };
}

/** SET, or the UPDATE of MySQL's ON DUPLICATE KEY UPDATE, which take assignments. */
const isAssignStart = (sig: Token[], i: number) => sig[i].upper === "SET" || (sig[i].upper === "UPDATE" && sig[i - 1]?.upper === "KEY");

function statementKind(sig: Token[], depths: number[]): StatementKind {
  let i = 0;
  if (sig[0]?.upper === "WITH") {
    // the first top-level statement keyword after the CTE definitions
    i = sig.findIndex((t, j) => j > 0 && depths[j] === 0 && ["SELECT", "INSERT", "REPLACE", "UPDATE", "DELETE"].includes(t.upper) && sig[j - 1]?.text === ")");
    if (i < 0) return "select";
  }
  const u = sig[i]?.upper;
  if (u === "SELECT" || u === "VALUES" || u === "TABLE") return "select";
  if (u === "INSERT" || u === "REPLACE") return "insert";
  if (u === "UPDATE") return "update";
  if (u === "DELETE") return "delete";
  if (u === "ALTER" && sig[i + 1]?.upper === "TABLE") return "alter";
  if (u === "CREATE" && sig.slice(i + 1, i + 3).some((t) => t.upper === "INDEX")) return "createIndex";
  return "other";
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
function detectClause(sig: Token[], cur: number, depths: number[], depth: number, stmt: StatementKind): Clause {
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
        if (p?.upper === "CONFLICT") return "indexColumns";
        if (stmt === "createIndex" && isIdent(p) && depths[i] === 0) return "indexColumns";
        if (p?.upper === "USING") return "using";
        // A derived table or set operand still empty: "FROM (", "JOIN LATERAL (", "UNION (".
        if (!p || ["FROM", "JOIN", "LATERAL", "UNION", "INTERSECT", "EXCEPT", "ALL", "DISTINCT", "(", ","].includes(p.upper) || p.text === "(" || p.text === ",") {
          if (!p || p.text !== "," || detectClause(sig, i, depths, depths[i], stmt) === "from") return "selectStart";
        }
        if (p && (p.upper === "IN" || p.upper === "EXISTS" || p.upper === "ANY" || p.upper === "ALL")) {
          // subquery start or value list
          const next = sig[i + 1];
          if (next && next.upper === "SELECT") break; // handled below at this depth
          return "where";
        }
        // function call arguments or expression grouping: treat like the enclosing expression
        if (p && (isIdent(p) || p.type === "word")) {
          const outer = detectClause(sig, i, depths, depths[i], stmt);
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
      if (p === "INSERT" || p === "REPLACE" || p === "IGNORE") return afterTableName(sig, i, cur) ? "afterInsertTable" : "insertTable";
    }
    if (u === "UPDATE") {
      if (isAssignStart(sig, i)) return "set";
      if (sig[i - 1]?.upper === "DO") continue; // ON CONFLICT DO UPDATE SET
      return afterTableName(sig, i, cur) ? "afterTable" : "updateTable";
    }
    if (u === "ON" && stmt === "createIndex") return afterTableName(sig, i, cur) ? "other" : "ddlTable";
    if (u === "DELETE") continue;
    if (u === "TABLE") {
      const p = sig[i - 1]?.upper;
      if (p === "ALTER" && afterTableName(sig, i, cur)) return alterState(sig, i, cur);
      if (p === "ALTER" || p === "DROP" || p === "TRUNCATE" || p === "LOCK") return afterTableName(sig, i, cur) ? "other" : "ddlTable";
    }
    if (u === "DESCRIBE" || (u === "DESC" && i === 0) || u === "EXPLAIN") {
      if (sig[i + 1]?.upper !== "SELECT") return afterTableName(sig, i, cur) ? "other" : "ddlTable";
    }
    if (u === "FROM" || u === "JOIN") {
      if (u === "FROM" && sig[i - 1]?.upper === "DELETE") return afterTableName(sig, i, cur) ? "afterTable" : "deleteTable";
      return fromState(sig, i, cur, depths, depth);
    }
    // DELETE FROM t USING a, b lists tables; JOIN ... USING (cols) doesn't.
    if (u === "USING" && stmt === "delete" && sig[i + 1]?.text !== "(") return fromState(sig, i, cur, depths, depth);
    const c = CLAUSE_KEYWORDS[u];
    if (c) return c;
  }
  return depth > 0 ? "other" : "start";
}

/** True if a complete table name has been typed after keyword index k (before cur). */
function afterTableName(sig: Token[], k: number, cur: number): boolean {
  return tableNameEnd(sig, k, cur) >= 0;
}

/** Index just past the table name after keyword index k, or -1 while it's still being typed. */
function tableNameEnd(sig: Token[], k: number, cur: number): number {
  let i = k + 1;
  if (sig[i]?.upper === "IF") i += sig[i + 1]?.upper === "NOT" ? 3 : 2; // IF [NOT] EXISTS
  if (sig[i]?.upper === "ONLY") i++;
  if (i >= cur) return -1;
  if (!isIdent(sig[i])) return -1;
  i++;
  while (i + 1 < cur && sig[i].text === "." && isIdent(sig[i + 1])) i += 2;
  return i <= cur && !(sig[i]?.text === "." && i === cur - 1) ? i : -1;
}

/** ALTER TABLE t ... : an action is expected right after the name or a comma, a column after DROP COLUMN etc. */
function alterState(sig: Token[], k: number, cur: number): Clause {
  const end = tableNameEnd(sig, k, cur);
  const prev = sig[cur - 1];
  if (cur === end || prev.text === ",") return "alterTable";
  if (["COLUMN", "MODIFY", "CHANGE"].includes(prev.upper)) return "alterColumn";
  if (["DROP", "ALTER", "RENAME", "ADD"].includes(prev.upper)) return "alterTable";
  return "other";
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
function findCTEs(sig: Token[], depths: number[]): TableRef[] {
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
    const sources = refsInRange(sig, i + 1, firstBranchEnd(sig, i + 1, close, depths), depths[i] + 1, depths, out);
    out.push({ name, kind: "cte", columns, sources, at });
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
    if (last.text === "*" && item.length === 1) names.push("*");
    else if (last.text === "*" && item.length === 3 && item[1].text === "." && isIdent(item[0])) names.push(item[0].value + ".*");
    else if (isIdent(last)) names.push(last.value);
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
function visibleTables(sig: Token[], cur: number, depths: number[], cursorDepth: number, ctes: TableRef[], d: DialectName, stmt: StatementKind): TableRef[] {
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
    const [bs, be] = branchAround(sig, start, end, Math.min(lo, sig.length), depth, depths);
    ranges.push([bs, be, depth]);
    if (depth === 0) break;
    // A derived table (FROM (...) x) can't see the query it's part of, unless LATERAL.
    const opener = sig[start - 2]?.upper;
    if ((opener === "FROM" || opener === "JOIN") && sig[start]?.upper === "SELECT") break;
    lo = start - 1;
    depth--;
  }

  const out: TableRef[] = [];
  for (const [start, end, dep] of ranges) {
    // Skip CTE bodies when collecting the main query's refs at depth 0.
    out.push(...refsInRange(sig, start, end, dep, depths, ctes, d, stmt));
  }
  return out;
}

const SET_OPS = new Set(["UNION", "INTERSECT", "EXCEPT"]);

/** End of the first SELECT of a set operation between start and end (all of it if none). */
function firstBranchEnd(sig: Token[], start: number, end: number, depths: number[]): number {
  const d = depths[start];
  for (let i = start; i < end; i++) if (depths[i] === d && SET_OPS.has(sig[i].upper)) return i;
  return end;
}

/** Narrow [start, end) to the SELECT of a set operation that contains position at. */
function branchAround(sig: Token[], start: number, end: number, at: number, depth: number, depths: number[]): [number, number] {
  let s = start;
  let e = end;
  for (let i = Math.min(at, end) - 1; i >= start; i--) {
    if (depths[i] === depth && SET_OPS.has(sig[i].upper)) {
      s = i + 1;
      break;
    }
  }
  for (let i = Math.max(at, start); i < end; i++) {
    if (depths[i] === depth && SET_OPS.has(sig[i].upper)) {
      e = i;
      break;
    }
  }
  return [s, e];
}

function refsInRange(sig: Token[], start: number, end: number, depth: number, depths: number[], ctes: TableRef[], _d?: DialectName, stmt?: StatementKind): TableRef[] {
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
      u === "FROM" || u === "JOIN" || u === "STRAIGHT_JOIN" ||
      // not ON DUPLICATE KEY UPDATE / ON CONFLICT DO UPDATE
      (u === "UPDATE" && sig[i - 1]?.upper !== "KEY" && sig[i - 1]?.upper !== "DO") ||
      // ALTER TABLE t, CREATE INDEX i ON t
      (u === "TABLE" && sig[i - 1]?.upper === "ALTER" && depth === 0) ||
      (u === "ON" && stmt === "createIndex" && depth === 0) ||
      (u === "INTO" && ["INSERT", "REPLACE", "IGNORE"].includes(sig[i - 1]?.upper ?? "")) ||
      // DELETE FROM t USING a, b (MySQL, PostgreSQL); JOIN ... USING (cols) is not a table list.
      (u === "USING" && sig[0]?.upper === "DELETE" && sig[i + 1]?.text !== "(");
    if (!startsRefs) {
      i++;
      continue;
    }
    i++;
    if (u === "TABLE" && sig[i]?.upper === "IF") i += 2;
    // comma separated list of references (FROM a, b) — JOINs restart the loop
    for (;;) {
      if (i >= end) break;
      if (sig[i].upper === "LATERAL" || sig[i].upper === "ONLY") i++;
      if (sig[i]?.text === "(") {
        // derived table
        const open = i;
        const close = matchParen(sig, i);
        const inner = sig.slice(i + 1, close);
        i = close + 1;
        const alias = readAlias(sig, i, end);
        if (alias) {
          const sources = refsInRange(sig, open + 1, firstBranchEnd(sig, open + 1, close, depths), depths[open] + 1, depths, ctes, _d);
          out.push({ name: alias.name, alias: alias.name, kind: "derived", columns: selectListNames(inner), sources, at: i });
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
          ? { name, kind: "cte", columns: cte.columns, sources: cte.sources, at }
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
