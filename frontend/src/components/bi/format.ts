// Number and date formatting for charts: Navicat's number formats (prefix, unit,
// suffix, decimals, separators) and date specifiers (YYYY, MMM, DD…), and
// readable labels for the date buckets Chloe's chart queries group by.
import type { B } from "../../api";

const MONTHS = ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"];
const DAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

const UNITS: [number, string][] = [
  [1e9, "B"],
  [1e6, "M"],
  [1e3, "K"],
];

/** Format a value with a chart's number format. compact shortens big numbers (axes)
 *  even when no unit is set. */
export function formatNumber(x: number | null | undefined, fmt: B.NumberFormat = {}, compact = false): string {
  if (x === null || x === undefined || Number.isNaN(x)) return "";
  let v = x;
  let unit = "";
  const want = fmt.unit || (compact ? "auto" : "");
  if (want === "auto") {
    const u = UNITS.find(([n]) => Math.abs(x) >= n);
    if (u) [v, unit] = [x / u[0], u[1]];
  } else if (want) {
    const u = UNITS.find(([, n]) => n === want);
    if (u) [v, unit] = [x / u[0], u[1]];
  }
  const decimals = fmt.decimals ?? undefined;
  const max = decimals ?? (unit ? 1 : Number.isInteger(v) ? 0 : Math.abs(v) < 1 ? 4 : 2);
  const text = new Intl.NumberFormat("en-US", {
    minimumFractionDigits: decimals ?? 0,
    maximumFractionDigits: max,
    useGrouping: !fmt.noSeparator,
  }).format(Math.abs(v));
  return (v < 0 ? "-" : "") + (fmt.prefix ?? "") + text + unit + (fmt.suffix ?? "");
}

/** A date from a bucket label or a database date/time string. */
export function parseDate(label: string, bucket = ""): Date | null {
  let m: RegExpMatchArray | null;
  switch (bucket) {
    case "year":
      return /^\d{4}$/.test(label) ? new Date(Number(label), 0, 1) : null;
    case "quarter":
      m = label.match(/^(\d{4})-Q([1-4])$/);
      return m ? new Date(Number(m[1]), (Number(m[2]) - 1) * 3, 1) : null;
    case "month":
      m = label.match(/^(\d{4})-(\d{2})$/);
      return m ? new Date(Number(m[1]), Number(m[2]) - 1, 1) : null;
    case "week":
      return null;
  }
  m = label.match(/^(\d{4})-(\d{2})-(\d{2})(?:[ T](\d{2})(?::(\d{2}))?(?::(\d{2}))?)?/);
  if (!m) return null;
  return new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]), Number(m[4] ?? 0), Number(m[5] ?? 0), Number(m[6] ?? 0));
}

const pad = (n: number) => String(n).padStart(2, "0");

/** Week of the year, Sunday first, as strftime's %U. */
function week(d: Date) {
  const start = new Date(d.getFullYear(), 0, 1);
  const yday = Math.round((new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime() - start.getTime()) / 86400000);
  return Math.floor((yday + 7 - d.getDay()) / 7);
}

const TOKENS: [string, (d: Date) => string][] = [
  ["MMMMM", (d) => MONTHS[d.getMonth()][0]],
  ["MMMM", (d) => MONTHS[d.getMonth()]],
  ["MMM", (d) => MONTHS[d.getMonth()].slice(0, 3)],
  ["YYYY", (d) => String(d.getFullYear())],
  ["MM", (d) => pad(d.getMonth() + 1)],
  ["DD", (d) => pad(d.getDate())],
  ["HH", (d) => pad(d.getHours())],
  ["hh", (d) => pad(d.getHours() % 12 || 12)],
  ["mm", (d) => pad(d.getMinutes())],
  ["SS", (d) => pad(d.getSeconds())],
  ["YY", (d) => pad(d.getFullYear() % 100)],
  ["WD", (d) => DAYS[d.getDay()]],
  ["Wd", (d) => DAYS[d.getDay()].slice(0, 3)],
  ["wd", (d) => DAYS[d.getDay()][0]],
  ["WW", (d) => "W" + pad(week(d))],
  ["QQ", (d) => "Q" + (Math.floor(d.getMonth() / 3) + 1)],
  ["M", (d) => String(d.getMonth() + 1)],
  ["D", (d) => String(d.getDate())],
  ["H", (d) => String(d.getHours())],
  ["h", (d) => String(d.getHours() % 12 || 12)],
  ["m", (d) => String(d.getMinutes())],
  ["S", (d) => String(d.getSeconds())],
  ["w", (d) => String(week(d))],
  ["q", (d) => String(Math.floor(d.getMonth() / 3) + 1)],
  ["p", (d) => (d.getHours() < 12 ? "AM" : "PM")],
];

