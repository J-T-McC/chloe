import { describe, expect, it } from "vitest";
import { CompletionCatalog, Suggestion, complete, makeAlias, quoteIdent } from "./complete";
import { analyze } from "./analyze";

const col = (name: string, type = "int", pk = false) => ({ name, type, pk });

function shop(dialect: "mysql" | "postgres"): CompletionCatalog {
  const schema = dialect === "postgres" ? "shop" : undefined;
  return {
    dialect,
    defaultSchema: dialect === "postgres" ? "shop" : "shop",
    schemas: dialect === "postgres" ? ["public", "shop"] : ["shop", "mysql"],
    tables: [
      { schema, name: "customers", kind: "table", columns: [col("id", "int", true), col("email", "text"), col("full_name", "varchar"), col("created_at", "timestamptz")] },
      { schema, name: "orders", kind: "table", columns: [col("id", "bigint", true), col("customer_id"), col("status", "order_status"), col("placed_at", "timestamp")] },
      { schema, name: "order_items", kind: "table", columns: [col("order_id", "bigint", true), col("product_id", "int", true), col("qty")] },
      { schema, name: "products", kind: "table", columns: [col("id", "int", true), col("sku", "text"), col("name", "text"), col("price", "numeric")] },
      { schema, name: "customer_orders", kind: "view", columns: [col("full_name", "varchar"), col("order_id", "bigint"), col("status", "order_status")] },
      ...(dialect === "postgres" ? [{ schema: "public", name: "audit_log", kind: "table" as const, columns: [col("id", "int", true), col("message", "text")] }] : []),
    ],
    fks: [
      { schema, table: "orders", columns: ["customer_id"], refSchema: schema, refTable: "customers", refColumns: ["id"] },
      { schema, table: "order_items", columns: ["order_id"], refSchema: schema, refTable: "orders", refColumns: ["id"] },
      { schema, table: "order_items", columns: ["product_id"], refSchema: schema, refTable: "products", refColumns: ["id"] },
    ],
    functions: [
      { name: "count", builtin: true },
      { name: "coalesce", builtin: true },
      { name: "order_total", schema: "shop", signature: "oid bigint", returnType: "numeric", builtin: false },
    ],
  };
}

/** Run completion with "|" marking the cursor. */
function at(sql: string, dialect: "mysql" | "postgres" = "postgres") {
  const offset = sql.indexOf("|");
  const text = sql.replace("|", "");
  const r = complete(text, offset, shop(dialect));
  const sorted = [...r.suggestions].sort((a, b) => a.rank - b.rank);
  return { ...r, sorted, labels: sorted.map((s) => s.label), find: (l: string) => r.suggestions.find((s) => s.label === l) as Suggestion };
}

describe("analyze", () => {
  it("finds tables and aliases, including text after the cursor", () => {
    const a = analyze("select | from orders o join customers as c on c.id = o.customer_id", 7, "postgres");
    expect(a.clause).toBe("select");
    expect(a.tables.map((t) => `${t.name}:${t.alias}`)).toEqual(["orders:o", "customers:c"]);
  });
  it("detects qualifier and prefix", () => {
    const a = analyze("select o.cu from orders o", 11, "postgres");
    expect(a.qualifier).toEqual(["o"]);
    expect(a.prefix).toBe("cu");
  });
  it("handles schema-qualified tables", () => {
    const a = analyze("select * from shop.orders x where x.", 36, "postgres");
    expect(a.tables[0]).toMatchObject({ schema: "shop", name: "orders", alias: "x" });
    expect(a.clause).toBe("where");
  });
  it("only sees the statement under the cursor", () => {
    const sql = "select * from products; select | from orders";
    const a = analyze(sql, sql.indexOf("|"), "postgres");
    expect(a.tables.map((t) => t.name)).toEqual(["orders"]);
  });
  it("scopes subqueries and sees outer tables", () => {
    const sql = "select * from customers c where exists (select 1 from orders o where o.customer_id = |)";
    const a = analyze(sql.replace("|", ""), sql.indexOf("|"), "postgres");
    expect(a.tables.map((t) => t.name)).toEqual(["orders", "customers"]);
  });
  it("parses CTEs and derived tables", () => {
    const sql = "with recent(cid, n) as (select customer_id, count(*) from orders group by 1) select | from recent r join (select id, email as mail from customers) cc on cc.id = r.cid";
    const a = analyze(sql.replace("|", ""), sql.indexOf("|"), "postgres");
    expect(a.ctes[0]).toMatchObject({ name: "recent", columns: ["cid", "n"] });
    expect(a.tables).toEqual([
      expect.objectContaining({ name: "recent", alias: "r", kind: "cte", columns: ["cid", "n"] }),
      expect.objectContaining({ name: "cc", kind: "derived", columns: ["id", "mail"] }),
    ]);
  });
});

