import { readFileSync } from "node:fs";
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
  try {
    await fn();
    console.log("OK  ", name);
  } catch (e) {
    console.log("FAIL", name, e.message.split("\n")[0]);
    await shot("fail-" + name.replace(/\W+/g, "_"));
  }
};

await page.goto("http://localhost:8080/");
await page.waitForTimeout(800);
await shot("01-welcome");

for (const [label, type, port, user] of [
  ["Local MySQL", "MySQL", "33306", "root"],
  ["Local Postgres", "PostgreSQL", "55432", "postgres"],
]) {
  await step(`create ${label}`, async () => {
    await page.getByRole("button", { name: "Connection" }).first().click();
    await page.locator(".type-card").filter({ has: page.locator(`span:text-is("${type}")`) }).click();
    const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
    await field("Connection Name").fill(label);
    await field("Host").fill("127.0.0.1");
    await field("Port").fill(port);
    await field("User Name").fill(user);
    await field("Password").fill("chloe");
    await page.getByRole("button", { name: "Test Connection" }).click();
    await page.locator(".status-line.ok").waitFor({ timeout: 10000 });
    if (label === "Local Postgres") await shot("02-conn-dialog");
    await page.getByRole("button", { name: "Save" }).click();
    await page.locator(".tree-row", { hasText: label }).waitFor();
  });
}

await step("expand mysql tree", async () => {
  await page.locator(".tree-row", { hasText: "Local MySQL" }).dblclick();
  await page.locator(".tree-row", { hasText: "shop" }).waitFor({ timeout: 10000 });
  await page.locator(".tree-row", { hasText: "shop" }).dblclick();
  await page.locator(".tree-row", { hasText: "Tables" }).first().click();
  await page.locator(".object-list tr", { hasText: "customers" }).first().click();
  await page.locator(".code-block").waitFor();
  await page.waitForTimeout(300);
  await shot("03-objects");
});

await step("open table + edit", async () => {
  await page.locator(".object-list tr", { hasText: "customers" }).first().dblclick();
  await page.locator(".grid-cell", { hasText: "ada@example.com" }).waitFor({ timeout: 10000 });
  await page.locator(".grid-cell", { hasText: "Ada Lovelace" }).dblclick();
  await page.keyboard.press("Meta+A");
  await page.keyboard.type("Ada King");
  await page.keyboard.press("Enter");
  await page.waitForTimeout(200);
  await shot("04-table-edit");
  await page.getByRole("button", { name: "Apply" }).click();
  await page.locator(".statusbar", { hasText: "applied" }).waitFor({ timeout: 5000 });
  await page.locator(".grid-cell", { hasText: "Ada King" }).waitFor();
});

await step("query editor autocomplete", async () => {
  await page.getByRole("button", { name: "New Query" }).first().click();
  await page.locator(".monaco-editor").first().waitFor();
  await page.locator(".monaco-editor .view-lines").first().click();
  await page.keyboard.type("select * from customers c join ", { delay: 15 });
  await page.locator(".suggest-widget .monaco-list-row").first().waitFor({ timeout: 5000 });
  await page.waitForTimeout(300);
  await shot("05-autocomplete-join");
  await page.keyboard.press("Enter");
  await page.keyboard.type(" where c.", { delay: 15 });
  await page.locator(".suggest-widget .monaco-list-row").first().waitFor({ timeout: 5000 });
  await page.waitForTimeout(300);
  await shot("06-autocomplete-columns");
  await page.keyboard.press("Escape");
  await page.keyboard.type("id > 0;", { delay: 10 });
  await page.keyboard.press("Meta+R");
  await page.locator(".result-tabs button", { hasText: "Result 1" }).waitFor({ timeout: 10000 });
  await page.waitForTimeout(400);
  await shot("07-query-result");
});

