// README screenshots: e2e/run.sh screenshots, then copy e2e/.out/shots/readme-*.png to docs/assets/screenshots/.
import { chromium } from "playwright";
import { readFileSync } from "node:fs";

const out = process.argv[2];
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1360, height: 820 }, deviceScaleFactor: 2, colorScheme: "dark" });
page.setDefaultTimeout(10000);
page.on("pageerror", (e) => console.log("PAGE ERROR", e.message));

const shot = (n) => page.screenshot({ path: `${out}/readme-${n}.png` });
const step = async (name, fn) => {
  try {
    await fn();
    console.log("OK  ", name);
  } catch (e) {
    console.log("FAIL", name, e.message.split("\n")[0]);
    await page.screenshot({ path: `${out}/fail-${name.replace(/\W+/g, "_")}.png` });
  }
};
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
const row = (text) => page.locator(".tree-row", { hasText: text });

await page.goto("http://localhost:8080/");
for (const [label, type, port, user] of [
  ["MySQL", "MySQL", "33306", "root"],
  ["PostgreSQL", "PostgreSQL", "55432", "postgres"],
  ["MongoDB", "MongoDB", "37017", "root"],
  ["Redis", "Redis", "63790", ""],
]) {
  await step(`create ${label}`, async () => {
    await page.getByRole("button", { name: "Connection" }).first().click();
    await page.locator(".type-card").filter({ has: page.locator(`span:text-is("${type}")`) }).click();
    await field("Connection Name").fill(label);
    await field("Host").fill("127.0.0.1");
    await field("Port").fill(port);
    if (user) await field("User Name").fill(user);
    await field("Password").fill("chloe");
    if (label === "PostgreSQL") {
      await page.getByRole("button", { name: "Test Connection" }).click();
      await page.locator(".status-line.ok").waitFor();
      await shot("connection");
    }
    await page.getByRole("button", { name: "Save" }).click();
    await row(label).first().waitFor();
  });
}

const close = async (conn) => {
  await row(conn).first().click({ button: "right" });
  await page.locator(".menu-item", { hasText: "Close Connection" }).click();
  await page.waitForTimeout(300);
};

await step("data grid", async () => {
  await row(/MySQL\s*$/).first().dblclick();
  await row(/^\s*shop\s*$/).first().dblclick();
  await row("Tables").first().dblclick();
  await row(/^\s*orders\s*$/).first().dblclick();
  await page.locator(".grid-cell").first().waitFor();
  await page.waitForTimeout(600);
  await shot("grid");
});

await step("table designer", async () => {
  await page.locator(".tab", { hasText: "Objects" }).click();
  await page.locator(".object-list tr", { hasText: "orders" }).first().click();
  await page.getByRole("button", { name: "Design Table" }).click();
  await page.locator(".fields-grid tbody tr").first().waitFor();
  await page.locator(".fields-grid tbody tr").nth(2).click();
  await page.waitForTimeout(500);
  await shot("designer");
});

await step("er diagram", async () => {
  await row(/^\s*shop\s*$/).first().click({ button: "right" });
  await page.locator(".menu-item", { hasText: "ER Diagram" }).click();
  await page.locator(".erd-table").first().waitFor();
  await page.waitForTimeout(1000);
  await shot("erd");
  await close(/MySQL\s*$/);
});

await step("model library", async () => {
  await row(/MySQL\s*$/).first().dblclick();
  await row(/^\s*shop\s*$/).first().click({ button: "right" });
  await page.locator(".menu-item", { hasText: "Reverse Database to Model" }).click();
  await page.locator(".model-editor .erd-table").first().waitFor();
  await page.locator(".model-editor:visible").getByRole("button", { name: "Save", exact: true }).click();
  await page.locator(".modal.small input").fill("Shop");
  await page.locator(".modal.small").getByRole("button", { name: "OK" }).click();
  await page.locator(".tab.active", { hasText: "Shop (model)" }).waitFor();
  await page.locator(".tool-button", { hasText: "Model" }).click();
  await page.locator(".models-list tbody tr", { hasText: "Shop" }).click();
  await page.locator(".info-panel .model-tables").waitFor();
  await page.waitForTimeout(400);
  await shot("models");
  await close(/MySQL\s*$/);
});

