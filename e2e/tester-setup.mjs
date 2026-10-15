// Sets up the tester sandbox in the browser build, then keeps the page open
// (exports are drawn by the page).
import { chromium } from "playwright";
import { readFileSync, writeFileSync } from "node:fs";
const out = process.argv[2];
const PORT = 47198;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
page.setDefaultTimeout(10000);
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
await page.goto("http://localhost:8080/");
await page.getByRole("button", { name: "Connection" }).first().click();
await page.locator(".type-card").filter({ has: page.locator(`span:text-is("SQLite")`) }).click();
await field("Connection Name").fill("games");
await field("Database File").fill(`${out}/games.db`);
await page.getByRole("button", { name: "Save" }).click();
await page.locator(".tree-row", { hasText: /games$/ }).first().waitFor();
await page.locator(".mcp-button").click();
await page.getByLabel("Port").fill(String(PORT));
await page.getByLabel("Port").press("Enter");
await page.getByLabel("Run MCP server").check();
await page.getByRole("button", { name: "New Endpoint" }).click();
await page.getByLabel("Endpoint name").fill("Tester");
await page.getByLabel("Endpoint name").blur();
await page.getByLabel("Access to games", { exact: true }).selectOption("read");
await page.getByLabel("Reports").check();
await page.waitForTimeout(500);
const token = JSON.parse(readFileSync(`${out}/config/mcp.json`, "utf8")).endpoints[0].token;
writeFileSync(`${out}/endpoint.json`, JSON.stringify({ url: `http://127.0.0.1:${PORT}/mcp`, token }, null, 2) + "\n");
console.log("ready");
// Show the BI library so opened dashboards have somewhere to go.
await page.getByRole("button", { name: "BI", exact: true }).click();
setInterval(() => page.screenshot({ path: `${out}/screen.png` }).catch(() => {}), 3000);
