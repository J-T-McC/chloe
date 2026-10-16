// MCP server: endpoint setup in the UI, tool calls over HTTP, approval dialog, danger mode, activity log.
import { chromium } from "playwright";
import { readFileSync, writeFileSync } from "node:fs";
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
const out = process.argv[2];
const dir = process.env.LITE;
const PORT = 47199; // not the default, so a running desktop Chloe can't collide
const URL = `http://127.0.0.1:${PORT}/mcp`;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 880 } });
page.setDefaultTimeout(8000);
const errors = [];
page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
page.on("console", (m) => m.type() === "error" && errors.push("console: " + m.text()));
const shot = (n) => page.screenshot({ path: `${out}/${n}.png` });
let failed = 0;
const step = async (name, fn) => {
  try { await fn(); console.log("OK  ", name); } catch (e) { failed++; console.log("FAIL", name, e.message.split("\n")[0]); await shot("fail-" + name.replace(/\W+/g, "_")); }
};
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input").first();
const row = (re) => page.locator(".tree-row", { hasText: re });
// The token is in mcp.json (plain under CHLOE_SECRETS=memory).
const token = () => JSON.parse(readFileSync(`${dir}/../config/mcp.json`, "utf8")).endpoints[0].token;

let rpcId = 0;
async function rpc(method, params, tok = token()) {
  const res = await fetch(URL, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream", Authorization: `Bearer ${tok}`, "MCP-Protocol-Version": "2025-06-18" },
    body: JSON.stringify({ jsonrpc: "2.0", id: ++rpcId, method, params }),
  });
  if (!res.ok) return { status: res.status };
  const text = await res.text();
  const data = text.startsWith("{") ? text : text.split("\n").filter((l) => l.startsWith("data:")).map((l) => l.slice(5)).join("");
  return { status: res.status, ...JSON.parse(data) };
}
const tool = async (name, args) => {
  const r = await rpc("tools/call", { name, arguments: args });
  if (r.error) throw new Error(JSON.stringify(r.error));
  return r.result;
};
const text = (r) => r.content.map((c) => c.text).join("");
const where = (id) => [{ column: "id", op: "=", value: id }];

await page.goto("http://localhost:8080/");

