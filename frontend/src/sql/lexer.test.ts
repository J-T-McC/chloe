import { describe, expect, it } from "vitest";
import { tokenize } from "./lexer";

describe("tokenize", () => {
  it("lexes the same text once per dialect", () => {
    const sql = "select `a` from t -- x";
    const a = tokenize(sql, "mysql");
    expect(tokenize(sql, "mysql")).toBe(a);
    // In PostgreSQL ` isn't a quote, so the result differs and isn't shared.
    expect(tokenize(sql, "postgres")).not.toBe(a);
    expect(tokenize(sql + " ", "mysql")).not.toBe(a);
    expect(tokenize(sql, "mysql")).toBe(a);
  });
});
