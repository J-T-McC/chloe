// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { useAsync, type Async } from "./useAsync";

vi.mock("../api", () => ({ errorText: (e: unknown) => String(e) }));

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function deferred<T>() {
  let resolve!: (v: T) => void, reject!: (e: unknown) => void;
  const p = new Promise<T>((a, b) => ((resolve = a), (reject = b)));
  return { p, resolve, reject };
}

function mount() {
  const el = document.createElement("div");
  const root = createRoot(el);
  let last!: Async<string[]>;
  function Probe({ conn, load, keep, delay }: { conn: string; load: ((c: string) => Promise<string[]>) | null; keep?: boolean | string; delay?: number }) {
    last = useAsync(load && (() => load(conn)), [conn], [], { keep, delay });
    return null;
  }
  return { root, Probe, get: () => last };
}

describe("useAsync", () => {
  it("drops a response that arrives after the deps changed", async () => {
    const a = deferred<string[]>(), b = deferred<string[]>();
    const load = (c: string) => (c === "a" ? a.p : b.p);
    const { root, Probe, get } = mount();
    await act(async () => root.render(<Probe conn="a" load={load} />));
    await act(async () => root.render(<Probe conn="b" load={load} />));
    await act(async () => b.resolve(["b1"]));
    await act(async () => a.resolve(["a1"]));
    expect(get().data).toEqual(["b1"]);
    expect(get().loading).toBe(false);
  });

  it("clears on a new key and keeps data on a reload of the same key", async () => {
    let n = 0;
    const gate = deferred<void>();
    const load = async (c: string) => (await gate.p, [`${c}${++n}`]);
    const { root, Probe, get } = mount();
    gate.resolve();
    await act(async () => root.render(<Probe conn="a" load={load} keep="a" />));
    expect(get().data).toEqual(["a1"]);
    let slow = deferred<string[]>();
    const slowLoad = () => slow.p;
    await act(async () => root.render(<Probe conn="a" load={slowLoad} keep="a" />));
    await act(async () => get().reload());
    expect(get().data).toEqual(["a1"]); // refreshing the same listing
    await act(async () => slow.resolve(["a2"]));
    expect(get().data).toEqual(["a2"]);
    slow = deferred<string[]>();
    await act(async () => root.render(<Probe conn="b" load={slowLoad} keep="b" />));
    expect(get().data).toEqual([]); // a different listing
    expect(get().loading).toBe(true);
  });

  it("reports errors, clears with a null fn and debounces", async () => {
    vi.useFakeTimers();
    try {
      const calls: string[] = [];
      const load = async (c: string) => {
        calls.push(c);
        if (c === "bad") throw "nope";
        return [c];
      };
      const { root, Probe, get } = mount();
      await act(async () => root.render(<Probe conn="x" load={load} delay={150} />));
      await act(async () => root.render(<Probe conn="y" load={load} delay={150} />));
      await act(async () => void vi.advanceTimersByTime(150));
      expect(calls).toEqual(["y"]);
      expect(get().data).toEqual(["y"]);
      await act(async () => root.render(<Probe conn="bad" load={load} delay={150} />));
      await act(async () => void vi.advanceTimersByTime(150));
      expect(get().error).toBe("nope");
      await act(async () => root.render(<Probe conn="z" load={null} />));
      expect(get()).toMatchObject({ data: [], error: "", loading: false });
    } finally {
      vi.useRealTimers();
    }
  });
});
