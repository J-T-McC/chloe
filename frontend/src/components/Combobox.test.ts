import { describe, expect, it } from "vitest";
import { filterOptions } from "./Combobox";

describe("combobox filtering", () => {
  it("puts prefix matches first and ignores case", () => {
    const opts = ["armscii8", "ascii", "latin1", "utf16", "utf8mb3", "utf8mb4", "ucs2"];
    expect(filterOptions(opts, "UTF8")).toEqual(["utf8mb3", "utf8mb4"]);
    expect(filterOptions(opts, "scii")).toEqual(["armscii8", "ascii"]);
    expect(filterOptions(opts, "as")).toEqual(["ascii"]);
    expect(filterOptions(opts, " ")).toEqual(opts);
    expect(filterOptions(["C.UTF-8", "C", "POSIX"], "c")).toEqual(["C", "C.UTF-8"]);
  });
});
