// Saved queries (Queries folder, save / save as / rename / duplicate / delete, the
// Save prompt on close), query tabs restored after a reload, reconnecting a query tab,
// and the History Log.
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
const pane = () => page.locator(".tab-pane:visible");
const activeTab = () => page.locator(".tab.active");
const dialog = () => page.locator(".modal.small");
// Replace the visible editor's text.
const setText = async (text) => {
  await pane().locator(".monaco-editor .view-lines").click();
  await page.keyboard.press("Meta+A");
  await page.keyboard.press("Backspace");
  await page.keyboard.insertText(text);
  await page.keyboard.press("Escape");
};
const queriesFolder = () => page.locator(".tree-row", { hasText: /^\s*Queries\s*$/ }).first();

await page.goto("http://localhost:8080/");
await page.waitForTimeout(600);

await step("create connection", async () => {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator('span:text-is("MySQL")') }).click();
  await field("Connection Name").fill("Local MySQL");
  await field("Host").fill("127.0.0.1");
  await field("Port").fill("33306");
  await field("User Name").fill("root");
  await field("Password").fill("chloe");
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator(".tree-row", { hasText: "Local MySQL" }).dblclick();
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).dblclick();
  await queriesFolder().waitFor();
});

await step("save a new query", async () => {
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).click({ button: "right" });
  await page.locator(".context-menu").getByText("New Query", { exact: true }).click();
  await pane().locator(".monaco-editor").waitFor();
  await setText("SELECT full_name FROM customers ORDER BY full_name;");
  await activeTab().locator(".tab-dirty").waitFor();
  await page.keyboard.press("Meta+R");
  await pane().locator(".result-tabs button", { hasText: "Result 1" }).waitFor({ timeout: 10000 });
  await pane().locator(".monaco-editor .view-lines").click();
  await page.keyboard.press("Meta+S");
  await dialog().locator("input").waitFor();
  if ((await dialog().locator("input").inputValue()) !== "Untitled") throw new Error("default name isn't Untitled");
  await dialog().locator("input").fill("Top customers");
  await dialog().getByRole("button", { name: "OK" }).click();
  await activeTab().filter({ hasText: "Top customers" }).waitFor();
  if (await activeTab().locator(".tab-dirty").count()) throw new Error("still marked unsaved after saving");
  await queriesFolder().dblclick();
  await page.locator(".tree-row", { hasText: "Top customers" }).waitFor();
});

await step("queries in the Objects pane", async () => {
  await page.locator(".tab", { hasText: "Objects" }).click();
  await page.locator(".object-list tr", { hasText: "Top customers" }).click();
  await page.locator(".info-panel .code-block", { hasText: "SELECT full_name FROM customers" }).waitFor();
  console.log("     row:", (await page.locator(".object-list tr", { hasText: "Top customers" }).innerText()).replace(/\s+/g, " "));
  await shot("q1-objects");
});

await step("save prompt on close", async () => {
  await page.locator(".tab", { hasText: "Top customers" }).click();
  await setText("SELECT full_name, email FROM customers;");
  await activeTab().locator(".tab-dirty").waitFor();
  await activeTab().locator(".tab-close").click();
  await dialog().getByText("Save changes to “Top customers”?").waitFor();
  await shot("q2-save-prompt");
  await dialog().getByRole("button", { name: "Cancel" }).click();
  await page.locator(".tab", { hasText: "Top customers" }).waitFor();
  await activeTab().locator(".tab-close").click();
  await dialog().getByRole("button", { name: "Don't Save" }).click();
  await page.locator(".tab", { hasText: "Top customers" }).waitFor({ state: "detached" });
  // reopening shows the saved text, not the discarded edit
  await page.locator(".tree-row", { hasText: "Top customers" }).dblclick();
  await pane().locator(".view-lines", { hasText: "SELECT full_name FROM customers ORDER BY full_name;" }).waitFor();
  // and opening it again reuses the tab
  await page.locator(".tree-row", { hasText: "Top customers" }).dblclick();
  if ((await page.locator(".tab", { hasText: "Top customers" }).count()) !== 1) throw new Error("a second tab opened");
});