await step("create connection", async () => {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("SQLite")`) }).click();
  await field("Connection Name").fill("big");
  await field("Database File").fill(`${dir}/big.db`);
  await page.getByRole("button", { name: "Save" }).click();
  await row(/big$/).first().waitFor();
});

await step("open MCP server tab", async () => {
  await page.locator(".mcp-button").click();
  await page.locator(".tab", { hasText: "MCP Server" }).waitFor();
  await page.getByText("Stopped").waitFor();
});

await step("start server on a port", async () => {
  await page.getByLabel("Port").fill(String(PORT));
  await page.getByLabel("Port").press("Enter");
  await page.getByLabel("Run MCP server").check();
  await page.getByText(`Listening on ${URL}`).waitFor();
});

await step("new endpoint with read-write access", async () => {
  await page.getByRole("button", { name: "New Endpoint" }).click();
  await page.getByLabel("Endpoint name").fill("Claude Code – dev");
  await page.getByLabel("Endpoint name").blur();
  await page.locator(".mcp-list-row", { hasText: "Claude Code – dev" }).waitFor();
  await page.getByLabel("Access to big", { exact: true }).selectOption("write");
  if (!(await page.locator(".mcp-snippet").innerText()).includes("--env CHLOE_MCP_TOKEN=")) throw new Error("the bridge isn't the default client");
  await page.getByLabel("Client").selectOption("claude-code");
  const snippet = await page.locator(".mcp-snippet").innerText();
  if (!snippet.includes(`claude mcp add --transport http chloe-claude-code-dev ${URL}`) || snippet.includes(token())) throw new Error("snippet: " + snippet);
  await page.getByLabel("Databases of big").click();
  await page.getByLabel("Access to big main").waitFor();
  await shot("mcp-endpoint");
});

await step("copy config puts the full token on the clipboard", async () => {
  await page.context().grantPermissions(["clipboard-read", "clipboard-write"]);
  await page.getByRole("button", { name: "Copy Config" }).click();
  await page.getByRole("button", { name: "Copied" }).waitFor();
  const clip = await page.evaluate(() => navigator.clipboard.readText());
  if (!clip.includes(`Authorization: Bearer ${token()}`)) throw new Error("clipboard: " + clip.slice(0, 80));
});

await step("no token is refused", async () => {
  const r = await rpc("tools/list", {}, "nope");
  if (r.status !== 401) throw new Error("status " + r.status);
});

await step("list and select over MCP", async () => {
  const tools = await rpc("tools/list", {});
  const names = tools.result.tools.map((t) => t.name);
  console.log("    tools:", names.join(", "));
  const conns = await tool("list_connections", {});
  if (!text(conns).includes('"access":"write"')) throw new Error(text(conns));
  const r = await tool("select_rows", { connection: "big", table: "n", where: [{ column: "id", op: "<=", value: 3 }], order_by: [{ column: "id", desc: true }] });
  const s = r.structuredContent;
  if (s.row_count !== 3 || s.rows[0][1] !== "row 3") throw new Error(text(r));
});

await step("write waits for approval, Approve runs it", async () => {
  const pending = tool("delete_rows", { connection: "big", table: "n", where: where(1) });
  await page.getByRole("dialog", { name: "Approve AI change" }).waitFor();
  const body = await page.locator(".mcp-approval").innerText();
  if (!body.includes("delete 1 row in n") || !body.includes(`DELETE FROM "main"."n" WHERE "id" = '1'`)) throw new Error(body);
  await shot("mcp-approval");
  await page.getByRole("button", { name: "Approve" }).click();
  const r = await pending;
  if (r.isError || r.structuredContent.rows !== 1) throw new Error(text(r));
});

await step("Deny declines it", async () => {
  const pending = tool("update_rows", { connection: "big", table: "n", where: where(2), set: { v: "changed" } });
  await page.getByRole("button", { name: "Deny" }).click();
  const r = await pending;
  if (!r.isError || !text(r).includes("declined by the user")) throw new Error(text(r));
  const check = await tool("select_rows", { connection: "big", table: "n", where: where(2) });
  if (check.structuredContent.rows[0][1] !== "row 2") throw new Error("denied update ran");
});

await step("run_query is read-only", async () => {
  const r = await tool("run_query", { connection: "big", sql: "SELECT count(*) FROM n" });
  if (r.structuredContent.rows[0][0] !== "249") throw new Error(text(r));
  const w = await tool("run_query", { connection: "big", sql: "DELETE FROM n" });
  if (!w.isError) throw new Error("DELETE ran");
});

await step("danger mode skips the dialog", async () => {
  await page.getByLabel("Danger mode").click();
  await page.getByRole("button", { name: "Turn On" }).click();
  await page.locator(".mcp-button.danger-mode", { hasText: "Danger mode" }).waitFor();
  const r = await tool("delete_rows", { connection: "big", table: "n", where: where(3) });
  if (r.isError || r.structuredContent.rows !== 1 || !r.structuredContent.approval.includes("danger mode")) throw new Error(text(r));
  if (await page.getByRole("dialog", { name: "Approve AI change" }).count()) throw new Error("asked anyway");
  await page.getByLabel("Danger mode").uncheck();
  await page.locator(".mcp-button.running").waitFor();
});

await step("redis connection in the access tree", async () => {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("Redis")`) }).click();
  await field("Connection Name").fill("cache");
  await field("Host").fill("127.0.0.1");
  await field("Port").fill("63790");
  await field("Password").fill("chloe");
  await page.getByRole("button", { name: "Save" }).click();
  await page.getByLabel("Access to cache", { exact: true }).selectOption("write");
  await page.getByLabel("Databases of cache").click();
  await page.getByLabel("Access to cache db1", { exact: true }).selectOption("off");
  await page.waitForTimeout(200);
  await shot("mcp-redis-access");
});

