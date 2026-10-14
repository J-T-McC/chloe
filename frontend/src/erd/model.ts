// ER diagram model: tables and relationships built from a database catalog, node
// sizing, automatic layout (ELK layered) and SVG export. No React here.
import ELK from "elkjs/lib/elk.bundled.js";

export interface ErdColumn {
  name: string;
  type: string;
  pk: boolean;
  fk: boolean;
  nullable: boolean;
}

export interface ErdTable {
  id: string; // schema.name or name
  schema?: string;
  name: string;
  kind: "table" | "view";
  columns: ErdColumn[];
  comment?: string;
}

export interface ErdRelation {
  id: string;
  from: string; // child (FK) table id
  to: string; // parent (referenced) table id
  fromColumns: string[];
  toColumns: string[];
  name?: string;
  /** child side: true when the FK columns are nullable (zero-or-many) */
  optional: boolean;
  /** true when the FK columns are also unique / the PK (one-to-one) */
  oneToOne: boolean;
}

export interface ErdModel {
  tables: ErdTable[];
  relations: ErdRelation[];
}

export type ColumnMode = "all" | "keys" | "none";

export interface CatalogLike {
  objects: Array<{ schema?: string; name: string; kind: string; comment?: string }>;
  columns: Array<{ schema?: string; table: string; name: string; type: string; pk?: boolean; nullable?: boolean }>;
  fks: Array<{ schema?: string; table: string; columns: string[] | null; refSchema?: string; refTable: string; refColumns: string[] | null }>;
}

const tid = (schema: string | undefined, name: string) => (schema ? `${schema}.${name}` : name);

/**
 * Build the diagram for one schema (PostgreSQL) or the whole database (MySQL).
 * For MySQL pass database: its foreign keys name the referenced database, while
 * its tables carry no schema.
 */
export function buildModel(cat: CatalogLike, schema?: string, includeViews = false, database?: string): ErdModel {
  const inScope = (s?: string) => !schema || (s ?? "") === schema;
  const tables = new Map<string, ErdTable>();
  for (const o of cat.objects) {
    if (!inScope(o.schema)) continue;
    const kind = o.kind === "table" ? "table" : o.kind === "view" || o.kind === "materialized_view" ? "view" : null;
    if (!kind || (kind === "view" && !includeViews)) continue;
    tables.set(tid(o.schema, o.name), { id: tid(o.schema, o.name), schema: o.schema, name: o.name, kind, columns: [], comment: o.comment });
  }
  for (const c of cat.columns) {
    const t = tables.get(tid(c.schema, c.table));
    if (t) t.columns.push({ name: c.name, type: c.type, pk: !!c.pk, fk: false, nullable: !!c.nullable });
  }
  const relations: ErdRelation[] = [];
  for (const fk of cat.fks) {
    const local = (s?: string) => (database && s === database ? undefined : s);
    const from = tables.get(tid(local(fk.schema), fk.table));
    const to = tables.get(tid(local(fk.refSchema || fk.schema), fk.refTable));
    if (!from || !to) continue;
    const cols = fk.columns ?? [];
    for (const c of from.columns) if (cols.includes(c.name)) c.fk = true;
    const fkCols = from.columns.filter((c) => cols.includes(c.name));
    const pkCols = from.columns.filter((c) => c.pk).map((c) => c.name);
    relations.push({
      id: `${from.id}:${cols.join(",")}->${to.id}`,
      from: from.id,
      to: to.id,
      fromColumns: cols,
      toColumns: fk.refColumns ?? [],
      optional: fkCols.some((c) => c.nullable),
      oneToOne: pkCols.length > 0 && pkCols.length === cols.length && pkCols.every((p) => cols.includes(p)),
    });
  }
  return { tables: [...tables.values()].sort((a, b) => a.name.localeCompare(b.name)), relations };
}

/** Columns shown for a table in the current mode. */
export function visibleColumns(t: ErdTable, mode: ColumnMode): ErdColumn[] {
  if (mode === "none") return [];
  if (mode === "keys") return t.columns.filter((c) => c.pk || c.fk);
  return t.columns;
}

export const HEADER_H = 30;
export const ROW_H = 20;
const CHAR_W = 6.6;

export function tableSize(t: ErdTable, mode: ColumnMode): { width: number; height: number } {
  const cols = visibleColumns(t, mode);
  const nameW = t.name.length * 7.4 + 44;
  const colW = cols.reduce((w, c) => Math.max(w, (c.name.length + c.type.length) * CHAR_W + 54), 0);
  return { width: Math.round(Math.min(360, Math.max(160, nameW, colW))), height: HEADER_H + Math.max(1, cols.length) * ROW_H + 6 };
}

export type Positions = Record<string, { x: number; y: number }>;

const elk = new ELK();

