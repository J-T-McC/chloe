// Editing query results in place: a single-table SELECT with its primary key is
// editable (computed columns stay read-only); a join is read-only with a reason.
import { chromium } from "playwright";
import { MYSQL } from "./lib/db.mjs";

const out = process.argv[2];
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 880 } });
page.setDefaultTimeout(8000);
const shot = (n) => page.screenshot({ path: `${out}/${n}.png` });
let failed = false;
const step = async (name, fn) => {
  try {
    await fn();
    console.log("OK  ", name);
  } catch (e) {
    failed = true;
    console.log("FAIL", name, e.message.split("\n")[0]);
    await shot("fail-" + name.replace(/\W+/g, "_"));
  }
};
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
const runSQL = async (sql) => {
  await page.locator(".monaco-editor .view-lines").first().click();
  await page.keyboard.press("Meta+A");
  await page.keyboard.press("Backspace");
  await page.keyboard.insertText(sql);
  await page.keyboard.press("Meta+R");
  await page.locator(".result-tabs button", { hasText: "Result 1" }).waitFor({ timeout: 10000 });
  await page.waitForTimeout(400);
};
const cell = (text) => page.locator(".result-grid .grid-cell", { hasText: text }).first();

await page.goto(process.env.BASE ?? "http://localhost:8080/");
await page.waitForTimeout(800);

await step("connect and open a query on shop", async () => {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("MySQL")`) }).click();
  await field("Connection Name").fill("Local MySQL");
  await field("Host").fill("127.0.0.1");
  await field("Port").fill(MYSQL);
  await field("User Name").fill("root");
  await field("Password").fill("chloe");
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator(".tree-row", { hasText: "Local MySQL" }).dblclick();
  await page.locator(".tree-row", { hasText: "shop" }).waitFor({ timeout: 10000 });
  await page.locator(".tree-row", { hasText: "shop" }).dblclick();
  await page.getByRole("button", { name: "New Query" }).first().click();
  await page.locator(".monaco-editor").first().waitFor();
});

await step("edit a cell in a query result and apply", async () => {
  await runSQL("SELECT id, full_name, upper(email) AS shout FROM customers WHERE id = 1");
  await page.locator(".result-grid .statusbar button[title^='Apply']").waitFor();
  await cell("Ada Lovelace").dblclick();
  await page.keyboard.press("Meta+A");
  await page.keyboard.type("Ada King");
  await page.keyboard.press("Enter");
  await page.locator(".result-grid .dirty-dot").waitFor();
  await shot("result-edit-pending");
  await page.locator(".result-grid .statusbar button[title^='Apply']").click();
  await page.locator(".result-grid .sql-preview", { hasText: "1 change applied to customers" }).waitFor();
  await cell("Ada King").waitFor();
  await shot("result-edit-applied");
});

await step("computed column stays read-only", async () => {
  await cell("ADA@EXAMPLE.COM").dblclick();
  await page.waitForTimeout(200);
  if (await page.locator(".result-grid .grid-cell input, .result-grid .grid-cell textarea").count()) throw new Error("computed column opened an editor");
});

await step("the edit reached the table", async () => {
  await runSQL("SELECT full_name FROM customers WHERE id = 1");
  await cell("Ada King").waitFor();
});

await step("a join is read-only with a reason", async () => {
  await runSQL("SELECT o.id, c.full_name FROM orders o JOIN customers c ON c.id = o.customer_id");
  const badge = page.locator(".result-grid .badge", { hasText: "read-only" });
  await badge.waitFor();
  if (!/joins tables/.test((await badge.getAttribute("title")) ?? "")) throw new Error("missing reason");
  if (await page.locator(".result-grid .statusbar button[title^='Apply']").count()) throw new Error("join should not be editable");
  await shot("result-readonly");
});

await step("restore", async () => {
  await runSQL("SELECT id, full_name FROM customers WHERE id = 1");
  await page.locator(".result-grid .statusbar button[title^='Apply']").waitFor();
  await cell("Ada King").dblclick();
  await page.keyboard.press("Meta+A");
  await page.keyboard.type("Ada Lovelace");
  await page.keyboard.press("Enter");
  await page.locator(".result-grid").press("Meta+s");
  await page.locator(".result-grid .sql-preview", { hasText: "applied" }).waitFor();
});

await browser.close();
process.exit(failed ? 1 : 0);