await step("redis tools over MCP", async () => {
  const names = (await rpc("tools/list", {})).result.tools.map((t) => t.name);
  for (const n of ["redis_databases", "redis_overview", "redis_scan", "redis_get", "redis_command", "redis_set", "redis_edit", "redis_delete", "redis_expire"])
    if (!names.includes(n)) throw new Error("missing " + n);
  const dbs = (await tool("redis_databases", { connection: "cache" })).structuredContent.databases;
  if (dbs.some((d) => d.db === 1) || !dbs.some((d) => d.db === 0 && d.keys > 0)) throw new Error(JSON.stringify(dbs));
  const scan = (await tool("redis_scan", { connection: "cache", pattern: "user:*", type: "hash" })).structuredContent;
  if (scan.keys.length !== 3) throw new Error(JSON.stringify(scan));
  const v = (await tool("redis_get", { connection: "cache", key: "leaderboard" })).structuredContent;
  if (v.type !== "zset" || v.items[0].value !== "alan" || v.items[0].score !== 85) throw new Error(JSON.stringify(v));
  const r = await tool("redis_command", { connection: "cache", args: ["HGET", "user:1", "name"] });
  if (r.structuredContent.reply !== '"Ada Lovelace"') throw new Error(text(r));
  const hidden = await tool("redis_scan", { connection: "cache", db: 1 });
  if (!hidden.isError || !text(hidden).includes("isn't shared")) throw new Error(text(hidden));
});

await step("redis write waits for approval", async () => {
  const pending = tool("redis_edit", { connection: "cache", key: "user:1", set_fields: { visits: 13 }, delete_fields: ["email"] });
  await page.getByRole("dialog", { name: "Approve AI change" }).waitFor();
  const body = await page.locator(".mcp-approval").innerText();
  if (!body.includes('change hash "user:1" in db0 (2 elements)') || !body.includes('HSET "user:1" "visits" "13"') || !body.includes('HDEL "user:1" "email"')) throw new Error(body);
  await shot("mcp-redis-approval");
  await page.getByRole("button", { name: "Approve" }).click();
  const r = await pending;
  if (r.isError) throw new Error(text(r));
  const v = (await tool("redis_get", { connection: "cache", key: "user:1" })).structuredContent;
  if (v.items.length !== 2 || !v.items.some((i) => i.field === "visits" && i.value === "13")) throw new Error(JSON.stringify(v));
  // Later steps check which tools a read-only endpoint gets.
  await page.getByLabel("Access to cache", { exact: true }).selectOption("off");
});

