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
await field("Connection Name").fill("M"); await field("Host").fill("127.0.0.1"); await field("Port").fill("33306"); await field("Password").fill("chloe");
await page.getByRole("button", { name: "Save" }).click();
await page.locator(".tree-row", { hasText: "M" }).first().dblclick();
const shop = page.locator(".tree-row", { hasText: /^\s*shop\s*$/ });
await shop.dblclick();
await shop.click({ button: "right" });
await page.locator(".context-menu").getByText("Reverse Database to Model…").click();
await page.locator(".model-editor .erd-table").first().waitFor();
await page.waitForTimeout(500);
console.log("tables:", await page.locator(".model-editor .erd-table").count(), "|", await page.locator(".model-editor .statusbar").innerText());
console.log("tab:", await page.locator(".tab.active").innerText());
await page.screenshot({ path: `${out}/m1.png` });

// New table from the canvas context menu
await page.locator(".model-editor .react-flow__pane").click({ button: "right", position: { x: 60, y: 500 } });
await page.locator(".context-menu").getByText("New Table").click();
const dlg = page.locator(".model-table-dialog");
await dlg.locator("input.name-input").last().fill("notes");
await dlg.getByRole("button", { name: "Add Field" }).click();
await page.waitForTimeout(200);
await dlg.locator(".seg-tabs button", { hasText: "Foreign Keys" }).click();
await dlg.getByRole("button", { name: "Add Foreign Key" }).click();
const fkRow = dlg.locator(".list-tab tbody tr").first();
await fkRow.locator("td").nth(1).locator("select").selectOption("column");
await fkRow.locator("td").nth(3).locator("input").fill("customers");
await fkRow.locator("td").nth(3).locator("input").blur();
await page.waitForTimeout(300);
await fkRow.locator("td").nth(4).locator("select").selectOption("email");
await page.waitForTimeout(200);
await page.screenshot({ path: `${out}/m2.png` });
await dlg.locator(".seg-tabs button", { hasText: "SQL Preview" }).click();
await page.waitForTimeout(400);
console.log("preview:", (await dlg.locator(".designer-sql").innerText()).slice(0, 120).replace(/\n/g, " "));
await dlg.getByRole("button", { name: "OK" }).click();
await page.waitForTimeout(300);
console.log("after add:", await page.locator(".model-editor .statusbar").innerText(), "|", await page.locator(".tab.active").innerText());

// Export SQL
await page.getByRole("button", { name: "Export SQL" }).click();
await page.locator(".modal .model-sql").getByText("CREATE TABLE", { exact: false }).first().waitFor();
const sqlText = await page.locator(".modal .model-sql").innerText();
console.log("export head:", sqlText.slice(0, 300).replace(/\n/g, " "), sqlText.match(/.*notes.*/g)); console.log("export has notes:", sqlText.includes("CREATE TABLE `notes`"), "fk:", sqlText.includes("FOREIGN KEY"));
await page.getByRole("button", { name: "Close" }).click();

// Sync
await page.getByRole("button", { name: "Synchronize" }).click();
const sd = page.locator(".sync-dialog");
await page.waitForTimeout(800);
await sd.getByRole("button", { name: "Compare" }).click();
await sd.locator(".sync-action, .sync-actions .center-hint").first().waitFor();
console.log("plan:", (await sd.locator(".sync-actions").innerText()).replace(/\n/g, " | "));
await page.screenshot({ path: `${out}/m3.png` });
await sd.getByRole("button", { name: "Execute" }).click();
await sd.locator(".status-line").first().waitFor();
console.log("result:", await sd.locator(".status-line").first().innerText());
await sd.getByRole("button", { name: "Compare" }).click();
await page.waitForTimeout(800);
console.log("replan:", (await sd.locator(".sync-actions").innerText()).replace(/\n/g, " | "));
await sd.getByRole("button", { name: /Close|Cancel/ }).click();

// Save the reverse-engineered model into Chloe's library: it asks for a name, not a file.
const dialog = () => page.locator(".modal.small");
const check = (ok, msg) => { if (!ok) throw new Error(msg); };
await page.locator(".tab", { hasText: "(model)" }).first().click();
await page.locator(".model-editor:visible").getByRole("button", { name: "Save", exact: true }).click();
await dialog().locator("input").fill("Shop model");
await dialog().getByRole("button", { name: "OK" }).click();
await page.locator(".tab.active", { hasText: "Shop model (model)" }).waitFor();
check(!(await page.locator(".tab.active").innerText()).includes("•"), "still dirty after saving");

// The Model toolbar button lists the library.
await page.locator(".tool-button", { hasText: "Model" }).click();
const lib = page.locator(".models-list");
const libRow = (name) => lib.locator("tbody tr", { has: page.locator(`td:first-child:text-is("${name}")`) });
await libRow("Shop model").waitFor();
const cells = await libRow("Shop model").locator("td").allInnerTexts();
console.log("library row:", cells.join(" | "));
check(cells[1] === "MySQL" && cells[2] === "5" && cells[3] === "M › shop", "row: " + cells.join(" | "));
await page.screenshot({ path: `${out}/m-library.png` });

await libRow("Shop model").click({ button: "right" });
await page.locator(".context-menu").getByText("Duplicate").click();
await libRow("Shop model copy").waitFor();
await libRow("Shop model copy").click({ button: "right" });
await page.locator(".context-menu").getByText("Rename…").click();
await dialog().locator("input").fill("Shop archive");
await dialog().getByRole("button", { name: "OK" }).click();
await libRow("Shop archive").waitFor();
await libRow("Shop archive").click();
await page.locator(".objects-pane:visible").getByRole("button", { name: "Delete Model" }).click();
await page.locator(".modal").getByRole("button", { name: "Delete" }).click();
await libRow("Shop archive").waitFor({ state: "detached" });
console.log("library after duplicate/rename/delete:", (await lib.locator("tbody tr td:first-child").allInnerTexts()).join(", "));
check((await lib.locator("tbody tr").count()) === 1, "expected one model left");
await libRow("Shop model").dblclick();
await page.locator(".tab.active", { hasText: "Shop model (model)" }).waitFor();

// New PG model from the Models list
await page.locator(".tool-button", { hasText: "Model" }).click();
await page.locator(".objects-pane:visible").getByRole("button", { name: "New PostgreSQL Model" }).click();
await page.locator(".model-empty").waitFor();
await page.locator(".model-empty button").click();
await page.locator(".model-table-dialog input.name-input").last().fill("things");
await page.locator(".model-table-dialog").getByRole("button", { name: "OK" }).click();
await page.waitForTimeout(400);
console.log("pg model:", await page.locator(".model-editor:visible .statusbar").innerText());
await page.screenshot({ path: `${out}/m4.png` });

// With model tabs open (they stay mounted), typing in a query editor keeps its spaces.
await page.locator(".tree-row", { hasText: /^\s*shop\s*$/ }).first().click();
await page.getByRole("button", { name: "New Query" }).first().click();
await page.locator(".tab-pane:visible .monaco-editor .view-lines").click();
await page.keyboard.type("SELECT 1 AS answer", { delay: 5 });
await page.waitForTimeout(300);
const typed = (await page.locator(".tab-pane:visible .monaco-editor .view-lines").innerText()).replace(/\u00a0/g, " ");
console.log("typed with a model open:", JSON.stringify(typed));
check(typed.includes("SELECT 1 AS answer"), "spaces swallowed: " + typed);
await browser.close();
