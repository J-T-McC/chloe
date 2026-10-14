import { ChevronRight } from "lucide-react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { create } from "zustand";

export interface MenuItem {
  label?: string;
  shortcut?: string;
  disabled?: boolean;
  danger?: boolean;
  separator?: boolean;
  checked?: boolean;
  /** Small colour dot before the label (connection colours). */
  swatch?: string;
  submenu?: MenuItem[];
  onClick?(): void;
}

const useMenu = create<{ x: number; y: number; items: MenuItem[] | null }>(() => ({ x: 0, y: 0, items: null }));

export function openMenu(e: { clientX: number; clientY: number; preventDefault(): void; stopPropagation?(): void }, items: MenuItem[]) {
  e.preventDefault();
  e.stopPropagation?.();
  useMenu.setState({ x: e.clientX, y: e.clientY, items });
}

const close = () => useMenu.setState({ items: null });

export function ContextMenuHost() {
  const { x, y, items } = useMenu();

  useEffect(() => {
    if (!items) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && close();
    window.addEventListener("mousedown", close);
    window.addEventListener("blur", close);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("mousedown", close);
      window.removeEventListener("blur", close);
      window.removeEventListener("keydown", onKey);
    };
  }, [items]);

  if (!items) return null;
  return <MenuPanel items={items} x={x} y={y} />;
}

function MenuPanel({ items, x, y, alignRightOf }: { items: MenuItem[]; x: number; y: number; alignRightOf?: number }) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState({ x, y });
  const [sub, setSub] = useState<{ index: number; x: number; y: number; left: number } | null>(null);

  useLayoutEffect(() => {
    // Keep the menu on screen; submenus flip to the left when there's no room.
    const el = ref.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    let nx = x;
    if (nx + r.width > window.innerWidth - 4) nx = alignRightOf !== undefined ? alignRightOf - r.width : window.innerWidth - r.width - 4;
    setPos({ x: Math.max(4, nx), y: Math.max(4, Math.min(y, window.innerHeight - r.height - 4)) });
  }, [x, y, items, alignRightOf]);

  return (
    <div ref={ref} className="context-menu" style={{ left: pos.x, top: pos.y }} onMouseDown={(e) => e.stopPropagation()}>
      {items.map((it, i) =>
        it.separator ? (
          <div key={i} className="menu-sep" />
        ) : (
          <button
            key={i}
            className={"menu-item" + (it.danger ? " danger" : "") + (sub?.index === i ? " open" : "")}
            disabled={it.disabled}
            onMouseEnter={(e) => {
              if (it.submenu) {
                const r = e.currentTarget.getBoundingClientRect();
                setSub({ index: i, x: r.right - 2, y: r.top - 4, left: r.left });
              } else setSub(null);
            }}
            onClick={() => {
              if (it.submenu) return;
              close();
              it.onClick?.();
            }}
          >
            <span className="menu-label">
              {it.checked !== undefined && <span className="menu-check">{it.checked ? "✓" : ""}</span>}
              {it.swatch !== undefined && <span className="menu-swatch" style={{ background: it.swatch || "transparent" }} />}
              {it.label}
            </span>
            {it.shortcut && <kbd>{it.shortcut}</kbd>}
            {it.submenu && <ChevronRight size={12} />}
          </button>
        ),
      )}
      {sub && items[sub.index]?.submenu && <MenuPanel items={items[sub.index].submenu!} x={sub.x} y={sub.y} alignRightOf={sub.left} />}
    </div>
  );
}
