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
      { schema, name: "orders", kind: "table", columns: [col("id", "bigint", true), col("customer_id"), { ...col("status", "order_status"), enumValues: ["new", "paid", "shipped"] }, col("placed_at", "timestamp")] },
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
      { name: "FROM_UNIXTIME", builtin: true },
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

describe("select list", () => {
  it("ranks FROM first after SELECT *", () => {
    for (const sql of ["select * fro|", "select * |", "select c.* fro| ", "select id, email fro|"]) {
      const r = at(sql, "mysql");
      expect(r.sorted[0].label).toBe("FROM");
      expect(r.labels.indexOf("FROM")).toBeLessThan(r.labels.findIndex((l) => l.startsWith("FROM_")));
    }
  });
  it("still offers functions after an operator", () => {
    expect(at("select 2 * |", "mysql").labels).toContain("COUNT");
  });
});

describe("enum values", () => {
  const enumsAt = (sql: string, dialect: "mysql" | "postgres" = "postgres") => {
    const offset = sql.indexOf("|");
    const r = complete(sql.replace("|", ""), offset, shop(dialect));
    return r.suggestions.filter((s) => s.kind === "enum").map((s) => s.insertText);
  };
  it("offers quoted values after a comparison", () => {
    expect(enumsAt("select * from orders where status = |")).toEqual(["'new'", "'paid'", "'shipped'"]);
    expect(enumsAt("select * from orders o where o.status <> |", "mysql")).toEqual(["'new'", "'paid'", "'shipped'"]);
  });
  it("offers bare values inside a string, and only those", () => {
    const offset = "select * from orders o where o.status = 'pa".length;
    const r = complete("select * from orders o where o.status = 'pa'", offset, shop("postgres"));
    expect(r.suggestions.map((s) => s.insertText)).toEqual(["new", "paid", "shipped"]);
    expect(r.replaceStart).toBe(offset - 2);
  });
  it("works in IN lists and UPDATE ... SET", () => {
    expect(enumsAt("select * from orders where status in ('new', '|')")).toEqual(["new", "paid", "shipped"]);
    expect(enumsAt("select * from orders where status not in (|")).toEqual(["'new'", "'paid'", "'shipped'"]);
    expect(enumsAt("update orders set status = '|'")).toEqual(["new", "paid", "shipped"]);
  });
  it("stays out of the way elsewhere", () => {
    expect(enumsAt("select * from orders where status = 'paid' and |")).toEqual([]);
    expect(enumsAt("select * from orders where customer_id = |")).toEqual([]);
  });
});

describe("sqlite dialect", () => {
  it("quotes identifiers with double quotes", () => {
    expect(quoteIdent("order items", "sqlite")).toBe('"order items"');
    expect(quoteIdent("Orders", "sqlite")).toBe("Orders");
    expect(quoteIdent("select", "sqlite")).toBe('"select"');
  });
  it("offers attached databases and SQLite starters", () => {
    const cat: CompletionCatalog = {
      dialect: "sqlite",
      defaultSchema: "main",
      schemas: ["main", "archive"],
      tables: [{ schema: "main", name: "orders", kind: "table", columns: [{ name: "id", type: "INTEGER", pk: true }] }],
      fks: [],
      functions: [],
    };
    const from = complete("select * from ", 14, cat).suggestions.map((s) => s.label);
    expect(from).toContain("orders");
    expect(from).toContain("archive");
    const start = complete("PRA", 3, cat).suggestions.map((s) => s.label);
    expect(start).toContain("PRAGMA");
    const cols = complete("select [o].| from orders o".replace("|", ""), 10, cat).suggestions.map((s) => s.label);
    expect(cols).toContain("id");
  });
  it("offers only operators and keywords the dialect has", () => {
    const cat = (dialect: "mysql" | "postgres" | "sqlite"): CompletionCatalog => ({
      dialect,
      defaultSchema: "main",
      schemas: [],
      tables: [{ schema: "main", name: "t", kind: "table", columns: [{ name: "email", type: "TEXT" }] }],
      fks: [],
      functions: [],
    });
    const at = (sql: string, d: "mysql" | "postgres" | "sqlite") => complete(sql, sql.length, cat(d)).suggestions.map((s) => s.label);
    const ops = at("select * from t where email ", "sqlite");
    expect(ops).toContain("GLOB");
    expect(ops).not.toContain("REGEXP");
    expect(ops).not.toContain("MATCH");
    expect(ops).not.toContain("ILIKE");
    expect(at("select * from t where email ", "mysql")).toContain("REGEXP");
    expect(at("select * from t where ", "sqlite")).not.toContain("INTERVAL");
    expect(at("", "sqlite")).not.toContain("TRUNCATE TABLE");
    expect(at("select * from t order by email ", "mysql")).not.toContain("NULLS FIRST");
    expect(at("select * from t order by email ", "sqlite")).toContain("NULLS FIRST");
    expect(at("select email from t group by email ", "sqlite")).not.toContain("WITH ROLLUP");
    expect(at("select * from t ", "mysql")).not.toContain("FULL OUTER JOIN");
  });
});

