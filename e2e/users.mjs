import { chromium } from "playwright";
const out = process.argv[2];
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 860 } });
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
const ud = page.locator(".user-designer:visible");
const tabBtn = (t) => ud.locator(".seg-tabs button", { hasText: new RegExp(`^\\s*${t}`) });
for (const [type, name, port, user] of [["MySQL", "MY", "33306", ""], ["PostgreSQL", "PGX", "55432", "postgres"]]) {
  const pg = type === "PostgreSQL";
  await conn(type, name, port, user);
  const c = page.locator(".tree-row", { hasText: name }).first();
  await c.dblclick();
  await c.click();
  await page.locator(".tool-button", { hasText: "User" }).click();
  await page.locator(".users-table tbody tr").first().waitFor();
  console.log(type, "users:", (await page.locator(".users-table tbody tr").allInnerTexts()).slice(0, 4).map((x) => x.replace(/\s+/g, " ").trim()));
  await page.locator(".users-pane:visible").getByRole("button", { name: /New (User|Role)/ }).click();
  await ud.waitFor();
  await field(pg ? "Role Name" : "User Name").fill("e2e_u");
  await ud.locator('input[type="password"]').nth(0).fill("Pw!12345");
  await ud.locator('input[type="password"]').nth(1).fill("Pw!12345");
  if (!pg) {
    await tabBtn("Server Privileges").click();
    await ud.locator(".server-privs label", { hasText: /^\s*PROCESS\s*$/ }).locator("input").check();
  }
  await tabBtn("Privileges").click();
  const row = (t) => ud.locator(".privs-row", { hasText: new RegExp(`^\\s*${t}\\s*$`) });
  if (pg) {
    await row("shop").locator(".twisty").click();
  } else {
    await row("shop").locator(".twisty").click();
  }
  await row("customers").click();
  await ud.locator(".privs-table tr", { hasText: /^\s*SELECT/ }).locator("input").first().check();
  if (pg) await ud.locator(".privs-table tr", { hasText: /^\s*SELECT/ }).locator("input").nth(1).check();
  else await ud.locator(".privs-head").getByText("Grant Option").click();
  await row("customers").locator(".twisty").click();
  await row("email").click();
  await ud.locator(".privs-table tr", { hasText: /^\s*UPDATE/ }).locator("input").first().check();
  await page.screenshot({ path: `${out}/u-${type}-privs.png` });
  await tabBtn("SQL Preview").click();
  await page.waitForTimeout(400);
  console.log(type, "sql:\n " + (await ud.locator(".designer-sql").innerText()).split("\n").filter(Boolean).join("\n "));
  await ud.getByRole("button", { name: "Save" }).click();
  await page.waitForTimeout(1000);
  console.log(type, "after save tab:", await page.locator(".tab.active").innerText(), "| error:", await ud.locator(".error-banner").count() ? await ud.locator(".error-banner").innerText() : "none");
  // reopen fresh from the list and check the grants loaded back
  await page.locator(".tab.active .tab-close").click();
  await page.locator(".tabbar .tab", { hasText: pg ? "Roles" : "Users" }).click();
  await page.locator(".users-pane:visible").getByRole("button", { name: "" }).last().click().catch(() => {});
  await page.waitForTimeout(500);
  await page.locator(".users-pane:visible tr", { hasText: "e2e_u" }).dblclick();
  await ud.waitFor();
  await tabBtn("Privileges").click();
  await row("shop").locator(".twisty").click().catch(() => {});
  await page.waitForTimeout(300);
  console.log(type, "granted rows:", (await ud.locator(".privs-row.granted").allInnerTexts()).map((x) => x.trim()));
  if (pg) {
    // revoke everything so the role can be dropped
    await row("customers").click();
    await ud.locator(".privs-head").getByRole("button", { name: "None" }).click();
    await row("customers").locator(".twisty").click();
    await row("email").click();
    await ud.locator(".privs-head").getByRole("button", { name: "None" }).click();
    await ud.getByRole("button", { name: "Save" }).click();
    await page.waitForTimeout(800);
  }
  await page.locator(".tab.active .tab-close").click();
  await page.locator(".tabbar .tab", { hasText: pg ? "Roles" : "Users" }).click();
  await page.locator(".users-pane:visible tr", { hasText: "e2e_u" }).click({ button: "right" });
  await page.locator(".context-menu").getByText(/Delete (User|Role)/).click();
  await page.locator(".modal.small").getByRole("button", { name: "Delete" }).click();
  await page.waitForTimeout(800);
  console.log(type, "after delete:", await page.locator(".users-pane:visible tr", { hasText: "e2e_u" }).count(), "| dialog:", await page.locator(".modal.small").count());
  if (await page.locator(".modal.small").count()) { console.log(await page.locator(".modal.small").innerText()); await page.keyboard.press("Escape"); }
  await c.click({ button: "right" });
  await page.locator(".context-menu").getByText("Close Connection").click();
}
await browser.close();
