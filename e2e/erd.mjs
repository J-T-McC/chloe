import { chromium } from "playwright";
import { MYSQL, PG } from "./lib/db.mjs";
const out = process.argv[2];
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 880 } });
page.setDefaultTimeout(10000);
const errors = [];
page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
page.on("console", (m) => m.type() === "error" && errors.push("console: " + m.text()));
const shot = (n) => page.screenshot({ path: `${out}/${n}.png` });
const step = async (name, fn) => { try { await fn(); console.log("OK  ", name); } catch (e) { console.log("FAIL", name, e.message.split("\n")[0]); await shot("fail-" + name.replace(/\W+/g, "_")); } };
await page.goto(process.env.BASE ?? "http://localhost:8080/");
for (const [label, type, port, user] of [["Local MySQL", "MySQL", MYSQL, "root"], ["Local Postgres", "PostgreSQL", PG, "postgres"]]) {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("${type}")`) }).click();
  const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
  await field("Connection Name").fill(label); await field("Host").fill("127.0.0.1"); await field("Port").fill(port); await field("User Name").fill(user); await field("Password").fill("chloe");
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator(".tree-row", { hasText: label }).waitFor();
}
await step("mysql erd", async () => {
  await page.locator(".tree-row", { hasText: "Local MySQL" }).dblclick();
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).click({ button: "right" });
  await page.locator(".menu-item", { hasText: "ER Diagram" }).click();
  await page.locator(".erd-table").first().waitFor();
  await page.waitForTimeout(800);
  await shot("e1-mysql-erd");
  const n = await page.locator(".erd-table").count();
  const edges = await page.locator(".react-flow__edge").count();
  if (n !== 4 || edges !== 3) throw new Error(`tables ${n} edges ${edges}`);
});
await step("select + keys only", async () => {
  await page.locator(".erd-table", { hasText: "orders" }).first().click();
  await page.locator(".seg button", { hasText: "Keys Only" }).click();
  await page.waitForTimeout(400);
  await shot("e2-keys-selected");
});
await step("pg erd", async () => {
  await page.locator(".tree-row", { hasText: "Local Postgres" }).dblclick();
  await page.locator(".tree-row", { hasText: /^\s*postgres\s*$/ }).first().dblclick();
  await page.locator(".tree-row", { hasText: "pg_catalog" }).waitFor();
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).last().click({ button: "right" });
  await page.locator(".menu-item", { hasText: "ER Diagram" }).click();
  await page.locator(".tab", { hasText: "ER — shop" }).last().waitFor();
  await page.waitForTimeout(1000);
  await shot("e3-pg-erd");
});
await step("designer split", async () => {
  await page.locator(".erd-table", { hasText: "customers" }).last().dblclick();
  await page.locator(".fields-grid tbody tr").nth(0).click();
  await page.waitForTimeout(300);
  const handle = page.locator(".designer .split-handle").first();
  const box = await handle.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + 2);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width / 2, box.y - 250, { steps: 8 });
  await page.mouse.up();
  await page.waitForTimeout(300);
  await shot("e4-designer-split");
});
console.log(errors.length ? "ERRORS:\n" + errors.join("\n") : "no page errors");
await browser.close();
