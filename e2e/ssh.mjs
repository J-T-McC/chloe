// SSH tunnels from the connection dialog: public-key auth through the chloe-sshd
// container, and a host that accepts TCP but never answers must fail, not spin.
import net from "node:net";
import { resolve } from "node:path";
import { chromium } from "playwright";

const out = process.argv[2];
const key = resolve("../testdata/sshd/id_ed25519");
const silent = net.createServer(() => {}).listen(0, "127.0.0.1");
await new Promise((r) => silent.once("listening", r));
const silentPort = String(silent.address().port);

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 880 } });
page.setDefaultTimeout(8000);
const shot = (n) => page.screenshot({ path: `${out}/${n}.png` });
let failed = false;
const step = async (name, fn) => {
  try {
    await fn();
    console.log("OK  ", name);
  } catch (e) {
    failed = true;
    console.log("FAIL", name, e.message.split("\n")[0]);
    await shot("fail-" + name.replace(/\W+/g, "_"));
  }
};
const field = (l) => page.locator(".field", { has: page.locator(`label:text-is("${l}")`) }).locator("input, select").first();

await page.goto(process.env.BASE ?? "http://localhost:8080/");
await page.waitForTimeout(800);

const openDialog = async (sshPort) => {
  await page.getByRole("button", { name: "Connection" }).first().click();
  await page.locator(".type-card").filter({ has: page.locator(`span:text-is("MySQL")`) }).click();
  await field("Connection Name").fill("Tunnelled MySQL");
  await field("Host").fill("mysql"); // resolved by the sshd container
  await field("Port").fill("3306");
  await field("User Name").fill("root");
  await field("Password").fill("chloe");
  await page.getByText("SSH", { exact: true }).click();
  await page.getByLabel("Use SSH Tunnel").check();
  await field("Host").fill("127.0.0.1");
  await field("Port").fill(sshPort);
  await field("User Name").fill("tunnel");
  await field("Authentication").selectOption({ label: "Public Key" });
  await field("Private Key").fill(key);
};

await step("public key tunnel tests ok", async () => {
  await openDialog("2222");
  await page.getByRole("button", { name: "Test Connection" }).click();
  await page.locator(".status-line.ok").waitFor({ timeout: 15000 });
  await page.locator('.tunnel-diagram[data-probe="ok"]').waitFor();
  if ((await page.locator(".tunnel-node.ok").count()) !== 3) throw new Error("all three nodes should be lit");
  await shot("ssh-key-ok");
  await field("Port").fill("2223"); // editing clears the lights
  await page.locator('.tunnel-diagram[data-probe="idle"]').waitFor();
  await page.getByRole("button", { name: "Cancel" }).click();
});

await step("silent ssh host reports an error", async () => {
  await openDialog(silentPort);
  const t0 = Date.now();
  await page.getByRole("button", { name: "Test Connection" }).click();
  await page.locator('.tunnel-diagram[data-probe="testing"]').waitFor();
  await shot("ssh-testing");
  await page.locator(".status-line:not(.ok)").waitFor({ timeout: 25000 });
  await page.locator('.tunnel-diagram[data-probe="ssh-failed"]').waitFor();
  const text = await page.locator(".status-line").innerText();
  if (!/no response from the SSH server/.test(text)) throw new Error("unexpected status: " + text);
  console.log(`     failed after ${((Date.now() - t0) / 1000).toFixed(1)}s: ${text}`);
  await shot("ssh-silent-error");
});

await step("database failure lights the second leg", async () => {
  await page.getByRole("button", { name: "Cancel" }).click();
  await openDialog("2222");
  await page.getByText("General", { exact: true }).click();
  await field("Password").fill("wrong");
  await page.getByText("SSH", { exact: true }).click();
  await page.getByRole("button", { name: "Test Connection" }).click();
  await page.locator('.tunnel-diagram[data-probe="db-failed"]').waitFor({ timeout: 15000 });
  if ((await page.locator(".tunnel-link.ok").count()) !== 1 || (await page.locator(".tunnel-link.fail").count()) !== 1)
    throw new Error("expected the SSH leg ok and the database leg failed");
  await shot("ssh-db-failed");
});

await browser.close();
silent.close();
process.exit(failed ? 1 : 0);
