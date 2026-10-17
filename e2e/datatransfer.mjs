// Data Transfer wizard: MySQL shop → a new PostgreSQL schema, created through Options;
// objects, summary, progress log, the result in the tree, and profiles.
import { chromium } from "playwright";
import { MYSQL, PG } from "./lib/db.mjs";

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
const wizard = () => page.locator(".modal.data-transfer");
const schema = "from_mysql_" + Date.now().toString(36);

await page.goto(process.env.BASE ?? "http://localhost:8080/");
await page.waitForTimeout(600);

for (const [label, type, port, user] of [
  ["Local MySQL", "MySQL", MYSQL, "root"],
  ["Local Postgres", "PostgreSQL", PG, "postgres"],
]) {
  await step(`create ${label}`, async () => {
    await page.getByRole("button", { name: "Connection" }).first().click();
    await page.locator(".type-card").filter({ has: page.locator(`span:text-is("${type}")`) }).click();
    await field("Connection Name").fill(label);
    await field("Host").fill("127.0.0.1");
    await field("Port").fill(port);
    await field("User Name").fill(user);
    await field("Password").fill("chloe");
    await page.getByRole("button", { name: "Save" }).click();
    await page.locator(".tree-row", { hasText: label }).waitFor();
  });
}

await step("open from the database menu", async () => {
  await page.locator(".tree-row", { hasText: "Local MySQL" }).dblclick();
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).click({ button: "right" });
  await page.locator(".context-menu").getByText("Data Transfer…").click();
  await wizard().waitFor();
  if ((await wizard().getByLabel("Source Database").inputValue()) !== "shop") throw new Error("source database not preset");
});

await step("choose a new target schema", async () => {
  await wizard().getByLabel("Target Connection").selectOption({ label: "Local Postgres" });
  await wizard().getByLabel("Target Database").click();
  await page.locator(".combo-option", { hasText: /^postgres$/ }).click();
  // The schema defaults to public once the database is chosen; then type a new one.
  await page.waitForFunction(() => document.querySelector('[aria-label="Target Schema"]')?.value === "public");
  await wizard().getByLabel("Target Schema").fill(schema);
  await wizard().getByLabel("Target Schema").press("Enter");
  await wizard().getByText(`Schema “${schema}” doesn't exist`).waitFor();
  await shot("dt1-general");
  // Next refuses nothing here, but the transfer would fail: turn on creating it
  await wizard().getByRole("button", { name: "Options…" }).click();
  const opts = page.locator(".modal.dt-options");
  await opts.getByText("Create target database/schema if not exist").click();
  await shot("dt2-options");
  await opts.getByRole("button", { name: "OK" }).click();
  await wizard().getByText(`Schema “${schema}” will be created.`).waitFor();
});

await step("pick objects", async () => {
  await wizard().getByRole("button", { name: "Next" }).click();
  await wizard().getByLabel("All Tables").waitFor();
  await wizard().locator(".dt-group-list label", { hasText: "customers" }).waitFor();
  await wizard().getByRole("button", { name: "Next" }).click();
  await page.locator(".modal.data-transfer .status-line.err", { hasText: "Choose the objects" }).waitFor();
  await wizard().getByLabel("All Tables").check();
  await wizard().getByText("Tables (*)").waitFor();
  // unticking one table turns "all" into a fixed list
  await wizard().locator(".dt-group-list label", { hasText: "order_items" }).locator("input").uncheck();
  await wizard().getByText("(3/4)").waitFor();
  await wizard().locator(".dt-group-list label", { hasText: "order_items" }).locator("input").check();
  await wizard().getByText("Views are only copied between databases of the same engine.").waitFor();
  await shot("dt3-objects");
});

await step("summary and run", async () => {
  await wizard().getByRole("button", { name: "Next" }).click();
  await wizard().locator(".dt-summary-row", { hasText: "order_items" }).waitFor();
  await shot("dt4-summary");
  await wizard().getByRole("button", { name: "Start" }).click();
  await wizard().locator(".dt-log", { hasText: "Finished successfully" }).waitFor({ timeout: 30000 });
  const log = await wizard().locator(".dt-log").innerText();
  console.log("     " + log.split("\n").filter((l) => /Create table|record\(s\)|Finished|Create foreign/.test(l)).slice(0, 12).join("\n     "));
  console.log("     ", (await wizard().locator(".dt-counters").innerText()).replace(/\s+/g, " "));
  await shot("dt5-progress");
});

await step("profiles", async () => {
  await wizard().getByRole("button", { name: "Save Profile" }).click();
  await page.locator(".modal.small input").fill("shop to postgres");
  await page.locator(".modal.small").getByRole("button", { name: "OK" }).click();
  await wizard().getByRole("button", { name: "Close" }).click();
  // reopen empty, then load it
  await page.locator(".tree-row", { hasText: "Local Postgres" }).click({ button: "right" });
  await page.locator(".context-menu").getByText("Data Transfer…").click();
  await wizard().getByRole("button", { name: /Load Profile/ }).click();
  await page.locator(".context-menu").getByText("shop to postgres").click();
  if ((await wizard().getByLabel("Target Schema").inputValue()) !== schema) throw new Error("profile didn't restore the target");
  await wizard().getByRole("button", { name: "Cancel" }).click();
});

await step("result in the tree", async () => {
  await page.locator(".tree-row", { hasText: "Local Postgres" }).dblclick();
  await page.locator(".tree-row", { hasText: /^\s*postgres\s*$/ }).first().dblclick();
  await page.locator(".tree-row", { hasText: schema }).dblclick();
  await page.locator(".tree-row", { hasText: "Tables" }).last().dblclick();
  await page.locator(".tree-row", { hasText: "order_items" }).last().waitFor();
  await page.locator(".tree-row", { hasText: schema }).click({ button: "right" });
  await page.locator(".context-menu").getByText("New Query", { exact: true }).click();
  await page.locator(".tab-pane:visible .monaco-editor .view-lines").click();
  await page.keyboard.insertText(`SELECT c.full_name, count(*) FROM ${schema}.orders o JOIN ${schema}.customers c ON c.id = o.customer_id GROUP BY 1 ORDER BY 1;`);
  await page.keyboard.press("Escape");
  await page.keyboard.press("Meta+R");
  await page.locator(".tab-pane:visible .grid-cell", { hasText: "Ada Lovelace" }).waitFor({ timeout: 10000 });
  await shot("dt6-result");
  // tidy up the schema this run created
  await page.locator(".tab-pane:visible .monaco-editor .view-lines").click();
  await page.keyboard.press("Meta+A");
  await page.keyboard.insertText(`DROP SCHEMA ${schema} CASCADE;`);
  await page.keyboard.press("Escape");
  await page.keyboard.press("Meta+R");
  await page.locator(".tab-pane:visible .messages", { hasText: "DROP SCHEMA" }).waitFor();
});

console.log(errors.length ? "ERRORS:\n" + errors.join("\n") : "no page errors");
await browser.close();
process.exit(failed ? 1 : 0);
