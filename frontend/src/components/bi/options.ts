// Turns a chart and its data into an ECharts option. Marks follow fixed specs:
// bars at most 24px with 4px rounded ends, 2px lines, area washes, hairline
// grids, one value axis, a legend only for two or more series.
import type { EChartsCoreOption } from "echarts/core";
import type { B } from "../../api";
import { formatLabel, formatNumber } from "./format";
import { ChartTheme, FONT, OTHERS_COLOR, TOKENS, seriesColors } from "./theme";

export interface Series {
  name: string;
  values: (number | null)[];
  /** a line in a bar and line chart */
  line?: boolean;
}

export interface Pivot {
  categories: string[];
  series: Series[];
}

const num = (v: unknown) => (typeof v === "number" ? v : v === null || v === undefined || v === "" ? null : Number.isFinite(Number(v)) ? Number(v) : null);
const keyOf = (v: unknown) => (v === null || v === undefined ? "\u0000" : String(v));

/** Lay rows out as categories (in the data's order) by series. Split series are
 *  ordered by name, so a series keeps its colour when others come and go. */
export function pivot(chart: B.Chart, data: B.Data): Pivot {
  const cols = data.columns ?? [];
  const rows = data.rows ?? [];
  const dim = cols.findIndex((c) => c.role === "dimension");
  const ser = cols.findIndex((c) => c.role === "series");
  const catKeys: string[] = [];
  const categories: string[] = [];
  const catIndex = new Map<string, number>();
  for (const r of rows) {
    const k = dim >= 0 ? keyOf(r?.[dim]) : "";
    if (!catIndex.has(k)) {
      catIndex.set(k, catKeys.length);
      catKeys.push(k);
      categories.push(dim >= 0 ? formatLabel(r?.[dim], cols[dim], chart.props.dateFormat) : "");
    }
  }
  const series: Series[] = [];
  if (ser >= 0) {
    const val = cols.findIndex((c) => c.role === "value");
    const names = new Map<string, Series>();
    for (const r of rows) {
      const sk = keyOf(r?.[ser]);
      let s = names.get(sk);
      if (!s) {
        s = { name: formatLabel(r?.[ser], cols[ser], chart.props.dateFormat), values: catKeys.map(() => null) };
        names.set(sk, s);
      }
      s.values[catIndex.get(dim >= 0 ? keyOf(r?.[dim]) : "")!] = num(r?.[val]);
    }
    series.push(...[...names.entries()].sort(([a], [b]) => a.localeCompare(b, undefined, { numeric: true })).map(([, s]) => s));
  } else {
    cols.forEach((c, j) => {
      if (c.role !== "value" && c.role !== "secondary") return;
      const s: Series = { name: c.label, values: catKeys.map(() => null), line: c.role === "secondary" };
      for (const r of rows) s.values[catIndex.get(dim >= 0 ? keyOf(r?.[dim]) : "")!] = num(r?.[j]);
      series.push(s);
    });
  }
  return { categories, series };
}

/** Each category's values as shares of 100 %. */
export function toPercent(p: Pivot): Pivot {
  const totals = p.categories.map((_, i) => p.series.reduce((t, s) => t + Math.abs(s.values[i] ?? 0), 0));
  return { ...p, series: p.series.map((s) => ({ ...s, values: s.values.map((v, i) => (v === null || !totals[i] ? v : (v / totals[i]) * 100)) })) };
}

const STACKED = new Set(["stacked_bar", "stacked_hbar", "stacked_area", "stacked_bar_line"]);

