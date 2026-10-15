import { describe, expect, it } from "vitest";
import type { B } from "../../api";
import { controlFilter, periodRange, pickFilter, reach } from "./interaction";

describe("dashboard interaction", () => {
  it("turns a period label into the days it covers", () => {
    expect(periodRange("month", "2026-02")).toEqual(["2026-02-01", "2026-02-28"]);
    expect(periodRange("quarter", "2026-Q4")).toEqual(["2026-10-01", "2026-12-31"]);
    expect(periodRange("week", "2026-03-29")).toEqual(["2026-03-29", "2026-04-04"]);
    expect(periodRange("year", "2025")).toEqual(["2025-01-01", "2025-12-31"]);
    expect(periodRange("hour", "2026-03-04 15")).toEqual(["2026-03-04 15:00:00", "2026-03-04 15:59:59.999"]);
    expect(periodRange("month_x", "Mar")).toBeNull();
  });

  it("filters by the clicked value", () => {
    const p = { item: "a", chart: "c", source: "s", field: "region", bucket: "", key: "North", label: "North" };
    expect(pickFilter(p)).toEqual({ field: "region", op: "=", values: ["North"] });
    expect(pickFilter({ ...p, key: null })).toEqual({ field: "region", op: "is_null", values: [] });
    expect(pickFilter({ ...p, key: "Others" })).toBeNull();
    expect(pickFilter({ ...p, field: "at", bucket: "month", key: "2026-02" })).toEqual({ field: "at", op: "between", values: ["2026-02-01", "2026-02-28"] });
    expect(pickFilter({ ...p, field: "at", bucket: "weekday_x", key: "Mon" })).toBeNull();
  });

  it("turns a control's value into a filter", () => {
    expect(controlFilter("r", "list", ["a", "b"])).toEqual({ field: "r", op: "in", values: ["a", "b"] });
    expect(controlFilter("r", "combo", ["a"])).toEqual({ field: "r", op: "=", values: ["a"] });
    expect(controlFilter("n", "range", ["5", ""])).toEqual({ field: "n", op: ">=", values: ["5"] });
    expect(controlFilter("n", "range", ["5", "9"])).toEqual({ field: "n", op: "between", values: ["5", "9"] });
    expect(controlFilter("r", "list", null)).toBeNull();
  });

  it("reaches charts on the same source, or a control's targets", () => {
    const chart = (id: string, source: string, dim = "region") => ({ id, dataSource: source, type: "bar", dimension: [{ field: dim }], props: {} }) as unknown as B.Chart;
    const pick = { item: "t1", chart: "c1", source: "s", field: "region", bucket: "", key: "N", label: "N" };
    const fields = (id: string) => (id === "s" ? ["region", "total"] : ["total"]);
    expect(reach({ item: "t2", chart: chart("c2", "s") }, [pick], [], "filter", fields).filters).toHaveLength(1);
    expect(reach({ item: "t3", chart: chart("c3", "other") }, [pick], [], "filter", fields).filters).toHaveLength(0);
    expect(reach({ item: "t1", chart: chart("c1", "s") }, [pick], [], "filter", fields).highlight).toBe("N");
    expect(reach({ item: "t2", chart: chart("c2", "s") }, [pick], [], "highlight", fields)).toEqual({ filters: [], highlight: "N" });
    expect(reach({ item: "t2", chart: chart("c2", "s") }, [pick], [], "none", fields).filters).toHaveLength(0);
    const ctl = { item: "k", field: "region", filter: { field: "region", op: "=", values: ["N"] } as B.Filter, targets: [] as string[] };
    expect(reach({ item: "t2", chart: chart("c2", "s") }, [], [ctl], "filter", fields).filters).toHaveLength(1);
    expect(reach({ item: "t3", chart: chart("c3", "other") }, [], [ctl], "filter", fields).filters).toHaveLength(0);
    expect(reach({ item: "t3", chart: chart("c3", "other") }, [], [{ ...ctl, targets: ["c3"] }], "filter", fields).filters).toHaveLength(1);
  });
});
