import { describe, expect, it } from "vitest";
import { formatDate, formatLabel, formatNumber, oneYear, parseDate, readable } from "./format";

describe("formatNumber", () => {
  it("formats with separators and sensible decimals", () => {
    expect(formatNumber(1234567)).toBe("1,234,567");
    expect(formatNumber(12.3456)).toBe("12.35");
    expect(formatNumber(0.012345)).toBe("0.0123");
    expect(formatNumber(-5)).toBe("-5");
    expect(formatNumber(null)).toBe("");
  });
  it("applies prefix, suffix, units and decimals", () => {
    expect(formatNumber(1234567, { prefix: "$", unit: "M" })).toBe("$1.2M");
    expect(formatNumber(-1500, { prefix: "$", unit: "auto" })).toBe("-$1.5K");
    expect(formatNumber(42, { suffix: " %", decimals: 1 })).toBe("42.0 %");
    expect(formatNumber(1234.5, { noSeparator: true, decimals: 2 })).toBe("1234.50");
  });
  it("shortens on axes", () => {
    expect(formatNumber(2500000, {}, true)).toBe("2.5M");
    expect(formatNumber(950, {}, true)).toBe("950");
  });
});

describe("readable", () => {
  it("makes enum-like values readable", () => {
    expect(readable("ended_early")).toBe("Ended Early");
    expect(readable("never started")).toBe("Never Started");
    expect(readable("frontier")).toBe("Frontier");
    expect(readable("signedIn")).toBe("Signed In");
    expect(readable("user_id")).toBe("User ID");
    expect(readable("stuck-playing")).toBe("Stuck Playing");
  });
  it("leaves other text alone", () => {
    for (const s of ["ada@example.com", "https://x.io/a", "North", "McDonald", "row 250x/1", "2026-03-01", "42", "NULL", "already Mixed Case", "SKU_ABC"]) expect(readable(s)).toBe(s);
  });
  it("labels with options", () => {
    expect(formatLabel("ended_early", { type: "string" })).toBe("Ended Early");
    expect(formatLabel("ended_early", { type: "string" }, "", { raw: true })).toBe("ended_early");
    expect(formatLabel("2026-09-16", { type: "datetime", aggregate: "day" }, "", { sameYear: true })).toBe("Sep 16");
    expect(formatLabel("2026-09-13", { type: "datetime", aggregate: "week" }, "", { sameYear: true })).toBe("Week of Sep 13");
    expect(oneYear(["2026-01-01", "2026-12-31"], { type: "datetime", aggregate: "day" })).toBe(true);
    expect(oneYear(["2025-12-31", "2026-01-01"], { type: "datetime", aggregate: "day" })).toBe(false);
  });
});

describe("dates", () => {
  it("parses bucket labels", () => {
    expect(parseDate("2026-Q2", "quarter")).toEqual(new Date(2026, 3, 1));
    expect(parseDate("2026-03", "month")).toEqual(new Date(2026, 2, 1));
    expect(parseDate("2026-03-01 14:05:09")).toEqual(new Date(2026, 2, 1, 14, 5, 9));
    expect(parseDate("nope")).toBeNull();
  });
  it("formats Navicat's specifiers", () => {
    const d = new Date(2026, 0, 4, 15, 7, 9); // a Sunday
    expect(formatDate(d, "YYYY-MM-DD HH:mm:SS")).toBe("2026-01-04 15:07:09");
    expect(formatDate(d, "MMMM D, YY hh p")).toBe("January 4, 26 03 PM");
    expect(formatDate(d, "WD Wd wd WW QQ q MMM MMMMM")).toBe("Sunday Sun S W01 Q1 1 Jan J");
  });
  it("labels buckets readably", () => {
    const b = (aggregate: string) => ({ type: "string", aggregate });
    expect(formatLabel("2026-03", b("month"))).toBe("Mar 2026");
    expect(formatLabel("2026-Q1", b("quarter"))).toBe("Q1 2026");
    expect(formatLabel("2026-09-13", b("week"))).toBe("Week of Sep 13, 2026");
    expect(formatLabel("2026-03-01 14", b("hour"))).toBe("Mar 1, 14:00");
    expect(formatLabel("03", b("month_x"))).toBe("Mar");
    expect(formatLabel("0", b("weekday_x"))).toBe("Sun");
    expect(formatLabel("2026-03", b("month"), "MM/YYYY")).toBe("03/2026");
    expect(formatLabel(null, b(""))).toBe("(null)");
    expect(formatLabel("2024", { type: "number" })).toBe("2024");
    expect(formatLabel("2024-03-01 10:00:00", { type: "datetime" }, "D MMM")).toBe("1 Mar");
  });
});
