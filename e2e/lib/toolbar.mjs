// Toolbars hide what doesn't fit behind a » menu (frontend Toolbar.tsx). Clicks a
// toolbar button wherever it is: in the bar, or in the » menu when it's hidden.
export async function clickTool(page, scope, name, opts = { exact: true }) {
  const inBar = scope.getByRole("button", { name, ...opts });
  if (await inBar.first().isVisible()) return inBar.first().click();
  const bar = scope.locator(".tb-fit", { has: page.locator(":scope > .tb-more:not([hidden])") }).filter({ has: page.getByRole("button", { name, includeHidden: true, ...opts }) });
  await bar.first().locator(":scope > .tb-more").click();
  return page.locator(".tb-menu").getByRole("button", { name, ...opts }).click();
}
