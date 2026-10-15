// Draws a chart: ECharts for the axis, pie, treemap, scatter, heatmap and gauge
// types, HTML for the figures (Value, KPI, Trend) and tables (Table, Pivot Table),
// each on the workspace theme's surface with the chart's title.
import { BarChart, CustomChart, GaugeChart, HeatmapChart, LineChart, PieChart, ScatterChart, TreemapChart } from "echarts/charts";
import { GridComponent, LegendComponent, TooltipComponent, VisualMapComponent } from "echarts/components";
import * as echarts from "echarts/core";
import { SVGRenderer } from "echarts/renderers";
import { useEffect, useMemo, useRef, useState } from "react";
import type { B } from "../../api";
import { formatLabel, formatNumber, oneYear } from "./format";
import { controlStyle } from "./interaction";
import { buildOption, pickKey, pivot } from "./options";
import { expandParams } from "./Parameters";
import { ChartTheme, METER, STATUS, TOKENS } from "./theme";

echarts.use([BarChart, LineChart, PieChart, TreemapChart, ScatterChart, HeatmapChart, GaugeChart, CustomChart, GridComponent, LegendComponent, TooltipComponent, VisualMapComponent, SVGRenderer]);

export interface ChartInteraction {
  /** The dimension value to stand out (the rest are dimmed). */
  selected?: unknown;
  /** A click on a point: its dimension value (undefined clears). */
  onPick?(key: unknown, label: string): void;
  /** A Control chart's current value and its changes. */
  control?: { value: string[] | null; onChange(v: string[] | null): void };
}

export function ChartView({
  chart,
  data,
  theme,
  error,
  loading,
  hint,
  still,
  interaction,
}: {
  chart: B.Chart;
  data: B.Data | null;
  theme: ChartTheme;
  error?: string;
  loading?: boolean;
  hint?: string;
  still?: boolean;
  interaction?: ChartInteraction;
}) {
  const t = TOKENS[theme];
  const title = chart.props?.hideTitle ? "" : expandParams(chart.props?.title || chart.name, data?.params);
  let body: React.ReactNode;
  if (error) body = <div className="bi-chart-msg error">{error}</div>;
  else if (!data || !data.complete) body = <div className="bi-chart-msg">{loading ? "Loading…" : (hint ?? "Drag fields onto the shelves to build the chart.")}</div>;
  else if (!(data.rows ?? []).length) body = <div className="bi-chart-msg">No data</div>;
  else if (chart.type === "value") body = <ValueFigure chart={chart} data={data} />;
  else if (chart.type === "kpi") body = <KPIFigure chart={chart} data={data} theme={theme} />;
  else if (chart.type === "trend") body = <TrendFigure chart={chart} data={data} theme={theme} />;
  else if (chart.type === "table") body = <TableChart chart={chart} data={data} />;
  else if (chart.type === "pivot") body = <PivotTable chart={chart} data={data} />;
  else if (chart.type === "control") body = <ControlView chart={chart} data={data} control={interaction?.control} />;
  else if (chart.type === "tornado" && pivot(chart, data).series.length !== 2)
    body = <div className="bi-chart-msg">A Tornado chart compares exactly two series values; this one has {pivot(chart, data).series.length}. Filter the series field to two values.</div>;
  else body = <EChart chart={chart} data={data} theme={theme} still={still} selected={interaction?.selected} onPick={interaction?.onPick} />;
  return (
    <div className={"bi-chart theme-" + theme} style={{ background: t.surface, color: t.ink, borderColor: t.border }} data-testid="bi-chart">
      {title && <div className="bi-chart-title">{title}</div>}
      <div className="bi-chart-body">{body}</div>
      {data?.truncated && <div className="bi-chart-note">Showing the first 10,000 groups. Use a coarser bucket or Top N.</div>}
    </div>
  );
}

