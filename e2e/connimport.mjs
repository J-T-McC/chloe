// Import Connections: settings that act on their own are left off, and the dialog says so.
import { chromium } from "playwright";
import { writeFileSync } from "node:fs";
const out = process.argv[2];
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 880 } });
page.setDefaultTimeout(8000);
const errors = [];
page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
const step = async (name, fn) => {
  try { await fn(); console.log("OK  ", name); } catch (e) { console.log("FAIL", name, e.message.split("\n")[0]); await page.screenshot({ path: `${out}/fail-${name.replace(/\W+/g, "_")}.png` }); }
};
const file = `${process.env.LITE}/shared-connections.json`;
writeFileSync(file, JSON.stringify({ format: "chloe-connections", version: 1, connections: [
  { name: "Shared PG", type: "postgres", host: "127.0.0.1", port: 55432, user: "postgres",
    advanced: { autoConnect: true, initialQueries: "SELECT 1", connectionParams: "application_name=x", cleartextAuth: true } }] }));
await page.goto("http://localhost:8080/");

await step("import dialog explains what's left off", async () => {
  await page.locator(".sidebar").click({ button: "right", position: { x: 100, y: 500 } });
  await page.locator(".menu-item", { hasText: "Manage Connections…" }).click();
  await page.getByRole("button", { name: "Import…" }).click();
  await page.getByText("Auto connect, initial queries, extra connection parameters and cleartext passwords are left off").waitFor();
  await page.locator(".modal input[placeholder='Choose a Chloe connections file']").fill(file);
  await page.screenshot({ path: `${out}/ci1-import.png` });
  await page.locator(".modal").getByRole("button", { name: "Import", exact: true }).click();
  await page.getByText("Imported 1 connection(s)").waitFor();
  await page.locator(".tree-row", { hasText: "Shared PG" }).waitFor();
});

await step("imported profile has them off", async () => {
  await page.getByRole("button", { name: "Done" }).click();
  await page.locator(".tree-row", { hasText: "Shared PG" }).click({ button: "right" });
  await page.locator(".menu-item", { hasText: "Edit Connection" }).click();
  await page.locator(".modal button, .modal .tab", { hasText: "Advanced" }).first().click();
  await page.screenshot({ path: `${out}/ci2-advanced.png` });
  const text = await page.locator(".modal").innerText();
  if (text.includes("SELECT 1") || text.includes("application_name=x")) throw new Error("risky settings were imported");
});
console.log(errors.length ? "ERRORS:\n" + errors.join("\n") : "no page errors");
await browser.close();
