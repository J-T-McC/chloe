// Long values (P-F4): the grid fetches them cut, so it shows the start; the
// value panel, the cell editor, copy and saving another column use the whole value.
import { execFileSync } from "node:child_process";
import { chromium } from "playwright";
const out = process.argv[2];
const dir = process.env.LITE;
const db = `${dir}/long.db`;
const LONG = "a".repeat(1000) + "END";
execFileSync("sqlite3", [db, `CREATE TABLE t (id INTEGER PRIMARY KEY, body TEXT, note TEXT); INSERT INTO t VALUES (1, '${LONG}', 'n');`]);
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 880 } });
page.setDefaultTimeout(8000);
await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
const errors = [];
page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
page.on("console", (m) => m.type() === "error" && errors.push("console: " + m.text()));
const shot = (n) => page.screenshot({ path: `${out}/${n}.png` });
let failed = 0;
const step = async (name, fn) => {
  try { await fn(); console.log("OK  ", name); } catch (e) { failed++; console.log("FAIL", name, e.message.split("\n")[0]); await shot("fail-" + name.replace(/\W+/g, "_")); }
};
const check = (ok, msg) => { if (!ok) throw new Error(msg); };
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
const row = (re) => page.locator(".tree-row", { hasText: re });
await page.goto(process.env.BASE ?? "http://localhost:8080/");

const cells = () => page.locator(".viewer .grid-row").first().locator(".grid-cell");
await step("open the table", async () => {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("SQLite")`) }).click();
  await field("Connection Name").fill("long");
  await field("Database File").fill(db);
  await page.getByRole("button", { name: "Save" }).click();
  await row(/long$/).first().dblclick();
  await row(/^\s*main\s*$/).first().dblclick();
  await row(/^\s*Tables\s*$/).first().dblclick();
  await row(/^\s*t\s*$/).first().dblclick();
  await cells().nth(1).waitFor();
});

await step("the grid shows the start of a long value", async () => {
  const text = await cells().nth(1).innerText();
  check(text.length === 301 && text.endsWith("…"), `cell: ${text.length} chars`);
});

await step("the value panel shows it whole", async () => {
  await cells().nth(1).click();
  await page.locator(".viewer").getByTitle(/Show the active cell's full value/).click();
  await page.locator(".value-panel", { hasText: "1,003 bytes" }).waitFor();
});

await step("the editor and copy have it whole", async () => {
  await cells().nth(1).click();
  await page.keyboard.press("Meta+c");
  // Copying fetches the whole value first.
  await page.waitForFunction((v) => navigator.clipboard.readText().then((t) => t === v), LONG, { timeout: 3000 }).catch(() => {});
  const copied = await page.evaluate(() => navigator.clipboard.readText());
  check(copied === LONG, `copied: ${copied.length} chars`);
  await page.keyboard.press("Enter");
  const v = await page.locator(".cell-editor").inputValue();
  check(v === LONG, `editor: ${v.length} chars`);
  await page.keyboard.press("Escape");
  check(!(await page.locator(".viewer .statusbar button[title^='Apply changes']").isEnabled().catch(() => false)), "an unchanged edit is a change");
});

await step("saving another column keeps it", async () => {
  await cells().nth(2).dblclick();
  await page.keyboard.press("Meta+a");
  await page.keyboard.type("edited");
  await page.keyboard.press("Enter");
  await page.locator(".viewer .statusbar button[title^='Apply changes']").click();
  await page.waitForTimeout(600);
  const got = execFileSync("sqlite3", [db, "SELECT length(body), substr(body, -3), note FROM t"]).toString().trim();
  check(got === "1003|END|edited", "row: " + got);
});

console.log(errors.length ? "errors:\n" + errors.join("\n") : "no page errors");
await browser.close();
process.exit(failed ? 1 : 0);
