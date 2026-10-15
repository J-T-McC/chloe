// Draws a chart: ECharts for the axis and pie types, HTML for Value (a hero
// figure) and Table, each on the workspace theme's surface with the chart's title.
import { BarChart, LineChart, PieChart } from "echarts/charts";
import { GridComponent, LegendComponent, TooltipComponent } from "echarts/components";
import * as echarts from "echarts/core";
import { SVGRenderer } from "echarts/renderers";
import { useEffect, useMemo, useRef } from "react";
import type { B } from "../../api";
import { formatLabel, formatNumber, oneYear } from "./format";
import { buildOption } from "./options";
import { ChartTheme, TOKENS } from "./theme";

echarts.use([BarChart, LineChart, PieChart, GridComponent, LegendComponent, TooltipComponent, SVGRenderer]);

export function ChartView({ chart, data, theme, error, loading, hint, still }: { chart: B.Chart; data: B.Data | null; theme: ChartTheme; error?: string; loading?: boolean; hint?: string; still?: boolean }) {
  const t = TOKENS[theme];
  const title = chart.props?.hideTitle ? "" : chart.props?.title || chart.name;
  let body: React.ReactNode;
  if (error) body = <div className="bi-chart-msg error">{error}</div>;
  else if (!data || !data.complete) body = <div className="bi-chart-msg">{loading ? "Loading…" : (hint ?? "Drag fields onto the shelves to build the chart.")}</div>;
  else if (!(data.rows ?? []).length) body = <div className="bi-chart-msg">No data</div>;
  else if (chart.type === "value") body = <ValueFigure chart={chart} data={data} />;
  else if (chart.type === "table") body = <TableChart chart={chart} data={data} />;
  else body = <EChart chart={chart} data={data} theme={theme} still={still} />;
  return (
    <div className={"bi-chart theme-" + theme} style={{ background: t.surface, color: t.ink, borderColor: t.border }} data-testid="bi-chart">
      {title && <div className="bi-chart-title">{title}</div>}
      <div className="bi-chart-body">{body}</div>
      {data?.truncated && <div className="bi-chart-note">Showing the first 10,000 groups. Use a coarser bucket or Top N.</div>}
    </div>
  );
}

/** still draws without animation (exports capture the finished chart). */
function EChart({ chart, data, theme, still }: { chart: B.Chart; data: B.Data; theme: ChartTheme; still?: boolean }) {
  const host = useRef<HTMLDivElement>(null);
  const inst = useRef<echarts.ECharts | null>(null);
  const option = useMemo(() => ({ ...buildOption(chart, data, theme), ...(still ? { animation: false } : {}) }), [chart, data, theme, still]);
  useEffect(() => {
    if (!host.current) return;
    const c = echarts.init(host.current, null, { renderer: "svg" });
    inst.current = c;
    const ro = new ResizeObserver(() => c.resize());
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
  const text = typeof v === "number" ? formatNumber(v, chart.props?.number ?? {}) : formatLabel(v, col, chart.props?.dateFormat, { raw: chart.props?.labelCase === "raw" });
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