/** still draws without animation (exports capture the finished chart). */
function EChart({ chart, data, theme, still, selected, onPick }: { chart: B.Chart; data: B.Data; theme: ChartTheme; still?: boolean; selected?: unknown; onPick?(key: unknown, label: string): void }) {
  const host = useRef<HTMLDivElement>(null);
  const inst = useRef<echarts.ECharts | null>(null);
  // The tile's height, so a gauge's text fits small tiles.
  const [size, setSize] = useState({ w: 0, h: 0 });
  const option = useMemo(() => fitGauge({ ...buildOption(chart, data, theme, selected), ...(still ? { animation: false } : {}) }, chart.type, size.w, size.h), [chart, data, theme, still, selected, size]);
  // The click handler reads the latest props, so it's registered once.
  const pick = useRef({ chart, data, onPick });
  pick.current = { chart, data, onPick };
  useEffect(() => {
    if (!host.current) return;
    const c = echarts.init(host.current, null, { renderer: "svg" });
    inst.current = c;
    c.on("click", (ev: { dataIndex?: number; value?: unknown; data?: unknown; name?: string }) => {
      const { chart, data, onPick } = pick.current;
      if (!onPick) return;
      const key = pickKey(chart, data, ev);
      if (key !== undefined) onPick(key, ev.name ?? String(key));
    });
    const ro = new ResizeObserver(() => {
      c.resize();
      setSize({ w: host.current?.clientWidth ?? 0, h: host.current?.clientHeight ?? 0 });
    });
    ro.observe(host.current);
    return () => {
      ro.disconnect();
      c.dispose();
      inst.current = null;
    };
  }, []);
  useEffect(() => {
    inst.current?.setOption(option, { notMerge: true });
  }, [option]);
  return <div ref={host} className="bi-echart" />;
}

function ValueFigure({ chart, data }: { chart: B.Chart; data: B.Data }) {
  const col = (data.columns ?? [])[0];
  const v = (data.rows ?? [])[0]?.[0];
  // No rows for the aggregate (e.g. a filter that matches nothing) reads as a dash.
  const text = v === null || v === undefined ? "—" : typeof v === "number" ? formatNumber(v, chart.props?.number ?? {}) : formatLabel(v, col, chart.props?.dateFormat, { raw: chart.props?.labelCase === "raw" });
  return (
    <div className="bi-value">
      <div className="bi-value-figure" data-testid="bi-value">
        {text}
      </div>
      <div className="bi-value-label">{col?.label}</div>
    </div>
  );
}