await step("autocomplete in UPDATE / DELETE / INSERT", async () => {
  const rows = page.locator(".suggest-widget .monaco-list-row");
  const expectRow = async (label) => {
    await rows.filter({ hasText: label }).first().waitFor({ timeout: 5000 });
    await page.keyboard.press("Escape");
  };
  await page.keyboard.press("Meta+A");
  await page.keyboard.press("Backspace");
  await page.keyboard.type("update customers ", { delay: 15 });
  await expectRow("SET");
  await page.keyboard.type("set ", { delay: 15 });
  await rows.filter({ hasText: "email" }).first().waitFor({ timeout: 5000 });
  await shot("07a-autocomplete-update-set");
  await page.keyboard.press("Escape");
  await page.keyboard.type("email = 'x' w", { delay: 15 });
  await expectRow("WHERE");
  await page.keyboard.press("Meta+A");
  await page.keyboard.press("Backspace");
  await page.keyboard.type("delete ", { delay: 15 });
  await expectRow("FROM");
  await page.keyboard.type("from customers ", { delay: 15 });
  await expectRow("WHERE");
  await page.keyboard.press("Meta+A");
  await page.keyboard.press("Backspace");
  await page.keyboard.type("insert into customers ", { delay: 15 });
  await expectRow("VALUES");
  await page.keyboard.press("Meta+A");
  await page.keyboard.press("Backspace");
});

await step("postgres tree", async () => {
  await page.locator(".tree-row", { hasText: "Local Postgres" }).dblclick();
  await page.locator(".tree-row", { hasText: /^\s*postgres\s*$/ }).first().waitFor({ timeout: 10000 });
  await page.locator(".tree-row", { hasText: /^\s*postgres\s*$/ }).first().dblclick();
  await page.locator(".tree-row", { hasText: "pg_catalog" }).waitFor();
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).last().dblclick();
  await page.locator(".tree-row", { hasText: "Functions" }).last().click();
  await page.getByRole("tab").first().waitFor({ timeout: 100 }).catch(() => {});
  await page.locator(".tab", { hasText: "Objects" }).click();
  await page.locator(".object-list tr", { hasText: "order_total" }).first().click();
  await page.waitForTimeout(600);
  await shot("08-pg-functions");
});


await step("postgres keyword highlighting", async () => {
  await page.getByRole("button", { name: "New Query" }).first().click();
  const editor = page.locator(".monaco-editor").last();
  await editor.locator(".view-lines").click();
  await page.keyboard.type("UPDATE users SET email = 'x' WHERE id = 1 RETURNING id", { delay: 5 });
  await page.keyboard.press("Escape");
  const spans = await editor.locator(".view-line span span").evaluateAll((els) => els.map((e) => [e.textContent.trim(), e.className]));
  const cls = (word) => spans.find(([t]) => t === word)?.[1];
  const where = await cls("WHERE");
  for (const w of ["UPDATE", "SET", "RETURNING"]) {
    const c = await cls(w);
    if (c !== where) throw new Error(`${w} is ${c}, WHERE is ${where}`);
  }
  if ((await cls("users")) === where) throw new Error("identifier highlighted as keyword");
  await shot("08a-pg-highlighting");
});

await step("db open/close", async () => {
  const shop = page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).first();
  await shop.click({ button: "right" });
  await page.locator(".menu-item", { hasText: "Close Database" }).click();
  await page.locator(".tree-row", { hasText: "Tables" }).first().waitFor({ state: "detached", timeout: 3000 }).catch(() => {});
  await shop.click({ button: "right" });
  await page.locator(".menu-item", { hasText: "Open Database" }).waitFor();
  await shot("09-db-closed-menu");
  await page.keyboard.press("Escape");
});

await step("empty-area menu + group", async () => {
  await page.locator(".sidebar .tree").click({ button: "right", position: { x: 100, y: (await page.locator(".sidebar .tree").boundingBox()).height - 8 } });
  await page.locator(".context-menu").waitFor();
  await shot("10-empty-menu");
  await page.locator(".menu-item", { hasText: "New Group" }).click();
  await page.locator(".modal input").fill("Production");
  await page.keyboard.press("Enter");
  await page.locator(".tree-row", { hasText: "Production" }).waitFor();
  await page.locator(".tree-row", { hasText: "Local Postgres" }).click({ button: "right" });
  await page.locator(".menu-item", { hasText: "Move to Group" }).hover();
  await page.locator(".context-menu .context-menu .menu-item", { hasText: "Production" }).click();
  await page.waitForTimeout(400);
  await shot("11-groups");
});

