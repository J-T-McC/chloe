import { describe, expect, it } from "vitest";
import { Val, cellText, editType, formatCompact, formatVal, parseIndexKeys, formatIndexKeys } from "./docs";

const doc: Val = {
  t: "object",
  f: [
    { k: "_id", v: { t: "oid", v: "65a1b2c3d4e5f60718293a4b" } },
    { k: "name", v: { t: "string", v: "it's" } },
    { k: "n", v: { t: "long", v: "5" } },
    { k: "x", v: { t: "double", v: "1" } },
    { k: "tags", v: { t: "array", a: [{ t: "string", v: "a" }] } },
    { k: "odd key", v: { t: "object", f: [] } },
  ],
};

describe("mongo docs", () => {
  it("formats like the backend", () => {
    expect(formatVal(doc)).toBe(`{ _id: ObjectId('65a1b2c3d4e5f60718293a4b'), name: 'it\\'s', n: NumberLong('5'), x: 1.0, tags: [ 'a' ], 'odd key': {} }`);
    expect(formatVal(doc, "  ", true)).toContain('\n  "_id": ObjectId("65a1b2c3d4e5f60718293a4b"),\n');
  });
  it("shows plain types as text and the rest in shell syntax", () => {
    expect(cellText({ t: "date", v: "2024-01-01T00:00:00.000Z" })).toBe("2024-01-01T00:00:00.000Z");
    expect(cellText({ t: "null" })).toBeNull();
    expect(cellText(undefined)).toBe("");
    expect(cellText({ t: "object", f: [{ k: "a", v: { t: "int", v: "1" } }] })).toBe("{ a: 1 }");
    expect(cellText({ t: "regex", v: "^a", s: "i" })).toBe("/^a/i");
  });
  it("keeps a cell's type when editing and guesses one for new values", () => {
    expect(editType("long", "7")).toBe("long");
    expect(editType("object", "{a: 2}")).toBe("shell");
    expect(editType(undefined, "42")).toBe("int");
    expect(editType(undefined, "9999999999")).toBe("long");
    expect(editType("null", "1.5")).toBe("double");
    expect(editType(undefined, "true")).toBe("bool");
    expect(editType(undefined, "ISODate('2024-01-01')")).toBe("shell");
    expect(editType(undefined, "hello")).toBe("string");
  });
  it("lays out like mongosh: short values on one line", () => {
    const short: Val = { t: "object", f: [{ k: "_id", v: { t: "int", v: "1" } }, { k: "s", v: { t: "string", v: "paid" } }] };
    expect(formatCompact({ t: "array", a: [short, short] })).toBe("[ { _id: 1, s: 'paid' }, { _id: 1, s: 'paid' } ]");
    const long: Val = { t: "array", a: Array.from({ length: 4 }, () => doc) };
    const text = formatCompact(long);
    expect(text.split("\n")[0]).toBe("[");
    expect(text).toContain("\n  {\n    _id: ObjectId('65a1b2c3d4e5f60718293a4b'),\n");
    expect(text).toContain("    tags: [ 'a' ],\n");
  });
  it("parses index keys", () => {
    const k = parseIndexKeys("email, created_at:-1, name : text");
    expect(k).toEqual([{ field: "email", type: "1" }, { field: "created_at", type: "-1" }, { field: "name", type: "text" }]);
    expect(formatIndexKeys(k)).toBe("email, created_at:-1, name:text");
  });
});