function TableChart({ chart, data }: { chart: B.Chart; data: B.Data }) {
  const cols = data.columns ?? [];
  const rows = data.rows ?? [];
  const fmt = chart.props?.number ?? {};
  const opts = cols.map((c, j) => ({ raw: chart.props?.labelCase === "raw", sameYear: oneYear(rows.map((r) => r?.[j]), c) }));
  const cell = (v: unknown, c: B.Column, j: number) => (typeof v === "number" ? formatNumber(v, fmt) : formatLabel(v, c, chart.props?.dateFormat, opts[j]));
  // Totals for aggregated numeric columns.
  const totals = cols.map((c, j) => (c.type === "number" && c.aggregate && c.aggregate !== "min" && c.aggregate !== "max" && c.aggregate !== "avg" && c.aggregate !== "median" ? rows.reduce((t, r) => t + (typeof r?.[j] === "number" ? (r[j] as number) : 0), 0) : null));
  return (
    <div className="bi-table-wrap">
      <table className="bi-table">
        <thead>
          <tr>
            {cols.map((c) => (
              <th key={c.key} className={c.type === "number" ? "num" : ""}>
                {c.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={i}>
              {cols.map((c, j) => (
                <td key={c.key} className={typeof r?.[j] === "number" ? "num" : r?.[j] === null ? "null" : ""}>
                  {cell(r?.[j], c, j)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
        {totals.some((x) => x !== null) && (
          <tfoot>
            <tr>
              {cols.map((c, j) => (
                <td key={c.key} className="num">
                  {j === 0 && totals[0] === null ? "Total" : totals[j] === null ? "" : formatNumber(totals[j], fmt)}
                </td>
              ))}
            </tr>
          </tfoot>
        )}
      </table>
    </div>
  );
}

/** A change shown with its arrow, sign and status colour (never colour alone). */
function Delta({ change, base, lowerIsBetter, fmt, vs }: { change: number; base: number | null; lowerIsBetter?: boolean; fmt: B.NumberFormat; vs: string }) {
  const good = change === 0 ? null : (change > 0) !== !!lowerIsBetter;
  const pct = base ? (change / Math.abs(base)) * 100 : null;
  const arrow = change > 0 ? "▲" : change < 0 ? "▼" : "■";
  return (
    <div className="bi-delta" data-testid="bi-delta">
      <span style={{ color: good === null ? undefined : good ? STATUS.good : STATUS.critical }}>
        {arrow} {change > 0 ? "+" : ""}
        {formatNumber(change, fmt)}
        {pct !== null && ` (${pct > 0 ? "+" : ""}${pct.toFixed(1)}%)`}
      </span>{" "}
      <span className="bi-delta-vs">{vs}</span>
    </div>
  );
}

function KPIFigure({ chart, data, theme }: { chart: B.Chart; data: B.Data; theme: ChartTheme }) {
  const cols = data.columns ?? [];
  const row = (data.rows ?? [])[0] ?? [];
  const fmt = chart.props?.number ?? {};
  const vi = cols.findIndex((c) => c.role === "value");
  const ti = cols.findIndex((c) => c.role === "target");
  const v = typeof row[vi] === "number" ? (row[vi] as number) : null;
  const target = ti >= 0 && typeof row[ti] === "number" ? (row[ti] as number) : (chart.props?.goal ?? null);
  const share = v !== null && target ? Math.max(0, Math.min(1, v / target)) : null;
  const m = METER[theme];
  return (
    <div className="bi-value">
      <div className="bi-value-figure" data-testid="bi-value">
        {v === null ? "—" : formatNumber(v, fmt)}
      </div>
      <div className="bi-value-label">{cols[vi]?.label}</div>
      {target !== null && v !== null && (
        <div className="bi-kpi">
          <div className="bi-meter" style={{ background: m.track }} role="meter" aria-valuenow={v} aria-valuemax={target} aria-label="Progress to target">
            <div className="bi-meter-fill" style={{ width: `${(share ?? 0) * 100}%`, background: m.fill }} />
          </div>
          <div className="bi-kpi-row">
            <span>{share !== null ? `${(Math.round((v / target) * 1000) / 10).toLocaleString()}% of target` : ""}</span>
            <span className="bi-delta-vs">Target {formatNumber(target, fmt)}</span>
          </div>
          <Delta change={v - target} base={null} lowerIsBetter={chart.props?.lowerIsBetter} fmt={fmt} vs="vs target" />
        </div>
      )}
    </div>
  );
}

function TrendFigure({ chart, data, theme }: { chart: B.Chart; data: B.Data; theme: ChartTheme }) {
  const cols = data.columns ?? [];
  const rows = data.rows ?? [];
  const fmt = chart.props?.number ?? {};
  const di = cols.findIndex((c) => c.role === "dimension");
  const vi = cols.findIndex((c) => c.role === "value");
  const points = rows.map((r) => ({ label: r?.[di], v: typeof r?.[vi] === "number" ? (r[vi] as number) : null }));
  const last = points[points.length - 1];
  const prev = points[points.length - 2];
  const label = (x: unknown) => formatLabel(x, cols[di], chart.props?.dateFormat, { sameYear: oneYear(points.map((p) => p.label), cols[di]) });
  // A period still going compares with the same days of the one before (sameDays).
  const same = data.previous;
  const base = same ? (typeof same.value === "number" ? same.value : null) : (prev?.v ?? null);
  const day = (d: string) => formatLabel(d, { type: "datetime", aggregate: "day" }, "MMM D");
  const vs = same ? `vs ${day(same.from)}–${day(same.to)}` : `vs ${label(prev?.label)}`;
  const t = TOKENS[theme];
  // A sparkline of the last twelve periods, the current one marked.
  const spark = points.slice(-12);
  const vals = spark.map((p) => p.v ?? 0);
  const lo = Math.min(...vals);
  const hi = Math.max(...vals);
  const W = 160;
  const H = 36;
  const xy = (i: number, v: number) => [spark.length > 1 ? (i / (spark.length - 1)) * (W - 8) + 4 : W / 2, H - 4 - (hi === lo ? 0.5 : (v - lo) / (hi - lo)) * (H - 8)];
  const path = vals.map((v, i) => (i ? "L" : "M") + xy(i, v).map((n) => n.toFixed(1)).join(" ")).join(" ");
  const end = xy(vals.length - 1, vals[vals.length - 1] ?? 0);
  return (
    <div className="bi-value">
      <div className="bi-value-figure" data-testid="bi-value">
        {formatNumber(last?.v ?? null, fmt)}
      </div>
      <div className="bi-value-label">
        {cols[vi]?.label} · {label(last?.label)}
      </div>
      {base !== null && last?.v !== null && <Delta change={(last?.v ?? 0) - base} base={base} lowerIsBetter={chart.props?.lowerIsBetter} fmt={fmt} vs={vs} />}
      {spark.length > 1 && (
        <svg className="bi-spark" width={W} height={H} viewBox={`0 0 ${W} ${H}`} aria-hidden>
          <path d={path} fill="none" stroke={t.axis} strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" />
          <circle cx={end[0]} cy={end[1]} r={4} fill={METER[theme].fill} stroke={t.surface} strokeWidth={2} />
        </svg>
      )}
    </div>
  );
}

/** Rows by the dimension, columns by the series (or by value field), with
 *  totals for sums and counts. */
function PivotTable({ chart, data }: { chart: B.Chart; data: B.Data }) {
  const cols = data.columns ?? [];
  const p = pivot(chart, data);
  const fmt = chart.props?.number ?? {};
  const dim = cols.find((c) => c.role === "dimension");
  const split = cols.some((c) => c.role === "series");
  const measures = cols.filter((c) => c.role === "value");
  const additive = (agg?: string) => agg === "sum" || agg === "count";
  const colTotals = p.series.map((s, j) => (additive(split ? measures[0]?.aggregate : measures[j]?.aggregate) ? s.values.reduce<number>((t, v) => t + (v ?? 0), 0) : null));
  const rowTotals = split && additive(measures[0]?.aggregate);
  return (
    <div className="bi-table-wrap">
      <table className="bi-table bi-pivot">
        <thead>
          <tr>
            <th>{dim?.label}</th>
            {p.series.map((s) => (
              <th key={s.name} className="num">
                {s.name}
              </th>
            ))}
            {rowTotals && <th className="num">Total</th>}
          </tr>
        </thead>
        <tbody>
          {p.categories.map((c, i) => (
            <tr key={i}>
              <td>{c}</td>
              {p.series.map((s) => (
                <td key={s.name} className={s.values[i] === null ? "null" : "num"}>
                  {formatNumber(s.values[i], fmt)}
                </td>
              ))}
              {rowTotals && <td className="num total">{formatNumber(p.series.reduce((t, s) => t + (s.values[i] ?? 0), 0), fmt)}</td>}
            </tr>
          ))}
        </tbody>
        {colTotals.some((x) => x !== null) && (
          <tfoot>
            <tr>
              <td>Total</td>
              {colTotals.map((x, j) => (
                <td key={j} className="num">
                  {x === null ? "" : formatNumber(x, fmt)}
                </td>
              ))}
              {rowTotals && <td className="num">{formatNumber(colTotals.reduce<number>((t, x) => t + (x ?? 0), 0), fmt)}</td>}
            </tr>
          </tfoot>
        )}
      </table>
    </div>
  );
}

/** A Control chart: picks values of its field to filter other charts on a
 *  dashboard. A list (several), a combo box (one) or a range (numbers and dates). */
function ControlView({ chart, data, control }: { chart: B.Chart; data: B.Data; control?: ChartInteraction["control"] }) {
  const col = (data.columns ?? [])[0];
  const values = (data.rows ?? []).map((r) => r?.[0]).filter((v) => v !== null && v !== undefined);
  const style = controlStyle(chart, col?.type);
  const value = control?.value ?? null;
  const set = (v: string[] | null) => control?.onChange(v);
  const label = (v: unknown) => formatLabel(v, col, chart.props?.dateFormat, { raw: chart.props?.labelCase === "raw" });
  if (style === "range") {
    const nums = col?.type === "number";
    const lo = String(values[0] ?? "");
    const hi = String(values[values.length - 1] ?? "");
    const input = (i: 0 | 1) => (
      <input
        aria-label={i ? "Control to" : "Control from"}
        type={nums ? "number" : "date"}
        value={value?.[i] ?? ""}
        placeholder={i ? hi : lo}
        min={nums ? lo : lo.slice(0, 10)}
        max={nums ? hi : hi.slice(0, 10)}
        onChange={(e) => {
          const next: string[] = [value?.[0] ?? "", value?.[1] ?? ""];
          next[i] = e.target.value;
          set(next[0] || next[1] ? next : null);
        }}
      />
    );
    return (
      <div className="bi-control range" data-testid="bi-control">
        {input(0)}
        <span className="muted">to</span>
        {input(1)}
        {value && (
          <button className="small-btn" onClick={() => set(null)}>
            Clear
          </button>
        )}
      </div>
    );
  }
  if (style === "combo")
    return (
      <div className="bi-control" data-testid="bi-control">
        <select aria-label={`Control ${col?.label ?? ""}`} value={value?.[0] ?? ""} onChange={(e) => set(e.target.value ? [e.target.value] : null)}>
          <option value="">All</option>
          {values.map((v) => (
            <option key={String(v)} value={String(v)}>
              {label(v)}
            </option>
          ))}
        </select>
      </div>
    );
  return (
    <div className="bi-control list" data-testid="bi-control">
      {values.map((v) => {
        const k = String(v);
        const on = !!value?.includes(k);
        return (
          <label key={k} className="check">
            <input
              type="checkbox"
              checked={on}
              onChange={() => {
                const next = on ? (value ?? []).filter((x) => x !== k) : [...(value ?? []), k];
                set(next.length ? next : null);
              }}
            />
            {label(v)}
          </label>
        );
      })}
    </div>
  );
}

/** Scale a gauge's figure, title, ticks and arc to the space it has. */
function fitGauge<T extends object>(option: T, type: string, w: number, h: number): T {
  if (type !== "gauge" || !h) return option;
  const o = option as unknown as { series: Record<string, Record<string, unknown>>[] };
  const g = o.series?.[0];
  if (!g) return option;
  const clamp = (x: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, x));
  const width = clamp(h * 0.06, 6, 14);
  return {
    ...option,
    series: [
      {
        ...g,
        progress: { ...g.progress, width },
        axisLine: { ...g.axisLine, lineStyle: { ...(g.axisLine?.lineStyle as object), width } },
        // The figure sits inside the arc, whose width follows the smaller side.
        detail: { ...g.detail, fontSize: clamp(Math.min(h * 0.13, Math.min(w, h * 1.4) * 0.075), 12, 32) },
        title: { ...g.title, show: h >= 140, fontSize: clamp(h * 0.05, 10, 12) },
        axisLabel: { ...g.axisLabel, show: h >= 110 },
      },
    ],
  };
}