await step("preset + url", async () => {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".picker-heading", { hasText: "Hosted services" }).waitFor();
  await shot("12-picker");
  await page.locator(".url-paste input").fill("postgresql://postgres.abcd:pw@aws-0-us-east-1.pooler.supabase.com:6543/postgres?sslmode=require");
  await page.getByRole("button", { name: "Use URL" }).click();
  await page.locator(".preset-help").waitFor();
  await shot("13-supabase-form");
  await page.getByRole("button", { name: "Advanced" }).click();
  await shot("14-advanced");
  await page.getByRole("button", { name: "Cancel" }).click();
});

await step("server monitor", async () => {
  await page.getByRole("button", { name: "Monitor" }).click();
  await page.locator(".monitor-table tbody tr").first().waitFor({ timeout: 8000 });
  await page.waitForTimeout(500);
  await shot("15-monitor");
  await page.locator(".seg button", { hasText: "Variables" }).click();
  await page.locator(".monitor-table td.mono").first().waitFor();
  await page.locator(".monitor .search").fill("max_connections");
  await page.waitForTimeout(300);
  await shot("16-monitor-vars");
});

await step("beautify", async () => {
  await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).last().click();
  await page.getByRole("button", { name: "New Query" }).first().click();
  await page.locator(".monaco-editor .view-lines").last().click();
  await page.keyboard.type("select c.full_name,count(*) as n from customers c join orders o on o.customer_id=c.id where o.status='paid' group by c.full_name order by n desc", { delay: 2 });
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "Beautify" }).click();
  await page.waitForTimeout(300);
  await shot("17-beautify");
});

await step("manage connections", async () => {
  // Below the last row, however many databases earlier runs left behind.
  const tree = await page.locator(".sidebar .tree").boundingBox();
  await page.locator(".sidebar .tree").click({ button: "right", position: { x: 100, y: tree.height - 8 } });
  await page.locator(".menu-item", { hasText: "Manage Connections" }).click();
  await page.locator(".modal.manage tbody tr").first().waitFor();
  await shot("18-manage");
  const field = (l) => page.locator(".modal .field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
  const file = `${process.env.LITE}/connections.chloe.json`;
  const before = await page.locator(".modal.manage tbody tr").count();

  // export with passwords: a short or mismatched passphrase is refused
  await page.getByRole("button", { name: /^Export all/ }).click();
  await field("File").fill(file);
  await page.getByText("Include passwords").click();
  await field("Passphrase").fill("short");
  await page.locator(".modal").last().getByRole("button", { name: "Export", exact: true }).click();
  await page.locator(".status-line.err", { hasText: "at least 8" }).waitFor();
  await field("Passphrase").fill("correct horse");
  await field("Confirm Passphrase").fill("correct horsf");
  await page.locator(".modal").last().getByRole("button", { name: "Export", exact: true }).click();
  await page.locator(".status-line.err", { hasText: "don't match" }).waitFor();
  await field("Confirm Passphrase").fill("correct horse");
  await shot("18b-export");
  await page.locator(".modal").last().getByRole("button", { name: "Export", exact: true }).click();
  await page.locator(".status-line.ok", { hasText: "with encrypted passwords" }).waitFor();
  const text = readFileSync(file, "utf8");
  if (!text.includes('"secrets"') || text.includes('"password":')) throw new Error("export isn't encrypted");

  // import: the file asks for its passphrase, a wrong one is refused
  await page.getByRole("button", { name: "Import…" }).click();
  await field("File").fill(file);
  await field("Passphrase").waitFor();
  await field("Passphrase").fill("wrong passphrase");
  await page.locator(".modal").last().getByRole("button", { name: "Import", exact: true }).click();
  await page.locator(".status-line.err", { hasText: "wrong passphrase" }).waitFor();
  await field("Passphrase").fill("correct horse");
  await shot("18c-import");
  await page.locator(".modal").last().getByRole("button", { name: "Import", exact: true }).click();
  await page.locator(".status-line.ok", { hasText: "Imported" }).waitFor();
  const after = await page.locator(".modal.manage tbody tr").count();
  if (after !== before * 2) throw new Error(`expected ${before * 2} connections, got ${after}`);
  console.log("     connections:", before, "->", after);
  await page.getByRole("button", { name: "Done" }).click();
});

console.log(errors.length ? "ERRORS:\n" + errors.join("\n") : "no page errors");
await browser.close();
