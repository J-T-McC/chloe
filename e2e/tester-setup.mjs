// Sets up the tester sandbox in the browser build, then keeps the page open
// (exports are drawn by the page).
import { chromium } from "playwright";
import { appendFileSync, readFileSync, writeFileSync } from "node:fs";
import { MONGO, REDIS } from "./lib/db.mjs";
const out = process.argv[2];
const PORT = 47198;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
page.setDefaultTimeout(10000);
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
await page.goto(process.env.BASE ?? "http://localhost:8080/");
await page.getByRole("button", { name: "Connection" }).first().click();
await page.locator(".type-card").filter({ has: page.locator(`span:text-is("SQLite")`) }).click();
await field("Connection Name").fill("games");
await field("Database File").fill(`${out}/games.db`);
await page.getByRole("button", { name: "Save" }).click();
await page.locator(".tree-row", { hasText: /games$/ }).first().waitFor();
const redis = !!process.env.TESTER_REDIS;
const mongo = !!process.env.TESTER_MONGO;
if (mongo) {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("MongoDB")`) }).click();
  await field("Connection Name").fill("docs");
  await field("Host").fill("127.0.0.1");
  await field("Port").fill(MONGO);
  await field("User Name").fill("root");
  await field("Password").fill("chloe");
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator(".tree-row", { hasText: /docs$/ }).first().waitFor();
}
if (redis) {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("Redis")`) }).click();
  await field("Connection Name").fill("cache");
  await field("Host").fill("127.0.0.1");
  await field("Port").fill(REDIS);
  await field("Password").fill("chloe");
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator(".tree-row", { hasText: /cache$/ }).first().waitFor();
}
await page.locator(".mcp-button").click();
await page.getByLabel("Port").fill(String(PORT));
await page.getByLabel("Port").press("Enter");
await page.getByLabel("Run MCP server").check();
await page.getByRole("button", { name: "New Endpoint" }).click();
await page.getByLabel("Endpoint name").fill("Tester");
await page.getByLabel("Endpoint name").blur();
await page.getByLabel("Access to games", { exact: true }).selectOption(process.env.TESTER_WRITE ? "write" : "read");
await page.getByLabel("Reports").check();
if (process.env.TESTER_WRITE) await page.getByLabel("Saved queries and query tabs").check();
if (redis) await page.getByLabel("Access to cache", { exact: true }).selectOption("write");
if (mongo) await page.getByLabel("Access to docs", { exact: true }).selectOption("write");
if (redis || mongo) await page.getByLabel("Monitor", { exact: true }).check();
await page.waitForTimeout(500);
const token = JSON.parse(readFileSync(`${out}/config/mcp.json`, "utf8")).endpoints[0].token;
writeFileSync(`${out}/endpoint.json`, JSON.stringify({ url: `http://127.0.0.1:${PORT}/mcp`, token }, null, 2) + "\n");
console.log("ready");
// Show the BI library so opened dashboards have somewhere to go.
await page.getByRole("button", { name: "BI", exact: true }).click();
// Approve writes as they come, logging what the dialog showed.
if (redis || mongo || process.env.TESTER_WRITE) {
  (async () => {
    for (;;) {
      const dlg = page.getByRole("dialog", { name: "Approve AI change" });
      try {
        await dlg.waitFor({ timeout: 0 });
        appendFileSync(`${out}/approvals.log`, (await page.locator(".mcp-approval .modal-body").innerText()) + "\n----\n");
        await page.getByRole("button", { name: "Approve" }).click();
        await page.waitForTimeout(100);
      } catch {
        await page.waitForTimeout(200);
      }
    }
  })();
}
setInterval(() => page.screenshot({ path: `${out}/screen.png` }).catch(() => {}), 3000);
