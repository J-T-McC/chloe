// Structure Synchronization: PostgreSQL shop → a drifted schema; compare results,
// DDL comparison, unticking a drop, deployment script, message log, recompare.
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
const wizard = () => page.locator(".modal.structure-sync");
const pane = () => page.locator(".tab-pane:visible");
const target = "sync_e2e";
const runSQL = async (sql) => {
  await pane().locator(".monaco-editor .view-lines").click();
  await page.keyboard.press("Meta+A");
  await page.keyboard.insertText(sql);
  await page.keyboard.press("Escape");
  await page.keyboard.press("Meta+R");
  await page.waitForTimeout(600);
};

await page.goto(process.env.BASE ?? "http://localhost:8080/");
await page.waitForTimeout(600);

await step("connection and a drifted schema", async () => {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator('span:text-is("PostgreSQL")') }).click();
  await field("Connection Name").fill("Local Postgres");
  await field("Host").fill("127.0.0.1");
  await field("Port").fill("55432");
  await field("User Name").fill("postgres");
  await field("Password").fill("chloe");
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator(".tree-row", { hasText: "Local Postgres" }).dblclick();
  await page.locator(".tree-row", { hasText: /^\s*postgres\s*$/ }).first().dblclick();
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).last().click({ button: "right" });
  await page.locator(".context-menu").getByText("New Query", { exact: true }).click();
  await runSQL(
    `DROP SCHEMA IF EXISTS ${target} CASCADE; CREATE SCHEMA ${target}; ` +
      `CREATE TABLE ${target}.customers (id serial PRIMARY KEY, email varchar(100) NOT NULL, nickname varchar(20)); ` +
      `CREATE TABLE ${target}.legacy (id int PRIMARY KEY);`,
  );
  await pane().locator(".messages", { hasText: "CREATE TABLE" }).first().waitFor();
});

await step("compare", async () => {
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).last().click({ button: "right" });
  await page.locator(".context-menu").getByText("Structure Synchronization…").click();
  await wizard().waitFor();
  await wizard().getByLabel("Target Connection").selectOption({ label: "Local Postgres" });
  await wizard().getByLabel("Target Database").selectOption("postgres");
  await wizard().getByLabel("Target Schema").selectOption(target);
  await shot("ss1-general");
  await wizard().getByRole("button", { name: "Compare" }).click();
  await wizard().locator(".ss-row", { hasText: "legacy" }).waitFor();
  const rows = (await wizard().locator(".ss-tree .ss-row:not(.head):not(.group)").allInnerTexts()).map((r) => r.replace(/\s+/g, " ").trim());
  console.log("     " + rows.join(" | "));
  for (const want of ["customers Modify customers", "products Create", "Drop legacy", "customer_orders Create"]) {
    if (!rows.some((r) => r.includes(want))) throw new Error(`missing row "${want}"`);
  }
});

await step("ddl comparison and script", async () => {
  await wizard().locator(".ss-row", { hasText: "Modify" }).click();
  await wizard().locator(".ddl-side .only-target", { hasText: "nickname" }).first().waitFor();
  await wizard().locator(".ddl-side .only-source", { hasText: "full_name" }).first().waitFor();
  await shot("ss2-results");
  await wizard().getByRole("button", { name: "Deployment Script" }).click();
  await wizard().locator(".ss-script", { hasText: "full_name" }).waitFor();
  // keep the legacy table
  await wizard().getByLabel("Include legacy").uncheck();
  await wizard().getByRole("button", { name: "Next" }).click();
  await wizard().locator(".ss-script .code-block", { hasText: "CREATE TABLE" }).waitFor();
  const script = await wizard().locator(".ss-script").innerText();
  if (/DROP TABLE[^;]*legacy/.test(script)) throw new Error("unticked drop is in the script");
  await shot("ss3-deploy");
});

await step("deploy and recompare", async () => {
  await wizard().getByRole("button", { name: "Start" }).click();
  await wizard().locator(".dt-log", { hasText: "Finished deployment" }).waitFor({ timeout: 20000 });
  console.log("     " + (await wizard().locator(".dt-log").innerText()).split("\n").slice(-1)[0]);
  await shot("ss4-log");
  await wizard().getByRole("button", { name: "Recompare" }).click();
  await wizard().locator(".ss-row", { hasText: "legacy" }).waitFor();
  const rows = (await wizard().locator(".ss-tree .ss-row:not(.head):not(.group)").allInnerTexts()).map((r) => r.replace(/\s+/g, " ").trim());
  const diff = rows.filter((r) => !/ Same /.test(r));
  console.log("     still different:", diff.join(" | "));
  if (diff.length !== 1 || !diff[0].includes("Drop legacy")) throw new Error("only the kept legacy table should still differ");
  await wizard().getByRole("button", { name: "Cancel" }).click();
});

await step("tidy up", async () => {
  await page.locator(".tab", { hasText: "Query" }).first().click();
  await runSQL(`DROP SCHEMA ${target} CASCADE;`);
  await pane().locator(".messages", { hasText: "DROP SCHEMA" }).waitFor();
});

console.log(errors.length ? "ERRORS:\n" + errors.join("\n") : "no page errors");
await browser.close();
process.exit(failed ? 1 : 0);