describe("joins", () => {
  const on = "select * from customers c join orders o on o.customer_id = c.id |";
  it("offers every join type the dialect has after an ON condition", () => {
    expect(at(on, "mysql").labels).toEqual(expect.arrayContaining(["CROSS JOIN", "RIGHT JOIN", "JOIN"]));
    expect(at(on, "mysql").labels).not.toContain("FULL OUTER JOIN");
    expect(at(on).labels).toContain("FULL OUTER JOIN");
  });
  it("completes the rest of a join after its modifier", () => {
    expect(at("select * from customers c cross |", "mysql").labels).toEqual(["JOIN"]);
    expect(at("select * from customers c left |", "mysql").labels).toEqual(["JOIN", "OUTER JOIN"]);
    expect(at("select * from customers c full |", "mysql").labels).not.toContain("OUTER JOIN");
    expect(at("select * from customers c full |").labels).toContain("OUTER JOIN");
  });
  it("suggests tables without a join condition after CROSS JOIN", () => {
    const r = at("select * from customers c cross join |", "mysql");
    expect(r.labels).toContain("orders");
    expect(r.sorted.some((s) => s.kind === "join")).toBe(false);
    expect(at("select * from customers c left join |", "mysql").sorted.some((s) => s.kind === "join")).toBe(true);
  });
  it("keeps offering joins, UNION and the later clauses after several ON conditions", () => {
    const q = "select * from customers c left join orders o on o.customer_id = c.id\ninner join orders o2 on o2.customer_id = c.id\n";
    for (const d of ["mysql", "postgres"] as const) {
      expect(at(q + "un|", d).labels).toEqual(expect.arrayContaining(["UNION", "UNION ALL", "CROSS JOIN", "LIMIT", "HAVING"]));
    }
  });
  it("expects a SELECT after UNION", () => {
    const q = "select id from customers c join orders o on o.customer_id = c.id union |";
    expect(analyze(q, q.length, "mysql").clause).toBe("selectStart");
    expect(at(q.replace(/\|?$/, "|"), "mysql").labels).toEqual(["SELECT", "ALL", "DISTINCT"]);
    expect(at("select id from customers union all |").labels).toEqual(["SELECT"]);
  });
});