await step("save as, rename, duplicate, delete", async () => {
  await pane().locator(".monaco-editor .view-lines").click();
  await page.keyboard.press("Meta+Shift+S");
  await dialog().locator("input").fill("Customer names");
  await dialog().getByRole("button", { name: "OK" }).click();
  await activeTab().filter({ hasText: "Customer names" }).waitFor();
  await page.locator(".tree-row", { hasText: "Customer names" }).click({ button: "right" });
  await page.locator(".context-menu").getByText("Rename…").click();
  await dialog().locator("input").fill("All customers");
  await dialog().getByRole("button", { name: "OK" }).click();
  await page.locator(".tree-row", { hasText: "All customers" }).waitFor();
  await page.locator(".tab", { hasText: "All customers" }).waitFor();
  await page.locator(".tree-row", { hasText: "All customers" }).click({ button: "right" });
  await page.locator(".context-menu").getByText("Duplicate").click();
  await page.locator(".tree-row", { hasText: "All customers copy" }).waitFor();
  await page.locator(".tree-row", { hasText: "All customers copy" }).click({ button: "right" });
  await page.locator(".context-menu").getByText("Delete Query").click();
  await dialog().getByRole("button", { name: "Delete" }).click();
  await page.locator(".tree-row", { hasText: "All customers copy" }).waitFor({ state: "detached" });
  // a duplicate name is refused
  await page.locator(".tree-row", { hasText: "All customers" }).click({ button: "right" });
  await page.locator(".context-menu").getByText("Rename…").click();
  await dialog().locator("input").fill("top CUSTOMERS");
  await dialog().getByRole("button", { name: "OK" }).click();
  await dialog().getByText("already exists").waitFor();
  await dialog().getByRole("button", { name: "OK" }).click();
  console.log("     queries:", (await page.locator(".tree-row").allInnerTexts()).filter((t) => /customers/i.test(t)).map((t) => t.trim()).join(" | "));
});

await step("reconnect a query tab", async () => {
  // Save As turned the tab into the new query
  await page.locator(".tab", { hasText: "All customers" }).click();
  await page.locator(".tree-row", { hasText: "Local MySQL" }).click({ button: "right" });
  await page.locator(".context-menu").getByText("Close Connection").click();
  await page.locator(".tab", { hasText: "All customers" }).click();
  await pane().locator(".qe-offline").waitFor();
  await shot("q3-offline");
  await pane().locator(".qe-offline").getByRole("button", { name: "Connect" }).click();
  await pane().locator(".qe-offline").waitFor({ state: "detached" });
  await pane().getByRole("button", { name: "Run", exact: true }).click();
  await pane().locator(".result-tabs button", { hasText: "Result 1" }).waitFor({ timeout: 10000 });
});

await step("tabs come back after a reload", async () => {
  await page.locator(".tree-row", { hasText: "Local MySQL" }).dblclick();
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).click({ button: "right" });
  await page.locator(".context-menu").getByText("New Query", { exact: true }).click();
  await setText("SELECT COUNT(*) FROM orders; -- unsaved draft");
  await pane().locator(".monaco-editor .view-lines").click();
  await page.keyboard.press("Meta+R");
  await page.keyboard.insertText(" ");
  await page.waitForTimeout(1500); // drafts are written shortly after typing stops
  const before = await page.locator(".tab").allInnerTexts();
  await page.reload();
  await page.locator(".tab", { hasText: "All customers" }).waitFor();
  const after = await page.locator(".tab").allInnerTexts();
  console.log("     tabs:", before.map((t) => t.trim()).join(" | "), "→", after.map((t) => t.trim()).join(" | "));
  const draft = page.locator(".tab", { hasText: "Query — shop" });
  await draft.click();
  await draft.locator(".tab-dirty").waitFor();
  await pane().locator(".view-lines", { hasText: "unsaved draft" }).waitFor();
  // it connects when first shown
  await pane().locator(".qe-offline").waitFor({ state: "detached", timeout: 10000 });
  await shot("q4-restored");
});

await step("history log", async () => {
  // a failing statement is logged too
  await setText("SELECT * FROM no_such_table;");
  await page.keyboard.press("Meta+R");
  await pane().locator(".messages .err, .messages", { hasText: "no_such_table" }).first().waitFor();
  await pane().getByRole("button", { name: "History" }).click();
  await activeTab().filter({ hasText: "History Log" }).waitFor();
  await pane().locator(".hist-body .hist-row").first().waitFor();
  const rows = await pane().locator(".hist-body .hist-row").allInnerTexts();
  console.log("     history:", rows.slice(0, 4).map((r) => r.replace(/\s+/g, " ").slice(0, 100)).join(" | "));
  if (!rows.some((r) => r.includes("SELECT full_name FROM customers ORDER BY full_name"))) throw new Error("saved query run missing from history");
  await pane().getByText("Errors only").click();
  const errs = await pane().locator(".hist-body .hist-row").count();
  if (errs !== 1) throw new Error(`errors only shows ${errs} rows`);
  await pane().locator(".hist-body .hist-row").first().click();
  await pane().locator(".hist-detail .error-banner").waitFor();
  await shot("q5-history");
  await pane().getByText("Errors only").click();
  await pane().locator(".hist-body .hist-row", { hasText: "ORDER BY full_name" }).first().dblclick();
  await pane().locator(".view-lines", { hasText: "ORDER BY full_name" }).waitFor();
});

console.log(errors.length ? "ERRORS:\n" + errors.join("\n") : "no page errors");
await browser.close();
process.exit(failed ? 1 : 0);
