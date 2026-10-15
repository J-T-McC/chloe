// BI: Create Chart from a table, build charts by dragging fields onto shelves,
// save and present them, the workspace list, and an archive data source.
import { execFileSync } from "node:child_process";
import { chromium } from "playwright";
const out = process.argv[2];
const dir = process.env.LITE;
execFileSync("sqlite3", [
  `${dir}/sales.db`,
  `CREATE TABLE orders (id INTEGER PRIMARY KEY, region TEXT, total REAL, placed_at TEXT);
   INSERT INTO orders (region, total, placed_at) VALUES
   ('North', 120, '2026-01-04'), ('South', 80, '2026-01-20'), ('North', 45.5, '2026-02-02'), ('East', 300, '2026-02-14'),
   ('South', 60, '2026-03-01'), ('North', 210, '2026-03-09'), ('West', 15, '2026-03-30'), ('East', 99, '2026-04-02');`,
]);

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
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
const check = (ok, msg) => {
  if (!ok) throw new Error(msg);
};
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
const row = (re) => page.locator(".tree-row", { hasText: re });
const editor = () => page.locator(".bi-chart-editor:visible");
const shelf = (s) => editor().locator(`.bi-shelf[data-shelf="${s}"]`);
const drag = (f, s) => editor().locator(".bi-field", { hasText: new RegExp(`^${f}$`) }).dragTo(shelf(s));
const bars = () => editor().locator(".bi-echart svg path[fill]:not([fill='none'])");
await page.goto("http://localhost:8080/");

