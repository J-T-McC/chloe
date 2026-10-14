import { beforeEach, describe, expect, it } from "vitest";
import { ShellHistory, isSensitiveMongo, isSensitiveRedis } from "./shellHistory";

describe("redis history redaction", () => {
  it.each([
    ["AUTH s3cret", true],
    ["auth default s3cret", true],
    ['"AUTH" x', true],
    ["ACL SETUSER app on >pw ~* +@all", true],
    ["HELLO 3 AUTH default pw", true],
    ["MIGRATE host 6379 key 0 5000 AUTH pw", true],
    ["CONFIG SET requirepass pw", true],
    ["config set masterauth pw", true],
    ["CONFIG SET maxmemory 100mb", false],
    ["GET auth", false],
    ["SET user:1:password x", false],
    ["HELLO 3", false],
    ["ACL LIST", false],
  ])("%s → %s", (cmd, want) => expect(isSensitiveRedis(cmd)).toBe(want));
});

describe("mongo history redaction", () => {
  it.each([
    ['db.createUser({user: "a", pwd: "b", roles: []})', true],
    ['db.auth("a", "b")', true],
    ['db.changeUserPassword("a", "b")', true],
    ['db.updateUser("a", {pwd: passwordPrompt()})', true],
    ['db.runCommand({createUser: "a", pwd: "b", roles: []})', true],
    ['connect("mongodb://a:b@host/db")', true],
    ['const uri = "mongodb://admin:pw@db.internal:27017"', true],
    ['db.users.find({password: {$exists: true}})', true],
    ["db.orders.find({status: 'open'})", false],
    ["show dbs", false],
    ['db.getSiblingDB("auth").stats()', false],
    ['db.events.find({url: "https://example.com/a"})', false],
  ])("%s → %s", (src, want) => expect(isSensitiveMongo(src)).toBe(want));
});

describe("ShellHistory", () => {
  let store: Record<string, string>;
  beforeEach(() => {
    store = {};
    globalThis.localStorage = {
      getItem: (k: string) => store[k] ?? null,
      setItem: (k: string, v: string) => void (store[k] = v),
    } as Storage;
  });

  it("keeps secrets for the session only", () => {
    const h = new ShellHistory("k", isSensitiveRedis);
    h.add("GET a");
    h.add("AUTH pw");
    expect(h.items).toEqual(["AUTH pw", "GET a"]);
    expect(JSON.parse(store.k)).toEqual(["GET a"]);
  });

  it("scrubs secrets saved by earlier releases", () => {
    store.k = JSON.stringify(["GET a", "AUTH pw", "CONFIG SET requirepass x"]);
    const h = new ShellHistory("k", isSensitiveRedis);
    expect(h.items).toEqual(["GET a"]);
    expect(JSON.parse(store.k)).toEqual(["GET a"]);
  });
});
