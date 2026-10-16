// Dashboard interaction: a click on a chart's point filters (or highlights) the
// page's other charts on the same data source, and Control charts filter their
// targets. Both become extra filters on the charts they reach, added when the
// chart is read; the stored charts never change.
import type { B } from "../../api";
import { arr } from "../../arr";

/** A clicked point: the tile, its chart and the dimension value. */
export interface Pick {
  item: string;
  chart: string;
  source: string;
  field: string;
  bucket: string;
  key: unknown;
  label: string;
}

const pad = (n: number) => String(n).padStart(2, "0");
const ymd = (d: Date) => `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}-${pad(d.getUTCDate())}`;

/** The dates a period label covers, as an inclusive [first day, last day]
 *  (whole days, which a between filter on a date/time field includes fully). */
export function periodRange(bucket: string, label: string): [string, string] | null {
  let m: RegExpMatchArray | null;
  switch (bucket) {
    case "year":
      return /^\d{4}$/.test(label) ? [`${label}-01-01`, `${label}-12-31`] : null;
    case "quarter":
      if (!(m = label.match(/^(\d{4})-Q([1-4])$/))) return null;
      return [ymd(new Date(Date.UTC(+m[1], (+m[2] - 1) * 3, 1))), ymd(new Date(Date.UTC(+m[1], +m[2] * 3, 0)))];
    case "month":
      if (!(m = label.match(/^(\d{4})-(\d{2})$/))) return null;
      return [`${label}-01`, ymd(new Date(Date.UTC(+m[1], +m[2], 0)))];
    case "week":
    case "day": {
      if (!(m = label.match(/^(\d{4})-(\d{2})-(\d{2})$/))) return null;
      const start = new Date(Date.UTC(+m[1], +m[2] - 1, +m[3]));
      return [label, bucket === "day" ? label : ymd(new Date(start.getTime() + 6 * 86400000))];
    }
    case "hour":
      if (!(m = label.match(/^(\d{4}-\d{2}-\d{2}) (\d{2})$/))) return null;
      return [`${m[1]} ${m[2]}:00:00`, `${m[1]} ${m[2]}:59:59.999`];
  }
  return null;
}

/** The filter a click on a dimension value stands for; null when it can't be one
 *  (an extracted part like "all Marches", or Others). */
export function pickFilter(p: Pick): B.Filter | null {
  if (p.key === "Others") return null;
  if (p.key === null || p.key === undefined) return { field: p.field, op: "is_null", values: [] };
  if (!p.bucket) return { field: p.field, op: "=", values: [String(p.key)] };
  const r = periodRange(p.bucket, String(p.key));
  return r ? { field: p.field, op: "between", values: r } : null;
}

/** A Control chart's value as a filter on its field; null when it filters nothing. */
export function controlFilter(field: string, style: string, value: string[] | null): B.Filter | null {
  if (!value) return null;
  if (style === "range") {
    const [lo, hi] = value;
    if (lo && hi) return { field, op: "between", values: [lo, hi] };
    if (lo) return { field, op: ">=", values: [lo] };
    if (hi) return { field, op: "<=", values: [hi] };
    return null;
  }
  const picked = value.filter((v) => v !== "");
  if (!picked.length) return null;
  return picked.length === 1 ? { field, op: "=", values: picked } : { field, op: "in", values: picked };
}

/** How a Control chart picks values when its style isn't set: a range for numbers
 *  and dates, a list otherwise. */
export function controlStyle(chart: B.Chart, type?: string): string {
  return chart.props?.control || (type === "number" || type === "datetime" ? "range" : "list");
}

/** What reaches one tile from the page's picks and controls: extra filters, and a
 *  value to highlight. */
export function reach(
  tile: { item: string; chart: B.Chart },
  picks: Pick[],
  controls: { item: string; field: string; filter: B.Filter | null; targets: string[] }[],
  mode: string,
  sourceFields: (id: string) => string[],
): { filters: B.Filter[]; highlight?: unknown } {
  const filters: B.Filter[] = [];
  let highlight: unknown;
  const dim = arr(tile.chart.dimension)[0];
  for (const p of picks) {
    if (p.item === tile.item) {
      highlight = p.key;
      continue;
    }
    if (mode === "none" || p.source !== tile.chart.dataSource) continue;
    if (mode === "highlight") {
      if (dim && dim.field === p.field && (dim.aggregate ?? "") === p.bucket) highlight = p.key;
      continue;
    }
    const f = pickFilter(p);
    if (f) filters.push(f);
  }
  for (const c of controls) {
    if (!c.filter || c.item === tile.item || tile.chart.type === "control") continue;
    const targeted = c.targets.length ? c.targets.includes(tile.chart.id) : sourceFields(tile.chart.dataSource).includes(c.field);
    if (targeted) filters.push(c.filter);
  }
  return { filters, highlight };
}
