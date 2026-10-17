// A slow listing for the previous selection must not replace the current one
// (C7): open two SQLite files, delay the first one's object list, switch to the
// second, and check the pane still shows the second's tables.
import { execFileSync } from "node:child_process";
import { chromium } from "playwright";
const out = process.argv[2];
const dir = process.env.LITE;
execFileSync("sqlite3", [`${dir}/alpha.db`, "CREATE TABLE t_alpha (id INTEGER PRIMARY KEY);"]);
execFileSync("sqlite3", [`${dir}/beta.db`, "CREATE TABLE t_beta (id INTEGER PRIMARY KEY);"]);
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 880 } });
page.setDefaultTimeout(8000);
const errors = [];
page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
page.on("console", (m) => m.type() === "error" && errors.push("console: " + m.text()));
const shot = (n) => page.screenshot({ path: `${out}/${n}.png` });
let failed = 0;
const step = async (name, fn) => {
  try { await fn(); console.log("OK  ", name); } catch (e) { failed++; console.log("FAIL", name, e.message.split("\n")[0]); await shot("fail-" + name.replace(/\W+/g, "_")); }
};
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
const row = (re) => page.locator(".tree-row", { hasText: re });
const objects = page.locator(".objects-pane:visible table");
await page.goto(process.env.BASE ?? "http://localhost:8080/");

for (const name of ["alpha", "beta"]) {
  await step(`connect ${name}`, async () => {
    await page.getByRole("button", { name: "Connection" }).first().click();
    await page.locator(".type-card").filter({ has: page.locator(`span:text-is("SQLite")`) }).click();
    await field("Connection Name").fill(name);
    await field("Database File").fill(`${dir}/${name}.db`);
    await page.getByRole("button", { name: "Save" }).click();
    await row(name).first().dblclick();
    await page.locator(".tree-row").filter({ hasText: /^\s*main\s*$/ }).nth(name === "alpha" ? 0 : 1).waitFor();
  });
}

const mains = page.locator(".tree-row").filter({ hasText: /^\s*main\s*$/ });
await step("both listings load", async () => {
  await mains.nth(0).dblclick();
  await mains.nth(1).dblclick();
  await mains.nth(0).click();
  await objects.getByText("t_alpha").waitFor();
  await mains.nth(1).click();
  await objects.getByText("t_beta").waitFor();
});

await step("a late answer for the previous database is dropped", async () => {
  // Hold back every runtime call made while alpha is selected.
  let hold = true;
  await page.route("**/wails/runtime*", async (route) => {
    if (hold && route.request().method() === "POST") await new Promise((r) => setTimeout(r, 1500));
    await route.continue();
  });
  await mains.nth(0).click();
  await page.waitForTimeout(200);
  hold = false;
  await mains.nth(1).click();
  await objects.getByText("t_beta").waitFor();
  await page.waitForTimeout(2000); // alpha's answer has arrived by now
  const text = await objects.innerText();
  await shot("stale-after");
  if (text.includes("t_alpha") || !text.includes("t_beta")) throw new Error("pane shows: " + text.replace(/\s+/g, " "));
  await page.unroute("**/wails/runtime*");
});

console.log(errors.length ? "errors:\n" + errors.join("\n") : "no page errors");
await browser.close();
process.exit(failed ? 1 : 0);
