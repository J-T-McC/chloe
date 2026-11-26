// Double-clicking an empty part of the window header zooms the window on macOS.
// The Wails runtime does that: on a double-click whose target has
// `--wails-draggable: drag` it sends "wails:drag:doubleclick", and the app follows
// the user's System Settings choice (zoom, minimise or nothing). The browser build
// has no native window, so this stands in a WKWebView message handler and a macOS
// environment, and checks which double-clicks reach the native side.
import { chromium } from "playwright";
const out = process.argv[2];

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1400, height: 860 } });
page.setDefaultTimeout(8000);
const errors = [];
page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
let failed = 0;
const step = async (name, fn) => {
  try {
    await fn();
    console.log("OK  ", name);
  } catch (e) {
    failed++;
    console.log("FAIL", name, e.message.split("\n")[0]);
    await page.screenshot({ path: `${out}/fail-${name.replace(/\W+/g, "_")}.png` });
  }
};
const check = (ok, msg) => {
  if (!ok) throw new Error(msg);
};

await page.addInitScript(() => {
  window.__invoked = [];
  window.webkit = { messageHandlers: { external: { postMessage: (m) => window.__invoked.push(String(m)) } } };
});
await page.goto(process.env.BASE ?? "http://localhost:8080/");
await page.locator(".main-toolbar .tool-button").first().waitFor();
await page.evaluate(() => {
  window._wails = window._wails || {};
  window._wails.environment = { ...window._wails.environment, OS: "darwin" };
});

// Double-clicks at (x, y) and says whether the runtime asked the window to zoom.
async function zooms(x, y) {
  await page.evaluate(() => (window.__invoked.length = 0));
  await page.mouse.dblclick(x, y);
  return page.evaluate(() => window.__invoked.includes("wails:drag:doubleclick"));
}

const buttons = page.locator(".main-toolbar .tool-button");
const first = await buttons.nth(0).boundingBox();
const second = await buttons.nth(1).boundingBox();
const last = await buttons.nth((await buttons.count()) - 1).boundingBox();
const right = await page.locator(".titlebar-right").boundingBox();
const header = await page.locator(".titlebar").boundingBox();
const midY = last.y + last.height / 2;

for (const [name, x, y] of [
  ["above the buttons", first.x + first.width / 2, header.y + 4],
  ["beside the traffic lights", 40, midY],
  ["between two buttons", (first.x + first.width + second.x) / 2, midY],
  ["after the last button", (last.x + last.width + right.x) / 2, midY],
  ["below the right-hand buttons", right.x + right.width / 2, header.y + header.height - 3],
]) {
  await step(`double-click ${name} zooms`, async () => {
    check(await zooms(x, y), `no zoom request at (${Math.round(x)}, ${Math.round(y)}) on ${await page.evaluate(([x, y]) => document.elementFromPoint(x, y)?.className, [x, y])}`);
  });
}

await step("double-click on a header button doesn't zoom", async () => {
  const r = await page.locator(".titlebar-right button").first().boundingBox();
  check(!(await zooms(r.x + r.width / 2, r.y + r.height / 2)), "a right-hand button asked to zoom");
  const b = await buttons.filter({ hasText: "Model" }).boundingBox();
  check(!(await zooms(b.x + b.width / 2, b.y + b.height / 2)), "a tool button asked to zoom");
});

await step("double-click below the header doesn't zoom", async () => {
  check(!(await zooms(700, header.y + header.height + 40)), "the workspace asked to zoom");
});

// The » menu of a narrow window is the header's toolbar again, outside the header.
await step("double-click in the » menu doesn't zoom", async () => {
  await page.setViewportSize({ width: 700, height: 700 });
  await page.locator(".main-toolbar").getByRole("button", { name: "More toolbar items" }).click();
  const m = await page.locator(".tb-menu").boundingBox();
  check(!(await zooms(m.x + 2, m.y + 2)), "the » menu asked to zoom");
  await page.keyboard.press("Escape");
});

if (errors.length) console.log("FAIL page errors", errors.join("\n"));
await browser.close();
process.exit(failed || errors.length ? 1 : 0);