await step("mongodb connection in the access tree", async () => {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("MongoDB")`) }).click();
  await field("Connection Name").fill("docs");
  await field("Host").fill("127.0.0.1");
  await field("Port").fill("37017");
  await field("User Name").fill("root");
  await field("Password").fill("chloe");
  await page.getByRole("button", { name: "Save" }).click();
  await page.getByLabel("Access to docs", { exact: true }).selectOption("write");
  await page.getByLabel("Databases of docs").click();
  await page.getByLabel("Access to docs analytics", { exact: true }).selectOption("off");
  await page.waitForTimeout(200);
});

await step("mongodb tools over MCP", async () => {
  const names = (await rpc("tools/list", {})).result.tools.map((t) => t.name);
  for (const n of ["mongo_databases", "mongo_collections", "mongo_describe", "mongo_find", "mongo_aggregate", "mongo_insert", "mongo_update", "mongo_delete"])
    if (!names.includes(n)) throw new Error("missing " + n);
  const dbs = (await tool("mongo_databases", { connection: "docs" })).structuredContent.databases.map((d) => d.name);
  if (!dbs.includes("shop") || dbs.includes("analytics")) throw new Error(dbs.join(", "));
  const r = await tool("mongo_find", { connection: "docs", database: "shop", collection: "customers", filter: { "address.country": "UK" }, sort: [{ field: "full_name", desc: true }], projection: { full_name: 1, _id: 0 } });
  const got = r.structuredContent.documents.map((d) => d.full_name).join(", ");
  if (got !== "Alan Turing, Ada Lovelace") throw new Error(text(r));
  const agg = await tool("mongo_aggregate", { connection: "docs", database: "shop", collection: "customers", pipeline: [{ $group: { _id: "$address.country", n: { $sum: 1 } } }, { $sort: { n: -1, _id: 1 } }] });
  if (agg.structuredContent.documents[0]._id !== "UK") throw new Error(text(agg));
  const out = await tool("mongo_aggregate", { connection: "docs", database: "shop", collection: "customers", pipeline: [{ $out: "copy" }] });
  if (!out.isError || !text(out).includes("writes to a collection")) throw new Error(text(out));
  const hidden = await tool("mongo_find", { connection: "docs", database: "analytics", collection: "pageviews" });
  if (!hidden.isError || !text(hidden).includes("isn't shared")) throw new Error(text(hidden));
});

await step("mongodb update waits for approval", async () => {
  const pending = tool("mongo_update", { connection: "docs", database: "shop", collection: "customers", filter: { tags: "vip" }, update: { $addToSet: { tags: "gold" } }, many: true });
  await page.getByRole("dialog", { name: "Approve AI change" }).waitFor();
  const body = await page.locator(".mcp-approval").innerText();
  if (!body.includes("update 2 documents in customers") || !body.includes("db.customers.updateMany(") || !body.includes("// _id: [1, 3]")) throw new Error(body);
  await shot("mcp-mongo-approval");
  await page.getByRole("button", { name: "Approve" }).click();
  const r = await pending;
  if (r.isError || r.structuredContent.modified !== 2 || r.structuredContent.approval !== "approved by the user in Chloe") throw new Error(text(r));
  await page.getByLabel("Access to docs", { exact: true }).selectOption("off");
});

await step("read-only access refuses writes", async () => {
  await page.getByLabel("Access to big", { exact: true }).selectOption("read");
  await page.waitForTimeout(200);
  const names = (await rpc("tools/list", {})).result.tools.map((t) => t.name);
  if (names.includes("insert_rows") || !names.includes("select_rows")) throw new Error("tools: " + names.join(", "));
  const r = await rpc("tools/call", { name: "insert_rows", arguments: { connection: "big", table: "n", rows: [{ v: "x" }] } });
  if (!r.error) throw new Error("insert_rows ran: " + JSON.stringify(r.result));
});

await step("activity log", async () => {
  await page.locator(".mcp .seg button", { hasText: "Activity" }).click();
  for (const o of ["Approved", "Denied", "Ran without asking"]) await page.locator(".mcp-outcome", { hasText: o }).first().waitFor();
  await page.locator(".mcp-activity tr", { hasText: "delete_rows" }).first().click();
  await page.locator(".mcp-activity .hist-detail pre", { hasText: "DELETE FROM" }).waitFor();
  await shot("mcp-activity");
});

await step("regenerate revokes the old token", async () => {
  const old = token();
  await page.locator(".mcp .seg button", { hasText: "Endpoints" }).click();
  await page.getByRole("button", { name: "Regenerate…" }).click();
  await page.getByRole("button", { name: "Regenerate", exact: true }).click();
  await page.waitForTimeout(300);
  if ((await rpc("tools/list", {}, old)).status !== 401) throw new Error("old token still works");
  if ((await rpc("tools/list", {})).status !== 200) throw new Error("new token refused");
});

await step("agent creates a connection into its group", async () => {
  writeFileSync(`${dir}/made.db`, ""); // an empty file is an empty SQLite database
  const before = (await rpc("tools/list", {})).result.tools.map((t) => t.name);
  if (before.includes("create_connection")) throw new Error("offered without the capability");
  await page.getByLabel("Create connections").check();
  await page.getByLabel("Group for new connections").waitFor();
  await page.getByLabel("Access to group Created by AI").first().selectOption("read");
  await page.waitForTimeout(200);
  const pending = tool("create_connection", { path: `${dir}/made.db`, name: "made" });
  await page.getByRole("dialog", { name: "Approve AI change" }).waitFor();
  const body = await page.locator(".mcp-approval").innerText();
  if (!body.includes("Add this connection?") || !body.includes(`${dir}/made.db`) || !body.includes("Group:    Created by AI (this endpoint: read)")) throw new Error(body);
  await shot("mcp-create-connection");
  await page.getByRole("button", { name: "Approve" }).click();
  const r = await pending;
  if (r.isError || r.structuredContent.access !== "read") throw new Error(text(r));
  await row(/Created by AI/).first().waitFor();
  await row(/made$/).first().waitFor();
  const conns = await tool("list_connections", {});
  if (!text(conns).includes('"group":"Created by AI"')) throw new Error(text(conns));
});

await step("connection inherits, then overrides, group access", async () => {
  const sel = page.getByLabel("Access to made", { exact: true });
  await sel.waitFor();
  if ((await sel.inputValue()) !== "inherit" || !(await sel.locator("option:checked").innerText()).includes("Same as group (Read)")) throw new Error("not inheriting");
  await page.getByLabel("Access to group Created by AI").first().selectOption("");
  await page.waitForTimeout(200);
  if (text(await tool("list_connections", {})).includes('"made"')) throw new Error("still visible with the group off");
  await sel.selectOption("read");
  await page.waitForTimeout(200);
  if (!text(await tool("list_connections", {})).includes('"name":"made"')) throw new Error("own level ignored");
  await shot("mcp-groups");
});

const pane = () => page.locator(".tab-pane:visible");
const mcpTab = () => page.locator(".tab", { hasText: "MCP Server" }).click();

await step("agent saves a query and opens query tabs", async () => {
  if ((await rpc("tools/list", {})).result.tools.some((t) => t.name === "save_query")) throw new Error("offered without the capability");
  await page.getByLabel("Saved queries and query tabs").check();
  await page.waitForTimeout(200);
  // Open big's Queries folder so the saved query shows up in it.
  await row(/big$/).first().dblclick();
  await row(/^\s*main\s*$/).first().dblclick();
  await row(/^\s*Queries\s*$/).first().dblclick();
  await page.getByText("big › main › Queries").waitFor();
  await page.waitForTimeout(300); // the empty folder finishes loading
  const saved = await tool("save_query", { connection: "big", name: "Agent report", sql: "SELECT count(*) AS n FROM n" });
  if (saved.isError) throw new Error(text(saved));
  await row(/Agent report/).first().waitFor();
  const again = await tool("save_query", { connection: "big", name: "agent report", sql: "DELETE FROM n" });
  if (!again.isError || !text(again).includes("can't replace")) throw new Error("replaced: " + text(again));

  const opened = await tool("open_query_tab", { connection: "big", sql: "UPDATE n SET v = 'from agent'" });
  if (opened.isError) throw new Error(text(opened));
  await page.locator(".tab.active", { hasText: "Query from" }).waitFor();
  await pane().locator(".view-lines", { hasText: "UPDATE n SET v = 'from agent'" }).waitFor();
  await shot("mcp-query-tab");
  const ran = await tool("run_query", { connection: "big", sql: "SELECT count(*) FROM n WHERE v = 'from agent'" });
  if (ran.structuredContent.rows[0][0] !== "0") throw new Error("the tab's SQL ran");

  await tool("open_query_tab", { connection: "big", name: "Agent report" });
  await page.locator(".tab.active", { hasText: "Agent report" }).waitFor();
  await pane().locator(".view-lines", { hasText: "SELECT count(*) AS n FROM n" }).waitFor();
  await mcpTab();
});

await step("monitor capability", async () => {
  await page.getByLabel("Monitor", { exact: true }).check();
  await page.waitForTimeout(200);
  const names = (await rpc("tools/list", {})).result.tools.map((t) => t.name);
  if (!names.includes("list_sessions") || names.includes("kill_session")) throw new Error("tools: " + names.join(", "));
  const r = await tool("list_sessions", { connection: "big" });
  if (!r.isError || !text(r).includes("no server to monitor")) throw new Error(text(r));
});

await step("agent builds an ER model", async () => {
  await page.getByLabel("ER models").check();
  await page.waitForTimeout(200);
  const table = (name, cols) => ({ name, columns: cols.map((c) => ({ name: c, type: "integer" })), primaryKey: { columns: [cols[0]] } });
  const m = await tool("create_model", { name: "Agent model", dialect: "postgres" });
  if (m.isError) throw new Error(text(m));
  const path = m.structuredContent.path;
  const ed = await tool("edit_model", { model: path, set_tables: [table("customers", ["id", "age"])] });
  if (ed.isError) throw new Error(text(ed));
  await tool("open_model", { model: path });
  await page.locator(".tab.active", { hasText: "Agent model" }).waitFor();
  await pane().getByText("customers", { exact: true }).waitFor();
  // A clean tab reloads when the agent edits the file.
  await tool("edit_model", { model: path, set_tables: [table("orders", ["id", "customer_id"])] });
  await pane().getByText("orders", { exact: true }).waitFor();
  await pane().getByText("2 tables").waitFor();
  await shot("mcp-model");
  if (!(await pane().locator(".name-input").isVisible())) throw new Error("model name hidden");
  // With unsaved changes here, it says so instead.
  await pane().locator(".name-input").fill("Agent model (mine)");
  await tool("edit_model", { model: path, drop_tables: ["orders"] });
  await pane().locator(".model-notice", { hasText: "An agent changed this model" }).waitFor();
  await pane().getByText("orders", { exact: true }).waitFor();
  const user = await tool("edit_model", { model: "/etc/hosts", drop_tables: ["x"] });
  if (!user.isError) throw new Error("edited an unlisted file");
  await mcpTab();
});

await step("agent builds a report and writes notes", async () => {
  await page.getByLabel("Reports").check();
  await page.waitForTimeout(200);
  const ok = async (name, args) => {
    const r = await tool(name, args);
    if (r.isError) throw new Error(`${name}: ${text(r)}`);
    return r.structuredContent;
  };
  await ok("create_workspace", { name: "Agent report" });
  await ok("create_data_source", { workspace: "Agent report", name: "Rows", connection: "big", table: "n" });
  await ok("create_chart", { workspace: "Agent report", name: "Row count", data_source: "Rows", type: "value", value: [{ field: "id", aggregate: "count" }] });
  await ok("create_chart", { workspace: "Agent report", name: "Last rows", data_source: "Rows", type: "table", value: [{ field: "id" }, { field: "v" }], sort: { by: "dimension", desc: true }, top: 5 });
  await ok("create_dashboard", { workspace: "Agent report", name: "Daily", pages: [{ rows: [
    { tiles: [{ text: "# Daily rows\nWhat the table looks like today." }] },
    { tiles: [{ chart: "Row count", span: 4 }, { chart: "Last rows" }] },
  ] }] });
  const r = await ok("refresh_dashboard", { workspace: "Agent report", dashboard: "Daily" });
  const n = r.pages[0].charts[0].rows[0][0];
  if (typeof n !== "number" || n < 200) throw new Error("refresh: " + JSON.stringify(r.pages[0].charts[0]));
  await ok("set_notes", { workspace: "Agent report", dashboard: "Daily", markdown: `**${n} rows**, unchanged. <script>window.pwned = 1</script>\n- ids run 1 to 250\n- two rows were deleted\n- values follow "row n"\n- nothing is NULL\n- no new rows today` });
  await ok("open_dashboard", { workspace: "Agent report", dashboard: "Daily" });
  const dash = page.locator(".bi-dash-editor:visible");
  await dash.getByTestId("bi-notes").waitFor();
  const notes = await dash.getByTestId("bi-notes").innerText();
  if (!notes.includes("Notes from") || !notes.includes(`${n} rows`)) throw new Error("notes: " + notes);
  if (await dash.getByTestId("bi-notes").locator("script, strong >> nth=1").count()) throw new Error("unsafe markup rendered");
  if (await page.evaluate(() => window.pwned)) throw new Error("agent script ran");
  await dash.getByTestId("bi-value").filter({ hasText: String(n) }).waitFor();
  await dash.locator(".bi-table tbody tr").first().waitFor();
  // Tiles fit the page.
  const fit = await page.evaluate(() => {
    const pg = document.querySelector(".bi-dash-editor .bi-canvas-page").getBoundingClientRect();
    return [...document.querySelectorAll(".bi-dash-editor .react-grid-item")].every((el) => el.getBoundingClientRect().right <= pg.right + 1);
  });
  if (!fit) throw new Error("tiles run past the page");
  // …and are actually visible: the page has height and the notes are on screen.
  const shown = await page.evaluate(() => {
    const fr = document.querySelector(".bi-dash-editor .bi-canvas-frame").getBoundingClientRect();
    const n = document.querySelector(".bi-dash-editor [data-testid=bi-notes]").getBoundingClientRect();
    return fr.height > 200 && n.bottom <= fr.bottom && n.height > 20;
  });
  if (!shown) throw new Error("the page isn't visible");
  // Text tiles fit their Markdown: nothing cut off, and less than a row to spare.
  const fits = await page.evaluate(() =>
    [...document.querySelectorAll(".bi-dash-editor .bi-text-tile")].map((el) => {
      // The notes caption floats beside the first line; tiles have 18px of padding and border.
      const need = el.querySelector(".bi-md").offsetHeight + 18;
      return [need, el.offsetHeight, el.scrollHeight > el.clientHeight];
    }),
  );
  if (!fits.length || fits.some(([need, have, scrolls]) => scrolls || need > have || have - need >= 32)) throw new Error("text tiles: " + JSON.stringify(fits));
  await shot("mcp-report");
  // The agent sees its page: export_dashboard returns a PNG of what Chloe draws.
  const ex = await tool("export_dashboard", { workspace: "Agent report", dashboard: "Daily" });
  if (ex.isError) throw new Error(text(ex));
  const img = ex.content.find((c) => c.type === "image");
  if (!img || img.mimeType !== "image/png" || img.data.length < 5000) throw new Error("no page image: " + JSON.stringify(ex.content.map((c) => c.type)));
  writeFileSync(`${out}/mcp-export.png`, Buffer.from(img.data, "base64"));
  const file = ex.structuredContent.files[0];
  if (!file.includes("/reports/Agent report/Daily p1 ") || readFileSync(file).length !== Buffer.from(img.data, "base64").length) throw new Error("export file: " + file);
  const pdf = await tool("export_dashboard", { workspace: "Agent report", dashboard: "Daily", format: "pdf" });
  if (pdf.isError || !readFileSync(pdf.structuredContent.files[0]).subarray(0, 4).equals(Buffer.from("%PDF"))) throw new Error("pdf: " + text(pdf));
  // An HTML report to share: the viewer, the pages and the data, in one file.
  const html = await tool("export_dashboard", { workspace: "Agent report", dashboard: "Daily", format: "html" });
  if (html.isError) throw new Error("html: " + text(html));
  const report = readFileSync(html.structuredContent.files[0], "utf8");
  if (!report.startsWith("<!doctype html>") || !report.includes('id="chloe-report"') || !report.includes('"name":"Daily"')) throw new Error("html report: " + report.slice(0, 200));
  await mcpTab();
});

// A stdio MCP client: one JSON-RPC message per line on the bridge's stdin and stdout.
function stdioClient(command, args, env) {
  const child = spawn(command, args, { env: { ...process.env, ...env }, stdio: ["pipe", "pipe", "pipe"] });
  const waiting = new Map();
  let stderr = "";
  child.stderr.on("data", (d) => (stderr += d));
  createInterface({ input: child.stdout }).on("line", (line) => {
    const msg = JSON.parse(line);
    waiting.get(msg.id)?.(msg);
    waiting.delete(msg.id);
  });
  let id = 0;
  const request = (method, params) =>
    new Promise((resolve, reject) => {
      const n = ++id;
      waiting.set(n, resolve);
      child.stdin.write(JSON.stringify({ jsonrpc: "2.0", id: n, method, params }) + "\n");
      setTimeout(() => reject(new Error(`${method} timed out; bridge stderr: ${stderr}`)), 8000);
    });
  const exited = new Promise((resolve) => child.on("exit", (code) => resolve({ code, stderr })));
  return { request, notify: (method) => child.stdin.write(JSON.stringify({ jsonrpc: "2.0", method }) + "\n"), close: () => child.stdin.end(), exited };
}

await step("agent connects through the bridge", async () => {
  await page.locator(".mcp .seg button", { hasText: "Endpoints" }).click();
  await page.getByLabel("Access to big", { exact: true }).selectOption("write");
  await page.waitForTimeout(200);
  await page.getByLabel("Client").selectOption("json-bridge");
  await page.getByRole("button", { name: "Copy Config" }).click();
  const cfg = Object.values(JSON.parse(await page.evaluate(() => navigator.clipboard.readText())).mcpServers)[0];
  if (!cfg.command.endsWith("bin/Chloe-server") || cfg.args[0] !== "mcp" || cfg.env.CHLOE_MCP_TOKEN !== token()) throw new Error("config: " + JSON.stringify(cfg));
  const c = stdioClient(cfg.command, cfg.args, { ...cfg.env, CHLOE_CONFIG_DIR: `${dir}/../config` });
  const init = await c.request("initialize", { protocolVersion: "2025-06-18", capabilities: {}, clientInfo: { name: "e2e", version: "1" } });
  if (init.result?.serverInfo?.name !== "chloe") throw new Error(JSON.stringify(init));
  c.notify("notifications/initialized");
  const tools = (await c.request("tools/list", {})).result.tools.map((t) => t.name);
  if (!tools.includes("insert_rows")) throw new Error("tools: " + tools.join(", "));
  // A write still waits in Chloe's approval dialog.
  const pending = c.request("tools/call", { name: "insert_rows", arguments: { connection: "big", table: "n", rows: [{ v: "via bridge" }] } });
  await page.getByRole("dialog", { name: "Approve AI change" }).waitFor();
  await page.getByRole("button", { name: "Approve" }).click();
  const r = await pending;
  if (r.result?.isError || r.result.structuredContent.inserted !== 1) throw new Error(JSON.stringify(r));

  // Without HTTP there's no network port, and the bridge keeps working.
  await page.getByLabel("HTTP", { exact: true }).uncheck();
  await page.getByText("Bridge only (no network port)").waitFor();
  if (await fetch(URL).then(() => true).catch(() => false)) throw new Error("HTTP still listening");
  const httpClients = await page.getByLabel("Client").locator("option", { hasText: "HTTP" }).count();
  if (httpClients !== 0) throw new Error("HTTP clients offered with HTTP off");
  const sel = await c.request("tools/call", { name: "select_rows", arguments: { connection: "big", table: "n", where: [{ column: "v", op: "=", value: "via bridge" }] } });
  if (sel.result.structuredContent.row_count !== 1) throw new Error(JSON.stringify(sel));
  await shot("mcp-bridge");
  c.close();
  const { code } = await c.exited;
  if (code !== 0) throw new Error("bridge exit code " + code);

  // A wrong token is refused with a clear message.
  const bad = stdioClient(cfg.command, cfg.args, { CHLOE_MCP_TOKEN: "chloe_nope", CHLOE_CONFIG_DIR: `${dir}/../config` });
  const out = await bad.exited;
  if (out.code !== 1 || !out.stderr.includes("unknown or disabled endpoint token")) throw new Error(JSON.stringify(out));
  await page.getByLabel("HTTP", { exact: true }).check();
  await page.getByText(`Listening on ${URL} and the bridge`).waitFor();
});

await step("stop server", async () => {
  await page.getByLabel("Run MCP server").uncheck();
  await page.getByText("Stopped").waitFor();
  const r = await fetch(URL).catch(() => null);
  if (r) throw new Error("still listening");
});

if (errors.length) console.log("page errors:\n  " + errors.join("\n  "));
await browser.close();
process.exit(failed ? 1 : 0);
