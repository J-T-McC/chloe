import { chromium } from "playwright";
const out = process.argv[2];
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1300, height: 860 } });
page.setDefaultTimeout(10000);
page.on("pageerror", (e) => console.log("PAGE ERROR", e.message));
await page.goto(process.env.BASE ?? "http://localhost:8080/");
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
async function conn(type, name, port, user) {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("${type}")`) }).click();
  await field("Connection Name").fill(name); await field("Host").fill("127.0.0.1"); await field("Port").fill(port); if (user) await field("User Name").fill(user); await field("Password").fill("chloe");
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator(".tree-row", { hasText: name }).first().waitFor();
}
const menu = async (row, item) => { await row.click({ button: "right" }); await page.locator(".context-menu").getByText(item, { exact: true }).click(); };
const dlg = page.locator(".database-dialog");
const sel = (l) => dlg.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator(".combobox input");
const pick = async (l, typed, expect) => {
  const i = sel(l);
  await i.click();
  await i.fill(typed);
  await page.waitForTimeout(100);
  await i.press("Enter");
  const v = await i.inputValue();
  if (v !== expect) console.log(`PICK MISMATCH ${l}: typed ${typed} got ${v} want ${expect}`);
};
const rows = async () => (await page.locator(".tree-row").allInnerTexts()).map((x) => x.trim());

for (const [type, name, port, user] of [["MySQL", "MY", "33306", ""], ["PostgreSQL", "PGX", "55432", "postgres"]]) {
  await conn(type, name, port, user);
  const c = page.locator(".tree-row", { hasText: name }).first();
  await c.dblclick();
  await page.waitForTimeout(500);
  await menu(c, "New Database…");
  await dlg.locator("input").first().fill("chloe_e2e");
  if (type === "MySQL") { await pick("Character Set", "LATIN", "latin1"); await pick("Collation", "latin1_bin", "latin1_bin"); }
  else { await pick("Owner", "postgres", "postgres"); await pick("Template", "template0", "template0"); await pick("Encoding", "LATIN1", "LATIN1"); await pick("Collation", "C", "C"); await pick("Character Type", "C", "C"); await dlg.locator("textarea").fill("e2e db"); }
  await dlg.getByRole("button", { name: "SQL Preview" }).click();
  await page.waitForTimeout(400);
  console.log(type, "preview:", (await dlg.locator(".modal-body").innerText()).replace(/\s+/g, " "));
  if (type === "PostgreSQL") await page.screenshot({ path: `${out}/db1.png` });
  await dlg.getByRole("button", { name: "OK" }).click();
  await dlg.waitFor({ state: "detached", timeout: 4000 }).catch(async (e) => { console.log("DIALOG ERROR:", await dlg.locator(".status-line").innerText().catch(() => "?")); throw e; });
  await page.waitForTimeout(500);
  console.log(type, "tree has db:", (await rows()).includes("chloe_e2e"));
  const db = page.locator(".tree-row", { hasText: /^\s*chloe_e2e\s*$/ });
  await menu(db, "Edit Database…");
  await page.waitForTimeout(400);
  if (type === "MySQL") { await pick("Character Set", "utf8mb4", "utf8mb4"); await pick("Collation", "", ""); }
  else { await dlg.locator("input").first().fill("chloe_e2e2"); }
  await dlg.getByRole("button", { name: "SQL Preview" }).click();
  await page.waitForTimeout(400);
  console.log(type, "edit preview:", (await dlg.locator(".modal-body").innerText()).replace(/\s+/g, " "));
  await dlg.getByRole("button", { name: "OK" }).click();
  await dlg.waitFor({ state: "detached", timeout: 4000 }).catch(async (e) => { console.log("DIALOG ERROR:", await dlg.locator(".status-line").innerText().catch(() => "?")); throw e; });
  await page.waitForTimeout(500);
  const final = type === "MySQL" ? "chloe_e2e" : "chloe_e2e2";
  console.log(type, "after edit:", (await rows()).filter((r) => r.startsWith("chloe_e2e")));
  await menu(page.locator(".tree-row", { hasText: new RegExp(`^\\s*${final}\\s*$`) }), "Delete Database");
  await page.locator(".modal.small").getByRole("button", { name: "Delete" }).click();
  await page.waitForTimeout(800);
  console.log(type, "after delete:", (await rows()).filter((r) => r.startsWith("chloe_e2e")));
  await c.click({ button: "right" });
  await page.screenshot({ path: `${out}/db-menu-${type}.png` });
  await page.keyboard.press("Escape");
  await page.locator(".context-menu").waitFor({ state: "detached" }).catch(() => {});
  await c.click();
  await page.keyboard.press("ArrowLeft").catch(() => {});
}
await browser.close();