/** Layered left-to-right layout: referenced (parent) tables end up to the right. */
export async function autoLayout(model: ErdModel, mode: ColumnMode): Promise<Positions> {
  const graph = {
    id: "root",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": "RIGHT", // child (FK) tables left, referenced tables right
      "elk.spacing.nodeNode": "40",
      "elk.layered.spacing.nodeNodeBetweenLayers": "80",
      "elk.layered.crossingMinimization.strategy": "LAYER_SWEEP",
      "elk.layered.nodePlacement.strategy": "BRANDES_KOEPF",
      // Disconnected tables are packed in a grid instead of one long column.
      "elk.separateConnectedComponents": "true",
      "elk.spacing.componentComponent": "60",
      "org.eclipse.elk.layered.considerModelOrder.strategy": "NODES_AND_EDGES",
    },
    children: model.tables.map((t) => ({ id: t.id, ...tableSize(t, mode) })),
    edges: model.relations.filter((r) => r.from !== r.to).map((r) => ({ id: r.id, sources: [r.from], targets: [r.to] })),
  };
  const res = await elk.layout(graph);
  const pos: Positions = {};
  for (const c of res.children ?? []) pos[c.id] = { x: c.x ?? 0, y: c.y ?? 0 };
  return pos;
}

// ---- SVG export ------------------------------------------------------------------

export interface ExportStyle {
  background: string;
  header: string;
  headerText: string;
  body: string;
  text: string;
  muted: string;
  border: string;
  line: string;
  key: string;
}

const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

/** Render the diagram as a standalone SVG document. */
export function toSVG(model: ErdModel, pos: Positions, mode: ColumnMode, st: ExportStyle): string {
  const boxes = model.tables.map((t) => ({ t, ...(pos[t.id] ?? { x: 0, y: 0 }), ...tableSize(t, mode) }));
  const minX = Math.min(...boxes.map((b) => b.x), 0) - 20;
  const minY = Math.min(...boxes.map((b) => b.y), 0) - 20;
  const maxX = Math.max(...boxes.map((b) => b.x + b.width), 0) + 20;
  const maxY = Math.max(...boxes.map((b) => b.y + b.height), 0) + 20;
  const byId = new Map(boxes.map((b) => [b.t.id, b]));
  const out: string[] = [];
  out.push(`<svg xmlns="http://www.w3.org/2000/svg" width="${maxX - minX}" height="${maxY - minY}" viewBox="${minX} ${minY} ${maxX - minX} ${maxY - minY}" font-family="-apple-system, Segoe UI, sans-serif" font-size="12">`);
  out.push(`<rect x="${minX}" y="${minY}" width="${maxX - minX}" height="${maxY - minY}" fill="${st.background}"/>`);
  for (const r of model.relations) {
    const a = byId.get(r.from);
    const b = byId.get(r.to);
    if (!a || !b) continue;
    const rowY = (box: typeof a, col: string) => {
      const i = visibleColumns(box.t, mode).findIndex((c) => c.name === col);
      return box.y + (i < 0 ? HEADER_H / 2 : HEADER_H + i * ROW_H + ROW_H / 2);
    };
    const fromRight = a.x + a.width / 2 < b.x + b.width / 2;
    const x1 = fromRight ? a.x + a.width : a.x;
    const x2 = fromRight ? b.x : b.x + b.width;
    const y1 = rowY(a, r.fromColumns[0]);
    const y2 = rowY(b, r.toColumns[0]);
    const mid = (x1 + x2) / 2;
    out.push(`<path d="M${x1},${y1} C${mid},${y1} ${mid},${y2} ${x2},${y2}" fill="none" stroke="${st.line}" stroke-width="1.4"${r.optional ? ' stroke-dasharray="5 3"' : ""}/>`);
    // crow's foot at the many (child) end, bar at the one (parent) end
    const d1 = fromRight ? 1 : -1;
    if (!r.oneToOne) out.push(`<path d="M${x1 + d1 * 10},${y1} L${x1},${y1 - 6} M${x1 + d1 * 10},${y1} L${x1},${y1 + 6}" stroke="${st.line}" stroke-width="1.4" fill="none"/>`);
    const d2 = fromRight ? -1 : 1;
    out.push(`<path d="M${x2 + d2 * 8},${y2 - 6} L${x2 + d2 * 8},${y2 + 6}" stroke="${st.line}" stroke-width="1.4"/>`);
  }
  for (const b of boxes) {
    const cols = visibleColumns(b.t, mode);
    out.push(`<g transform="translate(${b.x},${b.y})">`);
    out.push(`<rect width="${b.width}" height="${b.height}" rx="6" fill="${st.body}" stroke="${st.border}"/>`);
    out.push(`<path d="M0,6 a6,6 0 0 1 6,-6 h${b.width - 12} a6,6 0 0 1 6,6 v${HEADER_H - 6} h-${b.width} z" fill="${st.header}"/>`);
    out.push(`<text x="10" y="${HEADER_H / 2 + 4}" fill="${st.headerText}" font-weight="600">${esc(b.t.name)}</text>`);
    cols.forEach((c, i) => {
      const y = HEADER_H + i * ROW_H + ROW_H / 2 + 4;
      const mark = c.pk ? "PK" : c.fk ? "FK" : "";
      if (mark) out.push(`<text x="8" y="${y}" fill="${st.key}" font-size="9" font-weight="700">${mark}</text>`);
      out.push(`<text x="28" y="${y}" fill="${st.text}">${esc(c.name)}</text>`);
      out.push(`<text x="${b.width - 8}" y="${y}" fill="${st.muted}" text-anchor="end" font-size="11">${esc(c.type)}</text>`);
    });
    out.push(`</g>`);
  }
  out.push(`</svg>`);
  return out.join("\n");
}

