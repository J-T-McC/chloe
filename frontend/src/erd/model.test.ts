import { describe, expect, it } from "vitest";
import { autoLayout, buildModel, tableSize, toSVG, visibleColumns } from "./model";

const cat = {
  objects: [
    { schema: "shop", name: "customers", kind: "table" },
    { schema: "shop", name: "orders", kind: "table" },
    { schema: "shop", name: "order_items", kind: "table" },
    { schema: "shop", name: "v", kind: "view" },
    { schema: "other", name: "audit", kind: "table" },
  ],
  columns: [
    { schema: "shop", table: "customers", name: "id", type: "int", pk: true },
    { schema: "shop", table: "customers", name: "email", type: "text" },
    { schema: "shop", table: "orders", name: "id", type: "bigint", pk: true },
    { schema: "shop", table: "orders", name: "customer_id", type: "int", nullable: true },
    { schema: "shop", table: "order_items", name: "order_id", type: "bigint", pk: true },
    { schema: "shop", table: "order_items", name: "qty", type: "int" },
    { schema: "other", table: "audit", name: "id", type: "int", pk: true },
  ],
  fks: [
    { schema: "shop", table: "orders", columns: ["customer_id"], refTable: "customers", refColumns: ["id"] },
    { schema: "shop", table: "order_items", columns: ["order_id"], refSchema: "shop", refTable: "orders", refColumns: ["id"] },
  ],
};

describe("erd model", () => {
  it("builds tables and relationships for a schema", () => {
    const m = buildModel(cat, "shop");
    expect(m.tables.map((t) => t.name)).toEqual(["customers", "order_items", "orders"]);
    expect(m.relations).toHaveLength(2);
    const orders = m.relations.find((r) => r.from === "shop.orders")!;
    expect(orders).toMatchObject({ to: "shop.customers", optional: true, oneToOne: false });
    // order_items.order_id is its whole PK -> one-to-one with orders
    expect(m.relations.find((r) => r.from === "shop.order_items")!.oneToOne).toBe(true);
    expect(m.tables.find((t) => t.name === "orders")!.columns.find((c) => c.name === "customer_id")!.fk).toBe(true);
    expect(buildModel(cat, "shop", true).tables).toHaveLength(4);
  });

  it("filters columns by mode and sizes nodes", () => {
    const t = buildModel(cat, "shop").tables.find((x) => x.name === "orders")!;
    expect(visibleColumns(t, "keys").map((c) => c.name)).toEqual(["id", "customer_id"]);
    expect(visibleColumns(t, "none")).toEqual([]);
    expect(tableSize(t, "all").height).toBeGreaterThan(tableSize(t, "none").height);
  });

  it("lays out without overlaps and exports SVG", async () => {
    const m = buildModel(cat, "shop");
    const pos = await autoLayout(m, "all");
    const boxes = m.tables.map((t) => ({ ...pos[t.id], ...tableSize(t, "all") }));
    for (let i = 0; i < boxes.length; i++)
      for (let j = i + 1; j < boxes.length; j++) {
        const a = boxes[i];
        const b = boxes[j];
        const overlap = a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;
        expect(overlap).toBe(false);
      }
    // parents sit to the right of children
    expect(pos["shop.customers"].x).toBeGreaterThan(pos["shop.orders"].x);
    const svg = toSVG(m, pos, "all", { background: "#fff", header: "#eee", headerText: "#000", body: "#fff", text: "#000", muted: "#888", border: "#ccc", line: "#666", key: "#c90" });
    expect(svg.startsWith("<svg")).toBe(true);
    expect(svg).toContain(">customers</text>");
    expect((svg.match(/<path d="M[^"]*C/g) ?? []).length).toBe(2);
  });

  it("resolves MySQL foreign keys that name the database", () => {
    const my = {
      objects: [{ name: "a", kind: "table" }, { name: "b", kind: "table" }],
      columns: [{ table: "a", name: "id", type: "int", pk: true }, { table: "b", name: "a_id", type: "int" }],
      fks: [{ schema: "shop", table: "b", columns: ["a_id"], refSchema: "shop", refTable: "a", refColumns: ["id"] }],
    };
    expect(buildModel(my, undefined, false, "shop").relations).toHaveLength(1);
  });
});
