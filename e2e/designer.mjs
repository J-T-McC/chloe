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
for (const [label, type, port, user] of [["Local MySQL", "MySQL", "33306", "root"], ["Local Postgres", "PostgreSQL", "55432", "postgres"]]) {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("${type}")`) }).click();
  const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
  await field("Connection Name").fill(label);
  await field("Host").fill("127.0.0.1");
  await field("Port").fill(port);
  await field("User Name").fill(user);
  await field("Password").fill("chloe");
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator(".tree-row", { hasText: label }).waitFor();
}

await step("new table", async () => {
  await page.locator(".tree-row", { hasText: "Local MySQL" }).dblclick();
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).dblclick();
  await page.locator(".tree-row", { hasText: "Tables" }).first().click({ button: "right" });
  await page.locator(".menu-item", { hasText: "New Table" }).click();
  await page.locator(".designer .name-input").fill("pw_notes");
  await page.getByRole("button", { name: "Add Field" }).click();
  const row = page.locator(".fields-grid tbody tr").nth(1);
  await row.locator("td").nth(1).locator("input").fill("title");
  await row.locator("td").nth(3).locator("input").fill("120");
  await row.locator("td").nth(5).locator("input").check();
  await page.getByRole("button", { name: "Add Field" }).click();
  const row2 = page.locator(".fields-grid tbody tr").nth(2);
  await row2.locator("td").nth(1).locator("input").fill("body");
  await row2.locator("td").nth(2).locator("input").fill("text");
  await page.locator(".designer-tabs button", { hasText: "Indexes" }).click();
  await page.getByRole("button", { name: "Add Index" }).click();
  await page.locator(".list-tab .field-picker select").first().selectOption("title");
  await page.locator(".designer-tabs button", { hasText: "SQL Preview" }).click();
  await page.locator(".designer-sql .code-block").waitFor();
  await page.waitForTimeout(300);
  await shot("d1-new-table-sql");
  await page.locator(".designer-tabs button", { hasText: "Fields" }).click();
  await page.locator(".fields-grid tbody tr").nth(1).click();
  await shot("d2-fields");
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator(".tab", { hasText: "pw_notes (design)" }).waitFor();
  await page.locator(".tree-row", { hasText: "Tables" }).first().dblclick();
  await page.locator(".tree-row", { hasText: "pw_notes" }).waitFor({ timeout: 5000 });
});

await step("alter table", async () => {
  await page.locator(".tab", { hasText: "pw_notes (design)" }).click();
  const row = page.locator(".fields-grid tbody tr").nth(1);
  await row.locator("td").nth(3).locator("input").fill("200");
  await row.locator("td").nth(1).locator("input").fill("headline");
  await page.locator(".designer-tabs button", { hasText: "SQL Preview" }).click();
  await page.waitForTimeout(500);
  await shot("d3-alter-sql");
  const sql = (await page.locator(".designer-sql .code-block").innerText()).replace(/ /g, " ");
  if (!sql.includes("CHANGE COLUMN")) throw new Error("expected CHANGE COLUMN, got " + sql);
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator(".designer-tabs button", { hasText: /^SQL Preview$/ }).waitFor();
  await page.locator(".tab", { hasText: "Objects" }).click();
  await page.locator(".object-list tr", { hasText: "pw_notes" }).waitFor();
});

await step("save prompt on close", async () => {
  const dialog = () => page.locator(".modal.small");
  const tab = page.locator(".tab", { hasText: "pw_notes (design)" });
  await tab.click();
  await page.locator(".designer-tabs button", { hasText: "Fields" }).click();
  await page.locator(".fields-grid tbody tr").nth(1).locator("td").nth(3).locator("input").fill("250");
  await tab.locator(".tab-dirty").waitFor();
  await tab.locator(".tab-close").click();
  await dialog().getByText("Save changes to “pw_notes (design)”?").waitFor();
  await shot("d4-save-prompt");
  await dialog().getByRole("button", { name: "Cancel" }).click();
  await tab.waitFor();
  await tab.locator(".tab-close").click();
  await dialog().getByRole("button", { name: "Save", exact: true }).click();
  await tab.waitFor({ state: "detached" });
  // the change was applied before the tab closed
  await page.locator(".tree-row", { hasText: "pw_notes" }).click({ button: "right" });
  await page.locator(".menu-item", { hasText: "Design Table" }).click();
  const len = await page.locator(".fields-grid tbody tr").nth(1).locator("td").nth(3).locator("input").inputValue();
  if (len !== "250") throw new Error("expected length 250, got " + len);
  await page.locator(".tab.active .tab-close").click();
  await tab.waitFor({ state: "detached" });
});

await step("pg identity", async () => {
  await page.locator(".tree-row", { hasText: "Local Postgres" }).dblclick();
  await page.locator(".tree-row", { hasText: /^\s*postgres\s*$/ }).first().dblclick();
  await page.locator(".tree-row", { hasText: "pg_catalog" }).waitFor();
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).last().dblclick();
  await page.locator(".tree-row", { hasText: "Tables" }).last().dblclick();
  await page.waitForFunction(() => [...document.querySelectorAll(".tree-row")].filter((r) => r.textContent.trim() === "products").length === 2);
  await page.locator(".tree-row", { hasText: "products" }).last().click({ button: "right" });
  await page.locator(".menu-item", { hasText: "Design Table" }).click();
  await page.locator(".fields-grid tbody tr").first().waitFor();
  await page.locator(".fields-grid tbody tr").first().click();
  await page.waitForTimeout(300);
  await shot("d4-pg-identity");
});

await step("delete table", async () => {
  await page.locator(".tree-row", { hasText: "pw_notes" }).click({ button: "right" });
  await page.locator(".menu-item", { hasText: "Delete Table" }).click();
  await page.locator(".modal").waitFor();
  await shot("d5-delete-confirm");
  await page.getByRole("button", { name: "Delete" }).last().click();
  await page.locator(".tree-row", { hasText: "pw_notes" }).waitFor({ state: "detached" });
});
console.log(errors.length ? "ERRORS:\n" + errors.join("\n") : "no page errors");
await browser.close();
