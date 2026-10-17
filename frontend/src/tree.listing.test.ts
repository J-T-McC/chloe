import { describe, expect, it, vi } from "vitest";

const list = vi.fn(async (_c: string, _d: string, _s: string, kind: string) => [{ name: kind + list.mock.calls.length }]);
vi.mock("./api", () => ({ DBType: {}, Explorer: { ListObjects: list }, ObjectKind: {}, errorText: String, arr: (x: unknown[]) => x ?? [] }));
type S = { objectsVersion: number; folderVersions: Record<string, number> };
let listener: (s: S, prev: S) => void = () => {};
vi.mock("./store", () => ({ useApp: { getState: () => ({}), subscribe: (f: typeof listener) => (listener = f) }, connById: () => undefined }));

const { listObjects, forgetListings, folderKey } = await import("./tree");
const t = "table" as never;
const q = "query" as never;

describe("shared folder listings", () => {
  it("shares a listing between the tree and the Objects list", async () => {
    const [a, b] = await Promise.all([listObjects("c", "db", undefined, t), listObjects("c", "db", undefined, t)]);
    expect(a).toBe(b);
    expect(list).toHaveBeenCalledTimes(1);
  });

  it("drops a folder's listing when that folder changes, and only that one", async () => {
    list.mockClear();
    await listObjects("c", "db2", undefined, t);
    await listObjects("c", "db2", undefined, q);
    const v0 = { objectsVersion: 0, folderVersions: {} };
    listener({ objectsVersion: 0, folderVersions: { [folderKey("c", "db2", undefined, q)]: 1 } }, v0);
    await listObjects("c", "db2", undefined, t); // still shared
    await listObjects("c", "db2", undefined, q); // read again
    expect(list).toHaveBeenCalledTimes(3);
  });

  it("drops everything on a global change or a refresh under a key", async () => {
    list.mockClear();
    await listObjects("c", "db3", undefined, t);
    listener({ objectsVersion: 1, folderVersions: {} }, { objectsVersion: 0, folderVersions: {} });
    await listObjects("c", "db3", undefined, t);
    forgetListings("c/db3");
    await listObjects("c", "db3", undefined, t);
    expect(list).toHaveBeenCalledTimes(3);
  });
});
