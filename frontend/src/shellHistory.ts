// Console history kept in browser storage, which other apps on the machine can
// read. Like redis-cli and mongosh, commands that carry credentials are kept for
// the session (↑ still finds them) but never written there.

import { safeStorage } from "./prefs";

const MAX = 200;

const REDIS_CONFIG_SECRETS = new Set(["requirepass", "masterauth", "tls-key-file-pass", "tls-client-key-file-pass"]);

/** A Redis command that carries a password (the ones redis-cli leaves out of its history). */
export function isSensitiveRedis(cmd: string): boolean {
  const words = cmd.trim().split(/\s+/).map((w) => w.replace(/^["']|["']$/g, "").toLowerCase());
  const [c, sub] = words;
  if (c === "auth") return true;
  if (c === "acl" && sub === "setuser") return true;
  if ((c === "hello" || c === "migrate") && words.some((w) => w === "auth" || w === "auth2")) return true;
  if (c === "config" && sub === "set") return words.slice(2).some((w) => REDIS_CONFIG_SECRETS.has(w));
  return false;
}

const MONGO_SECRET_CALLS = /\b(auth|createUser|updateUser|changeUserPassword|connect|Mongo)\s*\(/;
const MONGO_SECRET_FIELDS = /\bpwd\b|password/i;
const URI_WITH_PASSWORD = /\b[a-z][a-z0-9+.-]*:\/\/[^\s/@:'"`]+:[^\s/@'"`]+@/i;

/** Shell input that may carry a password (what mongosh's history redaction removes, and a little more). */
export function isSensitiveMongo(src: string): boolean {
  return MONGO_SECRET_CALLS.test(src) || MONGO_SECRET_FIELDS.test(src) || URI_WITH_PASSWORD.test(src);
}

export class ShellHistory {
  /** Newest first. */
  items: string[];

  constructor(
    private key: string,
    private sensitive: (cmd: string) => boolean,
  ) {
    let saved: string[] = [];
    try {
      const v = JSON.parse(safeStorage.getItem(key) || "[]");
      if (Array.isArray(v)) saved = v.filter((x) => typeof x === "string");
    } catch {
      /* not JSON: start over */
    }
    this.items = saved.filter((c) => !sensitive(c));
    // Drop anything an earlier release saved before it redacted.
    if (this.items.length !== saved.length) this.save();
  }

  add(cmd: string) {
    this.items = [cmd, ...this.items.filter((h) => h !== cmd)].slice(0, MAX);
    this.save();
  }

  private save() {
    safeStorage.setItem(this.key, JSON.stringify(this.items.filter((c) => !this.sensitive(c))));
  }
}
