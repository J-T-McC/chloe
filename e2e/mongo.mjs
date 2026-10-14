// MongoDB: connection dialog, tree, collection list, viewer (grid / tree / JSON, filter,
// cell edit, add and delete), query tab with completion, MongoShell, designer, monitor.
import { chromium } from "playwright";
const out = process.argv[2];
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 880 } });
page.setDefaultTimeout(10000);
page.on("pageerror", (e) => console.log("PAGE ERROR", e.message));
const shot = (n) => page.screenshot({ path: `${out}/${n}.png` });
const step = async (name, fn) => {
  try {
    await fn();
    console.log("OK  ", name);
  } catch (e) {
    console.log("FAIL", name, e.message.split("\n")[0]);
    await shot("fail-" + name.replace(/\W+/g, "_"));
  }
};
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input, select, textarea").first();
const texts = async (sel) => (await page.locator(sel).allInnerTexts()).map((x) => x.replace(/\s+/g, " ").trim());
const tab = () => page.locator(".tab-pane:visible");

await page.goto("http://localhost:8080/");

await step("create connection", async () => {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("MongoDB")`) }).click();
  await field("Connection Name").fill("Mongo");
  await field("Host").fill("127.0.0.1");
  await field("Port").fill("37017");
  await field("User Name").fill("root");
  await field("Password").fill("chloe");
  await shot("m01-dialog");
  await page.getByRole("button", { name: "Test Connection" }).click();
  await page.locator(".status-line.ok").waitFor();
  console.log("    ", await page.locator(".status-line").innerText());
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator(".tree-row", { hasText: "Mongo" }).waitFor();
});

await step("tree", async () => {
  await page.locator(".tree-row", { hasText: "Mongo" }).dblclick();
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).waitFor();
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).dblclick();
  await page.locator(".tree-row", { hasText: "Collections" }).waitFor();
  await page.locator(".tree-row", { hasText: "Collections" }).dblclick();
  await page.locator(".tree-row", { hasText: "orders" }).first().waitFor();
  console.log("    ", (await texts(".tree-row")).join(" | "));
});

await step("collection list", async () => {
  await page.locator(".tree-row", { hasText: "Collections" }).click();
  await page.locator(".object-list tr", { hasText: "orders" }).waitFor();
  const rows = await texts(".object-list tbody tr");
  console.log("    ", rows.join(" | "));
  if (!rows.some((r) => r.startsWith("orders 60"))) throw new Error("orders count");
  await page.locator(".object-list tr", { hasText: "customers" }).click();
  await page.locator(".info-panel .info-head", { hasText: "customers" }).waitFor();
  await shot("m02-list");
});

await step("open collection grid", async () => {
  await page.locator(".object-list tr", { hasText: "orders" }).dblclick();
  await tab().locator(".grid-cell").first().waitFor();
  console.log("    ", await tab().locator(".statusbar").innerText().then((t) => t.replace(/\s+/g, " ")));
  const heads = await texts(".tab-pane:visible .grid-hcell");
  console.log("     columns:", heads.slice(0, 8).join(", "));
  await shot("m03-grid");
});

await step("filter", async () => {
  await tab().getByRole("button", { name: "Filter" }).click();
  await tab().locator(".mongo-filter input").first().fill("{ status: 'paid', customer_id: { $in: [1, 2] } }");
  await tab().locator(".mongo-filter input").nth(1).fill("{ total: -1 }");
  await tab().locator(".mongo-filter").getByRole("button", { name: "Apply" }).click();
  await tab().locator(".statusbar", { hasText: /^.*\b10 documents/ }).waitFor();
  console.log("    ", await tab().locator(".statusbar").innerText().then((t) => t.replace(/\s+/g, " ")));
});

await step("edit document loads the whole document", async () => {
  // The filter bar still has a sort; add a projection that hides items.
  await tab().locator(".mongo-filter input").nth(2).fill("{ items: 0 }");
  await tab().locator(".mongo-filter").getByRole("button", { name: "Apply" }).click();
  await page.waitForTimeout(500);
  await tab().locator(".grid-cell").first().click();
  await tab().locator(".toolbar").getByRole("button", { name: "Edit" }).click();
  await page.locator(".doc-editor .monaco-editor").waitFor();
  await page.waitForTimeout(300);
  const text = await page.locator(".doc-editor .view-lines").innerText();
  if (!text.includes("items")) throw new Error("projected fields missing from the editor: " + text.slice(0, 120));
  await page.locator(".doc-editor").getByRole("button", { name: "Cancel" }).click();
  await tab().locator(".mongo-filter input").nth(2).fill("");
  await tab().locator(".mongo-filter").getByRole("button", { name: "Apply" }).click();
});

await step("tree and JSON views", async () => {
  await tab().locator(".mode-switch button[title='Tree View']").click();
  await tab().locator(".doc-tree-row").first().waitFor();
  await tab().locator(".doc-tree-row").first().dblclick();
  await page.waitForTimeout(200);
  console.log("     tree:", (await texts(".tab-pane:visible .doc-tree-row")).slice(0, 7).join(" | "));
  await shot("m04-tree");
  await tab().locator(".mode-switch button[title='JSON View']").click();
  await page.waitForTimeout(500);
  const json = await tab().locator(".doc-json .view-lines").innerText();
  console.log("     json:", json.split("\n").slice(0, 4).join(" "));
  if (!/ObjectId\(/.test(json)) throw new Error("no ObjectId in JSON view");
  await shot("m05-json");
  await tab().locator(".mode-switch button[title='Grid View']").click();
});

await step("edit cell", async () => {
  await page.locator(".object-list tr", { hasText: "customers" }).dblclick().catch(async () => {
    await page.locator(".tab", { hasText: "Objects" }).click();
    await page.locator(".object-list tr", { hasText: "customers" }).dblclick();
  });
  await tab().locator(".grid-cell", { hasText: "Ada Lovelace" }).waitFor();
  await tab().locator(".grid-cell", { hasText: "Ada Lovelace" }).dblclick();
  await page.keyboard.press("Meta+A");
  await page.keyboard.type("Ada King");
  await page.keyboard.press("Enter");
  await tab().locator(".statusbar", { hasText: "Saved full_name" }).waitFor();
  await tab().locator(".grid-cell", { hasText: "Ada King" }).waitFor();
});

await step("add and delete document", async () => {
  await tab().locator(".toolbar").getByRole("button", { name: "Add" }).click();
  await page.locator(".doc-editor .monaco-editor").waitFor();
  await page.keyboard.press("Meta+A");
  await page.keyboard.type('{ _id: 99, email: "new@example.com", full_name: "New Person", n: NumberLong(7)', { delay: 5 });
  await shot("m06-add");
  await page.locator(".doc-editor").getByRole("button", { name: "Add" }).click();
  await page.locator(".doc-editor").waitFor({ state: "detached" });
  await tab().locator(".grid-cell", { hasText: "New Person" }).waitFor();
  await tab().locator(".statusbar", { hasText: "5 documents" }).waitFor();
  await tab().locator(".grid-cell", { hasText: "New Person" }).click();
  await tab().locator(".statusbar button[title='Delete Document']").click();
  await page.locator(".modal.small").getByRole("button", { name: "Delete" }).click();
  await tab().locator(".statusbar", { hasText: "4 documents" }).waitFor();
});

await step("validation error shows", async () => {
  await tab().locator(".toolbar").getByRole("button", { name: "Add" }).click();
  await page.locator(".doc-editor .monaco-editor").waitFor();
  await page.keyboard.press("Meta+A");
  await page.keyboard.type("{ full_name: 'no email'", { delay: 5 });
  await page.locator(".doc-editor").getByRole("button", { name: "Add" }).click();
  await page.locator(".doc-editor .status-line.err").waitFor();
  console.log("    ", (await page.locator(".doc-editor .status-line.err").innerText()).slice(0, 90));
  await page.locator(".doc-editor").getByRole("button", { name: "Cancel" }).click();
});

await step("query tab", async () => {
  await page.getByRole("button", { name: "New Query" }).first().click();
  await tab().locator(".monaco-editor").waitFor();
  await tab().locator(".monaco-editor .view-lines").click();
  await page.keyboard.type("db.", { delay: 30 });
  await page.locator(".suggest-widget .monaco-list-row").first().waitFor();
  const colls = await texts(".suggest-widget .monaco-list-row .label-name");
  console.log("     db. ->", colls.slice(0, 6).join(", "));
  if (!colls.includes("orders")) throw new Error("no collections after db.");
  await page.keyboard.type("orders.find({ st", { delay: 40 });
  await page.locator(".suggest-widget .monaco-list-row").first().waitFor();
  const fields = await texts(".suggest-widget .monaco-list-row .label-name");
  console.log("     fields ->", fields.slice(0, 6).join(", "));
  if (!fields.includes("status")) throw new Error("no field completion");
  await page.keyboard.press("Escape");
  await page.keyboard.press("Meta+A");
  await page.keyboard.press("Backspace");
  await page.keyboard.type('db.orders.find({ status: "paid" }).sort({ total: -1 })\nprint("hello")\ndb.orders.countDocuments()', { delay: 5 });
  await page.keyboard.press("Escape");
  await tab().getByRole("button", { name: "Run", exact: true }).click();
  await tab().locator(".result-tabs button", { hasText: "Result 2" }).waitFor();
  await tab().locator(".grid-cell").first().waitFor();
  console.log("    ", await tab().locator(".results .statusbar").innerText().then((t) => t.replace(/\s+/g, " ")));
  await shot("m07-query");
  await tab().locator(".result-tabs button", { hasText: "Message" }).click();
  console.log("     message:", (await tab().locator(".messages").innerText()).replace(/\s+/g, " ").slice(0, 200));
  await tab().locator(".result-tabs button", { hasText: "Result 2" }).click();
  const v = await tab().locator(".result-value").innerText();
  if (v.trim() !== "60") throw new Error("countDocuments result " + v);
});

await step("query errors", async () => {
  await tab().locator(".monaco-editor .view-lines").click();
  await page.keyboard.press("Meta+A");
  await page.keyboard.type("let x = 1\nnope.find()", { delay: 5 });
  await page.keyboard.press("Escape");
  await tab().getByRole("button", { name: "Run", exact: true }).click();
  await tab().locator(".msg.err").waitFor();
  console.log("    ", await tab().locator(".msg.err").innerText());
});

await step("mongoshell", async () => {
  await page.locator(".tab", { hasText: "Objects" }).click();
  await page.locator(".objects-pane .toolbar").getByRole("button", { name: "MongoShell" }).click();
  const input = tab().locator(".redis-console-in input");
  await input.waitFor();
  await page.waitForTimeout(300);
  for (const cmd of ["db.customers.countDocuments()", "var n = 2", "db.orders.find({}, { status: 1 }).limit(25)", "it", "use analytics", "show collections", "db.customers.find({"]) {
    await input.fill(cmd);
    await input.press("Enter");
    await page.waitForTimeout(250);
  }
  await input.fill("_id: 1 })");
  await input.press("Enter");
  await page.waitForTimeout(300);
  const lines = await tab().locator(".redis-console-out pre").allInnerTexts();
  console.log("     ", lines.slice(1).map((l) => l.replace(/\s+/g, " ").slice(0, 70)).join("\n      "));
  await shot("m08-shell");
  const all = lines.join("\n");
  if (!all.includes('Type "it" for more') || !all.includes("pageviews") || !all.includes("analytics>")) throw new Error("console output");
});

await step("designer", async () => {
  await page.locator(".tab", { hasText: "Objects" }).click();
  await page.locator(".object-list tr", { hasText: "customers" }).click();
  await page.locator(".objects-pane .toolbar").getByRole("button", { name: "Design Collection" }).click();
  await tab().locator(".fields-grid tbody tr").nth(1).waitFor();
  console.log("     indexes:", (await tab().locator(".fields-grid tbody tr input:not([type=checkbox])").evaluateAll((els) => els.map((e) => e.value))).filter(Boolean).join(" | "));
  await tab().getByRole("button", { name: "Add Index" }).click();
  await tab().locator(".fields-grid tbody tr").last().locator("input").nth(1).fill("full_name, created_at:-1");
  await tab().locator(".designer-tabs button", { hasText: "Validation" }).click();
  console.log("     validator:", (await tab().locator("textarea.code").inputValue()).replace(/\s+/g, " ").slice(0, 80));
  await tab().locator(".designer-tabs button", { hasText: "Script Preview" }).click();
  await page.waitForTimeout(400);
  console.log("     script:", (await tab().locator(".code-block").innerText()).replace(/\s+/g, " "));
  await shot("m09-designer");
  await tab().getByRole("button", { name: "Save" }).click();
  await tab().locator(".designer-tabs button", { hasText: "Indexes" }).click();
  await tab().locator(".fields-grid tbody tr input[value='full_name_1_created_at_-1']").waitFor();
});

await step("hide an index in place", async () => {
  const row = tab().locator(".fields-grid tbody tr", { has: page.locator("input[value='email_1']") });
  await row.locator("input[type=checkbox]").nth(2).check();
  await tab().locator(".designer-tabs button", { hasText: "Script Preview" }).click();
  await page.waitForTimeout(400);
  const script = (await tab().locator(".code-block").innerText()).replace(/\s+/g, " ");
  console.log("     script:", script);
  if (!script.includes("collMod") || script.includes("dropIndex")) throw new Error("hiding should use collMod");
  await tab().getByRole("button", { name: "Save" }).click();
  await tab().locator(".designer-tabs button", { hasText: "Indexes" }).click();
  await page.waitForTimeout(500);
  const hidden = await tab().locator(".fields-grid tbody tr", { has: page.locator("input[value='email_1']") }).locator("input[type=checkbox]").nth(2).isChecked();
  if (!hidden) throw new Error("index not hidden after saving");
});

await step("view is read-only", async () => {
  await page.locator(".tree-row", { hasText: "Views" }).dblclick();
  await page.locator(".tree-row", { hasText: "paid_orders" }).dblclick();
  await tab().locator(".badge", { hasText: "read-only" }).waitFor();
  await tab().locator(".grid-cell").first().waitFor();
  console.log("    ", await tab().locator(".statusbar").innerText().then((t) => t.replace(/\s+/g, " ")));
});

await step("server monitor", async () => {
  await page.getByRole("button", { name: "Monitor" }).click();
  await tab().locator("tbody tr").first().waitFor();
  console.log("     processes:", await tab().locator("tbody tr").count());
  await shot("m10-monitor");
});

await step("export and import", async () => {
  const dir = process.env.LITE;
  await page.locator(".tab", { hasText: "Objects" }).click();
  await page.locator(".tree-row", { hasText: "Collections" }).click();
  await page.locator(".object-list tr", { hasText: "customers" }).click();
  await page.locator(".objects-pane .toolbar").getByRole("button", { name: "Export" }).click();
  await field("Format").selectOption("csv");
  await field("File").fill(`${dir}/customers.csv`);
  await page.locator(".modal").getByRole("button", { name: "Export" }).click();
  await page.locator(".jobs-panel, .job", { hasText: "Exported 4 documents" }).first().waitFor();
  await page.locator(".object-list tr", { hasText: "events" }).click();
  await page.locator(".objects-pane .toolbar").getByRole("button", { name: "Import" }).click();
  await field("File").fill(`${dir}/customers.csv`);
  await field("Format").selectOption("csv");
  await field("Import Mode").selectOption("copy");
  await page.locator(".modal").getByRole("button", { name: "Import" }).click();
  await page.locator(".jobs-panel, .job", { hasText: "4 inserted" }).first().waitFor();
  console.log("    ", (await page.locator(".job").allInnerTexts()).map((t) => t.replace(/\s+/g, " ").slice(0, 110)).join(" | "));
  await shot("m11-jobs");
});

await step("users and roles", async () => {
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).click();
  await page.getByRole("button", { name: "User", exact: true }).click();
  await tab().locator(".object-list tbody tr").first().waitFor();
  console.log("     users:", (await texts(".tab-pane:visible .object-list tbody tr")).join(" | "));
  // a role
  await tab().locator(".seg-tabs button", { hasText: "Roles" }).click();
  await tab().getByRole("button", { name: "New Role" }).click();
  await tab().locator(".field", { has: page.locator("label:text-is('Role Name')") }).locator("input").fill("reporter");
  await tab().getByRole("button", { name: "Add Privilege" }).click();
  await tab().locator(".fields-grid tbody tr").last().locator("input").nth(1).fill("orders");
  await tab().locator(".fields-grid tbody tr").last().locator("input[list=mongo-actions]").fill("find, listIndexes");
  await page.waitForTimeout(400);
  console.log("     role script:", (await tab().locator(".code-block").innerText()).replace(/\s+/g, " "));
  await tab().getByRole("button", { name: "Create Role" }).click();
  await tab().locator(".object-list tr", { hasText: "reporter" }).waitFor();
  // a user granted it
  await tab().locator(".seg-tabs button", { hasText: "Users" }).click();
  await tab().getByRole("button", { name: "New User" }).click();
  const f = (l) => tab().locator(".field", { has: page.locator(`label:text-is('${l}')`) }).locator("input").first();
  await f("User Name").fill("app");
  await f("Password").fill("pw1");
  await f("Confirm Password").fill("pw1");
  await tab().getByRole("button", { name: "Add Role" }).click();
  await tab().locator(".fields-grid tbody tr").last().locator("input").first().fill("reporter");
  await page.waitForTimeout(400);
  console.log("     user script:", (await tab().locator(".code-block").innerText()).replace(/\s+/g, " "));
  await tab().getByRole("button", { name: "Create User" }).click();
  await tab().locator(".object-list tr", { hasText: /^\s*app/ }).waitFor();
  await shot("m12-users");
  // delete both again
  await tab().locator(".object-list tr", { hasText: /^\s*app/ }).click();
  await tab().getByRole("button", { name: "Delete User" }).click();
  await page.locator(".modal.small").getByRole("button", { name: "Delete" }).click();
  await tab().locator(".object-list tr", { hasText: /^\s*app/ }).waitFor({ state: "detached" });
  await tab().locator(".seg-tabs button", { hasText: "Roles" }).click();
  await tab().locator(".object-list tr", { hasText: "reporter" }).click();
  await tab().getByRole("button", { name: "Delete Role" }).click();
  await page.locator(".modal.small").getByRole("button", { name: "Delete" }).click();
  await tab().locator(".object-list tr", { hasText: "reporter" }).waitFor({ state: "detached" });
});

await browser.close();
