// Enum value completion: after "status = ", inside "status = '…", in
// "status IN ('a', '…" and in "SET status = '…", offer the column's enum values.

import { DialectName, Token, isIdent, significant, tokenize } from "./lexer";

export interface EnumContext {
  /** Column as written: [qualifier?, name]. */
  column: string[];
  /** Typed part of the value (inside quotes) or of a bare word. */
  prefix: string;
  /** Where the replaced text starts. */
  replaceStart: number;
  /** The cursor is inside a string literal; insert the bare value. */
  inString: boolean;
}

const COMPARE = new Set(["=", "<>", "!=", "<=>"]);

/** A column reference ending at sig[end]. */
function columnAt(sig: Token[], end: number): string[] | null {
  if (!isIdent(sig[end]) || isKeyword(sig[end])) return null;
  if (end >= 2 && sig[end - 1].text === "." && isIdent(sig[end - 2])) return [sig[end - 2].value, sig[end].value];
  return [sig[end].value];
}

const NOT_COLUMNS = new Set(["AND", "OR", "NOT", "WHERE", "ON", "SET", "IN", "IS", "NULL", "WHEN", "THEN", "ELSE", "SELECT", "CASE", "LIKE"]);
const isKeyword = (t: Token) => t.type === "word" && NOT_COLUMNS.has(t.upper);

export function enumContext(text: string, offset: number, dialect: DialectName): EnumContext | null {
  const tokens = tokenize(text, dialect);
  // Inside a string literal?
  const str = tokens.find((t) => t.type === "string" && t.text[0] === "'" && t.start < offset && (t.text.length > 1 && t.text.endsWith("'") ? offset < t.end : offset <= t.end));
  const sig = significant(tokens);
  let at: number;
  let prefix = "";
  let replaceStart = offset;
  if (str) {
    at = sig.indexOf(str);
    prefix = text.slice(str.start + 1, offset);
    replaceStart = str.start + 1;
  } else {
    // A bare word being typed (status = pa|) or nothing yet (status = |).
    // The token after the cursor, or a word that ends at it (being typed).
    at = sig.findIndex((t) => t.end > offset || (t.end === offset && t.type === "word"));
    if (at < 0) at = sig.length;
    const cur = sig[at];
    if (cur && cur.start < offset && cur.type === "word") {
      prefix = text.slice(cur.start, offset);
      replaceStart = cur.start;
    } else if (cur && cur.start < offset) {
      return null;
    }
  }
  const prev = sig[at - 1];
  if (!prev) return null;
  // col = '…'
  if (prev.type === "op" && COMPARE.has(prev.text)) {
    const column = columnAt(sig, at - 2);
    return column ? { column, prefix, replaceStart, inString: !!str } : null;
  }
  // col IN ('a', '…'
  if (prev.text === "(" || prev.text === ",") {
    let i = at - 1;
    while (i >= 0 && (sig[i].text === "," || sig[i].type === "string")) i--;
    if (i < 1 || sig[i].text !== "(" || sig[i - 1].upper !== "IN") return null;
    let c = i - 2;
    if (sig[c]?.upper === "NOT") c--;
    const column = columnAt(sig, c);
    return column ? { column, prefix, replaceStart, inString: !!str } : null;
  }
  return null;
}

/** Quote a value as a SQL string literal. */
export const sqlString = (v: string) => `'${v.replace(/'/g, "''")}'`;
