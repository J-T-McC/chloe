// Pure helpers for the Redis key browser: binary-safe display of values and the
// folder tree built by splitting key names on a separator (user:1:name -> user / 1).
import type { R } from "../api";

/** Decode a Value to bytes. */
export function bytesOf(v: R.Value): Uint8Array {
  if (!v.base64) return new TextEncoder().encode(v.text);
  const bin = atob(v.text);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

/** Text for display: UTF-8 as is, binary with \xHH escapes for unprintable bytes. */
export function display(v: R.Value): string {
  if (!v.base64) return v.text;
  let s = "";
  for (const b of bytesOf(v)) s += b >= 0x20 && b < 0x7f && b !== 0x5c ? String.fromCharCode(b) : b === 0x5c ? "\\\\" : "\\x" + b.toString(16).padStart(2, "0");
  return s;
}

/** Hex dump: offset, 16 bytes per line, ASCII column. */
export function hexDump(v: R.Value, max = 64 * 1024): string {
  const b = bytesOf(v);
  const lines: string[] = [];
  for (let off = 0; off < Math.min(b.length, max); off += 16) {
    const row = b.slice(off, off + 16);
    const hex = Array.from(row, (x) => x.toString(16).padStart(2, "0")).join(" ");
    const ascii = Array.from(row, (x) => (x >= 0x20 && x < 0x7f ? String.fromCharCode(x) : ".")).join("");
    lines.push(`${off.toString(16).padStart(8, "0")}  ${hex.padEnd(47)}  ${ascii}`);
  }
  if (b.length > max) lines.push(`… ${b.length - max} more bytes`);
  return lines.join("\n");
}

export const text = (s: string): R.Value => ({ text: s });

/** Glob pattern for a search box: plain text matches anywhere; globs pass through. */
export function toPattern(q: string): string {
  const t = q.trim();
  if (!t) return "*";
  if (/[*?[\]]/.test(t)) return t;
  return `*${t.replace(/\\/g, "\\\\")}*`;
}

export interface KeyRow {
  kind: "folder" | "key";
  /** folder: prefix including the trailing separator; key: the key text */
  id: string;
  label: string;
  depth: number;
  /** keys under a folder */
  count: number;
  key?: R.Key;
}

interface Folder {
  folders: Map<string, Folder>;
  keys: R.Key[];
  count: number;
}

const natural = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" }).compare;

/**
 * Rows for the key list. In tree mode, names are split on sep into folders (only
 * expanded folders show their contents); otherwise a flat sorted list.
 */
export function keyRows(keys: R.Key[], sep: string, expanded: Set<string>, tree: boolean): KeyRow[] {
  if (!tree || !sep) {
    return [...keys].sort((a, b) => natural(display(a.name), display(b.name))).map((k) => ({ kind: "key", id: keyId(k), label: display(k.name), depth: 0, count: 1, key: k }));
  }
  const root: Folder = { folders: new Map(), keys: [], count: 0 };
  for (const k of keys) {
    const name = display(k.name);
    const parts = name.split(sep);
    let f = root;
    f.count++;
    for (let i = 0; i < parts.length - 1; i++) {
      let next = f.folders.get(parts[i]);
      if (!next) f.folders.set(parts[i], (next = { folders: new Map(), keys: [], count: 0 }));
      next.count++;
      f = next;
    }
    f.keys.push(k);
  }
  const out: KeyRow[] = [];
  const walk = (f: Folder, prefix: string, depth: number) => {
    for (const name of [...f.folders.keys()].sort(natural)) {
      const sub = f.folders.get(name)!;
      const id = prefix + name + sep;
      out.push({ kind: "folder", id, label: name, depth, count: sub.count });
      if (expanded.has(id)) walk(sub, id, depth + 1);
    }
    for (const k of [...f.keys].sort((a, b) => natural(display(a.name), display(b.name)))) {
      const full = display(k.name);
      out.push({ kind: "key", id: keyId(k), label: full.slice(prefix.length), depth, count: 1, key: k });
    }
  };
  walk(root, "", 0);
  return out;
}

/** Stable id for a key (binary keys keep their base64 form). */
export const keyId = (k: { name: R.Value }) => (k.name.base64 ? "b64:" + k.name.text : k.name.text);

/** Human-readable TTL: 59s, 1h 2m, 3d 4h. */
export function formatTTL(ttl: number): string {
  if (ttl < 0) return "No limit";
  const d = Math.floor(ttl / 86400);
  const h = Math.floor((ttl % 86400) / 3600);
  const m = Math.floor((ttl % 3600) / 60);
  const s = ttl % 60;
  if (d) return `${d}d ${h}h`;
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m ${s}s`;
  return `${s}s`;
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

export const TYPES = ["string", "hash", "list", "set", "zset", "stream"] as const;
export const TYPE_LABEL: Record<string, string> = { string: "String", hash: "Hash", list: "List", set: "Set", zset: "Sorted Set", stream: "Stream" };