describe("complete", () => {
  it("offers statement keywords at the start", () => {
    expect(at("|").labels).toContain("SELECT");
    expect(at("sel|").labels).toContain("SELECT");
  });

  it("offers tables after FROM, schema-qualifying ones outside the default schema", () => {
    const r = at("select * from |");
    expect(r.labels).toEqual(expect.arrayContaining(["customers", "orders", "customer_orders", "public.audit_log", "shop"]));
    expect(r.find("public.audit_log").insertText).toBe("public.audit_log");
    expect(r.find("shop").retrigger).toBe(true);
  });

  it("offers tables of a schema after 'schema.'", () => {
    const r = at("select * from public.|");
    expect(r.labels).toEqual(["audit_log"]);
  });

  it("offers columns of the tables in scope in the select list", () => {
    const r = at("select | from orders");
    expect(r.labels.slice(0, 4)).toEqual(expect.arrayContaining(["id", "customer_id", "status", "placed_at"]));
    expect(r.labels).toContain("count");
  });

  it("qualifies ambiguous columns when several tables are in scope", () => {
    const r = at("select | from orders o join customers c on c.id = o.customer_id");
    const ids = r.suggestions.filter((s) => s.label === "id");
    expect(ids.map((s) => s.insertText).sort()).toEqual(["c.id", "o.id"].sort());
    expect(r.suggestions.find((s) => s.label === "email")!.insertText).toBe("email");
  });

  it("offers columns after alias dot", () => {
    const r = at("select c.| from customers c");
    expect(r.labels).toEqual(expect.arrayContaining(["id", "email", "full_name", "*"]));
    expect(r.labels).not.toContain("status");
  });

  it("offers columns of CTEs and derived tables", () => {
    expect(at("with x as (select id, email from customers) select x.| from x").labels).toEqual(["id", "email", "*"]);
    expect(at("select d.| from (select sku as code, price from products) d").labels).toEqual(["code", "price", "*"]);
  });

  it("suggests FK joins after JOIN", () => {
    const r = at("select * from customers c join |");
    const join = r.sorted[0];
    expect(join.kind).toBe("join");
    expect(join.insertText).toBe("orders o ON o.customer_id = c.id");
  });

  it("suggests FK conditions after ON", () => {
    const r = at("select * from orders o join order_items oi on |");
    expect(r.sorted[0].insertText).toBe("oi.order_id = o.id");
  });

  it("suggests aliases and clause keywords after a table", () => {
    const r = at("select * from order_items |");
    expect(r.sorted[0]).toMatchObject({ kind: "alias", label: "oi" });
    expect(r.labels).toEqual(expect.arrayContaining(["WHERE", "LEFT JOIN", "ORDER BY"]));
  });

  it("offers insert columns", () => {
    const r = at("insert into products (|)");
    expect(r.find("(all columns)").insertText).toBe("id, sku, name, price");
    expect(r.labels).toContain("sku");
  });

  it("offers columns in UPDATE SET and WHERE", () => {
    expect(at("update orders set | where id = 1").labels).toContain("status");
    expect(at("update orders set status = 'paid' where |").labels).toContain("customer_id");
    expect(at("delete from orders where |").labels).toContain("placed_at");
  });

  it("expands * into the column list", () => {
    const r = at("select *| from products");
    expect(r.find("Expand * to column list").insertText).toBe("id, sku, name, price");
  });

  it("works with MySQL quoting and databases", () => {
    const r = at("select * from `ord|", "mysql");
    expect(r.analysis.prefixQuoted).toBe(true);
    expect(r.find("orders").insertText).toBe("`orders`");
    expect(at("select * from shop.|", "mysql").labels).toContain("orders");
  });

  it("quotes identifiers that need it", () => {
    expect(quoteIdent("Order", "postgres")).toBe('"Order"');
    expect(quoteIdent("order", "postgres")).toBe('"order"');
    expect(quoteIdent("orders", "postgres")).toBe("orders");
    expect(quoteIdent("my table", "mysql")).toBe("`my table`");
  });

  it("makes short unique aliases", () => {
    expect(makeAlias("order_items", new Set())).toBe("oi");
    expect(makeAlias("OrderItems", new Set(["oi"]))).toBe("oi2");
    expect(makeAlias("orders", new Set())).toBe("o");
  });

  it("does not crash on garbage", () => {
    for (const s of ["(((", "select from where", "'unterminated", "select * from x.", "/* open", ")))", "with as ("]) {
      expect(() => complete(s, s.length, shop("postgres"))).not.toThrow();
    }
  });
});

describe("operators", () => {
  it("offers REGEXP after a column in MySQL and ranks operators first", () => {
    const r = at("select * from customers where email REG|", "mysql");
    expect(r.labels).toContain("REGEXP");
    expect(r.labels.indexOf("REGEXP")).toBeLessThan(r.labels.indexOf("email"));
    expect(at("select * from customers where email |", "mysql").sorted[0].kind).toBe("keyword");
  });
  it("offers ILIKE / SIMILAR TO in PostgreSQL", () => {
    const r = at("select * from customers where email |");
    expect(r.labels).toEqual(expect.arrayContaining(["ILIKE", "SIMILAR TO", "IS DISTINCT FROM"]));
    expect(r.labels).not.toContain("REGEXP");
  });
  it("offers values, not operators, after an operator", () => {
    const r = at("select * from customers where email = |");
    expect(r.sorted[0].kind).toBe("column");
    expect(r.labels).not.toContain("NOT LIKE");
    expect(at("select * from customers where email like 'a%' and |").sorted[0].kind).toBe("column");
  });
  it("offers operators after a closing paren and in ON", () => {
    expect(at("select * from customers where lower(email) |").labels).toContain("LIKE");
    expect(at("select * from orders o join customers c on c.id |").labels).toContain("IN");
  });
});
