// Object privileges of a user: a tree of databases, schemas, tables, columns,
// sequences and routines; the selected object's privileges as checkboxes.
import { ChevronDown, ChevronRight } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Explorer, ObjectInfo, ObjectKind, P, arr } from "../../api";
import { KindIcon } from "../icons";
import { Split } from "../Split";

type Target = Pick<P.Grant, "level" | "database" | "schema" | "object" | "column" | "routineType">;

interface Node {
  id: string;
  label: string;
  icon: string;
  target: Target;
  /** Loads children on first expand; undefined for leaves. */
  load?: () => Promise<Node[]>;
}

const same = (g: Target, t: Target) =>
  g.level === t.level && (g.database ?? "") === (t.database ?? "") && (g.schema ?? "") === (t.schema ?? "") && (g.object ?? "") === (t.object ?? "") && (g.column ?? "") === (t.column ?? "") && (g.routineType ?? "") === (t.routineType ?? "");

/** Whether g is on t or on something inside it (for marking tree nodes). */
const within = (g: Target, t: Target) => {
  if ((g.database ?? "") !== (t.database ?? "")) return false;
  switch (t.level) {
    case "database":
      return true;
    case "schema":
      return g.level !== "database" && (g.schema ?? "") === (t.schema ?? "");
    case "table":
      return (g.level === "table" || g.level === "column") && (g.schema ?? "") === (t.schema ?? "") && g.object === t.object;
    default:
      return same(g, t);
  }
};

function columns(connId: string, base: Target, kind: ObjectKind): () => Promise<Node[]> {
  return async () => {
    const d = await Explorer.TableDetail(connId, { database: base.database!, schema: base.schema, name: base.object!, kind });
    return arr(d.columns).map((c) => ({ id: `${base.database}/${base.schema ?? ""}/${base.object}/${c.name}`, label: c.name, icon: "column", target: { ...base, level: "column", column: c.name } }));
  };
}

async function objectsIn(connId: string, database: string, schema: string | undefined): Promise<Node[]> {
  const none: ObjectInfo[] = [];
  const list = async (k: ObjectKind): Promise<ObjectInfo[]> => arr(await Explorer.ListObjects(connId, database, schema ?? "", k).catch(() => none));
  const [tables, views, matviews, seqs, funcs, procs] = await Promise.all([
    list(ObjectKind.KindTable),
    list(ObjectKind.KindView),
    schema ? list(ObjectKind.KindMatView) : none,
    schema ? list(ObjectKind.KindSequence) : none,
    list(ObjectKind.KindFunction),
    schema ? none : list(ObjectKind.KindProcedure),
  ]);
  const out: Node[] = [];
  const pre = `${database}/${schema ?? ""}/`;
  for (const [objs, kind] of [[tables, ObjectKind.KindTable], [views, ObjectKind.KindView], [matviews, ObjectKind.KindMatView]] as const) {
    for (const o of objs) {
      const t: Target = { level: "table", database, schema, object: o.name };
      out.push({ id: pre + o.name, label: o.name, icon: kind, target: t, load: columns(connId, t, kind) });
    }
  }
  for (const o of seqs) out.push({ id: pre + "seq:" + o.name, label: o.name, icon: ObjectKind.KindSequence, target: { level: "sequence", database, schema, object: o.name } });
  for (const o of funcs) {
    // PostgreSQL grants name functions with their argument types; MySQL by name and kind.
    const object = schema ? `${o.name}(${o.signature ?? ""})` : o.name;
    out.push({ id: pre + "fn:" + object, label: schema ? object : o.name, icon: ObjectKind.KindFunction, target: { level: "function", database, schema, object, routineType: schema ? undefined : "FUNCTION" } });
  }
  for (const o of procs) out.push({ id: pre + "proc:" + o.name, label: o.name, icon: ObjectKind.KindProcedure, target: { level: "function", database, object: o.name, routineType: "PROCEDURE" } });
  return out;
}

