import { describe, expect, it } from "vitest";
import { tabComplete } from "./complete";

describe("console tab completion", () => {
  const m = ["ZRANDMEMBER", "ZRANGE", "ZRANGEBYLEX", "ZRANK"];
  it("extends to the common prefix, then cycles", () => {
    expect(tabComplete("ZR", m, null)).toBe("ZRAN");
    expect(tabComplete("ZRAN", m, null)).toBe("ZRANDMEMBER");
    expect(tabComplete("ZRANDMEMBER", m, "ZRANDMEMBER")).toBe("ZRANGE");
    expect(tabComplete("HGETA", ["HGETALL"], null)).toBe("HGETALL ");
    expect(tabComplete("X", [], null)).toBeNull();
  });
});
