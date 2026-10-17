import { chromium } from "playwright";
const out = process.argv[2];
const dir = process.env.LITE;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 880 } });
page.setDefaultTimeout(8000);
const errors = [];
page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
page.on("console", (m) => m.type() === "error" && errors.push("console: " + m.text()));
const shot = (n) => page.screenshot({ path: `${out}/${n}.png` });
const step = async (name, fn) => {
  try { await fn(); console.log("OK  ", name); } catch (e) { console.log("FAIL", name, e.message.split("\n")[0]); await shot("fail-" + name.replace(/\W+/g, "_")); }
};
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
const row = (re) => page.locator(".tree-row", { hasText: re });
const menu = (label) => page.locator(".menu-item", { hasText: label }).first().click();
await page.goto(process.env.BASE ?? "http://localhost:8080/");

await step("create connection", async () => {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("SQLite")`) }).click();
  await page.locator("label.check", { hasText: "New SQLite 3" }).locator("input").check();
  await field("Database File").fill(`${dir}/shop.db`);
  await page.getByRole("button", { name: "Test Connection" }).click();
  await page.locator(".status-line.ok").waitFor();
  console.log("   ", await page.locator(".status-line").innerText());
  await shot("lite-dialog");
  await page.getByRole("button", { name: "Save" }).click();
  await row("shop").first().waitFor();
});

await step("seed via query", async () => {
  await row(/shop$/).first().dblclick();
  await row(/^\s*main\s*$/).first().click();
  await page.keyboard.press("Meta+t");
  await page.locator(".monaco-editor .view-lines").last().click();
  await page.keyboard.insertText(`CREATE TABLE customers (id INTEGER PRIMARY KEY AUTOINCREMENT, email TEXT NOT NULL UNIQUE, name TEXT);
CREATE TABLE orders (id INTEGER PRIMARY KEY, customer_id INTEGER REFERENCES customers(id), total REAL, attrs JSON);
CREATE INDEX orders_cust ON orders (customer_id);
CREATE TRIGGER orders_ai AFTER INSERT ON orders BEGIN UPDATE customers SET name = name WHERE id = NEW.customer_id; END;
CREATE VIEW totals AS SELECT customer_id, sum(total) AS t FROM orders GROUP BY customer_id;
INSERT INTO customers (email, name) VALUES ('ada@x.io', 'Ada'), ('bob@x.io', 'Bob');
INSERT INTO orders (customer_id, total, attrs) VALUES (1, 9.5, '{"color":"red","size":{"w":2}}'), (2, 20, '{"color":"blue"}');
SELECT * FROM orders;`);
  await page.getByRole("button", { name: "Run", exact: true }).click();
  await page.locator(".result-grid .grid-cell").first().waitFor();
  console.log("    rows:", await page.locator(".result-grid .grid-row").count(), "| lang:", await page.evaluate(() => document.querySelector(".monaco-editor")?.getAttribute("data-mode-id")));
  await shot("lite-query");
});

await step("autocomplete", async () => {
  await page.locator(".monaco-editor .view-lines").last().click();
  await page.keyboard.press("Meta+a");
  await page.keyboard.press("Backspace");
  await page.keyboard.type("SELECT * FROM orders o WHERE o.", { delay: 20 });
  await page.locator(".suggest-widget .monaco-list-row").first().waitFor();
  const items = await page.locator(".suggest-widget .monaco-list-row .label-name").allInnerTexts();
  console.log("    cols:", items.slice(0, 6).join(", "));
  await page.keyboard.press("Escape");
  await page.keyboard.press("Meta+a");
  await page.keyboard.press("Backspace");
  await page.keyboard.type("SELECT * FROM orders WHERE json_extract(attrs, '$.", { delay: 20 });
  await page.waitForTimeout(600);
  const keys = await page.locator(".suggest-widget .monaco-list-row .label-name").allInnerTexts();
  console.log("    json keys:", keys.join(", "));
  await page.keyboard.press("Escape");
});

await step("copy quotes cells holding tabs or quotes", async () => {
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.locator(".monaco-editor .view-lines").last().click();
  await page.keyboard.press("Meta+a");
  await page.keyboard.press("Backspace");
  await page.keyboard.insertText(`SELECT 'a' || char(9) || 'b' AS x, 'say "hi"' AS y, 'plain' AS z`);
  await page.getByRole("button", { name: "Run", exact: true }).click();
  const cells = page.locator(".result-grid .grid-row").first().locator(".grid-cell");
  await cells.nth(2).waitFor();
  await page.waitForTimeout(300);
  await cells.nth(0).click();
  await cells.nth(2).click({ modifiers: ["Shift"] });
  await page.keyboard.press("Meta+c");
  await page.waitForTimeout(200);
  const block = await page.evaluate(() => navigator.clipboard.readText());
  if (block !== '"a\tb"\t"say ""hi"""\tplain') throw new Error("block: " + JSON.stringify(block));
  await cells.nth(1).click();
  await page.keyboard.press("Meta+c");
  await page.waitForTimeout(200);
  const one = await page.evaluate(() => navigator.clipboard.readText());
  if (one !== 'say "hi"') throw new Error("one cell: " + JSON.stringify(one));
});

await step("tree folders", async () => {
  await row(/^\s*main\s*$/).first().dblclick();
  const labels = await page.locator(".tree-row").allInnerTexts();
  console.log("    tree:", labels.map((l) => l.trim()).join(" | "));
  await row(/^\s*Indexes\s*$/).first().dblclick();
  await row(/^\s*Triggers\s*$/).first().dblclick();
  await row(/^\s*Tables\s*$/).first().dblclick();
  await row(/orders_cust/).first().waitFor();
  await row(/orders_ai/).first().waitFor();
  await shot("lite-tree");
});

await step("open table + edit", async () => {
  await row(/^\s*customers\s*$/).first().dblclick();
  await page.locator(".viewer .grid-cell").first().waitFor();
  const cell = page.locator(".viewer .grid-row").nth(1).locator(".grid-cell").nth(2);
  await cell.dblclick();
  await page.keyboard.press("Meta+a");
  await page.keyboard.type("Robert");
  await page.keyboard.press("Enter");
  await page.locator(".viewer .statusbar button[title^='Apply changes']").click();
  await page.waitForTimeout(500);
  console.log("    status:", await page.locator(".viewer .sql-preview").innerText());
});

await step("design table (rebuild)", async () => {
  await row(/^\s*orders\s*$/).first().click({ button: "right" });
  await menu("Design Table");
  await page.locator(".fields-grid tbody tr").first().waitFor();
  const hdr = await page.locator(".fields-grid thead th").allInnerTexts();
  console.log("    columns:", hdr.filter(Boolean).join(", "));
  // total REAL -> NUMERIC(10,2) forces a rebuild
  const r = page.locator(".fields-grid tbody tr").nth(2);
  await r.locator("td").nth(2).locator("input").fill("NUMERIC");
  await r.locator("td").nth(3).locator("input").fill("10");
  await r.locator("td").nth(4).locator("input").fill("2");
  await page.locator(".designer .seg-tabs button", { hasText: "SQL Preview" }).click();
  await page.waitForTimeout(400);
  console.log("    preview:", (await page.locator(".designer .code-block, .designer pre").first().innerText()).split("\n").slice(0, 3).join(" ⏎ "));
  await shot("lite-design-sql");
  await page.keyboard.press("Meta+s");
  await page.waitForTimeout(800);
  const err = await page.locator(".designer .status-line.err").count();
  console.log("    saved, error:", err ? await page.locator(".designer .status-line.err").innerText() : "none");
  for (const t of ["Triggers", "Options", "Indexes"]) {
    await page.locator(".designer .seg-tabs button", { hasText: t }).click();
    await shot("lite-design-" + t.toLowerCase());
  }
});

await step("attach database", async () => {
  await row(/^\s*main\s*$/).first().click({ button: "right" });
  await menu("Attach Database");
  await field("Database Name").fill("archive");
  await page.locator(".database-dialog .field-input input").nth(1).fill(`${dir}/archive.db`);
  await page.locator("label.check", { hasText: "Create a new database file" }).locator("input").check();
  await page.getByRole("button", { name: "OK" }).click();
  await row(/^\s*archive\s*$/).first().waitFor();
  await row(/^\s*archive\s*$/).first().click({ button: "right" });
  const items = await page.locator(".menu-item").allInnerTexts();
  console.log("    db menu:", items.map((s) => s.trim()).filter(Boolean).join(" | "));
  await page.keyboard.press("Escape");
});

await step("conn menu", async () => {
  await row(/shop$/).first().click({ button: "right" });
  const items = await page.locator(".menu-item").allInnerTexts();
  console.log("    conn menu:", items.map((s) => s.trim()).filter(Boolean).join(" | "));
  await page.keyboard.press("Escape");
});

console.log("errors:", errors.length ? errors.join("\n") : "none");
await browser.close();
