// Sample MongoDB data, matching the shop databases of the SQL seeds.
//   docker exec -i chloe-mongo mongosh -u root -p chloe --quiet < testdata/seed_mongo.js
db = db.getSiblingDB("shop");
db.dropDatabase();

db.createCollection("customers", {
  validator: { $jsonSchema: { bsonType: "object", required: ["email"], properties: { email: { bsonType: "string" } } } },
  validationLevel: "moderate",
});
db.customers.insertMany([
  { _id: 1, email: "ada@example.com", full_name: "Ada Lovelace", created_at: ISODate("2024-01-05T09:00:00Z"),
    address: { city: "London", country: "UK" }, tags: ["vip", "early"] },
  { _id: 2, email: "alan@example.com", full_name: "Alan Turing", created_at: ISODate("2024-02-11T14:30:00Z"),
    address: { city: "Manchester", country: "UK" }, tags: [] },
  { _id: 3, email: "grace@example.com", full_name: "Grace Hopper", created_at: ISODate("2024-03-20T08:15:00Z"),
    address: { city: "New York", country: "US" }, tags: ["vip"] },
  { _id: 4, email: "linus@example.com", full_name: "Linus Torvalds", created_at: ISODate("2024-04-01T00:00:00Z") },
]);
db.customers.createIndex({ email: 1 }, { unique: true });

db.products.insertMany([
  { sku: "KB-01", name: "Keyboard", price: NumberDecimal("49.90"), stock: 120, attrs: { color: "black", layout: "ISO" } },
  { sku: "MS-02", name: "Mouse", price: NumberDecimal("19.99"), stock: 300, attrs: { color: "white", dpi: 1600 } },
  { sku: "MN-27", name: "Monitor 27\"", price: NumberDecimal("329.00"), stock: 15, attrs: { size: 27, panel: "IPS" } },
  { sku: "CB-USB", name: "USB-C cable", price: NumberDecimal("9.50"), stock: 0, discontinued: true },
]);
db.products.createIndex({ sku: 1 }, { unique: true });
db.products.createIndex({ name: "text" });

const p = {};
db.products.find().forEach((d) => { p[d.sku] = d; });
const statuses = ["new", "paid", "shipped"];
const orders = [];
for (let i = 1; i <= 60; i++) {
  const items = [{ sku: "KB-01", qty: 1 + (i % 2) }];
  if (i % 3 === 0) items.push({ sku: "MS-02", qty: 2 });
  if (i % 7 === 0) items.push({ sku: "MN-27", qty: 1 });
  orders.push({
    customer_id: 1 + (i % 4),
    status: statuses[i % 3],
    placed_at: new Date(Date.UTC(2024, 4, 1) + i * 36e5 * 7),
    items: items.map((it) => ({ sku: it.sku, qty: it.qty, price: p[it.sku].price })),
    total: NumberDecimal(String(items.reduce((s, it) => s + it.qty * Number(p[it.sku].price.toString()), 0).toFixed(2))),
    ...(i % 10 === 0 ? { note: "gift wrap" } : {}),
  });
}
db.orders.insertMany(orders);
db.orders.createIndex({ customer_id: 1, placed_at: -1 });
db.orders.createIndex({ placed_at: 1 }, { name: "placed_at_ttl_demo", partialFilterExpression: { status: "new" } });

db.createView("paid_orders", "orders", [
  { $match: { status: "paid" } },
  { $lookup: { from: "customers", localField: "customer_id", foreignField: "_id", as: "customer" } },
  { $project: { placed_at: 1, total: 1, customer: { $first: "$customer.full_name" } } },
]);

db.createCollection("events", { capped: true, size: 1048576, max: 1000 });
for (let i = 0; i < 25; i++) {
  db.events.insertOne({ at: new Date(Date.UTC(2024, 5, 1, 12, i)), kind: i % 5 === 0 ? "error" : "info", msg: "event " + i, n: NumberLong(i) });
}

db = db.getSiblingDB("analytics");
db.dropDatabase();
db.createCollection("pageviews", { timeseries: { timeField: "ts", metaField: "page", granularity: "minutes" } });
for (let i = 0; i < 30; i++) {
  db.pageviews.insertOne({ ts: new Date(Date.UTC(2024, 5, 2, 10, i)), page: ["/", "/shop", "/about"][i % 3], ms: 20 + i });
}
