// Turns a chart and its data into an ECharts option. Marks follow fixed specs:
// bars at most 24px with 4px rounded ends, 2px lines, area washes, hairline
// grids, one value axis, a legend only for two or more series.
import type { EChartsCoreOption } from "echarts/core";
import type { B } from "../../api";
import { escapeHtml as esc, formatLabel, formatNumber, oneYear } from "./format";
import { ChartTheme, FONT, METER, OTHERS_COLOR, POLARITY, SEQUENTIAL, TOKENS, inkOn, seriesColors } from "./theme";

export interface Series {
  name: string;
  values: (number | null)[];
  /** a line in a bar and line chart */
  line?: boolean;
}

export interface Pivot {
  categories: string[];
  /** The dimension's raw value for each category (what a click selects). */
  keys: unknown[];
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
  const keys: unknown[] = [];
  const categories: string[] = [];
  const catIndex = new Map<string, number>();
  const raw = chart.props?.labelCase === "raw";
  const dimOpts = { raw, sameYear: dim >= 0 && oneYear(rows.map((r) => r?.[dim]), cols[dim]) };
  const serOpts = { raw, sameYear: ser >= 0 && oneYear(rows.map((r) => r?.[ser]), cols[ser]) };
  for (const r of rows) {
    const k = dim >= 0 ? keyOf(r?.[dim]) : "";
    if (!catIndex.has(k)) {
      catIndex.set(k, catKeys.length);
      catKeys.push(k);
      keys.push(dim >= 0 ? (r?.[dim] ?? null) : null);
      categories.push(dim >= 0 ? formatLabel(r?.[dim], cols[dim], chart.props.dateFormat, dimOpts) : "");
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
        s = { name: formatLabel(r?.[ser], cols[ser], chart.props.dateFormat, serOpts), values: catKeys.map(() => null) };
        names.set(sk, s);
      }
      s.values[catIndex.get(dim >= 0 ? keyOf(r?.[dim]) : "")!] = num(r?.[val]);
    }
    // Split series keep the order the data has for date parts (Sun…Sat) and
    // custom-sorted fields; otherwise they go by name, so a series keeps its
    // colour when others come and go.
    const order = cols[ser].order ?? [];
    const entries = [...names.entries()];
    if (order.length) {
      const at = (k: string) => (order.indexOf(k) < 0 ? order.length : order.indexOf(k));
      entries.sort(([a], [b]) => at(a) - at(b) || a.localeCompare(b, undefined, { numeric: true, sensitivity: "base" }));
    } else if (!cols[ser].aggregate) {
      entries.sort(([a], [b]) => a.localeCompare(b, undefined, { numeric: true, sensitivity: "base" }));
    }
    series.push(...entries.map(([, s]) => s));
  } else {
    cols.forEach((c, j) => {
      if (c.role !== "value" && c.role !== "secondary") return;
      const s: Series = { name: c.label, values: catKeys.map(() => null), line: c.role === "secondary" };
      for (const r of rows) s.values[catIndex.get(dim >= 0 ? keyOf(r?.[dim]) : "")!] = num(r?.[j]);
      series.push(s);
    });
  }
  return { categories, keys, series };
}

/** The dimension value a click on a chart's mark stands for; undefined when the
 *  mark isn't one category (a waterfall's Total, a scatter without points). */
export function pickKey(chart: B.Chart, data: B.Data, ev: { dataIndex?: number; value?: unknown; data?: unknown }): unknown {
  const p = pivot(chart, data);
  if (chart.type === "scatter") return (ev.data as { raw?: unknown } | undefined)?.raw;
  const i = chart.type === "heatmap" && Array.isArray(ev.value) ? Number(ev.value[0]) : ev.dataIndex;
  if (i === undefined || i < 0 || i >= p.keys.length) return undefined;
  return p.keys[i];
}

/** Dim every category but the selected one (Highlight interaction). */
function dimOthers(option: Record<string, unknown>, p: Pivot, chart: B.Chart, selected: unknown) {
  const at = p.keys.findIndex((k) => keyOf(k) === keyOf(selected));
  if (at < 0) return option;
  const dim = (i: number, v: unknown) => (i === at ? v : { ...(typeof v === "object" && v !== null && !Array.isArray(v) ? v : { value: v }), itemStyle: { ...((v as { itemStyle?: object })?.itemStyle ?? {}), opacity: 0.25 } });
  const series = (option.series as Record<string, unknown>[] | undefined) ?? [];
  option.series = series.map((s) => {
    if (!Array.isArray(s.data) || ["custom", "gauge", "heatmap"].includes(String(s.type)) || chart.type === "scatter") return s;
    return { ...s, data: (s.data as unknown[]).map((v, i) => dim(i, v)) };
  });
  return option;
}