/** Format a date with Navicat's specifiers; other characters are kept. */
export function formatDate(d: Date, spec: string): string {
  let out = "";
  for (let i = 0; i < spec.length; ) {
    const t = TOKENS.find(([k]) => spec.startsWith(k, i));
    if (t) {
      out += t[1](d);
      i += t[0].length;
    } else {
      out += spec[i++];
    }
  }
  return out;
}

const DEFAULT_SPEC: Record<string, string> = {
  year: "YYYY",
  quarter: "QQ YYYY",
  month: "MMM YYYY",
  day: "MMM D, YYYY",
  hour: "MMM D, HH:00",
  minute: "MMM D, HH:mm",
  second: "MMM D, HH:mm:SS",
};

const ACRONYMS: Record<string, string> = { id: "ID", ids: "IDs", url: "URL", api: "API", ui: "UI", ip: "IP", vip: "VIP", utc: "UTC", kpi: "KPI", sku: "SKU", usa: "USA", uk: "UK", eu: "EU", ai: "AI" };

/** Makes an enum-like value readable: ended_early → Ended Early, never started →
 *  Never Started, signedIn → Signed In. Anything else (mixed case, emails, URLs,
 *  paths, numbers) is left as stored. */
export function readable(s: string): string {
  // all lowercase (with _ - or spaces between words), or one camelCase word
  if (!/^[a-z][a-z0-9]*(?:[_\- ][a-z0-9]+)*$/.test(s) && !/^[a-z]+(?:[A-Z][a-z0-9]+)+$/.test(s)) return s;
  return s
    .replace(/([a-z0-9])([A-Z])/g, "$1 $2")
    .split(/[_\- ]+/)
    .map((w) => ACRONYMS[w.toLowerCase()] ?? w.charAt(0).toUpperCase() + w.slice(1))
    .join(" ");
}

export interface LabelOptions {
  /** Every date shown is in the same year, so days and weeks leave it out. */
  sameYear?: boolean;
  /** Show values as stored (the chart's labelCase raw). */
  raw?: boolean;
}

/** Whether date labels of a dimension all fall in one year. */
export function oneYear(values: unknown[], col: Pick<B.Column, "type" | "aggregate">): boolean {
  if (col.type !== "datetime" && !col.aggregate) return false;
  const years = new Set(values.filter((v) => typeof v === "string" && /^\d{4}-/.test(v)).map((v) => (v as string).slice(0, 4)));
  return years.size === 1;
}

/** A dimension or series label as shown: date buckets read naturally
 *  ("Mar 2026", "Q1 2026", "Mon"), or with the chart's date format; enum-like
 *  text readably. */
export function formatLabel(v: unknown, col: Pick<B.Column, "type" | "aggregate">, dateFormat = "", opts: LabelOptions = {}): string {
  if (v === null || v === undefined) return "(null)";
  const s = String(v);
  const bucket = col.aggregate ?? "";
  if (col.type === "number" && !bucket) {
    const x = Number(s);
    return Number.isFinite(x) && s.trim() !== "" ? formatNumber(x, { noSeparator: Math.abs(x) < 10000 && Number.isInteger(x) }) : s;
  }
  switch (bucket) {
    case "week": {
      // A week is the date of its Sunday.
      const d = parseDate(s);
      return d ? (dateFormat ? formatDate(d, dateFormat) : `Week of ${formatDate(d, opts.sameYear ? "MMM D" : "MMM D, YYYY")}`) : s;
    }
    case "quarter_x":
    case "week_x":
      return s;
    case "month_x":
      return MONTHS[Number(s) - 1]?.slice(0, 3) ?? s;
    case "day_x":
      return String(Number(s));
    case "weekday_x":
      return DAYS[Number(s)]?.slice(0, 3) ?? s;
    case "hour_x":
      return `${s}:00`;
    case "minute_x":
    case "second_x":
      return `:${s}`;
  }
  if (col.type !== "datetime" && !bucket) return opts.raw ? s : readable(s);
  const d = parseDate(s, bucket);
  if (!d) return s;
  const spec = dateFormat || (opts.sameYear && bucket === "day" ? "MMM D" : DEFAULT_SPEC[bucket]);
  return spec ? formatDate(d, spec) : s;
}