/** Rasterise an SVG document to a PNG data URL at the given scale. */
export async function svgToPNG(svg: string, scale = 2): Promise<string> {
  const img = new Image();
  const url = URL.createObjectURL(new Blob([svg], { type: "image/svg+xml" }));
  try {
    await new Promise<void>((resolve, reject) => {
      img.onload = () => resolve();
      img.onerror = () => reject(new Error("could not render diagram"));
      img.src = url;
    });
    const canvas = document.createElement("canvas");
    canvas.width = img.width * scale;
    canvas.height = img.height * scale;
    const ctx = canvas.getContext("2d")!;
    ctx.scale(scale, scale);
    ctx.drawImage(img, 0, 0);
    return canvas.toDataURL("image/png");
  } finally {
    URL.revokeObjectURL(url);
  }
}

// ---- data models -------------------------------------------------------------------

/** Minimal shape of a design table (internal/design.Table) used to draw a model. */
export interface DesignTableLike {
  schema?: string;
  name: string;
  comment?: string;
  columns: Array<{ name: string; type: string; length?: string; scale?: string; nullable: boolean }> | null;
  primaryKey?: { columns: string[] | null } | null;
  uniques?: Array<{ columns: string[] | null }> | null;
  foreignKeys: Array<{ name: string; columns: string[] | null; refSchema?: string; refTable: string; refColumns: string[] | null }> | null;
}

/** Table id in a model: schema.name for PostgreSQL, name for MySQL (matches datamodel.Key). */
export const modelKey = (t: { schema?: string; name: string }, dialect: string, defaultSchema?: string) =>
  dialect === "postgres" ? `${t.schema || defaultSchema || "public"}.${t.name}` : t.name;

export function typeLabel(c: { type: string; length?: string; scale?: string }) {
  if (!c.length || c.type === "enum" || c.type === "set") return c.type;
  return `${c.type}(${c.length}${c.scale ? "," + c.scale : ""})`;
}

/** Diagram for data model tables. */
export function fromTables(tables: DesignTableLike[], dialect: string, defaultSchema?: string): ErdModel {
  const byId = new Map<string, ErdTable>();
  for (const t of tables) {
    const pk = t.primaryKey?.columns ?? [];
    const id = modelKey(t, dialect, defaultSchema);
    byId.set(id, {
      id,
      schema: dialect === "postgres" ? t.schema || defaultSchema : undefined,
      name: t.name,
      kind: "table",
      comment: t.comment,
      columns: (t.columns ?? []).map((c) => ({ name: c.name, type: typeLabel(c), pk: pk.includes(c.name), fk: false, nullable: c.nullable })),
    });
  }
  const relations: ErdRelation[] = [];
  for (const t of tables) {
    const from = byId.get(modelKey(t, dialect, defaultSchema))!;
    for (const fk of t.foreignKeys ?? []) {
      const to = byId.get(modelKey({ schema: fk.refSchema || t.schema, name: fk.refTable }, dialect, defaultSchema));
      if (!to) continue;
      const cols = fk.columns ?? [];
      for (const c of from.columns) if (cols.includes(c.name)) c.fk = true;
      const pk = t.primaryKey?.columns ?? [];
      const sameSet = (a: string[]) => a.length === cols.length && a.every((x) => cols.includes(x));
      relations.push({
        id: `${from.id}:${fk.name || cols.join(",")}->${to.id}`,
        from: from.id,
        to: to.id,
        fromColumns: cols,
        toColumns: fk.refColumns ?? [],
        name: fk.name,
        optional: from.columns.some((c) => cols.includes(c.name) && c.nullable),
        oneToOne: (pk.length > 0 && sameSet(pk)) || (t.uniques ?? []).some((u) => sameSet(u.columns ?? [])),
      });
    }
  }
  return { tables: [...byId.values()].sort((a, b) => a.name.localeCompare(b.name)), relations };
}
