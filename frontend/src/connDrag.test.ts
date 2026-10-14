import { describe, expect, it } from "vitest";
import { resolveDrop } from "./connDrag";

const conns = [
  { id: "a", group: "G" },
  { id: "b", group: "G" },
  { id: "c", group: "G" },
  { id: "t1" },
  { id: "t2" },
];
const groups = ["G", "H", "I"];

describe("resolveDrop", () => {
  it("drops a connection into a group", () => {
    expect(resolveDrop({ type: "conn", id: "t1" }, { type: "group", name: "H" }, 0.2, conns, groups)).toEqual({
      pos: "into",
      move: { conn: "t1", group: "H", before: "" },
    });
  });

  it("places before or after a connection by pointer position", () => {
    expect(resolveDrop({ type: "conn", id: "t1" }, { type: "conn", id: "b" }, 0.3, conns, groups)?.move).toEqual({ conn: "t1", group: "G", before: "b" });
    expect(resolveDrop({ type: "conn", id: "t1" }, { type: "conn", id: "b" }, 0.7, conns, groups)?.move).toEqual({ conn: "t1", group: "G", before: "c" });
    expect(resolveDrop({ type: "conn", id: "t1" }, { type: "conn", id: "c" }, 0.7, conns, groups)?.move).toEqual({ conn: "t1", group: "G", before: "" });
  });

  it("skips the dragged connection when finding the next one", () => {
    expect(resolveDrop({ type: "conn", id: "b" }, { type: "conn", id: "a" }, 0.9, conns, groups)?.move).toEqual({ conn: "b", group: "G", before: "c" });
  });

  it("moves out of a group to the top level", () => {
    expect(resolveDrop({ type: "conn", id: "a" }, { type: "conn", id: "t2" }, 0.1, conns, groups)?.move).toEqual({ conn: "a", group: "", before: "t2" });
    expect(resolveDrop({ type: "conn", id: "a" }, { type: "end" }, 0, conns, groups)).toEqual({ pos: "end", move: { conn: "a", group: "", before: "" } });
  });

  it("ignores dropping on itself", () => {
    expect(resolveDrop({ type: "conn", id: "a" }, { type: "conn", id: "a" }, 0.5, conns, groups)).toBeNull();
    expect(resolveDrop({ type: "group", name: "H" }, { type: "group", name: "H" }, 0.5, conns, groups)).toBeNull();
  });

  it("reorders groups among themselves", () => {
    expect(resolveDrop({ type: "group", name: "I" }, { type: "group", name: "G" }, 0.2, conns, groups)?.move).toEqual({ group: "I", before: "G" });
    expect(resolveDrop({ type: "group", name: "G" }, { type: "group", name: "H" }, 0.8, conns, groups)?.move).toEqual({ group: "G", before: "I" });
    expect(resolveDrop({ type: "group", name: "G" }, { type: "group", name: "I" }, 0.8, conns, groups)?.move).toEqual({ group: "G", before: "" });
    expect(resolveDrop({ type: "group", name: "G" }, { type: "conn", id: "t1" }, 0.5, conns, groups)).toBeNull();
  });
});
