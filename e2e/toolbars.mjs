// Toolbars at narrow window sizes: nothing is cut off, and whatever doesn't fit
// is reachable from the » menu at the end of the bar.
import { execFileSync } from "node:child_process";
import { chromium } from "playwright";
const out = process.argv[2];
const dir = process.env.LITE;
execFileSync("sqlite3", [
  `${dir}/bars.db`,
  `CREATE TABLE orders (id INTEGER PRIMARY KEY, region TEXT, total REAL, placed_at TEXT);
   INSERT INTO orders (region, total, placed_at) VALUES ('North', 120, '2026-01-04'), ('South', 80, '2026-01-20'), ('East', 300, '2026-02-14');`,
]);

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 860 } });
page.setDefaultTimeout(8000);
const errors = [];
page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
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
const check = (ok, msg) => {
  if (!ok) throw new Error(msg);
};
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
const row = (re) => page.locator(".tree-row", { hasText: re });
const tab = (name) => page.locator(".tab", { hasText: name }).first();

// Every visible toolbar: its shown items end inside it, and when some are hidden
// the » button is there.
async function checkBars(where) {
  const report = await page.evaluate(() =>
    [...document.querySelectorAll(".tb-fit")]
      .filter((b) => b.offsetParent !== null)
      .map((b) => {
        const r = b.getBoundingClientRect();
        const kids = [...b.children].filter((c) => !c.classList.contains("tb-more"));
        const shown = kids.filter((c) => c.offsetParent !== null || getComputedStyle(c).display !== "none");
        const hidden = kids.filter((c) => c.hasAttribute("data-tb-hidden"));
        const more = b.querySelector(":scope > .tb-more");
        const cut = shown
          .filter((c) => !c.hasAttribute("data-tb-hidden"))
          .filter((c) => c.getBoundingClientRect().width > 0 && c.getBoundingClientRect().right > r.right + 1)
          .map((c) => (c.textContent || c.getAttribute("aria-label") || c.className).trim().slice(0, 30));
        return { cls: b.className, width: Math.round(r.width), hidden: hidden.length, more: !!more && !more.hidden && more.getBoundingClientRect().right <= r.right + 1, cut };
      }),
  );
  check(report.length > 0, `${where}: no toolbars`);
  for (const b of report) {
    check(!b.cut.length, `${where}: ${b.cls} (${b.width}px) cuts off ${b.cut.join(", ")}`);
    check(!b.hidden || b.more, `${where}: ${b.cls} hides ${b.hidden} items without a » button`);
  }
  return report;
}

await page.goto(process.env.BASE ?? "http://localhost:8080/");

