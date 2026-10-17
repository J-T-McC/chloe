import { describe, expect, it, vi } from "vitest";

vi.mock("./api", () => ({ Connections: {}, Explorer: {}, ObjectKind: {}, arr: (x: unknown[]) => x ?? [], errorText: String }));
vi.mock("./components/Dialogs", () => ({ alert: vi.fn(), prompt: vi.fn() }));

const { useApp } = await import("./store");
const table = (name: string) => ({ database: "shop", name, kind: "table" }) as never;

describe("openTab", () => {
  const open = (t: Parameters<ReturnType<typeof useApp.getState>["openTab"]>[0]) => useApp.getState().openTab(t);

  it("shows the open tab for the same object, connection view or file", () => {
    const a = open({ kind: "table", title: "orders", connId: "c", ref: table("orders") });
    expect(open({ kind: "table", title: "orders", connId: "c", ref: table("orders") })).toBe(a);
    expect(open({ kind: "table", title: "orders", connId: "d", ref: table("orders") })).not.toBe(a);
    expect(open({ kind: "ddl", title: "orders", connId: "c", ref: table("orders") })).not.toBe(a);
    const m = open({ kind: "monitor", title: "Monitor", connId: "c" });
    expect(open({ kind: "monitor", title: "Monitor", connId: "c" })).toBe(m);
    const e = open({ kind: "erd", title: "ERD", connId: "c", database: "shop" });
    expect(open({ kind: "erd", title: "ERD", connId: "c", database: "shop" })).toBe(e);
    expect(open({ kind: "erd", title: "ERD", connId: "c", database: "app" })).not.toBe(e);
    const w = open({ kind: "bi", title: "W", biPath: "/w" });
    expect(open({ kind: "bi", title: "W", biPath: "/w" })).toBe(w);
    expect(open({ kind: "history", title: "History" })).toBe(open({ kind: "history", title: "History" }));
  });

  it("always opens a new tab for queries, consoles and new designers", () => {
    expect(open({ kind: "query", title: "Q", connId: "c" })).not.toBe(open({ kind: "query", title: "Q", connId: "c" }));
    expect(open({ kind: "redisconsole", title: "R", connId: "c", database: "0" })).not.toBe(open({ kind: "redisconsole", title: "R", connId: "c", database: "0" }));
    expect(open({ kind: "design", title: "New", connId: "c", ref: table("") })).not.toBe(open({ kind: "design", title: "New", connId: "c", ref: table("") }));
    expect(open({ kind: "bichart", title: "New", biPath: "/w", biId: "" })).not.toBe(open({ kind: "bichart", title: "New", biPath: "/w", biId: "" }));
  });
});
