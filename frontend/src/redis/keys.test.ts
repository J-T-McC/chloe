import { describe, expect, it } from "vitest";
import { display, formatTTL, hexDump, keyRows, toPattern } from "./keys";

const k = (name: string, type = "string") => ({ name: { text: name }, type, ttl: -1 });

describe("redis key helpers", () => {
  it("groups keys into folders by separator", () => {
    const keys = [k("user:1"), k("user:2"), k("user:1:prefs", "hash"), k("greeting"), k("user:10")];
    const collapsed = keyRows(keys, ":", new Set(), true);
    expect(collapsed.map((r) => `${r.kind}:${r.label}:${r.count}`)).toEqual(["folder:user:4", "key:greeting:1"]);
    const open = keyRows(keys, ":", new Set(["user:", "user:1:"]), true);
    expect(open.map((r) => `${"  ".repeat(r.depth)}${r.label}`)).toEqual(["user", "  1", "    prefs", "  1", "  2", "  10", "greeting"]);
    expect(open.find((r) => r.label === "prefs")!.key!.type).toBe("hash");
    expect(keyRows(keys, ":", new Set(), false).map((r) => r.label)).toEqual(["greeting", "user:1", "user:1:prefs", "user:2", "user:10"]);
  });

  it("displays binary safely", () => {
    const v = { text: btoa("\x00a\xff\\"), base64: true };
    expect(display(v)).toBe("\\x00a\\xff\\\\");
    expect(hexDump(v)).toBe(`00000000  ${"00 61 ff 5c".padEnd(47)}  .a.\\`);
  });

  it("builds patterns and formats TTLs", () => {
    expect(toPattern("user")).toBe("*user*");
    expect(toPattern("user:*")).toBe("user:*");
    expect(toPattern("")).toBe("*");
    expect(formatTTL(-1)).toBe("No limit");
    expect(formatTTL(3725)).toBe("1h 2m");
    expect(formatTTL(90061)).toBe("1d 1h");
  });
});
