import { Boxes, Braces, Clock, Columns3, Database, Eye, FileCode2, FolderTree, Hash, Layers, ListTree, Table2, Zap, type LucideIcon } from "lucide-react";
import { DBType, ObjectKind } from "../api";

// Neutral engine badges (we don't ship vendor logos).
const ENGINE: Record<string, { label: string; color: string }> = {
  mysql: { label: "My", color: "#3e7cb1" },
  mariadb: { label: "Ma", color: "#b07a4f" },
  postgres: { label: "Pg", color: "#4a73a8" },
  mongodb: { label: "Mo", color: "#4b9e5b" },
  redis: { label: "Rd", color: "#c94b3f" },
  sqlite: { label: "SL", color: "#5b8db3" },
  sqlserver: { label: "MS", color: "#b4474e" },
  oracle: { label: "Or", color: "#c4473a" },
};

export function DBIcon({ type, size = 16, dim = false }: { type: DBType | string; size?: number; dim?: boolean }) {
  const e = ENGINE[type] ?? { label: "?", color: "#888" };
  return (
    <span
      className="db-icon"
      style={{ width: size, height: size, fontSize: size * 0.48, background: e.color, opacity: dim ? 0.45 : 1 }}
      aria-label={String(type)}
    >
      {e.label}
    </span>
  );
}

// Hosted-service badges: neutral initials in a brand-agnostic palette.
const PRESET: Record<string, { label: string; color: string }> = {
  supabase: { label: "Sb", color: "#3a9e72" },
  neon: { label: "Ne", color: "#2bb5a3" },
  "rds-postgres": { label: "Rd", color: "#c9792b" },
  "rds-mysql": { label: "Rd", color: "#c9792b" },
  "digitalocean-postgres": { label: "DO", color: "#2f73d9" },
  "digitalocean-mysql": { label: "DO", color: "#2f73d9" },
  planetscale: { label: "PS", color: "#555b66" },
  "tidb-cloud": { label: "Ti", color: "#d2463f" },
};

export function PresetIcon({ id, size = 16, dim = false }: { id: string; size?: number; dim?: boolean }) {
  const p = PRESET[id] ?? { label: "?", color: "#888" };
  return (
    <span className="db-icon" style={{ width: size, height: size, fontSize: size * 0.48, background: p.color, opacity: dim ? 0.45 : 1 }} aria-label={id}>
      {p.label}
    </span>
  );
}

/** Icon for a saved connection: its preset badge if any, else the engine badge. */
export function ConnIcon({ type, preset, size = 16, dim = false }: { type: DBType | string; preset?: string; size?: number; dim?: boolean }) {
  return preset && PRESET[preset] ? <PresetIcon id={preset} size={size} dim={dim} /> : <DBIcon type={type} size={size} dim={dim} />;
}

export const KIND_ICON: Record<string, LucideIcon> = {
  [ObjectKind.KindDatabase]: Database,
  [ObjectKind.KindSchema]: FolderTree,
  [ObjectKind.KindTable]: Table2,
  [ObjectKind.KindView]: Eye,
  [ObjectKind.KindMatView]: Layers,
  [ObjectKind.KindFunction]: Braces,
  [ObjectKind.KindProcedure]: FileCode2,
  [ObjectKind.KindTrigger]: Zap,
  [ObjectKind.KindEvent]: Clock,
  [ObjectKind.KindSequence]: Hash,
  [ObjectKind.KindType]: Layers,
  [ObjectKind.KindIndex]: ListTree,
  [ObjectKind.KindCollection]: Boxes,
  column: Columns3,
};

export const KIND_LABEL: Record<string, string> = {
  [ObjectKind.KindTable]: "Tables",
  [ObjectKind.KindView]: "Views",
  [ObjectKind.KindMatView]: "Materialized Views",
  [ObjectKind.KindFunction]: "Functions",
  [ObjectKind.KindProcedure]: "Procedures",
  [ObjectKind.KindTrigger]: "Triggers",
  [ObjectKind.KindEvent]: "Events",
  [ObjectKind.KindSequence]: "Sequences",
  [ObjectKind.KindType]: "Types",
  [ObjectKind.KindIndex]: "Indexes",
  [ObjectKind.KindCollection]: "Collections",
};

export function KindIcon({ kind, size = 14 }: { kind: string; size?: number }) {
  const I = KIND_ICON[kind] ?? Table2;
  return <I size={size} className={"kind-icon kind-" + kind} />;
}
