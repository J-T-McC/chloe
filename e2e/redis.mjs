import { chromium } from "playwright";
import { REDIS } from "./lib/db.mjs";
const out = process.argv[2];
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 860 } });
page.setDefaultTimeout(10000);
page.on("pageerror", (e) => console.log("PAGE ERROR", e.message));
await page.goto(process.env.BASE ?? "http://localhost:8080/");
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
await page.getByRole("button", { name: "Connection" }).first().click();
await page.locator(".type-card").filter({ has: page.locator(`span:text-is("Redis")`) }).click();
await field("Connection Name").fill("R"); await field("Host").fill("127.0.0.1"); await field("Port").fill(REDIS); await field("Password").fill("chloe");
await page.getByRole("button", { name: "Test Connection" }).click();
console.log("test:", await page.locator(".status-line").innerText());
await page.getByRole("button", { name: "Save" }).click();
const conn = page.locator(".tree-row", { hasText: /^\s*R/ }).first();
await conn.dblclick();
await page.locator(".tree-row", { hasText: "db0" }).waitFor();
console.log("tree:", (await page.locator(".tree-row").allInnerTexts()).slice(0, 4).map((x) => x.replace(/\s+/g, " ").trim()));
await page.locator(".tree-row", { hasText: "db0" }).click();
await page.locator(".redis-key-row").first().waitFor();
await page.waitForTimeout(800);
console.log("keys:", await page.locator(".redis-keys .statusbar").innerText().then((t) => t.replace(/\s+/g, " ")));
console.log("rows:", (await page.locator(".redis-key-row").allInnerTexts()).slice(0, 14).map((x) => x.replace(/\s+/g, " ").trim()));
await page.screenshot({ path: `${out}/r1.png` });

// string: edit and save
await page.locator(".redis-key-row", { hasText: /^\s*STR\s*greeting/ }).click();
await page.locator(".redis-text").waitFor();
console.log("greeting:", await page.locator(".redis-text").inputValue());
await page.locator(".redis-text").fill("hello chloe");
await page.locator(".redis-string").getByRole("button", { name: "Save" }).click();
await page.waitForTimeout(400);
console.log("saved:", await page.locator(".redis-text").inputValue());

// json string
await page.locator(".redis-key-row", { hasText: "config" }).first().click();
await page.locator(".redis-key-row", { hasText: /json/ }).click();
await page.waitForTimeout(400);
console.log("json mode:", await page.locator(".redis-string .seg button.active").innerText(), "|", (await page.locator(".redis-text").inputValue()).split("\n").slice(0, 3).join(" "));

// hash: open user folder, user:1, add a field
await page.locator(".redis-key-row", { hasText: /^\s*user/ }).first().click();
await page.locator(".redis-key-row", { hasText: /HASH\s*1$/ }).click();
await page.locator(".redis-table tbody tr").first().waitFor();
console.log("hash rows:", (await page.locator(".redis-items .redis-table tbody tr").allInnerTexts()).map((x) => x.replace(/\s+/g, " ")));
await page.getByRole("button", { name: "Add Field" }).click();
await page.locator(".redis-detail-edit input").first().fill("city");
await page.locator(".redis-detail-edit textarea").fill("London");
await page.locator(".redis-detail-edit").getByRole("button", { name: "Add" }).click();
await page.waitForTimeout(500);
console.log("hash after add:", (await page.locator(".redis-items .redis-table tbody tr").allInnerTexts()).map((x) => x.replace(/\s+/g, " ")));
// TTL
await page.locator(".redis-value-meta .ttl input").fill("600");
await page.locator(".redis-value-meta").getByRole("button", { name: "Set" }).click();
await page.waitForTimeout(400);
console.log("ttl:", await page.locator(".redis-value-meta .ttl").innerText().then((t) => t.replace(/\s+/g, " ")));
await page.screenshot({ path: `${out}/r2.png` });

// zset, list, stream
for (const name of ["leaderboard", "queue", "events"]) {
  await page.locator(".redis-key-row", { hasText: name }).first().click();
  if (name === "queue") await page.locator(".redis-key-row", { hasText: "jobs" }).click();
  await page.waitForTimeout(400);
  console.log(name + ":", (await page.locator(".redis-items .redis-table tbody tr").allInnerTexts()).slice(0, 4).map((x) => x.replace(/\s+/g, " ")));
}
await page.screenshot({ path: `${out}/r3.png` });

// filter + new key + delete
await page.locator(".redis-filter input").fill("bulk:item:19");
await page.locator(".redis-filter input").press("Enter");
await page.waitForTimeout(800);
console.log("filtered:", await page.locator(".redis-keys .statusbar").innerText().then((t) => t.replace(/\s+/g, " ")));
await page.locator(".redis-filter input").fill("");
await page.locator(".redis-filter input").press("Enter");
await page.getByRole("button", { name: "New Key" }).click();
await page.locator(".modal input").first().fill("e2e:new");
await page.locator(".modal select").selectOption("set");
await page.locator(".modal textarea").fill("first");
await page.locator(".modal").getByRole("button", { name: "OK" }).click();
await page.waitForTimeout(500);
console.log("new key header:", await page.locator(".redis-value-head input").inputValue(), (await page.locator(".redis-items .redis-table tbody tr").allInnerTexts()).join(","));
await page.locator(".redis-value-head button[title='Delete key']").click();
await page.locator(".modal.small").getByRole("button", { name: "Delete" }).click();
await page.waitForTimeout(400);
console.log("after delete current:", await page.locator(".redis-browser .center-hint").count());

// console
await conn.click({ button: "right" });
await page.locator(".context-menu").getByText("Console", { exact: true }).click();
const ci = page.locator(".redis-console-in input");
await ci.waitFor();
await page.waitForTimeout(400);
for (const cmd of ["HGETALL user:2", "SELECT 1", "KEYS *", "ZRANGE leaderboard 0 -1 WITHSCORES", "NOPE"]) {
  await ci.fill(cmd);
  await ci.press("Enter");
  await page.waitForTimeout(250);
}
await ci.fill("ZRAN");
await page.waitForTimeout(150);
console.log("hints:", await page.locator(".redis-console-hints").innerText().then((t) => t.replace(/\s+/g, " ")));
await ci.fill("ZR");
await ci.press("Tab");
const t1 = await ci.inputValue();
await ci.press("Tab");
const t2 = await ci.inputValue();
await ci.press("Tab");
console.log("tab:", JSON.stringify([t1, t2, await ci.inputValue()]));
// a command with a password is recallable this session but never saved
await ci.fill("AUTH chloe");
await ci.press("Enter");
await page.waitForTimeout(250);
await ci.press("ArrowUp");
const recalled = await ci.inputValue();
const saved = await page.evaluate(() => localStorage.getItem("redis-console-history"));
console.log("history:", recalled, "| saved:", saved);
if (recalled !== "AUTH chloe" || saved.includes("AUTH") || !saved.includes("HGETALL user:2")) throw new Error("console history redaction failed");
await ci.fill("");
console.log("console:\n" + (await page.locator(".redis-console-out").innerText()));
await page.screenshot({ path: `${out}/r4.png` });

// monitor
await conn.click({ button: "right" });
await page.locator(".context-menu").getByText("Server Monitor", { exact: true }).click();
await page.waitForTimeout(1500);
console.log("monitor rows:", await page.locator(".monitor table tbody tr, .server-monitor tbody tr").count());
await page.screenshot({ path: `${out}/r5.png` });
await browser.close();
