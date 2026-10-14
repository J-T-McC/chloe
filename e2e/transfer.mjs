import { chromium } from "playwright";
const out = process.argv[2];
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 880 } });
page.setDefaultTimeout(8000);
const errors = [];
page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
page.on("console", (m) => m.type() === "error" && errors.push("console: " + m.text()));
const shot = (n) => page.screenshot({ path: `${out}/${n}.png` });
const step = async (name, fn) => {
  try { await fn(); console.log("OK  ", name); } catch (e) { console.log("FAIL", name, e.message.split("\n")[0]); await shot("fail-" + name.replace(/\W+/g, "_")); }
};
await page.goto("http://localhost:8080/");
await page.getByRole("button", { name: "Connection" }).first().click();
await page.locator(".type-card").filter({ has: page.locator(`span:text-is("MySQL")`) }).click();
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
await field("Connection Name").fill("Local MySQL");
await field("Host").fill("127.0.0.1");
await field("Port").fill("33306");
await field("Password").fill("chloe");
await page.getByRole("button", { name: "Save" }).click();
await page.locator(".tree-row", { hasText: "Local MySQL" }).dblclick();
await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).dblclick();
await page.locator(".tree-row", { hasText: "Tables" }).first().click();
await page.locator(".object-list tr", { hasText: "customers" }).first().waitFor();

await step("table menu", async () => {
  await page.locator(".object-list tr", { hasText: "customers" }).first().click({ button: "right" });
  await page.locator(".menu-item", { hasText: "Maintain" }).hover();
  await page.waitForTimeout(200);
  await shot("t1-table-menu");
  await page.keyboard.press("Escape");
});
await step("empty area menu", async () => {
  await page.locator(".object-list").click({ button: "right", position: { x: 300, y: 500 } });
  await page.locator(".context-menu").waitFor();
  await shot("t2-area-menu");
  await page.keyboard.press("Escape");
});
await step("multi select + maintain", async () => {
  await page.locator(".object-list tr", { hasText: "customers" }).first().click();
  await page.locator(".object-list tr", { hasText: "orders" }).first().click({ modifiers: ["Meta"] });
  await page.locator(".object-list tr", { hasText: "orders" }).first().click({ button: "right" });
  await page.locator(".menu-item", { hasText: "Maintain" }).hover();
  await page.locator(".context-menu .context-menu .menu-item", { hasText: "Analyze Table" }).click();
  await page.locator(".modal .dialog-message").waitFor();
  await shot("t3-maintain-result");
  await page.getByRole("button", { name: "OK" }).click();
});
await step("export wizard", async () => {
  await page.getByRole("button", { name: "Export" }).click();
  await page.locator(".format-list").waitFor();
  await page.locator(".format-item", { hasText: "Excel" }).click();
  await page.getByRole("button", { name: "Next" }).click();
  await page.locator(".table-pick").waitFor();
  await shot("t4-export-tables");
  await page.getByRole("button", { name: "Next" }).click();
  await shot("t5-export-options");
  await page.getByRole("button", { name: "Cancel" }).click();
});
await step("import wizard", async () => {
  await page.getByRole("button", { name: "Import" }).click();
  await page.locator(".wizard-steps").waitFor();
  await shot("t6-import");
  await page.getByRole("button", { name: "Cancel" }).click();
});
await step("tree db menu", async () => {
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).click({ button: "right" });
  await page.locator(".menu-item", { hasText: "Dump SQL File" }).hover();
  await page.waitForTimeout(200);
  await shot("t7-db-menu");
  await page.keyboard.press("Escape");
});
console.log(errors.length ? "ERRORS:\n" + errors.join("\n") : "no page errors");
await browser.close();
