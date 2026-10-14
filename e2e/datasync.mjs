// Data Synchronization: MySQL shop → a drifted copy; mapping, compare results,
// record view, unticking an action, deployment script, message log, recompare.
import { chromium } from "playwright";

const out = process.argv[2];
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 880 } });
page.setDefaultTimeout(8000);
const errors = [];
page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
page.on("console", (m) => m.type() === "error" && errors.push("console: " + m.text()));

const shot = (n) => page.screenshot({ path: `${out}/${n}.png` });
let failed = 0;
const step = async (name, fn) => {
  try {
    await fn();
    console.log("OK  ", name);
  } catch (e) {
    failed++;
    console.log("FAIL", name, e.message.split("\n")[0]);
    await shot("fail-" + name.replace(/\W+/g, "_"));
  }
};
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
const wizard = () => page.locator(".modal.data-sync");
const pane = () => page.locator(".tab-pane:visible");
const db = "dsync_e2e";
const runSQL = async (sql) => {
  await page.locator(".tab", { hasText: "Query" }).first().click();
  await pane().locator(".monaco-editor .view-lines").click();
  await page.keyboard.press("Meta+A");
  await page.keyboard.insertText(sql);
  await page.keyboard.press("Escape");
  await page.keyboard.press("Meta+R");
  await page.waitForTimeout(800);
};

await page.goto("http://localhost:8080/");
await page.waitForTimeout(600);

await step("connection and a drifted copy", async () => {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator('span:text-is("MySQL")') }).click();
  await field("Connection Name").fill("Local MySQL");
  await field("Host").fill("127.0.0.1");
  await field("Port").fill("33306");
  await field("User Name").fill("root");
  await field("Password").fill("chloe");
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator(".tree-row", { hasText: "Local MySQL" }).dblclick();
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).click({ button: "right" });
  await page.locator(".context-menu").getByText("New Query", { exact: true }).click();
  await runSQL(
    `DROP DATABASE IF EXISTS ${db}; CREATE DATABASE ${db}; ` +
      `CREATE TABLE ${db}.customers LIKE shop.customers; INSERT INTO ${db}.customers SELECT * FROM shop.customers; ` +
      `CREATE TABLE ${db}.products LIKE shop.products; INSERT INTO ${db}.products SELECT * FROM shop.products; ` +
      `UPDATE ${db}.customers SET full_name = 'Somebody Else' WHERE id = 2; ` +
      `DELETE FROM ${db}.customers WHERE id = 3; ` +
      `INSERT INTO ${db}.customers (id, email, full_name) VALUES (99, 'extra@example.com', 'Extra Row');`,
  );
  await pane().locator(".messages", { hasText: "INSERT INTO" }).first().waitFor();
});

await step("mapping", async () => {
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).click({ button: "right" });
  await page.locator(".context-menu").getByText("Data Synchronization…").click();
  await wizard().waitFor();
  await wizard().getByLabel("Target Connection").selectOption({ label: "Local MySQL" });
  await wizard().getByLabel("Target Database").selectOption(db);
  await wizard().getByRole("button", { name: "Next" }).click();
  await wizard().locator(".dsync-row", { hasText: "orders" }).first().waitFor();
  const rows = (await wizard().locator(".dsync-grid .dsync-row:not(.head)").allInnerTexts()).map((r) => r.replace(/\s+/g, " ").trim());
  console.log("     " + rows.join(" | "));
  if (!(await wizard().getByLabel("Map customers").isChecked())) throw new Error("customers should be mapped");
  if (!(await wizard().getByLabel("Map orders").isDisabled())) throw new Error("orders has no target table and can't be mapped");
  await shot("ds1-mapping");
});

await step("compare results and records", async () => {
  await wizard().getByRole("button", { name: "Compare & Preview" }).click();
  await wizard().locator(".dsr-row", { hasText: "customers" }).waitFor();
  const row = (await wizard().locator(".dsr-row", { hasText: "customers" }).innerText()).replace(/\s+/g, " ");
  console.log("     customers:", row);
  if (!/1 1 1 1/.test(row)) throw new Error("customers should have 1 insert, 1 update, 1 delete, 1 same");
  await wizard().locator(".dsync-table td.changed", { hasText: "Somebody Else" }).waitFor();
  await wizard().locator(".dsync-table tr.op-delete", { hasText: "Extra Row" }).waitFor();
  await wizard().getByLabel("Show records").selectOption("insert");
  await wizard().locator(".dsync-table tr.op-insert", { hasText: "Grace Hopper" }).waitFor();
  await shot("ds2-results");
  // keep the extra row
  await wizard().getByLabel("delete customers").uncheck();
  await wizard().getByRole("button", { name: "Next" }).click();
  await wizard().locator(".ss-script .code-block", { hasText: "UPDATE" }).waitFor();
  const script = await wizard().locator(".ss-script").innerText();
  if (/DELETE/.test(script)) throw new Error("unticked delete is in the script");
  await shot("ds3-deploy");
});

await step("deploy and recompare", async () => {
  await wizard().getByRole("button", { name: "Start" }).click();
  await wizard().locator(".dt-log", { hasText: "Finished deployment" }).waitFor({ timeout: 20000 });
  console.log("     " + (await wizard().locator(".dt-log").innerText()).split("\n").slice(-1)[0]);
  await wizard().getByRole("button", { name: "Recompare" }).click();
  await wizard().locator(".dsr-row", { hasText: "customers" }).waitFor();
  const row = (await wizard().locator(".dsr-row", { hasText: "customers" }).innerText()).replace(/\s+/g, " ");
  console.log("     after:", row);
  if (!/0 0 1 3/.test(row)) throw new Error("only the kept extra row should differ");
  await wizard().getByRole("button", { name: "Cancel" }).click();
});

await step("tidy up", async () => {
  await runSQL(`DROP DATABASE ${db};`);
  await pane().locator(".messages", { hasText: "DROP DATABASE" }).waitFor();
});

console.log(errors.length ? "ERRORS:\n" + errors.join("\n") : "no page errors");
await browser.close();
process.exit(failed ? 1 : 0);
