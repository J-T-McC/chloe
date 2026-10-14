// Dragging connections into, out of and within groups, reordering groups, and the order surviving a reload.
import { chromium } from "playwright";

const out = process.argv[2];
const dir = process.env.LITE;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1200, height: 800 } });
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
const row = (name) => page.locator(".sidebar .tree-row", { has: page.locator(`.tree-label:text-is("${name}")`) });
// The tree as text: groups as "G[a b]", top-level connections bare.
const tree = () =>
  page.evaluate(() => {
    const rows = [...document.querySelectorAll(".sidebar .tree-row")].sort((a, b) => parseFloat(a.style.top) - parseFloat(b.style.top));
    let s = "";
    let inGroup = false;
    for (const r of rows) {
      const label = r.querySelector(".tree-label").textContent.trim();
      const depth = parseFloat(r.style.paddingLeft) > 6;
      if (r.querySelector(".kind-group")) {
        s += (inGroup ? "] " : "") + label + "[";
        inGroup = true;
      } else if (depth) s += (s.endsWith("[") ? "" : " ") + label;
      else {
        s += (inGroup ? "] " : "") + label + " ";
        inGroup = false;
      }
    }
    return (s + (inGroup ? "]" : "")).trim();
  });
const expectTree = async (want) => {
  for (let i = 0; i < 20 && (await tree()) !== want; i++) await page.waitForTimeout(100);
  const got = await tree();
  console.log("     " + got);
  if (got !== want) throw new Error(`tree is "${got}", want "${want}"`);
};
// Drop on the top or bottom quarter of a row (before/after), or its middle.
const drag = async (from, to, where = "middle") => {
  if (to !== "end") return row(from).dragTo(row(to), { targetPosition: { x: 80, y: where === "before" ? 4 : where === "after" ? 20 : 12 } });
  const box = await page.locator(".sidebar .tree").boundingBox();
  return row(from).dragTo(page.locator(".sidebar .tree"), { targetPosition: { x: 100, y: box.height - 20 } });
};

await page.goto("http://localhost:8080/");
await page.waitForTimeout(500);

await step("connections and groups", async () => {
  for (const name of ["Alpha", "Bravo", "Charlie"]) {
    await page.getByRole("button", { name: "Connection" }).first().click();
    await page.locator(".type-card").filter({ has: page.locator('span:text-is("SQLite")') }).click();
    await field("Connection Name").fill(name);
    await field("Database File").fill(`${dir}/big.db`);
    await page.getByRole("button", { name: "Save" }).click();
    await row(name).waitFor();
  }
  for (const g of ["Prod", "Dev"]) {
    await page.locator(".sidebar .tree").click({ button: "right", position: { x: 100, y: 600 } });
    await page.locator(".menu-item", { hasText: "New Group" }).click();
    await page.locator(".modal input").fill(g);
    await page.keyboard.press("Enter");
    await row(g).waitFor();
  }
  await expectTree("Prod[] Dev[] Alpha Bravo Charlie");
});

await step("drag into groups", async () => {
  await drag("Charlie", "Prod");
  await expectTree("Prod[Charlie] Dev[] Alpha Bravo");
  await drag("Alpha", "Prod");
  await expectTree("Prod[Charlie Alpha] Dev[] Bravo");
});

await step("reorder within a group", async () => {
  await drag("Alpha", "Charlie", "before");
  await expectTree("Prod[Alpha Charlie] Dev[] Bravo");
});

await step("drag between groups and out", async () => {
  await drag("Charlie", "Dev");
  await expectTree("Prod[Alpha] Dev[Charlie] Bravo");
  await drag("Alpha", "Bravo", "after");
  await expectTree("Prod[] Dev[Charlie] Bravo Alpha");
  await drag("Charlie", "end");
  await expectTree("Prod[] Dev[] Bravo Alpha Charlie");
  await drag("Charlie", "Bravo", "before");
  await expectTree("Prod[] Dev[] Charlie Bravo Alpha");
});

await step("drop indicator", async () => {
  // Hold a drag over a row to see the indicator, then drop.
  await row("Bravo").hover();
  await page.mouse.down();
  await row("Dev").hover({ position: { x: 80, y: 12 } });
  await row("Dev").hover({ position: { x: 82, y: 12 } });
  const into = await row("Dev").evaluate((el) => el.className);
  await shot("conndrag-indicator");
  await page.mouse.up();
  console.log("     hovering Dev:", into);
  if (!into.includes("drop-into")) throw new Error("no drop-into indicator");
  await expectTree("Prod[] Dev[Bravo] Charlie Alpha");
});

await step("reorder groups", async () => {
  await drag("Dev", "Prod", "before");
  await expectTree("Dev[Bravo] Prod[] Charlie Alpha");
  // A group can't go among the connections.
  await drag("Prod", "Alpha");
  await expectTree("Dev[Bravo] Prod[] Charlie Alpha");
});

await step("order survives a reload", async () => {
  await drag("Bravo", "Prod");
  await drag("Alpha", "Bravo", "before");
  await page.reload();
  await expectTree("Dev[] Prod[Alpha Bravo] Charlie");
  await shot("conndrag-final");
});

console.log(errors.length ? "ERRORS:\n" + errors.join("\n") : "no page errors");
await browser.close();
process.exit(failed ? 1 : 0);
