// MCP server: endpoint setup in the UI, tool calls over HTTP, approval dialog, danger mode, activity log.
import { chromium } from "playwright";
import { readFileSync, writeFileSync } from "node:fs";
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
  if (r.isError || r.structuredContent.rows !== 1) throw new Error(text(r));
  if (await page.getByRole("dialog", { name: "Approve AI change" }).count()) throw new Error("asked anyway");
  await page.getByLabel("Danger mode").uncheck();
  await page.locator(".mcp-button.running").waitFor();
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

await step("stop server", async () => {
  await page.getByLabel("Run MCP server").uncheck();
  await page.getByText("Stopped").waitFor();
  const r = await fetch(URL).catch(() => null);
  if (r) throw new Error("still listening");
});

if (errors.length) console.log("page errors:\n  " + errors.join("\n  "));
await browser.close();
process.exit(failed ? 1 : 0);
