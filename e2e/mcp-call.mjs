// Calls a Chloe MCP tool in the tester sandbox and prints the result; images it
// returns are saved under e2e/.tester/images and their paths printed.
//   node e2e/mcp-call.mjs instructions          # what the server tells an agent when a session starts
//   node e2e/mcp-call.mjs tools                 # list the tools with their input schemas
//   node e2e/mcp-call.mjs <tool> '<json args>'
import { readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
const here = dirname(fileURLToPath(import.meta.url));
const out = join(here, ".tester");
const { url, token } = JSON.parse(readFileSync(join(out, "endpoint.json"), "utf8"));
const [name, args = "{}"] = process.argv.slice(2);
async function rpc(method, params) {
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream", Authorization: `Bearer ${token}`, "MCP-Protocol-Version": "2025-06-18" },
    body: JSON.stringify({ jsonrpc: "2.0", id: 1, method, params }),
  });
  const text = await res.text();
  const data = text.startsWith("{") ? text : text.split("\n").filter((l) => l.startsWith("data:")).map((l) => l.slice(5)).join("");
  return JSON.parse(data);
}
if (name === "instructions") {
  const r = await rpc("initialize", { protocolVersion: "2025-06-18", capabilities: {}, clientInfo: { name: "tester", version: "1" } });
  console.log(r.result.instructions);
  process.exit(0);
}
if (name === "tools") {
  const r = await rpc("tools/list", {});
  for (const t of r.result.tools) console.log(`## ${t.name}\n${t.description}\n${JSON.stringify(t.inputSchema)}\n`);
  process.exit(0);
}
const r = await rpc("tools/call", { name, arguments: JSON.parse(args) });
if (r.error) {
  console.log("ERROR", JSON.stringify(r.error));
  process.exit(1);
}
let n = 0;
for (const c of r.result.content) {
  if (c.type === "image") {
    const f = join(out, "images", `${name}-${Date.now()}-${++n}.png`);
    writeFileSync(f, Buffer.from(c.data, "base64"));
    console.log("IMAGE", f);
  } else if (c.type === "text") console.log(r.result.isError ? "TOOL ERROR " + c.text : c.text);
}
