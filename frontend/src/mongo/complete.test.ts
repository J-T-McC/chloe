import { describe, expect, it } from "vitest";
import { MongoCatalog, completeMongo } from "./complete";

const cat: MongoCatalog = {
  collections: ["customers", "orders", "order-items"],
  databases: ["shop", "analytics"],
  fields: (c) => (c === "orders" ? ["_id", "customer_id", "status", "items.sku", "total"] : c === "customers" ? ["_id", "email", "address.city"] : undefined),
};

function at(src: string) {
  const off = src.indexOf("|");
  const r = completeMongo(src.replace("|", ""), off, cat);
  const sorted = [...r.suggestions].sort((a, b) => a.rank - b.rank);
  return { ...r, labels: sorted.map((s) => s.label), find: (l: string) => sorted.find((s) => s.label === l)! };
}

describe("mongo completion", () => {
  it("offers collections and db methods after db.", () => {
    const r = at("db.|");
    expect(r.labels.slice(0, 3)).toEqual(["customers", "orders", "order-items"]);
    expect(r.find("order-items").insertText).toBe('getCollection("order-items")');
    expect(r.labels).toContain("getCollectionNames");
    expect(at("db.ord|").replaceStart).toBe(3);
  });
  it("offers collection methods, also via getCollection and brackets", () => {
    for (const src of ["db.orders.|", 'db.getCollection("orders").fi|', 'db["orders"].|']) {
      const r = at(src);
      expect(r.labels).toContain("find");
      expect(r.labels).toContain("aggregate");
      expect(r.collection).toBe("orders");
    }
  });
  it("offers cursor methods after find() and chained calls", () => {
    expect(at("db.orders.find({}).|").labels).toEqual(expect.arrayContaining(["sort", "limit", "toArray"]));
    expect(at("db.orders.find({ status: 'paid' }).sort({ total: -1 }).|").labels).toContain("limit");
    expect(at("db.orders.aggregate([]).|").labels).toContain("toArray");
    expect(at("db.orders.countDocuments().|").labels).toEqual([]);
  });
  it("offers fields and query operators in a filter", () => {
    const r = at("db.orders.find({ |");
    expect(r.labels.slice(0, 5)).toEqual(["_id", "customer_id", "status", "items.sku", "total"]);
    expect(r.find("items.sku").insertText).toBe('"items.sku"');
    expect(r.labels).toContain("$or");
    expect(at("db.orders.find({ status: 'paid', |").labels).toContain("customer_id");
    expect(at("db.orders.find({ total: { $g|").labels.slice(0, 1)).toEqual(expect.arrayContaining([expect.stringMatching(/^\$/)]));
    expect(at("db.orders.find({ total: { $g|").labels).toContain("$gte");
    expect(at("db.orders.find({ total: { $g|").replaceStart).toBe("db.orders.find({ total: { ".length);
  });
  it("offers update operators in the update document", () => {
    const r = at("db.orders.updateOne({ _id: 1 }, { $|");
    expect(r.labels).toContain("$set");
    expect(r.labels).not.toContain("$gt");
    expect(at("db.orders.updateOne({ _id: 1 }, { $set: { |").labels).toContain("status");
    expect(at("db.orders.updateOne({ |").labels).toContain("$and");
  });
  it("offers stages in a pipeline and expressions inside them", () => {
    const r = at("db.orders.aggregate([{ $|");
    expect(r.labels).toContain("$match");
    expect(r.labels).toContain("$group");
    expect(r.labels).not.toContain("$sum");
    expect(at("db.orders.aggregate([{ $match: { status: 'paid' } }, { $|").labels).toContain("$lookup");
    expect(at("db.orders.aggregate([{ $group: { _id: '$status', n: { $|").labels).toContain("$sum");
    expect(at("db.orders.aggregate([{ $match: { |").labels).toContain("status");
  });
  it("offers $field paths in aggregation strings", () => {
    const r = at("db.orders.aggregate([{ $group: { _id: \"$st|");
    expect(r.labels).toContain("$status");
    expect(r.replaceStart).toBe("db.orders.aggregate([{ $group: { _id: \"".length);
  });
  it("offers collections inside getCollection(\"", () => {
    expect(at('db.getCollection("|').labels).toContain("order-items");
  });
  it("offers databases after use and targets after show", () => {
    expect(at("use |").labels).toEqual(["shop", "analytics"]);
    expect(at("db.orders.find()\nshow |").labels).toContain("collections");
  });
  it("offers globals at the start of a statement and constructors in values", () => {
    expect(at("|").labels).toEqual(expect.arrayContaining(["db", "print", "ObjectId"]));
    const v = at("db.orders.find({ _id: |");
    expect(v.labels).toContain("ObjectId");
    expect(v.labels).not.toContain("db");
  });
  it("ignores strings and comments when finding the context", () => {
    expect(at("// db.\ndb.orders.find({ note: 'a { b', |").labels).toContain("status");
    expect(at("db.orders.find({ note: \"x\" /* , { */, |").labels).toContain("total");
  });
});
