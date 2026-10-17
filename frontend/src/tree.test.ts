import { describe, expect, it, vi } from "vitest";

vi.mock("./api", () => ({ DBType: {}, Explorer: {}, ObjectKind: {}, errorText: String, arr: (x: unknown[]) => x ?? [] }));
vi.mock("./store", () => ({ useApp: { getState: () => ({}) }, connById: () => undefined }));

const { flatten, matchParts } = await import("./tree");

const node = (key: string, label: string, type = "object") => ({ key, label, type, connId: "c", depth: 0, expandable: type !== "object" }) as never;

describe("tree filter", () => {
  const roots = [node("c1", "prod", "conn"), node("c2", "local", "conn")];
  const children = {
    c1: [node("c1/shop", "shop", "db")],
    "c1/shop": [node("c1/shop/#table", "Tables", "folder")],
    "c1/shop/#table": [node("t1", "customers"), node("t2", "orders")],
    c2: [node("c2/app", "app", "db")],
  };
  const labels = (rows: Array<{ label: string }>) => rows.map((r) => r.label);

  it("shows everything expanded without a filter", () => {
    expect(labels(flatten(roots, { c1: true }, children, ""))).toEqual(["prod", "shop", "local"]);
  });

  it("reveals matches inside collapsed nodes and hides the rest", () => {
    expect(labels(flatten(roots, {}, children, "ORD"))).toEqual(["prod", "shop", "Tables", "orders"]);
  });

  it("shows a matching container with its normal contents", () => {
    expect(labels(flatten(roots, { c2: true }, children, "loc"))).toEqual(["local", "app"]);
    expect(labels(flatten(roots, {}, children, "nothing"))).toEqual([]);
  });

  it("splits labels for highlighting", () => {
    expect(matchParts("customers", "TOM")).toEqual(["cus", "tom", "ers"]);
    expect(matchParts("customers", "x")).toBeNull();
  });
});
