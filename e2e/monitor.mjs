// Server Monitor polling (P-F7): no polls while its tab is hidden, a poll on
// coming back, and no overlapping polls when the server answers slowly.
import { chromium } from "playwright";
import { MYSQL } from "./lib/db.mjs";
const out = process.argv[2];
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 880 } });
page.setDefaultTimeout(10000);
const errors = [];
page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
page.on("console", (m) => m.type() === "error" && errors.push("console: " + m.text()));
const shot = (n) => page.screenshot({ path: `${out}/${n}.png` });
let failed = 0;
const step = async (name, fn) => {
  try { await fn(); console.log("OK  ", name); } catch (e) { failed++; console.log("FAIL", name, e.message.split("\n")[0]); await shot("fail-" + name.replace(/\W+/g, "_")); }
};
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
// Runtime calls in flight and started, counted while armed.
let calls = 0, inFlight = 0, maxInFlight = 0, delay = 0;
await page.route("**/wails/runtime*", async (route) => {
  if (route.request().method() !== "POST") return route.continue();
  calls++;
  inFlight++;
  maxInFlight = Math.max(maxInFlight, inFlight);
  if (delay) await new Promise((r) => setTimeout(r, delay));
  await route.continue();
  inFlight--;
});
const count = async (ms) => {
  calls = 0;
  maxInFlight = inFlight;
  await page.waitForTimeout(ms);
  return calls;
};
await page.goto(process.env.BASE ?? "http://localhost:8080/");

await step("open the monitor", async () => {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("MySQL")`) }).click();
  await field("Connection Name").fill("Local MySQL");
  await field("Host").fill("127.0.0.1");
  await field("Port").fill(MYSQL);
  await field("User Name").fill("root");
  await field("Password").fill("chloe");
  await page.getByRole("button", { name: "Save" }).click();
  await page.locator(".tree-row", { hasText: "Local MySQL" }).dblclick();
  await page.locator(".tree-row", { hasText: "shop" }).waitFor();
  await page.getByRole("button", { name: "Monitor" }).click();
  await page.locator(".monitor-table tbody tr").first().waitFor();
  await page.locator(".monitor select").first().selectOption("1");
});

await step("polls while shown", async () => {
  const n = await count(3500);
  console.log("    calls in 3.5 s:", n);
  if (n < 2) throw new Error(`only ${n} polls`);
});

await step("no polls while hidden", async () => {
  await page.locator(".tabbar .tab", { hasText: "Objects" }).click();
  await page.waitForTimeout(300);
  const n = await count(3500);
  console.log("    calls in 3.5 s:", n);
  if (n > 0) throw new Error(`${n} calls while hidden`);
});

await step("refreshes on return", async () => {
  calls = 0;
  await page.locator(".tabbar .tab", { hasText: "Monitor" }).click();
  await page.waitForTimeout(500);
  const n = calls;
  if (n < 1) throw new Error("no load on return");
});

await step("slow answers don't overlap", async () => {
  delay = 2500;
  const n = await count(6000);
  console.log("    calls in 6 s with 2.5 s answers:", n, "max in flight:", maxInFlight);
  delay = 0;
  if (maxInFlight > 1) throw new Error(`${maxInFlight} polls in flight at once`);
});

await shot("monitor");
console.log(errors.length ? "errors:\n" + errors.join("\n") : "no page errors");
await browser.close();
process.exit(failed ? 1 : 0);
