// MongoDB documents as the backend sends them (mdb.Val): formatting in shell syntax,
// grid cell text, type names and type-preserving edits.
import type { Val } from "../../bindings/chloe/internal/mdb/models";

export type { Val };

/** Navicat's names for the BSON types. */
export const TYPE_LABEL: Record<string, string> = {
  object: "Document",
  array: "Array",
  string: "String",
  int: "Int32",
  long: "Int64",
  double: "Double",
  decimal: "Decimal128",
  bool: "Boolean",
  null: "Null",
  oid: "ObjectId",
  date: "Date",
  binary: "Binary",
  uuid: "UUID",
  regex: "Regular Expression",
  timestamp: "Timestamp",
  minKey: "MinKey",
  maxKey: "MaxKey",
  undefined: "Undefined",
  code: "JavaScript",
  symbol: "Symbol",
};

/** Types a grid cell shows and edits as plain text; the rest use shell syntax. */
const PLAIN = new Set(["string", "int", "long", "double", "decimal", "bool", "date", "oid"]);

export const fields = (v: Val | undefined) => v?.f ?? [];
export const items = (v: Val | undefined) => v?.a ?? [];

export function field(doc: Val, name: string): Val | undefined {
  return fields(doc).find((f) => f.k === name)?.v;
}

const IDENT = /^[A-Za-z_$][\w$]*$/;

function quote(s: string, json: boolean): string {
  const q = JSON.stringify(s);
  if (json) return q;
  return "'" + q.slice(1, -1).replace(/\\"/g, '"').replace(/'/g, "\\'") + "'";
}

/**
 * Shell syntax for a value. indent "" gives one line. json writes double-quoted keys
 * and strings (the JSON view); otherwise bare keys and single quotes (mongosh).
 */
export function formatVal(v: Val, indent = "", json = false, depth = 0): string {
  const nl = (d: number) => (indent ? "\n" + indent.repeat(d) : " ");
  switch (v.t) {
    case "object": {
      const fs = fields(v);
      if (!fs.length) return "{}";
      const parts = fs.map((f) => nl(depth + 1) + (json || !IDENT.test(f.k) ? quote(f.k, json) : f.k) + ": " + formatVal(f.v, indent, json, depth + 1));
      return "{" + parts.join(",") + nl(depth) + "}";
    }
    case "array": {
      const as = items(v);
      if (!as.length) return "[]";
      return "[" + as.map((e) => nl(depth + 1) + formatVal(e, indent, json, depth + 1)).join(",") + nl(depth) + "]";
    }
  }
  return scalar(v, json);
}

/**
 * mongosh-style layout: a value stays on one line when it fits in width columns,
 * otherwise its fields or items go one per line, each laid out the same way.
 */
export function formatCompact(v: Val, width = 80, depth = 0): string {
  const one = formatVal(v);
  const pad = "  ".repeat(depth);
  if ((v.t !== "object" && v.t !== "array") || pad.length + one.length <= width) return one;
  const inner = "  ".repeat(depth + 1);
  if (v.t === "array") return "[\n" + items(v).map((e) => inner + formatCompact(e, width, depth + 1)).join(",\n") + "\n" + pad + "]";
  const key = (k: string) => (IDENT.test(k) ? k : quote(k, false));
  return "{\n" + fields(v).map((f) => inner + key(f.k) + ": " + formatCompact(f.v, width - key(f.k).length - 2, depth + 1)).join(",\n") + "\n" + pad + "}";
}

function scalar(v: Val, json: boolean): string {
  const q = (s: string) => quote(s, json);
  const t = v.v ?? "";
  switch (v.t) {
    case "string":
      return q(t);
    case "int":
    case "bool":
      return t;
    case "double":
      return t === "NaN" || t.endsWith("Infinity") || /[.eE]/.test(t) ? t : t + ".0";
    case "long":
      return `NumberLong(${q(t)})`;
    case "decimal":
      return `NumberDecimal(${q(t)})`;
    case "null":
      return "null";
    case "oid":
      return `ObjectId(${q(t)})`;
    case "date":
      return `ISODate(${q(t)})`;
    case "binary":
      return `BinData(${v.s ?? "0"}, ${q(t)})`;
    case "uuid":
      return `UUID(${q(t)})`;
    case "regex":
      return !t.includes("/") && !/[lux]/.test(v.s ?? "") ? `/${t}/${v.s ?? ""}` : `RegExp(${q(t)}, ${q(v.s ?? "")})`;
    case "timestamp":
      return `Timestamp(${t}, ${v.s ?? "0"})`;
    case "minKey":
      return "MinKey()";
    case "maxKey":
      return "MaxKey()";
    case "undefined":
      return "undefined";
    case "code":
      return `Code(${q(t)})`;
  }
  return q(t);
}

/** Grid cell text: plain types as their text, everything else on one line in shell syntax. null is BSON null. */
export function cellText(v: Val | undefined): string | null {
  if (!v) return "";
  if (v.t === "null") return null;
  if (PLAIN.has(v.t)) return v.v ?? "";
  return formatVal(v);
}

/** Whether a cell of type t is edited as shell syntax rather than plain text. */
export const editsAsShell = (t: string) => !PLAIN.has(t) && t !== "null";

/**
 * How to read a grid edit: the type the cell had, or for a missing or null cell the
 * type the text looks like (number, boolean, or a shell expression such as { } or ISODate()).
 */
export function editType(oldType: string | undefined, text: string): string {
  if (oldType && oldType !== "null") return editsAsShell(oldType) ? "shell" : oldType;
  const s = text.trim();
  if (/^-?\d+$/.test(s)) return Math.abs(Number(s)) <= 2147483647 ? "int" : "long";
  if (/^-?(\d+\.\d*|\.\d+|\d+)([eE][-+]?\d+)?$/.test(s)) return "double";
  if (s === "true" || s === "false") return "bool";
  if (/^[[{]/.test(s) || /^(ObjectId|ISODate|NumberLong|NumberInt|NumberDecimal|UUID|BinData|Timestamp|new Date)\b/.test(s)) return "shell";
  return "string";
}

/** The most common type of a field over some documents. */
export function columnType(docs: Val[], name: string): string {
  const n = new Map<string, number>();
  for (const d of docs) {
    const v = field(d, name);
    if (v) n.set(v.t, (n.get(v.t) ?? 0) + 1);
  }
  let best = "";
  for (const [t, c] of n) if (c > (n.get(best) ?? 0)) best = t;
  return best;
}

/** One-line summary of a value for tree views. */
export function summary(v: Val): string {
  if (v.t === "object") return `{ ${fields(v).length} fields }`;
  if (v.t === "array") return `[ ${items(v).length} elements ]`;
  return cellText(v) ?? "null";
}

export function humanBytes(n: number): string {
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(2) + " GB";
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(2) + " MB";
  if (n >= 1 << 10) return (n / (1 << 10)).toFixed(2) + " KB";
  return n + " B";
}

/** Split "field, other:-1, name:text" into index keys. */
export function parseIndexKeys(s: string): { field: string; type: string }[] {
  return s
    .split(",")
    .map((p) => p.trim())
    .filter(Boolean)
    .map((p) => {
      const i = p.lastIndexOf(":");
      if (i <= 0) return { field: p, type: "1" };
      return { field: p.slice(0, i).trim(), type: p.slice(i + 1).trim() || "1" };
    });
}

export const formatIndexKeys = (keys: { field: string; type: string }[] | null | undefined) =>
  (keys ?? []).map((k) => (k.type === "1" ? k.field : `${k.field}:${k.type}`)).join(", ");
