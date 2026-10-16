import { useEffect, useLayoutEffect, useRef, useState, type HTMLAttributes, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { ChevronsRight } from "lucide-react";

// A toolbar that never cuts its items off. When the window is too narrow, the
// items that don't fit are hidden and a » button at the end shows them in a
// menu, as a macOS toolbar does. Items before the spacer go first, from the end
// inward; then those after it. The menu renders the toolbar's own children again
// (only while it's open, so nothing is in the page twice), so buttons, selects
// and segmented controls keep working there with their state.
//
// Items are the bar's element children; a fragment's elements are items of their
// own. data-tb-keep on an item keeps it in the bar.

const HIDDEN = "tbHidden";

function isSpacer(el: Element) {
  return el.classList.contains("spacer");
}

function isOwn(el: Element) {
  return el.classList.contains("tb-more");
}

export function Toolbar({ className = "toolbar", children, ...rest }: HTMLAttributes<HTMLDivElement> & { children?: ReactNode }) {
  const bar = useRef<HTMLDivElement>(null);
  const more = useRef<HTMLButtonElement>(null);
  const menu = useRef<HTMLDivElement>(null);
  const [hidden, setHidden] = useState<number[]>([]);
  const [open, setOpen] = useState(false);
  const [at, setAt] = useState({ top: 0, right: 0 });
  const hiddenKey = useRef("");

  // Decide which items fit. Everything is shown first so each is measured at its
  // natural width.
  const fit = useRef(() => {});
  fit.current = () => {
    const el = bar.current;
    const btn = more.current;
    if (!el || !btn) return;
    const items = [...el.children].filter((c) => !isOwn(c)) as HTMLElement[];
    for (const i of items) delete i.dataset[HIDDEN];
    btn.hidden = false;
    const cs = getComputedStyle(el);
    const gap = parseFloat(cs.columnGap) || 0;
    const avail = el.clientWidth - (parseFloat(cs.paddingLeft) || 0) - (parseFloat(cs.paddingRight) || 0);
    const width = (i: HTMLElement) => {
      if (isSpacer(i)) return 0;
      const s = getComputedStyle(i);
      return i.getBoundingClientRect().width + (parseFloat(s.marginLeft) || 0) + (parseFloat(s.marginRight) || 0);
    };
    const w = items.map(width);
    const moreW = btn.getBoundingClientRect().width + gap;
    const shown = items.map((_, i) => i);
    const total = () => shown.reduce((s, i) => s + w[i], 0) + gap * Math.max(0, shown.length - 1);
    const out: number[] = [];
    if (total() > avail + 0.5) {
      const spacer = items.findIndex(isSpacer);
      const left = items.map((_, i) => i).filter((i) => spacer < 0 || i < spacer);
      const right = spacer < 0 ? [] : items.map((_, i) => i).filter((i) => i > spacer);
      const order = [...left.reverse(), ...right.reverse()].filter((i) => items[i].dataset.tbKeep === undefined);
      for (const i of order) {
        if (total() + moreW <= avail + 0.5) break;
        out.push(i);
        shown.splice(shown.indexOf(i), 1);
      }
      // A separator left at the end of what's shown separates nothing.
      for (;;) {
        const lastLeft = shown.filter((i) => spacer < 0 || i < spacer).pop();
        if (lastLeft === undefined || !items[lastLeft].classList.contains("tb-sep")) break;
        out.push(lastLeft);
        shown.splice(shown.indexOf(lastLeft), 1);
      }
    }
    out.sort((a, b) => a - b);
    for (const i of out) items[i].dataset[HIDDEN] = "";
    btn.hidden = out.length === 0;
    const key = out.join(",");
    if (key !== hiddenKey.current) {
      hiddenKey.current = key;
      setHidden(out);
      if (!out.length) setOpen(false);
    }
  };

  useLayoutEffect(() => {
    const el = bar.current;
    if (!el) return;
    fit.current();
    let frame = 0;
    const again = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => fit.current());
    };
    const ro = new ResizeObserver(again);
    ro.observe(el);
    // Items appear, go and change their text as the view changes.
    const mo = new MutationObserver(again);
    mo.observe(el, { childList: true, subtree: true, characterData: true, attributes: true, attributeFilter: ["class", "disabled"] });
    return () => {
      cancelAnimationFrame(frame);
      ro.disconnect();
      mo.disconnect();
    };
  }, []);

  // The menu shows only the items the bar hides.
  useLayoutEffect(() => {
    if (!open || !menu.current) return;
    const set = new Set(hidden);
    ([...menu.current.children] as HTMLElement[]).forEach((c, i) => {
      if (set.has(i) && !isSpacer(c)) delete c.dataset[HIDDEN];
      else c.dataset[HIDDEN] = "";
    });
  });

  useEffect(() => {
    if (!open) return;
    const away = (e: MouseEvent) => {
      const t = e.target as Node;
      if (!menu.current?.contains(t) && !more.current?.contains(t)) setOpen(false);
    };
    const key = (e: KeyboardEvent) => e.key === "Escape" && (setOpen(false), more.current?.focus());
    const close = () => setOpen(false);
    document.addEventListener("mousedown", away, true);
    document.addEventListener("keydown", key);
    window.addEventListener("resize", close);
    return () => {
      document.removeEventListener("mousedown", away, true);
      document.removeEventListener("keydown", key);
      window.removeEventListener("resize", close);
    };
  }, [open]);

  return (
    <div ref={bar} className={className + " tb-fit"} {...rest}>
      {children}
      <button ref={more} type="button" className="tb-more" hidden aria-label="More toolbar items" title="More" aria-expanded={open} onClick={(e) => {
          const r = e.currentTarget.getBoundingClientRect();
          setAt({ top: r.bottom + 4, right: Math.max(8, window.innerWidth - r.right) });
          setOpen((o) => !o);
        }}>
        <ChevronsRight size={14} />
      </button>
      {open &&
        createPortal(
          <div
            ref={menu}
            className={className + " tb-menu"}
            style={{ top: at.top, right: at.right }}
            onClick={(e) => {
              // A button does its thing and the menu closes; selects and inputs stay open.
              if ((e.target as HTMLElement).closest("button:not(:disabled)")) setOpen(false);
            }}
          >
            {children}
          </div>,
          document.body,
        )}
    </div>
  );
}