await step("connect to the SQLite file", async () => {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("SQLite")`) }).click();
  await field("Connection Name").fill("sales");
  await field("Database File").fill(`${dir}/sales.db`);
  await page.getByRole("button", { name: "Save" }).click();
  await row(/sales$/).first().dblclick();
  await row(/^\s*main\s*$/).first().dblclick();
  await row(/^\s*Tables\s*$/).first().dblclick();
  await row(/^\s*orders\s*$/).first().dblclick();
  await page.locator(".viewer:visible").waitFor();
});

await step("Create Chart from the table into a new workspace", async () => {
  await page.locator(".viewer:visible").getByRole("button", { name: "Create Chart" }).click();
  const dlg = page.locator(".modal", { hasText: "Create Chart" });
  await dlg.getByLabel("New workspace name").fill("Sales");
  await dlg.getByRole("button", { name: "Create Chart" }).click();
  await editor().waitFor();
  check((await page.locator(".tab.active").innerText()).includes("Chart of orders"), "chart tab title");
  const names = await editor().locator(".bi-field").allInnerTexts();
  check(names.join(",") === "id,region,total,placed_at", "fields: " + names.join(","));
  // Dates kept as text are typed as dates.
  check((await editor().locator(".bi-field", { hasText: "placed_at" }).getAttribute("title")).includes("datetime"), "placed_at type");
});

await step("drag fields onto the shelves", async () => {
  await drag("region", "dimension");
  await drag("total", "value");
  check((await shelf("value").innerText()).includes("Sum of total"), "value pill: " + (await shelf("value").innerText()));
  await editor().locator(".bi-echart svg").waitFor();
  await page.waitForTimeout(600);
  const n = await bars().count();
  check(n >= 4, `expected 4 bars, saw ${n} paths`);
  // Biggest region first: East (399), North (375.5), South (140), West (15).
  const labels = await editor().locator(".bi-echart svg text").allTextContents();
  check(labels.indexOf("East") < labels.indexOf("West") && labels.indexOf("East") >= 0, "order: " + labels.join(" "));
  await shot("bi-bar");
});

await step("line chart by month with a cumulative total", async () => {
  await editor().getByRole("radio", { name: "Line", exact: true }).click();
  await drag("placed_at", "dimension");
  check((await shelf("dimension").innerText()).includes("placed_at (Month)"), "bucket: " + (await shelf("dimension").innerText()));
  await shelf("value").locator(".bi-pill").click();
  await page.locator(".menu-item", { hasText: "Value Operation" }).hover();
  await page.locator(".menu-item", { hasText: "Cumulative" }).click();
  await editor().getByLabel("Prefix").fill("$");
  await editor().getByLabel("Show data labels").check();
  await page.waitForTimeout(800);
  const text = (await editor().locator(".bi-echart svg").innerHTML()).replace(/<[^>]+>/g, " ");
  check(text.includes("Jan 2026") && text.includes("Apr 2026"), "month labels: " + text.slice(0, 200));
  check(text.includes("$929.5"), "cumulative total label missing: " + text.slice(0, 400));
  await shot("bi-line");
});

await step("save the chart", async () => {
  await editor().getByLabel("Chart name").fill("Revenue to date");
  await editor().getByRole("button", { name: "Save" }).click();
  await page.locator(".tab.active", { hasText: "Revenue to date" }).waitFor();
  await page.waitForTimeout(200);
  check(!(await page.locator(".tab.active .tab-dirty").count()), "still dirty after saving");
});

await step("present the chart", async () => {
  await editor().getByRole("button", { name: "Present" }).click();
  await page.getByTestId("bi-present").locator(".bi-echart svg").waitFor();
  await shot("bi-present");
  await page.keyboard.press("Escape");
  await page.getByTestId("bi-present").waitFor({ state: "detached" });
});

await step("the BI library and workspace lists", async () => {
  await page.getByRole("button", { name: "BI", exact: true }).click();
  const r = page.locator(".object-list tr", { hasText: "Sales" });
  await r.waitFor();
  check((await r.innerText()).replace(/\s+/g, " ").includes("Sales 1 1"), "library row: " + (await r.innerText()));
  await r.dblclick();
  const ws = page.locator(".bi-workspace:visible");
  await ws.locator("tr", { hasText: "Revenue to date" }).waitFor();
  await ws.locator("tr", { has: page.locator("td:first-child", { hasText: /^\s*orders$/ }) }).click();
  await ws.locator(".bi-props").getByRole("button", { name: "Used By" }).click();
  check((await ws.locator(".bi-deps").innerText()).includes("Revenue to date"), "used by");
  await ws.getByRole("button", { name: "Chart", exact: true }).click();
  check((await ws.locator("tbody tr").count()) === 1, "chart filter");
  await ws.getByTitle("Icon view").click();
  await ws.locator(".bi-icon").first().waitFor();
  await shot("bi-workspace");
  await ws.getByTitle("Detail view").click();
});

await step("a value chart from New Chart", async () => {
  const ws = page.locator(".bi-workspace:visible");
  await ws.getByRole("button", { name: "New Chart" }).click();
  await editor().waitFor();
  await editor().getByRole("radio", { name: "Value" }).click();
  await drag("total", "value");
  await editor().getByLabel("Prefix").fill("$");
  await editor().getByTestId("bi-value").waitFor();
  check((await editor().getByTestId("bi-value").innerText()) === "$929.5", "value: " + (await editor().getByTestId("bi-value").innerText()));
  await editor().getByLabel("Decimals").selectOption("0");
  await page.waitForTimeout(200);
  check((await editor().getByTestId("bi-value").innerText()) === "$930", "decimals");
  check((await editor().locator(".bi-value-label").innerText()) === "Sum of Total", "label: " + (await editor().locator(".bi-value-label").innerText()));
  // Names are unique per kind: a chart may share its data source's name.
  await editor().getByLabel("Chart name").fill("orders");
  await editor().getByRole("button", { name: "Save" }).click();
  await page.locator(".tab.active", { hasText: "orders" }).waitFor();
  await page.waitForTimeout(200);
  check(!(await editor().locator(".error-banner").count()), "save error: " + (await editor().locator(".error-banner").allInnerTexts()));
});

await step("table sorted by a named column, with a relative date filter", async () => {
  await page.locator(".tab", { hasText: "Sales (BI)" }).click();
  await page.locator(".bi-workspace:visible").getByRole("button", { name: "New Chart" }).click();
  await editor().waitFor();
  await editor().getByRole("radio", { name: "Table" }).click();
  await drag("region", "value");
  await drag("id", "value");
  await drag("total", "value");
  const pill = async (i, agg) => {
    await shelf("value").locator(".bi-pill").nth(i).click();
    await page.locator(".menu-item", { hasText: "Aggregate" }).hover();
    await page.locator(".menu-item", { hasText: new RegExp(`^\\s*${agg}\\s*$`) }).click();
  };
  await pill(1, "Count");
  await pill(2, "Sum");
  await editor().getByLabel("Sort").selectOption({ label: "By Sum of total, largest first" });
  await page.waitForTimeout(600);
  const firstCells = await editor().locator(".bi-table tbody tr td:first-child").allInnerTexts();
  check(firstCells.join(",") === "East,North,South,West", "table order: " + firstCells.join(","));
  const heads = await editor().locator(".bi-table th").allInnerTexts();
  check(heads.join("|") === "Region|Count of ID|Sum of Total", "headers: " + heads.join("|"));
  // Everything is within the last 100 years; nothing in the last day.
  await editor().getByRole("button", { name: "Add Condition" }).click();
  await editor().getByLabel("Filter field").selectOption("placed_at");
  await editor().getByLabel("Filter operator").selectOption("in_last");
  await editor().getByLabel("Filter count").fill("100");
  await editor().getByLabel("Filter unit").selectOption("year");
  await page.waitForTimeout(600);
  check((await editor().locator(".bi-table tbody tr").count()) === 4, "in the last 100 years");
  await editor().getByLabel("Filter unit").selectOption("day");
  await editor().getByLabel("Filter count").fill("1");
  await editor().getByText("No data").waitFor();
  await shot("bi-table");
});

await step("archive data source with a filter", async () => {
  await page.locator(".tab", { hasText: "Sales (BI)" }).click();
  const ws = page.locator(".bi-workspace:visible");
  await ws.getByRole("button", { name: "All", exact: true }).click();
  await ws.locator("tr", { has: page.locator("td:first-child", { hasText: /^\s*orders$/ }) }).filter({ hasText: "Data Source" }).dblclick();
  const src = page.locator(".bi-source:visible");
  await src.locator(".bi-preview tbody tr").first().waitFor();
  check((await src.locator(".bi-preview tbody tr").count()) === 8, "preview rows");
  await src.getByRole("button", { name: "Archive" }).click();
  await src.getByRole("button", { name: "Filter" }).click();
  await src.getByRole("button", { name: "Add Condition" }).click();
  await src.getByLabel("Filter field").selectOption("region");
  await src.getByLabel("Filter value").fill("North");
  await src.getByRole("button", { name: "Save" }).click();
  await src.getByText(/8 rows, read/).waitFor();
  await src.getByRole("button", { name: "Apply and Refresh Data" }).click();
  await page.waitForTimeout(500);
  check((await src.locator(".bi-preview tbody tr").count()) === 3, "filtered preview rows: " + (await src.locator(".bi-preview tbody tr").count()));
  await shot("bi-source");
  // The chart now reads the snapshot, filtered.
  await page.locator(".tab", { hasText: "Revenue to date" }).click();
  await editor().getByRole("button", { name: "Refresh" }).click();
  await page.waitForTimeout(800);
  const text = (await editor().locator(".bi-echart svg").innerHTML()).replace(/<[^>]+>/g, " ");
  check(text.includes("$375.5") && !text.includes("$929.5"), "filtered cumulative: " + text.slice(0, 300));
  check((await editor().locator(".bi-asof").innerText()).startsWith("Snapshot"), "snapshot status");
});

await step("a data source time zone moves dates across days", async () => {
  await page.locator(".tab", { hasText: /^\s*orders\s*$/ }).last().click();
  const src = page.locator(".bi-source:visible");
  await src.getByLabel("Time zone").fill("America/Los_Angeles");
  await src.getByRole("button", { name: "Save" }).click();
  await page.waitForTimeout(500);
  check(!(await src.locator(".error-banner").count()), "save error: " + (await src.locator(".error-banner").allInnerTexts()));
  await page.locator(".tab", { hasText: "Revenue to date" }).click();
  await editor().locator(".bi-pill.dim").click();
  await page.locator(".menu-item", { hasText: "Group By" }).hover();
  await page.locator(".menu-item", { hasText: "Weekday (Extract)" }).click();
  await page.waitForTimeout(800);
  // North's orders (Sun Jan 4, Mon Feb 2, Mon Mar 9, at midnight UTC) fall a day earlier in Los Angeles.
  const text = (await editor().locator(".bi-echart svg").innerHTML()).replace(/<[^>]+>/g, " ");
  check(text.includes("Sat") && text.includes("Sun") && !text.includes("Mon"), "zoned weekdays: " + text.slice(0, 300));
});

const dash = () => page.locator(".bi-dash-editor:visible");
await step("a dashboard of charts and text", async () => {
  await page.locator(".tab", { hasText: "Sales (BI)" }).click();
  await page.locator(".bi-workspace:visible").getByRole("button", { name: "New Dashboard" }).click();
  await dash().waitFor();
  await dash().getByRole("button", { name: "Add Text" }).click();
  await dash().getByLabel("Text").fill("# Sales overview\n**North** leads. <img src=x onerror=alert(1)>");
  await dash().getByRole("button", { name: "Add Chart" }).click();
  await page.locator(".menu-item", { hasText: "Revenue to date" }).click();
  await dash().getByRole("button", { name: "Add Chart" }).click();
  await page.locator(".menu-item", { hasText: /^\s*orders\s*$/ }).click();
  await dash().locator(".bi-tile").nth(2).waitFor();
  check((await dash().locator(".bi-tile").count()) === 3, "tiles");
  await dash().locator(".bi-tile .bi-echart svg").first().waitFor();
  await dash().getByTestId("bi-value").waitFor();
  const text = dash().getByTestId("bi-text");
  check((await text.locator("h1").innerText()) === "Sales overview", "markdown heading");
  check(!(await text.locator("img").count()), "markup in text rendered");
  // Tiles stay inside the 16:9 slide.
  const box = await dash().locator(".bi-canvas-frame").boundingBox();
  check(Math.abs(box.width / box.height - 16 / 9) < 0.02, `slide ratio ${box.width}x${box.height}`);
  await dash().locator(".bi-dash-canvas-wrap").click({ position: { x: 4, y: 4 } });
  await dash().getByLabel("Dashboard name").fill("Sales overview");
  await dash().getByRole("button", { name: "Save" }).click();
  await page.locator(".tab.active", { hasText: "Sales overview" }).waitFor();
  await page.waitForTimeout(200);
  check(!(await page.locator(".tab.active .tab-dirty").count()), "dirty after save");
  await shot("bi-dashboard");
});

await step("pages, report size and present", async () => {
  await dash().getByTitle("Add Page").click();
  check((await dash().locator(".bi-page-thumb").count()) === 2, "pages");
  await dash().getByLabel("Page size").selectOption("report");
  await dash().getByRole("button", { name: "Add Text" }).click();
  await dash().getByLabel("Text").fill("## Details\nMore below.");
  await dash().getByRole("button", { name: "Save" }).click();
  await page.waitForTimeout(300);
  await dash().locator(".bi-page-thumb").first().click();
  await dash().getByRole("button", { name: "Present" }).click();
  const pres = page.getByTestId("bi-present");
  await pres.locator(".bi-echart svg").waitFor();
  await shot("bi-dashboard-present");
  await page.keyboard.press("ArrowRight");
  await pres.getByText("Page 2 of 2").waitFor();
  await pres.getByText("More below.").waitFor();
  await page.keyboard.press("Escape");
  await pres.waitFor({ state: "detached" });
  // The workspace knows the chart is on it.
  await page.locator(".tab", { hasText: "Sales (BI)" }).click();
  const ws = page.locator(".bi-workspace:visible");
  await ws.getByRole("button", { name: "Dashboard", exact: true }).click();
  check((await ws.locator("tbody tr").count()) === 1, "dashboard filter");
  await ws.getByRole("button", { name: "All", exact: true }).click();
  await ws.locator("tr", { hasText: "Revenue to date" }).click();
  await ws.locator(".bi-props").getByRole("button", { name: "Used By" }).click();
  check((await ws.locator(".bi-deps").innerText()).includes("Sales overview"), "chart used by");
});

await step("dark theme", async () => {
  await page.locator(".tab", { hasText: "Sales (BI)" }).click();
  await page.locator(".bi-workspace:visible").getByLabel("Theme").selectOption("dark");
  await page.locator(".tab", { hasText: "Revenue to date" }).click();
  await editor().locator(".bi-chart.theme-dark").waitFor();
  await page.waitForTimeout(500);
  await shot("bi-dark");
});

console.log(errors.length ? "page errors:\n  " + errors.join("\n  ") : "no page errors");
await browser.close();
if (failed || errors.length) process.exit(1);
