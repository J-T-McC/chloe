import { describe, expect, it } from "vitest";
import type { B } from "../../api";
import { buildOption, niceMax } from "./options";

const col = (key: string, role: string, type = "number", label = key): B.Column => ({ key, label, role, field: key, type }) as B.Column;
const data = (columns: B.Column[], rows: unknown[][]): B.Data => ({ columns, rows, complete: true, mode: "live", ranAt: "" }) as B.Data;
const chart = (type: string, props: Partial<B.Props> = {}): B.Chart => ({ id: "c", name: "C", dataSource: "s", type, props }) as B.Chart;
// eslint-disable-next-line @typescript-eslint/no-explicit-any
const opt = (c: B.Chart, d: B.Data) => buildOption(c, d, "light") as any;

describe("phase 3 chart options", () => {
  it("waterfall steps run from the previous total and end with the total", () => {
    const o = opt(chart("waterfall"), data([col("d", "dimension", "string"), col("v", "value")], [["a", 10], ["b", -4], ["c", 6]]));
    expect(o.series[0].data).toEqual([[0, 0, 10], [1, 10, 6], [2, 6, 12], [3, 0, 12]]);
    expect(o.xAxis.data).toEqual(["A", "B", "C", "Total"]);
  });

  it("tornado mirrors the first series to the left", () => {
    const o = opt(chart("tornado"), data([col("d", "dimension", "string"), col("s", "series", "string"), col("v", "value")], [["x", "f", 3], ["x", "m", 5]]));
    expect(o.series[0].data).toEqual([-3]);
    expect(o.series[1].data).toEqual([5]);
  });

  it("heatmap has a cell per category and series", () => {
    const o = opt(chart("heatmap"), data([col("d", "dimension", "string"), col("s", "series", "string"), col("v", "value")], [["a", "x", 1], ["a", "y", 2], ["b", "x", 3], ["b", "y", 4]]));
    expect(o.series[0].data).toHaveLength(4);
    expect(o.visualMap.min).toBe(1);
    expect(o.visualMap.max).toBe(4);
  });

  it("treemap tiles past the palette share the neutral", () => {
    const rows = Array.from({ length: 10 }, (_, i) => [`c${i}`, 10 - i]);
    const o = opt(chart("treemap"), data([col("d", "dimension", "string"), col("v", "value")], rows));
    const fills = o.series[0].data.map((x: { itemStyle: { color: string } }) => x.itemStyle.color);
    expect(new Set(fills.slice(0, 8)).size).toBe(8);
    expect(fills[8]).toBe(fills[9]);
  });

  it("scatter groups points by series and sizes them by area", () => {
    const cols = [col("d", "dimension", "string"), col("s", "series", "string"), col("x", "value"), col("sz", "secondary"), col("y", "target")];
    const o = opt(chart("scatter"), data(cols, [["p1", "a", 1, 1, 2], ["p2", "b", 3, 4, 4]]));
    expect(o.series).toHaveLength(2);
    expect(o.series[1].data[0].symbolSize).toBe(32);
    expect(o.series[0].data[0].symbolSize).toBe(20);
  });

  it("gauge takes its maximum from the target, else a round number", () => {
    expect(opt(chart("gauge"), data([col("v", "value"), col("t", "target")], [[40, 50]])).series[0].max).toBe(50);
    expect(niceMax(87)).toBe(100);
    expect(niceMax(0)).toBe(1);
  });
});
