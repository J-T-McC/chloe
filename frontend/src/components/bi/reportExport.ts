// HTML export: one self-contained file holding the report viewer (built into
// /report by vite.report.config.ts), a dashboard's pages and every chart's data
// as read now. It opens in any browser, offline; nothing in it can reach a
// database: no connection, query or SQL goes into the file.
import { B, BI, arr } from "../../api";
import { PAYLOAD_ID, ReportPayload } from "../../report/payload";
import { escapeHtml } from "./format";
import { ParamValues, dashboardParams, paramValues } from "./Parameters";

export interface ReportOptions {
  /** Pages to include (0-based); default all. */
  pages?: number[];
  overrides?: ParamValues;
  /** Read data at or after this time (ms); 0 takes recent data. */
  since?: number;
  /** Told as each chart's data arrives. */
  progress?(done: number, total: number): void;
  cancelled?(): boolean;
}

let viewer: Promise<{ js: string; css: string }> | null = null;

/** The viewer's script and stylesheet, as the app serves them. */
function viewerAssets() {
  viewer ??= Promise.all(
    ["report/report.js", "report/report.css"].map(async (f) => {
      const res = await fetch(new URL(f, document.baseURI));
      if (!res.ok) throw new Error(`the report viewer is missing (${f}: ${res.status}); rebuild Chloe`);
      return res.text();
    }),
  )
    .then(([js, css]) => ({ js, css }))
    .catch((e) => {
      viewer = null;
      throw e;
    });
  return viewer;
}


/** Build the HTML report of a dashboard (saved, by id, or a draft). */
export async function buildReport(path: string, dashboard: B.Dashboard | string, opt: ReportOptions = {}): Promise<string> {
  const assets = viewerAssets();
  const w = await BI.Open(path);
  const d = typeof dashboard === "string" ? arr(w.dashboards).find((x) => x.id === dashboard) : dashboard;
  if (!d) throw new Error("the dashboard is no longer in the workspace");
  const pages = (opt.pages ?? arr(d.pages).map((_, i) => i)).map((i) => arr(d.pages)[i]).filter(Boolean);
  const ids = new Set(pages.flatMap((p) => arr(p.items).flatMap((it) => (it.kind === "chart" && it.chart ? [it.chart] : []))));
  const charts = arr(w.charts).filter((c) => ids.has(c.id));
  const overrides = opt.overrides ?? {};
  const params = Object.keys(overrides).length ? overrides : null;

  const data: ReportPayload["data"] = {};
  let done = 0;
  await Promise.all(
    charts.map(async (c) => {
      if (opt.cancelled?.()) throw new Error("cancelled");
      try {
        // The SQL stays behind: a report shows results, not how they were read.
        const { sql: _sql, ...rest } = await BI.ChartData(path, c, params, opt.since ?? 0);
        data[c.id] = rest as B.Data;
      } catch {
        // Driver errors can name hosts, databases and SQL, which a report leaves out.
        data[c.id] = { error: "This chart couldn't be read when the report was exported." };
      }
      opt.progress?.(++done, charts.length);
    }),
  );
  if (opt.cancelled?.()) throw new Error("cancelled");

  const values = paramValues(w.parameters, overrides);
  const shown = dashboardParams(w, { ...d, pages }).map((p) => {
    const v = values[p.name.toLowerCase()] ?? p.value;
    return { name: p.name, value: arr(p.list).find((x) => x.value === v)?.label || v };
  });
  const read = Object.values(data).flatMap((x) => ("ranAt" in x && x.ranAt ? [x.ranAt] : []));
  const exported = new Date().toISOString();
  const used = new Set(charts.map((c) => c.dataSource));
  const payload: ReportPayload = {
    version: 1,
    name: d.name,
    workspace: w.name,
    theme: w.theme === "dark" ? "dark" : "light",
    exported,
    asOf: read.length ? read.reduce((a, b) => (a < b ? a : b)) : exported,
    interaction: d.interaction ?? "",
    pages,
    charts,
    // Controls need to know a source's fields; nothing else about it goes in.
    sources: arr(w.dataSources)
      .filter((s) => used.has(s.id))
      .map((s) => ({ id: s.id, name: s.name, fields: s.fields, customFields: s.customFields, projection: s.projection }) as B.DataSource),
    values,
    params: shown,
    data,
  };
  const { js, css } = await assets;
  // Nothing in the JSON or the inlined code may close its element early.
  const json = JSON.stringify(payload).replace(/</g, "\\u003c");
  const script = js.replace(/<\/script/gi, "<\\/script");
  // Only the report's own script runs and nothing loads from the network, so text
  // from the data can't run code or send anything anywhere.
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", new TextEncoder().encode(script)));
  const csp = `default-src 'none'; script-src 'sha256-${btoa(String.fromCharCode(...digest))}'; style-src 'unsafe-inline'; img-src data: blob:; font-src data:`;
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="generator" content="Chloe">
<meta http-equiv="Content-Security-Policy" content="${csp}">
<title>${escapeHtml(d.name)}</title>
<style>${css.replace(/<\/style/gi, "<\\/style")}</style>
</head>
<body>
<div id="root"></div>
<noscript>This report needs JavaScript to draw its charts.</noscript>
<script type="application/json" id="${PAYLOAD_ID}">${json}</script>
<script>${script}</script>
</body>
</html>
`;
}
