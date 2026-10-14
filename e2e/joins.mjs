// Join and set-operator completion after earlier joins, and keyword highlighting of JOIN/UNION.
import { chromium } from "playwright";
const out = process.argv[2];
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 860 } });
page.setDefaultTimeout(10000);
page.on("pageerror", (e) => console.log("PAGE ERROR", e.message));
await page.goto("http://localhost:8080/");
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
await page.getByRole("button", { name: "Connection" }).first().click();
await page.locator(".type-card").filter({ has: page.locator(`span:text-is("MySQL")`) }).click();
await field("Connection Name").fill("Join Test"); await field("Host").fill("127.0.0.1"); await field("Port").fill("33306");
await field("User Name").fill("root"); await field("Password").fill("chloe");
await page.getByRole("button", { name: "Save" }).click();
await page.locator(".tree-row", { hasText: "Join Test" }).dblclick();
await page.locator(".tree-row", { hasText: "shop" }).dblclick();
await page.locator(".tree-row", { hasText: "Tables" }).first().waitFor();
await page.getByRole("button", { name: "New Query" }).first().click();
await page.locator(".monaco-editor .view-lines").first().click();

const suggestions = async () => {
  await page.locator(".suggest-widget .monaco-list-row").first().waitFor({ timeout: 5000 });
  await page.waitForTimeout(300);
  return page.locator(".suggest-widget .monaco-list-row .label-name").allInnerTexts();
};
const text = () => page.evaluate(() => document.querySelector(".monaco-editor .view-lines").innerText.replace(/\s+/g, " "));
const check = async (name, ok, extra = "") => console.log(ok ? "OK  " : "FAIL", name, extra, ok ? "" : "| text: " + (await text()));

await page.keyboard.type("SELECT * FROM customers c LEFT JOIN orders o ON o.customer_id = c.id ", { delay: 10 });
await page.keyboard.press("Escape");
await page.keyboard.type("INNER JOIN orders o2 ON o2.customer_id = c.id ", { delay: 10 });
await page.keyboard.press("Escape");
await page.keyboard.type("UN", { delay: 30 });
let s = await suggestions();
await check("UNION after two joins", s.includes("UNION") && s.includes("UNION ALL"), s.slice(0, 6).join(", "));
await page.keyboard.press("Escape");
await page.keyboard.type("ION ", { delay: 30 });
s = await suggestions();
await check("SELECT after UNION", s[0] === "SELECT", s.join(", "));
await page.keyboard.press("Escape");
for (let i = 0; i < 6; i++) await page.keyboard.press("Backspace"); // drop "UNION "
await page.keyboard.type("CR", { delay: 30 });
s = await suggestions();
await check("CROSS JOIN after two joins", s.includes("CROSS JOIN"), s.slice(0, 6).join(", "));
await page.keyboard.press("Escape");
await page.screenshot({ path: `${out}/joins.png` });

// Derived tables and union branches, typed with Monaco's auto-closing brackets.
const retype = async (sql) => {
  await page.keyboard.press("Escape");
  await page.keyboard.press("Meta+A");
  await page.keyboard.press("Backspace");
  await page.keyboard.type(sql, { delay: 10 });
};
await retype("select * from (select * from customers) x where x.");
s = await suggestions();
await check("derived SELECT * columns", s.includes("email") && !s.includes("status"), s.slice(0, 6).join(", "));
await retype("select c.id from customers c union select p.");
await page.keyboard.press("Escape");
await page.keyboard.type(" from products p");
for (let i = 0; i < " from products p".length; i++) await page.keyboard.press("ArrowLeft");
await page.keyboard.press("Control+Space");
s = await suggestions();
await check("union branch sees only its table", s.includes("sku") && !s.includes("email"), s.slice(0, 6).join(", "));
await retype("SELECT * FROM customers c LEFT JOIN orders o ON o.customer_id = c.id INNER JOIN orders o2 ON o2.customer_id = c.id ");

// JOIN, LEFT and INNER should be coloured like ON (keywords), not like operators.
const colors = await page.evaluate(() => {
  const c = {};
  for (const span of document.querySelectorAll(".monaco-editor .view-line span span")) {
    const t = span.textContent.trim();
    if (["ON", "JOIN", "LEFT", "INNER", "SELECT", "FROM"].includes(t)) c[t] = getComputedStyle(span).color;
  }
  return c;
});
await check("JOIN highlighted as keyword", colors.JOIN && colors.JOIN === colors.ON && colors.LEFT === colors.ON && colors.INNER === colors.ON, JSON.stringify(colors));
await browser.close();
