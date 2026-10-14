import { chromium } from "playwright";
const out = process.argv[2];
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1300, height: 860 } });
page.setDefaultTimeout(10000);
page.on("pageerror", (e) => console.log("PAGE ERROR", e.message));
await page.goto("http://localhost:8080/");
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
async function conn(type, name, port, user) {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("${type}")`) }).click();
  await field("Connection Name").fill(name); await field("Host").fill("127.0.0.1"); await field("Port").fill(port); if (user) await field("User Name").fill(user); await field("Password").fill("chloe");
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator(".tree-row", { hasText: name }).first().waitFor();
}
const menu = async (row, item) => { await row.click({ button: "right" }); await page.locator(".context-menu").getByText(item, { exact: true }).click(); };
const vd = page.locator(".view-designer:visible");
const setDef = async (sql) => {
  await vd.locator(".monaco-editor .view-lines").click();
  await page.keyboard.press("Meta+a");
  await page.keyboard.insertText(sql);
};

for (const [type, name, port, user] of [["MySQL", "MY", "33306", ""], ["PostgreSQL", "PGX", "55432", "postgres"]]) {
  const pg = type === "PostgreSQL";
  await conn(type, name, port, user);
  const c = page.locator(".tree-row", { hasText: name }).first();
  await c.dblclick();
  if (pg) await page.locator(".tree-row", { hasText: /^\s*postgres\s*$/ }).first().dblclick();
  const shop = page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).last();
  if (!pg) await shop.dblclick(); else await shop.dblclick();
  await page.waitForTimeout(400);
  const views = page.locator(".tree-row", { hasText: /^\s*Views\s*$/ }).last();
  await menu(views, "New View");
  await vd.waitFor();
  await setDef("SELECT id, email FROM customers");
  await vd.getByRole("button", { name: "Preview", exact: true }).click();
  await vd.locator(".result-grid").waitFor();
  console.log(type, "preview rows:", (await vd.locator(".result-grid .statusbar").innerText()).replace(/\s+/g, " ").slice(-40));
  await vd.locator(".seg-tabs button", { hasText: "Advanced" }).click();
  if (pg) await vd.getByText("Security barrier").click();
  else await vd.locator(".field", { has: page.locator('label:text-is("Algorithm")') }).locator("select").selectOption("MERGE");
  await vd.locator(".seg-tabs button", { hasText: "SQL Preview" }).click();
  await page.waitForTimeout(400);
  console.log(type, "sql:", (await vd.locator(".designer-sql").innerText()).replace(/\s+/g, " "));
  await vd.locator("input.name-input").fill("v_ui");
  await vd.getByRole("button", { name: "Save", exact: true }).click();
  await page.waitForTimeout(800);
  console.log(type, "tab:", await page.locator(".tab.active").innerText(), "| error:", await vd.locator(".error-banner").count());
  // edit: change definition, save
  await vd.locator(".seg-tabs button", { hasText: "Definition" }).click();
  await setDef("SELECT id, email, full_name FROM customers");
  await vd.locator(".seg-tabs button", { hasText: "SQL Preview" }).click();
  await page.waitForTimeout(400);
  console.log(type, "edit sql:", (await vd.locator(".designer-sql").innerText()).replace(/\s+/g, " ").slice(0, 160));
  await page.keyboard.press("Meta+s").catch(() => {});
  await vd.getByRole("button", { name: "Save", exact: true }).click().catch(() => {});
  await page.waitForTimeout(800);
  console.log(type, "after edit tab:", await page.locator(".tab.active").innerText());
  if (pg) {
    // materialized view via the views folder
    await menu(views, "New Materialized View");
    await page.waitForTimeout(400);
    await setDef("SELECT id FROM customers");
    await vd.locator("input.name-input").fill("mv_ui");
    await vd.getByRole("button", { name: "Save", exact: true }).click();
    await page.waitForTimeout(800);
    console.log("matview tab:", await page.locator(".tab.active").innerText());
    await page.screenshot({ path: `${out}/v-mat.png` });
  }
  // expand the views folder and use row menus
  await views.click();
  await page.keyboard.press("ArrowRight");
  await page.waitForTimeout(300);
  if (!(await page.locator(".tree-row", { hasText: /^\s*v_ui\s*$/ }).count())) await views.dblclick();
  await page.waitForTimeout(500);
  const v = page.locator(".tree-row", { hasText: /^\s*v_ui\s*$/ });
  await menu(v, "Duplicate View");
  await page.waitForTimeout(800);
  await menu(views, "Refresh");
  await page.waitForTimeout(500);
  console.log(type, "views:", (await page.locator(".tree-row").allInnerTexts()).map((x) => x.trim()).filter((x) => /^(m?v_ui)/.test(x)));
  await page.screenshot({ path: `${out}/v-${type}.png` });
  if (pg) {
    await page.locator(".tree-row", { hasText: /^\s*Materialized Views\s*$/ }).last().dblclick();
    await page.waitForTimeout(500);
  }
  // search box: only the matches (and their parents) remain
  await page.locator(".sidebar-search input").fill("v_ui");
  await page.waitForTimeout(200);
  console.log(type, "search:", (await page.locator(".tree-row").allInnerTexts()).map((x) => x.trim()));
  await page.screenshot({ path: `${out}/search-${type}.png` });
  await page.locator(".sidebar-search input").fill("");
  for (const n of ["v_ui_copy", "v_ui", ...(pg ? ["mv_ui"] : [])]) {
    const row = page.locator(".tree-row", { hasText: new RegExp(`^\\s*${n}\\s*$`) });
    if (pg && n === "mv_ui") {
      await menu(row, "Refresh");
      await page.waitForTimeout(300);
    }
    await row.click({ button: "right" });
    await page.locator(".context-menu .menu-item", { hasText: /^Delete (Materialized )?View/ }).click();
    await page.locator(".modal.small").getByRole("button", { name: "Delete" }).click();
    await page.waitForTimeout(500);
  }
  console.log(type, "after delete:", (await page.locator(".tree-row").allInnerTexts()).map((x) => x.trim()).filter((x) => /^(m?v_ui)/.test(x)));
  await c.click({ button: "right" });
  await page.locator(".context-menu").getByText("Close Connection").click();
}
await browser.close();
