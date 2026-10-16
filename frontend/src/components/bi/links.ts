// Where a link in dashboard text opens: the system browser in Chloe (set at
// startup), a new tab in an exported report.
let open: (href: string) => void = (href) => {
  window.open(href, "_blank", "noopener");
};

export const openLink = (href: string) => open(href);

export function setOpenLink(fn: (href: string) => void) {
  open = fn;
}
