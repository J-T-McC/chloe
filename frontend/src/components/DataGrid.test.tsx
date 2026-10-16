// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { DataGrid, type Cell } from "./DataGrid";

// jsdom has no layout, so the virtualizer would show no rows; show the first two.
vi.mock("@tanstack/react-virtual", () => ({
  useVirtualizer: () => ({
    getVirtualItems: () => [0, 1].map((i) => ({ index: i, key: i, start: i * 22, size: 22 })),
    getTotalSize: () => 44,
    scrollToIndex: () => {},
    measure: () => {},
  }),
}));
vi.mock("../api", () => ({ copyText: () => {} }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe("DataGrid", () => {
  it("drops a page that arrives after the data changed", async () => {
    let resolveOld!: (rows: Cell[][]) => void;
    const oldFetch = () => new Promise<Cell[][]>((r) => (resolveOld = r));
    const newFetch = () => Promise.resolve<Cell[][]>([["new-0"], ["new-1"]]);
    const columns = [{ name: "v", type: "TEXT" }] as never;

    const el = document.createElement("div");
    document.body.appendChild(el);
    const root = createRoot(el);
    await act(async () => root.render(<DataGrid columns={columns} rowCount={2} dataKey="a" fetchPage={oldFetch} />));
    await act(async () => root.render(<DataGrid columns={columns} rowCount={2} dataKey="b" fetchPage={newFetch} />));
    expect(el.textContent).toContain("new-0");

    await act(async () => resolveOld([["old-0"], ["old-1"]]));
    expect(el.textContent).toContain("new-0");
    expect(el.textContent).not.toContain("old-0");
    act(() => root.unmount());
  });
});
