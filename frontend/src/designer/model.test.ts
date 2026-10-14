import { describe, expect, it } from "vitest";
import { deleteColumn, insertColumn, moveColumn, newTable, renameColumn, togglePrimaryKey } from "./model";

function fixture() {
  const t = newTable("mysql", "shop", undefined);
  t.name = "items";
  t.columns.push({ name: "sku", origName: "sku", type: "varchar", length: "32", nullable: true }, { name: "cust", origName: "cust", type: "int", nullable: true });
  t.primaryKey = { name: "", columns: ["id", "sku"] };
  t.indexes = [{ name: "ix_sku", columns: [{ name: "sku" }, { name: "", expr: "lower(sku)" }] }];
  t.uniques = [{ name: "uq_sku", columns: ["sku"] }];
  t.foreignKeys = [{ name: "fk_cust", columns: ["cust"], refTable: "customers", refColumns: ["id"] }];
  return t;
}

describe("designer model", () => {
  it("renames references with the column", () => {
    const t = renameColumn(fixture(), 1, "code");
    expect(t.primaryKey!.columns).toEqual(["id", "code"]);
    expect(t.indexes[0].columns.map((c) => c.name)).toEqual(["code", ""]);
    expect(t.uniques[0].columns).toEqual(["code"]);
    expect(t.columns[1].origName).toBe("sku"); // identity kept for the diff
  });

  it("deletes a column and its references", () => {
    let t = deleteColumn(fixture(), 1);
    expect(t.primaryKey!.columns).toEqual(["id"]);
    expect(t.indexes[0].columns).toHaveLength(1); // expression column remains
    expect(t.uniques).toHaveLength(0);
    t = deleteColumn(t, t.columns.findIndex((c) => c.name === "cust"));
    expect(t.foreignKeys).toHaveLength(0);
    t = deleteColumn(t, 0);
    expect(t.primaryKey).toBeUndefined();
  });

  it("toggles composite primary keys and makes key columns NOT NULL", () => {
    let t = togglePrimaryKey(fixture(), "cust");
    expect(t.primaryKey!.columns).toEqual(["id", "sku", "cust"]);
    expect(t.columns[2].nullable).toBe(false);
    t = togglePrimaryKey(togglePrimaryKey(togglePrimaryKey(t, "cust"), "sku"), "id");
    expect(t.primaryKey).toBeUndefined();
  });

  it("moves and inserts columns", () => {
    let t = moveColumn(fixture(), 0, 1);
    expect(t.columns.map((c) => c.name)).toEqual(["sku", "id", "cust"]);
    expect(moveColumn(t, 0, -1)).toBe(t);
    t = insertColumn(t, 1, "postgres");
    expect(t.columns[1]).toMatchObject({ name: "column", origName: "", type: "text" });
  });
});