/** Scatter axes leave a little room around the points, but never go below zero
 *  for values that aren't negative. */
const padMin = (v: { min: number; max: number }) => {
  const pad = (v.max - v.min || Math.abs(v.max) || 1) * 0.1;
  return v.min >= 0 ? Math.max(0, v.min - pad) : v.min - pad;
};
const padMax = (v: { min: number; max: number }) => v.max + (v.max - v.min || Math.abs(v.max) || 1) * 0.1;

/** A round maximum a little above x, for a gauge with no range set. */
export function niceMax(x: number): number {
  if (x <= 0) return 1;
  const p = 10 ** Math.floor(Math.log10(x));
  for (const m of [1, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]) if (m * p >= x * 1.1) return m * p;
  return 10 * p;
}

/** A value axis for a rate shown as a percentage stops at 100 % when the values
 *  come close to it, in quarter steps, rather than at a round step past it
 *  (120 %). Small rates and values over 100 % keep the automatic axis. */
export function percentCap(fmt: B.NumberFormat, p: Pivot, stacked: boolean): { max?: number; interval?: number } {
  const cap = fmt.percent ? 1 : (fmt.suffix ?? "").startsWith("%") ? 100 : 0;
  if (!cap) return {};
  const column = (i: number) => p.series.map((s) => s.values[i] ?? 0);
  const tops = p.categories.map((_, i) => (stacked ? column(i).reduce((t, v) => t + v, 0) : Math.max(...column(i))));
  const all = p.series.flatMap((s) => s.values.filter((v): v is number => v !== null));
  const hi = Math.max(...tops, ...all);
  return all.length && Math.min(...all) >= 0 && hi <= cap && hi > cap / 2 ? { max: cap, interval: cap / 4 } : {};
}

/** Each category's values as shares of 100 %. */
export function toPercent(p: Pivot): Pivot {
  const totals = p.categories.map((_, i) => p.series.reduce((t, s) => t + Math.abs(s.values[i] ?? 0), 0));
  return { ...p, series: p.series.map((s) => ({ ...s, values: s.values.map((v, i) => (v === null || !totals[i] ? v : (v / totals[i]) * 100)) })) };
}

const STACKED = new Set(["stacked_bar", "stacked_hbar", "stacked_area", "stacked_bar_line"]);

export function buildOption(chart: B.Chart, data: B.Data, theme: ChartTheme, selected?: unknown): EChartsCoreOption {
  const option = buildBase(chart, data, theme) as Record<string, unknown>;
  return selected === undefined ? option : dimOthers(option, pivot(chart, data), chart, selected);
}