// Odd query shapes: each row is [query with "|" at the cursor, dialect, labels that must be
// offered, labels that must not be]. Exclusions catch tables leaking in from the wrong scope.
describe("aliases, subqueries, unions and CTEs", () => {
  const C = ["id", "email", "full_name"]; // customers
  const CX = ["email", "full_name"]; // only customers has these
  const O = ["customer_id", "status", "placed_at"]; // orders
  const P = ["sku", "name", "price"]; // products
  const I = ["order_id", "product_id", "qty"]; // order_items
  type Row = [string, "mysql" | "postgres", string[], string[]];
  const rows: Row[] = [
    // derived tables: select lists, stars, joins and unions inside
    ["select x.| from (select * from customers) x", "postgres", C, O],
    ["select x.| from (select c.*, o.status from customers c join orders o on o.customer_id = c.id) x", "postgres", [...C, "status"], ["placed_at"]],
    ["select u.| from (select id, email from customers union all select id, sku from products) u", "postgres", ["id", "email"], ["sku", "full_name"]],
    ["select u.| from (select * from customers union select * from customers) u", "postgres", C, P],
    ["select b.| from (select * from (select id, full_name from customers) a) b", "postgres", ["id", "full_name"], ["email"]],
    ["select x.| from (select id, email from customers) as x(a, b)", "postgres", ["id", "email"], O],
    ["select * from customers c join (select customer_id, count(*) as n from orders group by customer_id) t on t.|", "postgres", ["customer_id", "n"], CX],
    ["select * from customers c right join (select customer_id, max(placed_at) last_at from orders group by customer_id) r on r.|", "postgres", ["customer_id", "last_at"], ["placed_at"]],
    ["select * from (select * from customers c join orders o on o.|) x", "postgres", O, ["email"]],
    ["select * from (select * from customers c join orders o on o.customer_id = c.id where o.|) x", "postgres", O, ["email"]],
    ["select * from (select | from customers c join orders o on o.customer_id = c.id) x", "postgres", [...C, ...O, "c", "o"], ["x"]],
    ["select * from (select id from customers union select p.| from products p) u", "postgres", P, ["email"]],
    ["select * from (select * from orders) o join customers c on c.id = o.customer_id where o.|", "postgres", O, ["email"]],
    ["select x.| from customers c cross join lateral (select * from orders o where o.customer_id = c.id limit 1) x", "postgres", O, ["email"]],
    ["select x.| from customers c, lateral (select * from orders o where o.customer_id = c.id) x", "postgres", O, ["email"]],
    // unions: each SELECT sees only its own tables
    ["select c.id from customers c union select p.| from products p", "postgres", P, ["email"]],
    ["select c.| from customers c union select p.id from products p", "postgres", C, P],
    ["select x.| from customers x union select x.sku from products x", "postgres", C, P],
    ["select x.id from customers x union select x.| from products x", "postgres", P, ["email"]],
    ["select id from customers c union select | from products p", "postgres", [...P, "p"], ["email", "c"]],
    ["(select c.id from customers c) union (select p.| from products p)", "postgres", P, ["email"]],
    ["select id from customers union select id from products order by |", "postgres", ["id"], ["sku", "email"]],
    // correlated and nested subqueries see enclosing queries, innermost alias first
    ["select c.id, (select count(*) from orders o where o.customer_id = c.|) from customers c", "postgres", C, O],
    ["select * from customers c where c.id in (select o.| from orders o)", "postgres", O, ["email"]],
    ["select * from customers c where exists (select 1 from orders c where c.|)", "postgres", O, ["email"]],
    ["select * from customers c where c.id = (select max(o.|) from orders o)", "postgres", O, ["email"]],
    ["select * from customers c where c.id in (select o.customer_id from orders o where o.id in (select i.order_id from order_items i where i.qty > c.|))", "postgres", C, I],
    ["select * from customers c join orders o on o.customer_id = (select max(c2.id) from customers c2 where c2.|)", "postgres", C, O],
    ["select (select max(x.|) from (select id from orders) x) from customers", "postgres", ["id"], ["email"]],
    // statements still being typed (no closing paren yet)
    ["select * from (select * from customers c where c.|", "postgres", C, O],
    ["select * from customers c where c.id in (select o.| from orders o", "postgres", O, ["email"]],
    ["select * from customers c where exists (select 1 from orders o join order_items i on i.|", "postgres", I, ["email"]],
    // CTEs
    ["with x as (select id, email from customers union select id, sku from products) select x.| from x", "postgres", ["id", "email"], ["sku"]],
    ["with a as (select id, email from customers), b as (select a.| from a) select * from b", "postgres", ["id", "email"], ["full_name"]],
    ["with a as (select * from customers) select a.| from a", "postgres", C, O],
    ["with a(k, v) as (select id, email from customers) select a.| from a", "postgres", ["k", "v"], ["email"]],
    ["with recursive t(n) as (select 1 union all select n + 1 from t where n < 5) select t.| from t", "postgres", ["n"], CX],
    ["with orders as (select id, sku from products) select orders.| from orders", "postgres", ["id", "sku"], ["status"]],
    // aliases: AS, quoted, keyword-named, schema- or database-qualified tables
    ["select o.| from orders as o", "postgres", O, CX],
    ['select "order".| from orders "order"', "postgres", O, CX],
    ["select `order`.| from orders `order`", "mysql", O, CX],
    ["select orders.| from customers orders", "postgres", C, O],
    ["select o.| from shop.orders o", "postgres", O, CX],
    ["select o.| from shop.orders o", "mysql", O, CX],
    ["select a.| from public.audit_log a", "postgres", ["message"], CX],
    ["select c.| from customers -- note\n c", "postgres", C, O],
    ["select c.| from customers /* x */ c", "postgres", C, O],
    ["select a.| from customers a, orders b where a.id = b.customer_id", "postgres", C, O],
    ["select i.| from orders o straight_join order_items i on i.order_id = o.id", "mysql", I, CX],
    // several statements: only the one under the cursor counts
    ["select * from products p; select o.| from orders o", "postgres", O, P],
    ["select * from (((; select o.| from orders o", "postgres", O, P],
    // clauses
    ["select * from orders o join order_items i using (|", "postgres", I, ["i.order_id = o.id"]],
    ["select o.status as s from orders o order by |", "postgres", ["s", ...O], []],
    ["select date(o.placed_at) as day, count(*) from orders o group by |", "postgres", ["day", ...O], []],
    ["select row_number() over (partition by o.| order by o.id) from orders o", "postgres", O, CX],
    ["update orders o join customers c on c.id = o.customer_id set o.|", "mysql", O, ["email"]],
    ["delete o from orders o join customers c on c.id = o.customer_id where c.|", "mysql", C, O],
    ["delete from orders o using customers c where c.|", "postgres", C, O],
    ["insert into orders (customer_id) select c.| from customers c join orders o on o.customer_id = c.id", "postgres", C, O],
    ["insert into orders (customer_id, status) values (|", "postgres", ["NULL", "DEFAULT"], ["status"]],
    ["select * from (|", "postgres", ["SELECT", "WITH", "VALUES"], ["customers"]],
    ["select * from customers c join (|", "mysql", ["SELECT"], ["VALUES", "email"]],
    ["select * from customers c where c.id in (|", "postgres", ["SELECT"], []],
  ];
  for (const [sql, d, want, not] of rows) {
    it(`${d}: ${sql.replace(/\s+/g, " ")}`, () => {
      const labels = at(sql, d).labels;
      expect(labels).toEqual(expect.arrayContaining(want));
      for (const n of not) expect(labels).not.toContain(n);
    });
  }
});