await step("query editor", async () => {
  await row(/PostgreSQL\s*$/).first().dblclick();
  await row(/^\s*postgres\s*$/).first().dblclick();
  await row("pg_catalog").waitFor();
  await row(/^\s*shop\s*$/).last().click();
  await page.getByRole("button", { name: "New Query" }).first().click();
  const editor = page.locator(".monaco-editor").last();
  await editor.locator(".view-lines").click();
  // Typed line by line: Monaco auto-indents after Enter.
  for (const line of [
    "SELECT c.full_name, count(o.id) AS orders, sum(oi.qty) AS items",
    "FROM customers c",
    "JOIN orders o ON o.customer_id = c.id",
    "JOIN order_items oi ON oi.order_id = o.id",
    "GROUP BY c.full_name",
    "ORDER BY orders DESC;",
  ]) {
    await page.keyboard.type(line, { delay: 2 });
    await page.keyboard.press("Escape");
    await page.keyboard.press("Enter");
    await page.keyboard.press("Home");
  }
  await page.getByRole("button", { name: "Beautify" }).click();
  await page.waitForTimeout(300);
  await editor.locator(".view-lines").click();
  await page.keyboard.press("Meta+R");
  await page.locator(".result-tabs button", { hasText: "Result 1" }).waitFor();
  await editor.locator(".view-lines").click();
  await page.keyboard.press("Meta+ArrowDown");
  await page.keyboard.type("\n\nSELECT * FROM orders o WHERE o.", { delay: 15 });
  await page.locator(".suggest-widget .monaco-list-row").first().waitFor();
  await page.waitForTimeout(500);
  await shot("query");
  await page.keyboard.press("Escape");
  await close(/PostgreSQL\s*$/);
});

await step("mongodb", async () => {
  await row(/MongoDB\s*$/).first().dblclick();
  await row(/^\s*shop\s*$/).first().waitFor();
  await row(/^\s*shop\s*$/).first().dblclick();
  await row("Collections").first().dblclick();
  await row("Collections").first().click();
  await page.locator(".object-list tr", { hasText: "customers" }).dblclick();
  await page.locator(".tab-pane:visible .mode-switch button[title='Tree View']").click();
  await page.locator(".tab-pane:visible .doc-tree-row").first().dblclick();
  await page.waitForTimeout(800);
  await shot("mongo");
  await close(/MongoDB\s*$/);
});

await step("redis", async () => {
  await row(/Redis\s*$/).first().dblclick();
  await row("db0").click();
  await page.locator(".redis-key-row").first().waitFor();
  await page.locator(".redis-key-row", { hasText: /^\s*user/ }).first().click();
  await page.locator(".redis-key-row", { hasText: /HASH\s*1$/ }).first().click();
  await page.waitForTimeout(600);
  await shot("redis");
});

// MCP: an endpoint's access, then a write waiting for approval (denied).
const MCP_URL = "http://127.0.0.1:47199/mcp";
const token = () => JSON.parse(readFileSync(`${process.env.LITE}/../config/mcp.json`, "utf8")).endpoints[0].token;
const callTool = (name, args) =>
  fetch(MCP_URL, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream", Authorization: `Bearer ${token()}`, "MCP-Protocol-Version": "2025-06-18" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/call", params: { name, arguments: args } }),
  }).then((r) => r.text());

await step("mcp server", async () => {
  await page.locator(".mcp-button").click();
  await page.getByLabel("Port").fill("47199");
  await page.getByLabel("Port").press("Enter");
  await page.getByLabel("Run MCP server").check();
  await page.getByText(`Listening on ${MCP_URL}`).waitFor();
  await page.getByRole("button", { name: "New Endpoint" }).click();
  await page.getByLabel("Endpoint name").fill("Claude Code");
  await page.getByLabel("Endpoint name").blur();
  await page.locator(".mcp-list-row", { hasText: "Claude Code" }).waitFor();
  await page.getByLabel("Access to MySQL", { exact: true }).selectOption("write");
  await page.getByLabel("Access to PostgreSQL", { exact: true }).selectOption("read");
  await page.getByLabel("Saved queries and query tabs").check();
  await page.getByLabel("ER models").check();
  // The browser build runs from bin/; show the path the installed app reports instead.
  await page.locator(".mcp-snippet").evaluate((n) => (n.textContent = n.textContent.replace(/"[^"]*\/bin\/Chloe-server"/, '"/Applications/Chloe.app/Contents/MacOS/Chloe"')));
  if (!(await page.locator(".mcp-snippet").innerText()).includes("/Applications/Chloe.app/Contents/MacOS/Chloe")) throw new Error("snippet: " + (await page.locator(".mcp-snippet").innerText()));
  await page.waitForTimeout(400);
  await shot("mcp");
});

await step("mcp approval", async () => {
  const pending = callTool("update_rows", { connection: "MySQL", database: "shop", table: "products", where: [{ column: "id", op: "=", value: 1 }], set: { price: "44.99" } });
  await page.getByRole("dialog", { name: "Approve AI change" }).waitFor();
  await page.waitForTimeout(300);
  await shot("mcp-approval");
  await page.getByRole("button", { name: "Deny" }).click();
  await pending;
  await page.getByLabel("Run MCP server").uncheck();
});

await browser.close();
