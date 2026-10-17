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
const copied: string[] = [];
vi.mock("../api", () => ({ copyText: (s: string) => void copied.push(s) }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

describe("DataGrid", () => {
  it("drops a page that arrives after the data changed", async () => {
    let resolveOld!: (rows: Cell[][]) => void;
    const oldFetch = () => new Promise<Cell[][]>((r) => (resolveOld = r)).then((rows) => ({ rows }));
    const newFetch = () => Promise.resolve({ rows: [["new-0"], ["new-1"]] as Cell[][] });
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

  it("edits and copies a cut value whole", async () => {
    const columns = [{ name: "v", type: "TEXT" }] as never;
    const fetchPage = () => Promise.resolve({ rows: [["abc"], ["short"]] as Cell[][], cut: [[0, 0]] });
    const fetchFull = vi.fn((offset: number, limit: number) => Promise.resolve([["abcdef"], ["short"]].slice(offset, offset + limit) as Cell[][]));
    const edited: Array<[number, number, Cell]> = [];
    const el = document.createElement("div");
    document.body.appendChild(el);
    const root = createRoot(el);
    await act(async () =>
      root.render(<DataGrid columns={columns} rowCount={2} dataKey="a" fetchPage={fetchPage} fetchFull={fetchFull} editable onEditCell={(r, c, v) => void edited.push([r, c, v])} />),
    );
    const cell = el.querySelectorAll<HTMLElement>(".grid-cell")[0];
    expect(cell.textContent).toBe("abc…");
    const grid = el.querySelector<HTMLElement>(".grid")!;
    // Opening the editor fetches the whole value; leaving it unchanged edits nothing.
    await act(async () => cell.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, button: 0 })));
    await act(async () => grid.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    const input = el.querySelector<HTMLInputElement>(".cell-editor")!;
    expect(input.value).toBe("abcdef");
    await act(async () => input.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(edited).toEqual([]);
    // Copy reads the whole value too.
    await act(async () => grid.dispatchEvent(new KeyboardEvent("keydown", { key: "c", metaKey: true, bubbles: true })));
    expect(copied[copied.length - 1]).toBe("abcdef");
    expect(fetchFull).toHaveBeenCalledTimes(2);
    act(() => root.unmount());
  });
});
