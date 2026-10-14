// Pure editing operations on a table design. The designer UI keeps references
// (primary key, indexes, uniques, foreign keys) consistent through these.
import type { D } from "../api";

// Strict versions of the generated types: Go nil slices are normalised to arrays once,
// in normalize(), so the UI never has to null-check them.
export type Column = D.Column;
export type Index = Omit<D.Index, "columns"> & { columns: D.IndexColumn[] };
export type Unique = Omit<D.Unique, "columns"> & { columns: string[] };
export type ForeignKey = Omit<D.ForeignKey, "columns" | "refColumns"> & { columns: string[]; refColumns: string[] };
export type Trigger = Omit<D.Trigger, "events"> & { events: string[] };
export type PrimaryKey = Omit<D.PrimaryKey, "columns"> & { columns: string[] };
export type Table = Omit<D.Table, "columns" | "indexes" | "uniques" | "foreignKeys" | "checks" | "triggers" | "primaryKey"> & {
  columns: Column[];
  indexes: Index[];
  uniques: Unique[];
  foreignKeys: ForeignKey[];
  checks: D.Check[];
  triggers: Trigger[];
  primaryKey?: PrimaryKey;
};
export type Dialect = "mysql" | "postgres";

const arr = <T,>(x: T[] | null | undefined): T[] => x ?? [];

/** Replace Go's null slices with arrays so the UI can map over everything. */
export function normalize(t: D.Table): Table {
  return {
    ...t,
    columns: arr(t.columns),
    indexes: arr(t.indexes).map((i) => ({ ...i, columns: arr(i.columns) })),
    uniques: arr(t.uniques).map((u) => ({ ...u, columns: arr(u.columns) })),
    foreignKeys: arr(t.foreignKeys).map((f) => ({ ...f, columns: arr(f.columns), refColumns: arr(f.refColumns) })),
    checks: arr(t.checks),
    triggers: arr(t.triggers).map((tr) => ({ ...tr, events: arr(tr.events) })),
    primaryKey: t.primaryKey ? { ...t.primaryKey, columns: arr(t.primaryKey.columns) } : undefined,
    options: t.options ?? {},
  };
}

export function newColumn(d: Dialect, taken: string[]): Column {
  let name = "column";
  for (let i = 1; taken.includes(name); i++) name = `column${i}`;
  return { name, origName: "", type: d === "postgres" ? "text" : "varchar", length: d === "postgres" ? "" : "255", nullable: true };
}

export function newTable(d: Dialect, database: string, schema: string | undefined, meta?: D.Meta): Table {
  const id: Column =
    d === "postgres"
      ? { name: "id", origName: "", type: "bigint", nullable: false, identity: "by default" }
      : { name: "id", origName: "", type: "bigint", unsigned: true, nullable: false, autoIncrement: true };
  return normalize({
    database,
    schema,
    name: "",
    origName: "",
    columns: [id],
    primaryKey: { columns: ["id"] },
    indexes: [],
    uniques: [],
    foreignKeys: [],
    checks: [],
    triggers: [],
    options: d === "mysql" ? { engine: meta?.defaultEngine || "InnoDB", charset: meta?.defaultCharset, collation: meta?.defaultCollation } : {},
  });
}

const replaceIn = (cols: string[], from: string, to: string) => cols.map((c) => (c === from ? to : c));

/** Rename a column and every reference to it. */
export function renameColumn(t: Table, index: number, name: string): Table {
  const from = t.columns[index].name;
  if (from === name) return t;
  return {
    ...t,
    columns: t.columns.map((c, i) => (i === index ? { ...c, name } : c)),
    primaryKey: t.primaryKey ? { ...t.primaryKey, columns: replaceIn(t.primaryKey.columns, from, name) } : t.primaryKey,
    indexes: t.indexes.map((ix) => ({ ...ix, columns: ix.columns.map((c) => (c.name === from && !c.expr ? { ...c, name } : c)) })),
    uniques: t.uniques.map((u) => ({ ...u, columns: replaceIn(u.columns, from, name) })),
    foreignKeys: t.foreignKeys.map((f) => ({ ...f, columns: replaceIn(f.columns, from, name) })),
  };
}

/** Delete a column, dropping it from keys; indexes/uniques/FKs left empty go too. */
export function deleteColumn(t: Table, index: number): Table {
  const name = t.columns[index].name;
  const pk = t.primaryKey ? t.primaryKey.columns.filter((c) => c !== name) : [];
  return {
    ...t,
    columns: t.columns.filter((_, i) => i !== index),
    primaryKey: pk.length ? { ...t.primaryKey!, columns: pk } : undefined,
    indexes: t.indexes.map((ix) => ({ ...ix, columns: ix.columns.filter((c) => c.expr || c.name !== name) })).filter((ix) => ix.columns.length),
    uniques: t.uniques.map((u) => ({ ...u, columns: u.columns.filter((c) => c !== name) })).filter((u) => u.columns.length),
    // A foreign key loses its meaning without all its columns.
    foreignKeys: t.foreignKeys.filter((f) => !f.columns.includes(name)),
  };
}

/** Toggle a column's membership in the (possibly composite) primary key. */
export function togglePrimaryKey(t: Table, name: string): Table {
  const cols = t.primaryKey?.columns ?? [];
  const next = cols.includes(name) ? cols.filter((c) => c !== name) : [...cols, name];
  const columns = next.includes(name) ? t.columns.map((c) => (c.name === name ? { ...c, nullable: false } : c)) : t.columns;
  return { ...t, columns, primaryKey: next.length ? { name: t.primaryKey?.name ?? "", columns: next } : undefined };
}

export function moveColumn(t: Table, index: number, delta: number): Table {
  const to = index + delta;
  if (to < 0 || to >= t.columns.length) return t;
  const columns = [...t.columns];
  const [c] = columns.splice(index, 1);
  columns.splice(to, 0, c);
  return { ...t, columns };
}

export function insertColumn(t: Table, index: number, d: Dialect): Table {
  const columns = [...t.columns];
  columns.splice(index, 0, newColumn(d, t.columns.map((c) => c.name)));
  return { ...t, columns };
}

/** A name that isn't used by another element of the same kind. */
export function uniqueName(base: string, taken: string[]): string {
  let n = base;
  for (let i = 2; taken.includes(n); i++) n = `${base}_${i}`;
  return n;
}

/** Types whose Length column is meaningful (others hide/disable it). */
export function takesLength(type: string): boolean {
  return /^(var)?char|^(var)?binary|^bit|^varbit|^decimal|^numeric|^float|^double|^(tiny|small|medium|big)?int|^time|^datetime|^timestamp|^enum|^set|^interval/i.test(type);
}

export function takesScale(type: string): boolean {
  return /^(decimal|numeric|float|double)/i.test(type);
}

export const isTextType = (type: string) => /char|text|enum|set|json/i.test(type);

/** Prepare a draft for the server: blank generated expressions mean "not generated". */
export function toWire(t: Table): Table {
  return {
    ...t,
    columns: t.columns.map((c) => (c.generatedExpr?.trim() ? { ...c, generatedExpr: c.generatedExpr.trim() } : { ...c, generatedExpr: undefined, generatedStored: undefined })),
  };
}
