import { describe, expect, it } from "vitest";
import { analyze } from "./analyze";
import { CompletionCatalog } from "./complete";
import { ARRAY, buildKeyTree, jsonKeySuggestions, jsonPathContext, pathsInText, resolveColumn } from "./jsonpath";

function ctxAt(sql: string, dialect: "mysql" | "postgres" | "sqlite") {
  const offset = sql.indexOf("|");
  return jsonPathContext(sql.replace("|", ""), offset, dialect);
}

const docs = [
  JSON.stringify({ color: "red", size: { w: 1, h: 2 }, tags: ["a"], items: [{ sku: "x", qty: 1 }], "odd key": true }),
  JSON.stringify({ color: "blue", size: { w: 3 }, items: [{ sku: "y", price: 2 }] }),
  "not json",
];
const tree = buildKeyTree(docs);

const labels = (sql: string, dialect: "mysql" | "postgres") => {
  const c = ctxAt(sql, dialect);
  expect(c).not.toBeNull();
  return jsonKeySuggestions(c!, tree);
};

describe("json path context", () => {
  it("parses SQLite json_extract, json_type and ->> paths", () => {
    expect(ctxAt("select json_extract(attrs, '$.size.|') from t", "sqlite")).toMatchObject({ column: ["attrs"], path: ["size"], style: "jsonpath" });
    expect(ctxAt("select json_type(\"attrs\", '$.co|') from t", "sqlite")).toMatchObject({ column: ["attrs"], prefix: "co" });
    expect(ctxAt("select attrs ->> '$.items[0].|' from t", "sqlite")).toMatchObject({ path: ["items", ARRAY] });
  });

  it("parses MySQL paths in JSON functions and -> operators", () => {
    expect(ctxAt("select JSON_EXTRACT(p.attrs, '$.size.|') from products p", "mysql")).toMatchObject({ column: ["p", "attrs"], path: ["size"], prefix: "", style: "jsonpath" });
    expect(ctxAt("select json_unquote(json_extract(attrs, '$.items[0].sk|'))", "mysql")).toMatchObject({ column: ["attrs"], path: ["items", ARRAY], prefix: "sk" });
    expect(ctxAt("select attrs->>'$.co|' from t", "mysql")).toMatchObject({ column: ["attrs"], path: [], prefix: "co" });
    expect(ctxAt("select JSON_SET(attrs, '$.a', 1, '$.|')", "mysql")).toMatchObject({ path: [] });
    expect(ctxAt("select JSON_SET(attrs, '$.a', '|')", "mysql")).toBeNull(); // a value, not a path
    expect(ctxAt("select JSON_EXTRACT(attrs, '|')", "mysql")).toMatchObject({ root: true });
    expect(ctxAt("select JSON_EXTRACT(attrs, '$.\"odd k|')", "mysql")).toMatchObject({ quoted: true, prefix: "odd k" });
    expect(ctxAt("select concat(attrs, '$.|')", "mysql")).toBeNull();
    expect(ctxAt("select * from t where name = '$.|'", "mysql")).toBeNull();
  });

  it("parses PostgreSQL operator chains, array paths and functions", () => {
    expect(ctxAt("select attrs->'size'->>'|' from t", "postgres")).toMatchObject({ column: ["attrs"], path: ["size"], style: "key" });
    expect(ctxAt("select t.attrs->'items'->0->>'s|' from t", "postgres")).toMatchObject({ column: ["t", "attrs"], path: ["items", ARRAY], prefix: "s" });
    expect(ctxAt("select attrs #>> '{size,|}' from t", "postgres")).toMatchObject({ path: ["size"], style: "array" });
    expect(ctxAt("select jsonb_path_query(attrs, '$.items[*].|') from t", "postgres")).toMatchObject({ path: ["items", ARRAY] });
    expect(ctxAt("select jsonb_extract_path_text(attrs, 'size', '|') from t", "postgres")).toMatchObject({ path: ["size"], style: "key" });
    expect(ctxAt("select jsonb_set(attrs, '{size,|}', '1') from t", "postgres")).toMatchObject({ path: ["size"], style: "array" });
    expect(ctxAt("select attrs @? '$.|' from t", "postgres")).toMatchObject({ path: [] });
  });
});

describe("json key suggestions", () => {
  it("ranks keys by frequency and describes their types", () => {
    const s = labels("select JSON_EXTRACT(attrs, '$.|')", "mysql");
    expect(s.map((x) => x.label).slice(0, 3)).toEqual(["color", "items", "size"]);
    expect(s.find((x) => x.label === "color")!.detail).toBe("string · 100%");
    expect(s.find((x) => x.label === "tags")!.detail).toBe("array · 50%");
    expect(labels("select JSON_EXTRACT(attrs, '$.size.|')", "mysql").map((x) => x.label)).toEqual(["w", "h"]);
  });

  it("descends into arrays and quotes awkward keys", () => {
    expect(labels("select attrs->'items'->>'|' from t", "postgres").map((x) => x.label).sort()).toEqual(["price", "qty", "sku"]);
    expect(labels("select JSON_EXTRACT(attrs, '$.items[*].|')", "mysql").map((x) => x.label)).toContain("sku");
    expect(labels("select JSON_EXTRACT(attrs, '$.|')", "mysql").find((x) => x.label === "odd key")!.insertText).toBe('"odd key"');
    expect(labels("select JSON_EXTRACT(attrs, '|')", "mysql")[0].insertText).toBe("$.color");
    expect(labels("select attrs #> '{|}' from t", "postgres").find((x) => x.label === "odd key")!.insertText).toBe('"odd key"');
  });

  it("collects paths already written in the query", () => {
    const paths = pathsInText("select JSON_EXTRACT(attrs, '$.meta.owner'), attrs->>'$.size.w' from t", "mysql");
    expect(paths.get("attrs")).toEqual([["meta", "owner"], ["size", "w"]]);
  });

  it("resolves the column through aliases", () => {
    const cat: CompletionCatalog = {
      dialect: "postgres", defaultSchema: "public", schemas: ["public"], fks: [], functions: [],
      tables: [{ schema: "public", name: "products", kind: "table", columns: [{ name: "id", type: "int" }, { name: "attrs", type: "jsonb" }] }],
    };
    const sql = "select p.attrs->'' from products p";
    const offset = sql.indexOf("''") + 1;
    const ctx = jsonPathContext(sql, offset, "postgres")!;
    expect(resolveColumn(ctx, analyze(sql, offset, "postgres"), cat)).toEqual({ schema: "public", table: "products", column: "attrs", type: "jsonb" });
  });
});
