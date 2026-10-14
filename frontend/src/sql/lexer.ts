// A small, forgiving SQL tokenizer used by autocomplete. It never throws: incomplete
// strings and comments simply run to the end of the input.

export type DialectName = "mysql" | "postgres";

export type TokenType =
  | "word" // identifier or keyword
  | "quoted" // quoted identifier (`x`, "x", [x])
  | "string"
  | "number"
  | "op"
  | "punct" // , ; ( ) .
  | "comment"
  | "ws"
  | "param"; // $1, ?, :name

export interface Token {
  type: TokenType;
  text: string;
  start: number;
  end: number;
  /** Identifier value: unquoted text for quoted identifiers, raw text for words. */
  value: string;
  /** Upper-cased text for words, for keyword checks. */
  upper: string;
}

const isIdentStart = (c: string) => /[A-Za-z_\u0080-￿]/.test(c);
const isIdentPart = (c: string) => /[A-Za-z0-9_$\u0080-￿]/.test(c);

export function tokenize(sql: string, dialect: DialectName): Token[] {
  const out: Token[] = [];
  const n = sql.length;
  let i = 0;
  const push = (type: TokenType, start: number, end: number, value?: string) => {
    const text = sql.slice(start, end);
    out.push({ type, text, start, end, value: value ?? text, upper: type === "word" ? text.toUpperCase() : "" });
  };

  while (i < n) {
    const c = sql[i];
    const start = i;

    if (/\s/.test(c)) {
      while (i < n && /\s/.test(sql[i])) i++;
      push("ws", start, i);
      continue;
    }
    if ((c === "-" && sql[i + 1] === "-") || (c === "#" && dialect === "mysql")) {
      while (i < n && sql[i] !== "\n") i++;
      push("comment", start, i);
      continue;
    }
    if (c === "/" && sql[i + 1] === "*") {
      const end = sql.indexOf("*/", i + 2);
      i = end < 0 ? n : end + 2;
      push("comment", start, i);
      continue;
    }
    if (c === "'" || (c === '"' && dialect === "mysql")) {
      i = skipString(sql, i, c, dialect === "mysql" || (i > 0 && /[eE]/.test(sql[i - 1])));
      push("string", start, i);
      continue;
    }
    if (c === "`" || (c === '"' && dialect === "postgres")) {
      i = skipString(sql, i, c, false);
      const closed = sql[i - 1] === c && i - start > 1;
      const inner = sql.slice(start + 1, closed ? i - 1 : i).split(c + c).join(c);
      push("quoted", start, i, inner);
      continue;
    }
    if (c === "$" && dialect === "postgres") {
      const m = /^\$([A-Za-z_][A-Za-z0-9_]*)?\$/.exec(sql.slice(i));
      if (m) {
        const tag = m[0];
        const end = sql.indexOf(tag, i + tag.length);
        i = end < 0 ? n : end + tag.length;
        push("string", start, i);
        continue;
      }
      const p = /^\$\d+/.exec(sql.slice(i));
      if (p) {
        i += p[0].length;
        push("param", start, i);
        continue;
      }
    }
    if (c === "?") {
      i++;
      push("param", start, i);
      continue;
    }
    if (/[0-9]/.test(c) || (c === "." && /[0-9]/.test(sql[i + 1] ?? ""))) {
      while (i < n && /[0-9.eE]/.test(sql[i])) i++;
      push("number", start, i);
      continue;
    }
    if (isIdentStart(c)) {
      while (i < n && isIdentPart(sql[i])) i++;
      push("word", start, i);
      continue;
    }
    if ("(),;.".includes(c)) {
      i++;
      push("punct", start, i);
      continue;
    }
    if (c === ":" && sql[i + 1] === ":") {
      i += 2;
      push("op", start, i);
      continue;
    }
    // operators: group runs of operator characters
    while (i < n && /[<>=!~+\-*/%^&|@:#]/.test(sql[i])) i++;
    if (i === start) i++;
    push("op", start, i);
  }
  return out;
}

function skipString(s: string, i: number, q: string, backslash: boolean): number {
  let j = i + 1;
  while (j < s.length) {
    const ch = s[j];
    if (backslash && ch === "\\") {
      j += 2;
      continue;
    }
    if (ch === q) {
      if (s[j + 1] === q) {
        j += 2;
        continue;
      }
      return j + 1;
    }
    j++;
  }
  return s.length;
}

/** Tokens without whitespace and comments. */
export function significant(tokens: Token[]): Token[] {
  return tokens.filter((t) => t.type !== "ws" && t.type !== "comment");
}

export const isIdent = (t: Token | undefined) => !!t && (t.type === "word" || t.type === "quoted");