function buildBase(chart: B.Chart, data: B.Data, theme: ChartTheme): EChartsCoreOption {
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
  const axisText = { color: t.muted, fontSize: 11 };
  const valueAxisOf = (name: string | undefined, extra: object = {}) => ({
    type: "value",
    name,
    nameLocation: "middle",
    nameGap: 44,
    nameTextStyle: { color: t.ink2 },
    splitNumber: 4,
    axisLine: { show: false },
    axisTick: { show: false },
    splitLine: { lineStyle: { color: t.grid, width: 1 } },
    axisLabel: { ...axisText, formatter: (v: number) => formatNumber(v, { ...fmt, decimals: undefined }, true) },
    ...extra,
  });
  const categoryAxisOf = (data: string[], name?: string, extra: object = {}) => ({
    type: "category",
    data,
    name,
    nameLocation: "middle",
    nameGap: 28,
    nameTextStyle: { color: t.ink2 },
    axisLine: { lineStyle: { color: t.axis } },
    axisTick: { show: false },
    axisLabel: { ...axisText, hideOverlap: true },
    ...extra,
  });

  if (chart.type === "treemap") {
    const s = p.series[0] ?? { name: "", values: [] };
    return {
      ...base,
      tooltip: { ...tooltip, trigger: "item", formatter: (x: { name: string; value: number }) => `${esc(x.name)}<br/><b>${esc(valueText(x.value))}</b>` },
      series: [
        {
          type: "treemap",
          roam: false,
          nodeClick: false,
          breadcrumb: { show: false },
          left: 0,
          right: 0,
          top: 0,
          bottom: 0,
          itemStyle: { borderColor: t.surface, borderWidth: 2, gapWidth: 2, borderRadius: 4 },
          // Past the palette's eight hues, tiles share the neutral (use Top N with Others).
          data: p.categories.map((name, i) => {
            const fill = name === "Others" || i >= colors.length ? OTHERS_COLOR[theme] : colors[i];
            return {
              name,
              value: Math.max(0, s.values[i] ?? 0),
              itemStyle: { color: fill },
              label: { show: true, color: inkOn(fill), fontSize: 12, overflow: "truncate", formatter: (x: { name: string; value: number }) => (props.labels ? `${x.name}\n${valueText(x.value)}` : x.name) },
            };
          }),
        },
      ],
    };
  }

  if (chart.type === "heatmap") {
    const ys = p.series.map((s) => s.name);
    const cells: [number, number, number | null][] = [];
    let lo = Infinity;
    let hi = -Infinity;
    p.series.forEach((s, y) =>
      s.values.forEach((v, x) => {
        cells.push([x, y, v]);
        if (v !== null) [lo, hi] = [Math.min(lo, v), Math.max(hi, v)];
      }),
    );
    return {
      ...base,
      tooltip: { ...tooltip, trigger: "item", formatter: (x: { value: [number, number, number | null] }) => `${esc(ys[x.value[1]])} · ${esc(p.categories[x.value[0]])}<br/><b>${esc(valueText(x.value[2]))}</b>` },
      grid: { left: 8, right: 16, top: 12, bottom: 44, containLabel: true },
      xAxis: categoryAxisOf(p.categories, props.xTitle, { splitArea: { show: false } }),
      // Rows read top-down in the series' order (Sun first).
      yAxis: categoryAxisOf(ys, props.yTitle, { nameGap: 60, inverse: true }),
      visualMap: {
        min: Number.isFinite(lo) ? lo : 0,
        max: Number.isFinite(hi) ? hi : 1,
        calculable: false,
        orient: "horizontal",
        left: "center",
        bottom: 0,
        itemWidth: 10,
        itemHeight: 120,
        textGap: 8,
        text: [formatNumber(Number.isFinite(hi) ? hi : 1, { ...fmt, decimals: undefined }, true), formatNumber(Number.isFinite(lo) ? lo : 0, { ...fmt, decimals: undefined }, true)],
        textStyle: { color: t.muted, fontSize: 11 },
        inRange: { color: SEQUENTIAL[theme] },
      },
      series: [
        {
          type: "heatmap",
          data: cells,
          itemStyle: { borderColor: t.surface, borderWidth: 2, borderRadius: 2 },
          label: { show: !!props.labels, fontSize: 11, formatter: (x: { value: [number, number, number | null] }) => valueText(x.value[2]) },
          emphasis: { itemStyle: { borderColor: t.ink, borderWidth: 1 } },
        },
      ],
    };
  }

  if (chart.type === "waterfall") {
    const s = p.series[0] ?? { name: "", values: [] };
    const pol = POLARITY[theme];
    // Each step runs from the running total before it to the one after; the
    // last bar is the total from zero.
    let run = 0;
    const steps = p.categories.map((name, i) => {
      const v = s.values[i] ?? 0;
      const from = run;
      run += v;
      return { name, from, to: run, change: v };
    });
    steps.push({ name: "Total", from: 0, to: run, change: run });
    const cats = steps.map((x) => x.name);
    return {
      ...base,
      tooltip: {
        ...tooltip,
        trigger: "item",
        formatter: (x: { dataIndex: number }) => {
          const st = steps[x.dataIndex];
          const total = x.dataIndex === steps.length - 1;
          return `${esc(st.name)}<br/>${total ? "Total" : "Change"} <b>${esc(valueText(st.change))}</b>${total ? "" : `<br/>Running total ${esc(valueText(st.to))}`}`;
        },
      },
      grid: { left: 8, right: 16, top: props.labels ? 22 : 12, bottom: 4 + (props.xTitle ? 24 : 0), containLabel: true },
      xAxis: categoryAxisOf(cats, props.xTitle),
      yAxis: valueAxisOf(props.yTitle, { boundaryGap: [0, "8%"] }),
      series: [
        {
          type: "custom",
          renderItem: (_: unknown, api: { value(i: number): number; coord(p: [number, number]): [number, number]; size(p: [number, number]): [number, number] }) => {
            const i = api.value(0);
            const a = api.coord([i, api.value(1)]);
            const b = api.coord([i, api.value(2)]);
            const w = Math.min(24, api.size([1, 0])[0] * 0.6);
            const st = steps[i];
            const total = i === steps.length - 1;
            const fill = total ? pol.total : st.change >= 0 ? pol.up : pol.down;
            const top = Math.min(a[1], b[1]);
            return {
              type: "rect",
              shape: { x: a[0] - w / 2, y: top, width: w, height: Math.max(1, Math.abs(a[1] - b[1])), r: 2 },
              style: { fill },
            };
          },
          encode: { x: 0, y: [1, 2] },
          data: steps.map((x, i) => [i, x.from, x.to]),
          label: { show: !!props.labels, position: "top", color: t.ink2, fontSize: 11, formatter: (x: { dataIndex: number }) => valueText(steps[x.dataIndex].change) },
          z: 2,
        },
      ],
    };
  }

  if (chart.type === "tornado") {
    // Two series mirrored about zero: the first to the left, the second to the right.
    const [left, right] = p.series;
    // One scale both ways, so the sides compare at a glance.
    const reach = niceMax(Math.max(0, ...[left, right].flatMap((s) => (s?.values ?? []).map((v) => Math.abs(v ?? 0)))));
    // Even ticks both ways: 0, half the reach, the reach.
    const mirror = (s: Series | undefined, sign: number) => ({
      name: s?.name ?? "",
      type: "bar",
      stack: "tornado",
      barMaxWidth: 24,
      data: (s?.values ?? []).map((v) => (v === null ? null : sign * v)),
      itemStyle: { borderRadius: sign < 0 ? [4, 0, 0, 4] : [0, 4, 4, 0] },
      label: { show: !!props.labels, position: sign < 0 ? "left" : "right", color: t.ink2, fontSize: 11, formatter: (x: { value: number | null }) => valueText(x.value === null ? null : Math.abs(x.value)) },
      emphasis: { focus: "series" },
    });
    return {
      ...base,
      legend: { ...legend, show: props.legend !== "none" },
      tooltip: { ...tooltip, trigger: "axis", axisPointer: { type: "shadow" }, valueFormatter: (v: number | null) => valueText(v === null ? null : Math.abs(v)) },
      grid: { left: 8, right: 16, top: props.legend === "none" ? 12 : 36, bottom: 4, containLabel: true },
      xAxis: valueAxisOf(props.xTitle, { min: -reach, max: reach, interval: reach / 2, axisLabel: { ...axisText, formatter: (v: number) => formatNumber(Math.abs(v), { ...fmt, decimals: undefined }, true) } }),
      yAxis: categoryAxisOf(p.categories, props.yTitle, { inverse: true }),
      series: [mirror(left, -1), mirror(right, 1)],
    };
  }

  if (chart.type === "scatter") {
    const cols = data.columns ?? [];
    const rows = data.rows ?? [];
    const at = (role: string) => cols.findIndex((c) => c.role === role);
    const [dim, ser, xi, yi, si] = [at("dimension"), at("series"), at("value"), at("target"), at("secondary")];
    const sizes = si >= 0 ? rows.map((r) => num(r?.[si]) ?? 0) : [];
    const maxSize = Math.max(1, ...sizes.map(Math.abs));
    const groups = new Map<string, { name: string; points: { raw: unknown; value: [number | null, number | null]; name: string; size: number; sizeValue: number | null }[] }>();
    rows.forEach((r, i) => {
      const g = ser >= 0 ? keyOf(r?.[ser]) : "";
      if (!groups.has(g)) groups.set(g, { name: ser >= 0 ? formatLabel(r?.[ser], cols[ser], props.dateFormat, { raw: props.labelCase === "raw" }) : cols[yi]?.label ?? "", points: [] });
      groups.get(g)!.points.push({
        raw: dim >= 0 ? (r?.[dim] ?? null) : undefined,
        value: [num(r?.[xi]), num(r?.[yi])],
        name: dim >= 0 ? formatLabel(r?.[dim], cols[dim], props.dateFormat, { raw: props.labelCase === "raw" }) : "",
        // Area follows the size value: 8px for the smallest, 32px for the largest.
        size: si >= 0 ? 8 + 24 * Math.sqrt(Math.abs(sizes[i]) / maxSize) : 10,
        sizeValue: si >= 0 ? num(r?.[si]) : null,
      });
    });
    const list = [...groups.entries()].sort(([a], [b]) => a.localeCompare(b, undefined, { numeric: true })).map(([, g]) => g);
    return {
      ...base,
      legend: { ...legend, show: props.legend !== "none" && list.length > 1 },
      tooltip: {
        ...tooltip,
        trigger: "item",
        formatter: (x: { data: { name: string; value: [number, number] }; dataIndex: number; seriesIndex: number }) => {
          const pt = list[x.seriesIndex].points[x.dataIndex];
          const extra = si >= 0 ? `<br/>${esc(cols[si].label)}: ${esc(valueText(pt.sizeValue))}` : "";
          return `<b>${esc(pt.name)}</b><br/>${esc(cols[xi].label)}: ${esc(valueText(pt.value[0]))}<br/>${esc(cols[yi].label)}: ${esc(valueText(pt.value[1]))}${extra}`;
        },
      },
      // Room on the left for the Y title; padding inside the axes so points
      // and their labels don't sit on them.
      grid: { left: 36, right: 24, top: list.length > 1 && props.legend !== "none" ? 36 : 16, bottom: 28, containLabel: true },
      xAxis: valueAxisOf(props.xTitle || cols[xi]?.label, { nameGap: 28, scale: true, min: padMin, max: padMax, splitLine: { show: false } }),
      yAxis: valueAxisOf(props.yTitle || cols[yi]?.label, { scale: true, min: padMin, max: padMax, nameGap: 40 }),
      series: list.map((g) => ({
        type: "scatter",
        name: g.name,
        data: g.points.map((pt) => ({ value: pt.value, name: pt.name, symbolSize: pt.size, raw: pt.raw })),
        itemStyle: { opacity: 0.85, borderColor: t.surface, borderWidth: 2 },
        label: { show: !!props.labels, position: "right", color: t.ink2, fontSize: 11, formatter: (x: { name: string }) => x.name },
        labelLayout: { hideOverlap: true },
        emphasis: { focus: "series", scale: 1.15 },
      })),
    };
  }

  if (chart.type === "gauge") {
    const cols = data.columns ?? [];
    const row = (data.rows ?? [])[0] ?? [];
    const v = num(row[cols.findIndex((c) => c.role === "value")]);
    const target = num(row[cols.findIndex((c) => c.role === "target")]);
    const min = props.min ?? 0;
    const max = props.max ?? target ?? niceMax(v ?? 0);
    const m = METER[theme];
    return {
      ...base,
      series: [
        {
          type: "gauge",
          min,
          max,
          startAngle: 210,
          endAngle: -30,
          radius: "92%",
          center: ["50%", "58%"],
          progress: { show: true, width: 14, roundCap: true, itemStyle: { color: m.fill } },
          axisLine: { roundCap: true, lineStyle: { width: 14, color: [[1, m.track]] } },
          pointer: { show: false },
          axisTick: { show: false },
          splitLine: { show: false },
          axisLabel: { show: true, distance: -40, color: t.muted, fontSize: 11, formatter: (x: number) => (x === min || x === max ? formatNumber(x, { ...fmt, decimals: undefined }, true) : "") },
          anchor: { show: false },
          title: { show: true, offsetCenter: [0, "34%"], color: t.ink2, fontSize: 12 },
          detail: { valueAnimation: !base.animationDuration ? false : true, offsetCenter: [0, "4%"], color: t.ink, fontSize: 32, fontWeight: 600, formatter: (x: number) => valueText(x) },
          data: [{ value: v ?? 0, name: cols.find((c) => c.role === "value")?.label ?? "" }],
        },
      ],
    };
  }

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
    // Every label when there are few enough to fit; otherwise ECharts thins them.
    axisLabel: { color: t.muted, fontSize: 11, hideOverlap: true, interval: !horizontal && p.categories.length <= 12 ? 0 : "auto" },
    boundaryGap: !isLine,
  };
  const valueAxis = {
    type: "value",
    name: horizontal ? props.xTitle : props.yTitle,
    nameLocation: "middle",
    nameGap: 44,
    nameTextStyle: { color: t.ink2 },
    max: pct ? 100 : undefined,
    ...(pct ? {} : percentCap(fmt, p, stacked)),
    // Room above the tallest value, so bars and labels don't touch the top; a
    // few round ticks, so short charts aren't crowded.
    boundaryGap: pct ? undefined : [0, "8%"],
    splitNumber: 4,
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
        // Lines run edge to edge, so the grid leaves room for half of the end labels.
    grid: { left: isLine && !horizontal ? 28 : 8, right: legendShown && legendPos === "right" ? 140 : (isLine && !horizontal) || (props.labels && horizontal) ? 40 : 16, top: legendShown && legendPos === "top" ? 36 : props.labels ? 22 : 12, bottom: (legendShown && legendPos === "bottom" ? 32 : 4) + (props.xTitle && !horizontal ? 24 : 0), containLabel: true },
    xAxis: horizontal ? valueAxis : categoryAxis,
    yAxis: horizontal ? { ...categoryAxis, inverse: true } : valueAxis,
    series,
  };
}