await step("open a table, a query, a BI workspace and a dashboard", async () => {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("SQLite")`) }).click();
  await field("Connection Name").fill("bars");
  await field("Database File").fill(`${dir}/bars.db`);
  await page.getByRole("button", { name: "Save" }).click();
  await row(/bars$/).first().dblclick();
  await row(/^\s*main\s*$/).first().dblclick();
  await row(/^\s*Tables\s*$/).first().dblclick();
  await row(/^\s*orders\s*$/).first().dblclick();
  await page.locator(".viewer:visible").waitFor();
  await page.locator(".viewer:visible").getByRole("button", { name: "Create Chart" }).click();
  const dlg = page.locator(".modal", { hasText: "Create Chart" });
  await dlg.getByLabel("New workspace name").fill("Bars");
  await dlg.getByRole("button", { name: "Create Chart" }).click();
  await page.locator(".bi-chart-editor:visible").waitFor();
  await page.locator(".bi-chart-editor:visible").getByLabel("Chart name").fill("Orders");
  await page.locator(".bi-chart-editor:visible").getByRole("button", { name: "Save" }).click();
  await page.getByRole("button", { name: "BI", exact: true }).click();
  await page.locator(".object-list tr", { hasText: "Bars" }).dblclick();
  const ws = page.locator(".bi-workspace:visible");
  await ws.locator("tr", { hasText: "Orders" }).first().waitFor();
  await ws.getByRole("button", { name: "New Dashboard" }).click();
  await page.locator(".bi-dash-editor:visible").waitFor();
  await row(/^\s*main\s*$/).first().click();
  await page.locator(".main-toolbar").getByRole("button", { name: "New Query" }).click();
  await page.locator(".monaco-editor:visible").waitFor();
});

const views = [
  ["objects", "Objects"],
  ["table", "orders"],
  ["chart", "Orders"],
  ["workspace", "Bars"],
  ["dashboard", "New Dashboard"],
  ["query", "Query"],
];
for (const width of [1200, 900, 700]) {
  await step(`toolbars fit at ${width}px`, async () => {
    await page.setViewportSize({ width, height: 860 });
    const problems = [];
    for (const [name, title] of views) {
      const t = tab(title);
      if (!(await t.count())) {
        problems.push(`no ${name} tab`);
        continue;
      }
      await t.click();
      await page.waitForTimeout(250);
      try {
        await checkBars(`${name} at ${width}`);
      } catch (e) {
        problems.push(e.message);
      }
      await shot(`toolbars-${width}-${name}`);
    }
    check(!problems.length, problems.join("; "));
  });
}

await step("a hidden filter is reachable from the » menu", async () => {
  await page.setViewportSize({ width: 700, height: 860 });
  await tab("Bars").click();
  const ws = page.locator(".bi-workspace:visible");
  const bar = ws.locator(".tb-fit").first();
  await page.waitForTimeout(250);
  check(!(await bar.getByRole("button", { name: "Chart", exact: true }).isVisible()), "the Chart filter still fits at 700px");
  await bar.getByRole("button", { name: "More toolbar items" }).click();
  const menu = page.locator(".tb-menu");
  await menu.waitFor();
  await shot("toolbars-menu");
  // Only the hidden items are in the menu.
  check(!(await menu.getByRole("button", { name: "New Data Source" }).isVisible()), "the menu repeats an item the bar shows");
  await menu.getByRole("button", { name: "Chart", exact: true }).click();
  await menu.waitFor({ state: "detached" });
  check((await ws.locator("tbody tr").count()) === 1, "the filter didn't apply");
  // Escape and clicking away close it.
  await bar.getByRole("button", { name: "More toolbar items" }).click();
  await menu.waitFor();
  await page.keyboard.press("Escape");
  await menu.waitFor({ state: "detached" });
  await bar.getByRole("button", { name: "More toolbar items" }).click();
  await ws.locator(".bi-props, .object-list").first().click({ position: { x: 5, y: 5 } });
  await menu.waitFor({ state: "detached" });
});

await step("tabs that don't fit are listed by the tab bar's button", async () => {
  await page.setViewportSize({ width: 700, height: 860 });
  await tab("Query").click();
  await page.waitForTimeout(250);
  const objects = page.locator(".tabbar .tab", { hasText: "Objects" });
  const inView = async (l) => {
    const [t, b] = [await l.boundingBox(), await page.locator(".tabbar").boundingBox()];
    return t.x >= b.x - 1 && t.x + t.width <= b.x + b.width + 1;
  };
  check(!(await inView(objects)), "the Objects tab is still in view at 700px");
  await page.getByRole("button", { name: "All tabs" }).click();
  await shot("toolbars-tab-list");
  await page.locator(".menu-item", { hasText: "Objects" }).click();
  await page.locator(".tab.active", { hasText: "Objects" }).waitFor();
  await page.waitForTimeout(300);
  check(await inView(objects), "the active tab isn't scrolled into view");
  await page.setViewportSize({ width: 1400, height: 860 });
  await page.waitForTimeout(250);
  check(!(await page.getByRole("button", { name: "All tabs" }).count()), "the tab list button stays when every tab fits");
});

await step("the main toolbar keeps every button reachable", async () => {
  await page.setViewportSize({ width: 700, height: 860 });
  const main = page.locator(".main-toolbar");
  await page.waitForTimeout(250);
  check(!(await main.getByRole("button", { name: "Monitor" }).isVisible()), "Monitor still fits at 700px");
  await main.getByRole("button", { name: "More toolbar items" }).click();
  const menu = page.locator(".tb-menu");
  const r = await menu.boundingBox();
  check(r && r.y > 40, "the menu isn't under the » button: " + JSON.stringify(r));
  await menu.getByRole("button", { name: "Disconnect" }).click();
  await menu.waitFor({ state: "detached" });
  await main.getByRole("button", { name: "More toolbar items" }).click();
  await menu.getByRole("button", { name: "Connect" }).waitFor();
  await page.keyboard.press("Escape");
  await page.setViewportSize({ width: 1400, height: 860 });
  await page.waitForTimeout(250);
  check(!(await main.getByRole("button", { name: "More toolbar items" }).isVisible()), "» stays when everything fits again");
});

if (errors.length) console.log("FAIL page errors", errors.join("\n"));
await browser.close();
process.exit(failed ? 1 : 0);