export function buildOption(chart: B.Chart, data: B.Data, theme: ChartTheme): EChartsCoreOption {
  const t = TOKENS[theme];
  const props = chart.props ?? {};
  const fmt = props.number ?? {};
  const pct = !!props.stack100 && STACKED.has(chart.type);
  let p = pivot(chart, data);
  if (pct) p = toPercent(p);
  const colors = seriesColors(theme, props.palette, props.colors);
  const valueText = (v: number | null) => (pct ? formatNumber(v, { decimals: 1, suffix: "%" }) : formatNumber(v, fmt));
  const legendShown = props.legend !== "none" && (chart.type === "pie" || chart.type === "donut" || p.series.length > 1);
  const legendPos = props.legend && props.legend !== "auto" ? props.legend : "top";
  const legend = legendShown
    ? {
        type: "scroll",
        icon: "roundRect",
        itemWidth: 10,
        itemHeight: 10,
        itemGap: 14,
        textStyle: { color: t.ink2, fontSize: 12 },
        pageTextStyle: { color: t.muted },
        ...(legendPos === "right" ? { orient: "vertical", right: 8, top: "middle" } : legendPos === "bottom" ? { bottom: 4, left: "center" } : { top: 0, left: 0 }),
      }
    : { show: false };
  const tooltip = {
    backgroundColor: t.surface,
    borderColor: t.border,
    borderWidth: 1,
    padding: [6, 10],
    textStyle: { color: t.ink, fontSize: 12, fontFamily: FONT },
    extraCssText: "box-shadow: 0 4px 16px rgba(0,0,0,0.12); border-radius: 6px;",
    valueFormatter: (v: number | null) => valueText(v),
  };
  const base = { backgroundColor: "transparent", textStyle: { fontFamily: FONT }, color: colors, animationDuration: 400 };

  if (chart.type === "pie" || chart.type === "donut") {
    const s = p.series[0] ?? { name: "", values: [] };
    const donut = chart.type === "donut";
    return {
      ...base,
      legend,
      tooltip: { ...tooltip, trigger: "item" },
      series: [
        {
          type: "pie",
          name: s.name,
          radius: donut ? ["52%", "76%"] : ["0%", "76%"],
          center: ["50%", legendShown && legendPos === "top" ? "56%" : "50%"],
          avoidLabelOverlap: true,
          itemStyle: { borderColor: t.surface, borderWidth: 2, borderRadius: donut ? 4 : 0 },
          label: {
            show: !!props.labels,
            color: t.ink2,
            fontSize: 12,
            formatter: (x: { name: string; value: number; percent: number }) => (props.labelPercent ? `${x.name}: ${x.percent.toFixed(1)}%` : `${x.name}: ${valueText(x.value)}`),
          },
          labelLine: { lineStyle: { color: t.axis } },
          data: p.categories.map((name, i) => ({
            name,
            value: s.values[i],
            ...(name === "Others" ? { itemStyle: { color: OTHERS_COLOR[theme] } } : {}),
          })),
        },
      ],
    };
  }

  const horizontal = chart.type === "hbar" || chart.type === "stacked_hbar";
  const stacked = STACKED.has(chart.type);
  const isLine = chart.type === "line" || chart.type === "area" || chart.type === "stacked_area";
  const area = chart.type === "area" || chart.type === "stacked_area";
  const categoryAxis = {
    type: "category",
    data: p.categories,
    name: horizontal ? props.yTitle : props.xTitle,
    nameLocation: "middle",
    nameGap: horizontal ? 60 : 28,
    nameTextStyle: { color: t.ink2 },
    axisLine: { lineStyle: { color: t.axis } },
    axisTick: { show: false },
    // Lines run edge to edge, so the first and last labels align inward rather than being cut off.
    axisLabel: { color: t.muted, fontSize: 11, hideOverlap: true, ...(isLine && !horizontal ? { alignMinLabel: "left", alignMaxLabel: "right" } : {}) },
    boundaryGap: !isLine,
  };
  const valueAxis = {
    type: "value",
    name: horizontal ? props.xTitle : props.yTitle,
    nameLocation: "middle",
    nameGap: 44,
    nameTextStyle: { color: t.ink2 },
    max: pct ? 100 : undefined,
    axisLine: { show: false },
    axisTick: { show: false },
    splitLine: { lineStyle: { color: t.grid, width: 1 } },
    axisLabel: { color: t.muted, fontSize: 11, formatter: (v: number) => (pct ? `${v}%` : formatNumber(v, { ...fmt, decimals: undefined }, true)) },
  };
  const few = p.categories.length <= 24;
  const series = p.series.map((s) => {
    const line = isLine || s.line;
    const others = s.name === "Others";
    const common = {
      name: s.name,
      data: s.values,
      ...(others ? { itemStyle: { color: OTHERS_COLOR[theme] } } : {}),
      emphasis: { focus: "series" },
      label: {
        show: !!props.labels,
        color: t.ink2,
        fontSize: 11,
        position: line ? "top" : stacked ? "inside" : horizontal ? "right" : "top",
        formatter: (x: { value: number | null }) => valueText(x.value),
      },
    };
    if (line) {
      return {
        ...common,
        type: "line",
        stack: stacked && isLine ? "total" : undefined,
        smooth: !!props.smooth,
        lineStyle: { width: 2, cap: "round", join: "round" },
        symbol: "circle",
        symbolSize: 8,
        showSymbol: few,
        itemStyle: { ...(common.itemStyle ?? {}), borderColor: t.surface, borderWidth: 2 },
        areaStyle: area ? { opacity: stacked ? 0.35 : 0.1 } : undefined,
        z: 3,
      };
    }
    const radius = stacked ? 0 : horizontal ? [0, 4, 4, 0] : [4, 4, 0, 0];
    return {
      ...common,
      type: "bar",
      stack: stacked ? "total" : undefined,
      barMaxWidth: 24,
      barGap: "12%",
      itemStyle: { ...(common.itemStyle ?? {}), borderRadius: radius, ...(stacked ? { borderColor: t.surface, borderWidth: 1 } : {}) },
      z: 2,
    };
  });
  return {
    ...base,
    legend,
    tooltip: { ...tooltip, trigger: "axis", axisPointer: isLine ? { type: "line", lineStyle: { color: t.axis } } : { type: "shadow", shadowStyle: { color: theme === "dark" ? "rgba(255,255,255,0.04)" : "rgba(0,0,0,0.04)" } } },
    grid: { left: 8, right: legendShown && legendPos === "right" ? 140 : props.labels && (isLine || horizontal) ? 40 : 16, top: legendShown && legendPos === "top" ? 36 : props.labels ? 22 : 12, bottom: (legendShown && legendPos === "bottom" ? 32 : 4) + (props.xTitle && !horizontal ? 24 : 0), containLabel: true },
    xAxis: horizontal ? valueAxis : categoryAxis,
    yAxis: horizontal ? { ...categoryAxis, inverse: true } : valueAxis,
    series,
  };
}