export function PrivilegesTab(p: {
  connId: string;
  pg: boolean;
  user: P.User;
  meta: P.Meta;
  /** PostgreSQL: the database whose objects are shown. */
  database: string;
  databases: string[];
  onDatabase(db: string): void;
  update(fn: (u: P.User) => P.User): void;
}) {
  const { connId, pg, user, meta } = p;
  const grants = arr(user.privileges);
  const [kids, setKids] = useState<Record<string, Node[]>>({});
  const [open, setOpen] = useState<Set<string>>(new Set());
  const [selected, setSelected] = useState<Node | null>(null);

  // Roots: MySQL databases; PostgreSQL the chosen database and its schemas.
  const [roots, setRoots] = useState<Node[]>([]);
  useEffect(() => {
    setKids({});
    setOpen(new Set());
    setSelected(null);
    (async () => {
      if (pg) {
        const db = p.database;
        const schemas = arr(await Explorer.ListSchemas(connId, db).catch(() => [] as string[])).filter((s) => s !== "information_schema" && !s.startsWith("pg_"));
        setRoots([
          { id: "db:" + db, label: db, icon: ObjectKind.KindDatabase, target: { level: "database", database: db } },
          ...schemas.map((s) => ({ id: `${db}/${s}`, label: s, icon: ObjectKind.KindSchema, target: { level: "schema", database: db, schema: s }, load: () => objectsIn(connId, db, s) })),
        ]);
      } else {
        const dbs = arr(await Explorer.ListDatabases(connId).catch(() => [] as string[]));
        setRoots(dbs.map((d) => ({ id: "db:" + d, label: d, icon: ObjectKind.KindDatabase, target: { level: "database", database: d }, load: () => objectsIn(connId, d, undefined) })));
      }
    })();
  }, [connId, pg, p.database]);

  const toggle = async (n: Node) => {
    if (!n.load) return;
    const next = new Set(open);
    if (next.has(n.id)) next.delete(n.id);
    else {
      next.add(n.id);
      if (!kids[n.id]) {
        const c = await n.load().catch(() => []);
        setKids((k) => ({ ...k, [n.id]: c }));
      }
    }
    setOpen(next);
  };

  const rows = useMemo(() => {
    const out: Array<{ n: Node; depth: number }> = [];
    const walk = (ns: Node[], depth: number) => {
      for (const n of ns) {
        out.push({ n, depth });
        if (open.has(n.id) && kids[n.id]) walk(kids[n.id], depth + 1);
      }
    };
    walk(roots, 0);
    return out;
  }, [roots, kids, open]);

  const level = selected?.target.level;
  const available = level ? arr(meta.levels?.[level]) : [];
  const onTarget = selected ? grants.filter((g) => same(g, selected.target)) : [];
  const has = (priv: string) => onTarget.find((g) => g.privilege === priv);

  const setPriv = (priv: string, on: boolean, grantable?: boolean) =>
    p.update((u) => {
      const rest = arr(u.privileges).filter((g) => !(same(g, selected!.target) && g.privilege === priv));
      // MySQL grant options cover every privilege on the object.
      const objectGrantable = !pg && onTarget.some((g) => g.grantable);
      return on ? { ...u, privileges: [...rest, { ...selected!.target, privilege: priv, grantable: grantable ?? objectGrantable }] } : { ...u, privileges: rest };
    });

  const setObjectGrantable = (on: boolean) => p.update((u) => ({ ...u, privileges: arr(u.privileges).map((g) => (same(g, selected!.target) ? { ...g, grantable: on } : g)) }));

  return (
    <Split
      id="user-privileges"
      className="user-privileges"
      firstSize={320}
      minFirst={200}
      maxFirst="70"
      first={
        <div className="privs-tree">
          {pg && (
            <div className="privs-db">
              <span className="muted small">Database</span>
              <select value={p.database} onChange={(e) => p.onDatabase(e.target.value)}>
                {p.databases.map((d) => (
                  <option key={d}>{d}</option>
                ))}
              </select>
            </div>
          )}
          <div className="privs-tree-rows">
            {rows.map(({ n, depth }) => {
              const marked = grants.some((g) => within(g, n.target));
              return (
                <div
                  key={n.id}
                  className={"privs-row" + (selected?.id === n.id ? " selected" : "") + (marked ? " granted" : "")}
                  style={{ paddingLeft: 6 + depth * 14 }}
                  onClick={() => setSelected(n)}
                  onDoubleClick={() => toggle(n)}
                >
                  <span className="twisty" onClick={(e) => (e.stopPropagation(), toggle(n))}>
                    {n.load ? open.has(n.id) ? <ChevronDown size={12} /> : <ChevronRight size={12} /> : null}
                  </span>
                  <KindIcon kind={n.icon} size={13} />
                  <span className="privs-label">{n.label}</span>
                  {marked && <span className="privs-dot" title="Has privileges" />}
                </div>
              );
            })}
          </div>
        </div>
      }
      second={
        !selected ? (
          <div className="center-hint">Select a database, table, column or routine to set its privileges.</div>
        ) : (
          <div className="privs-list">
            <div className="privs-head">
              <KindIcon kind={selected.icon} size={14} /> <b>{selected.label}</b>
              <span className="muted small">{level}</span>
              <span className="spacer" />
              {!pg && (
                <label className="check" title="Allow granting these privileges to others (WITH GRANT OPTION)">
                  <input type="checkbox" disabled={!onTarget.length} checked={onTarget.some((g) => g.grantable)} onChange={(e) => setObjectGrantable(e.target.checked)} /> Grant Option
                </label>
              )}
              <button className="small-btn" onClick={() => available.forEach((pr) => !has(pr) && setPriv(pr, true))}>
                All
              </button>
              <button className="small-btn" disabled={!onTarget.length} onClick={() => onTarget.forEach((g) => setPriv(g.privilege, false))}>
                None
              </button>
            </div>
            <table className="redis-table privs-table">
              <thead>
                <tr>
                  <th>Privilege</th>
                  <th>Granted</th>
                  {pg && <th>With Grant Option</th>}
                </tr>
              </thead>
              <tbody>
                {available.map((pr) => {
                  const g = has(pr);
                  return (
                    <tr key={pr}>
                      <td>{pr}</td>
                      <td>
                        <input type="checkbox" checked={!!g} onChange={(e) => setPriv(pr, e.target.checked)} />
                      </td>
                      {pg && (
                        <td>
                          <input type="checkbox" checked={!!g?.grantable} onChange={(e) => setPriv(pr, true, e.target.checked)} />
                        </td>
                      )}
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )
      }
    />
  );
}
