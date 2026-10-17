// User (MySQL) / role (PostgreSQL) editor: General, Advanced, Member Of, Server
// Privileges (MySQL), Privileges and SQL Preview. Saving runs the minimal
// CREATE/ALTER/RENAME and GRANT/REVOKE statements.
import { Save } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Explorer, P, Users, arr, errorText } from "../../api";
import { Tab, connById, useApp } from "../../store";
import { Combobox } from "../Combobox";
import { CodeBlock } from "../ObjectsPane";
import { accountLabel, isPGConn } from "./actions";
import { PrivilegesTab } from "./PrivilegesTab";
import { Toolbar } from "../Toolbar";
import { useAsync } from "../useAsync";
import { useSavableTab } from "../useSavableTab";
import { Field } from "../form";

type Section = "general" | "advanced" | "members" | "server" | "privileges" | "sql";

function blank(pg: boolean): P.User {
  return pg
    ? { name: "", canLogin: true, inherit: true, connLimit: -1, memberOf: [], privileges: [] }
    : { name: "", host: "%", connLimit: -1, memberOf: [], privileges: [] };
}

export function UserDesigner({ tab }: { tab: Tab }) {
  const connId = tab.connId!;
  const pg = isPGConn(connId);
  const isNew = !tab.ref?.name;
  const noun = pg ? "role" : "user";
  const [original, setOriginal] = useState<P.User | null>(null);
  const [draft, setDraft] = useState<P.User | null>(null);
  const [meta, setMeta] = useState<P.Meta | null>(null);
  const [section, setSection] = useState<Section>("general");
  const [password, setPassword] = useState("");
  const [confirmPw, setConfirmPw] = useState("");
  const [error, setError] = useState<{ text: string; sql?: string } | null>(null);
  const [saving, setSaving] = useState(false);
  const [databases, setDatabases] = useState<string[]>([]);
  const [pgDb, setPgDb] = useState(() => connById(connId)?.initialDatabase || "postgres");
  const [loadedDbs, setLoadedDbs] = useState<Set<string>>(new Set());

  useEffect(() => {
    (async () => {
      try {
        setMeta(await Users.Meta(connId));
        if (pg) Explorer.ListDatabases(connId).then((d) => setDatabases(arr(d))).catch(() => {});
        if (isNew) {
          setDraft(blank(pg));
        } else {
          const u = await Users.Load(connId, tab.ref!.name, tab.ref!.schema ?? "", pg ? pgDb : "");
          setOriginal(u);
          setDraft(u);
          setLoadedDbs(new Set([pgDb]));
        }
      } catch (e) {
        setError({ text: errorText(e) });
      }
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connId]);

  // PostgreSQL object privileges live per database: load another database's on demand.
  const switchDb = async (db: string) => {
    setPgDb(db);
    if (!original || loadedDbs.has(db)) return;
    try {
      const u = await Users.Load(connId, original.origName ?? original.name, "", db);
      const mine = (g: P.Grant) => g.level !== "database" && g.database === db;
      const theirs = arr(u.privileges).filter(mine);
      setOriginal((o) => (o ? { ...o, privileges: [...arr(o.privileges).filter((g) => !mine(g)), ...theirs] } : o));
      setDraft((d) => (d ? { ...d, privileges: [...arr(d.privileges).filter((g) => !mine(g)), ...theirs] } : d));
      setLoadedDbs((s) => new Set([...s, db]));
    } catch (e) {
      setError({ text: errorText(e) });
    }
  };

  // What will be saved: the draft plus a password when one was typed.
  const toSave = useMemo<P.User | null>(() => (draft ? { ...draft, password: password ? password : undefined } : null), [draft, password]);

  // An answer for an older draft is dropped.
  const { data: preview } = useAsync<{ stmts: P.Stmt[]; error?: string }>(
    toSave &&
      (async () => {
        if (!toSave.name.trim()) return { stmts: [], error: `Enter a ${noun} name to see the SQL.` };
        return Users.Preview(connId, original, toSave).then(
          (s) => ({ stmts: arr(s) }),
          (e) => ({ stmts: [], error: errorText(e) }),
        );
      }),
    [toSave, original, connId, noun],
    { stmts: [] },
    { delay: 150, keep: true },
  );

  const dirty = preview.stmts.length > 0;
  useEffect(() => {
    const base = draft?.name ? accountLabel(draft) : "Untitled";
    useApp.getState().updateTab(tab.id, { title: `${base} (${noun})` });
  }, [draft?.name, draft?.host, tab.id, noun]);

  const update = useCallback((fn: (u: P.User) => P.User) => setDraft((d) => (d ? fn(d) : d)), []);
  const set = (patch: Partial<P.User>) => update((u) => ({ ...u, ...patch }));

  const save = async (): Promise<boolean> => {
    if (!toSave) return false;
    if (!toSave.name.trim()) return (setSection("general"), setError({ text: `Enter a ${noun} name.` }), false);
    if (password !== confirmPw) return (setSection("general"), setError({ text: "The passwords don't match." }), false);
    if (!dirty) return false;
    setSaving(true);
    setError(null);
    try {
      const res = await Users.Apply(connId, original, toSave);
      if (res.error) return (setError({ text: res.error + (res.executed ? `\n(${res.executed} statement(s) before it were applied)` : ""), sql: res.failedStatement }), false);
      const u = await Users.Load(connId, toSave.name, toSave.host ?? "", pg ? pgDb : "");
      setOriginal(u);
      setDraft(u);
      setLoadedDbs(new Set([pgDb]));
      setPassword("");
      setConfirmPw("");
      useApp.getState().updateTab(tab.id, { ref: { ...tab.ref!, name: u.name, schema: u.host, signature: "user" } });
      useApp.setState((s) => ({ objectsVersion: s.objectsVersion + 1 }));
      return true;
    } catch (e) {
      setError({ text: errorText(e) });
      return false;
    } finally {
      setSaving(false);
    }
  };
  useSavableTab(tab.id, dirty, save);

  if (!draft || !meta) return <div className="center-hint">{error ? <span className="err">{error.text}</span> : "Loading…"}</div>;

  const sections: Array<[Section, string, boolean]> = [
    ["general", "General", true],
    ["advanced", "Advanced", !pg],
    ["members", "Member Of", true],
    ["server", "Server Privileges", !pg],
    ["privileges", "Privileges", true],
    ["sql", `SQL Preview${preview.stmts.length ? ` (${preview.stmts.length})` : ""}`, true],
  ];
  const self = accountLabel(draft);
  const roles = arr(meta.roles).filter((r) => r !== self && r !== draft.name);
  const grants = arr(draft.privileges);
  const globalHas = (p: string) => grants.find((g) => g.level === "global" && g.privilege === p);
  const setGlobal = (p: string, on: boolean) =>
    update((u) => {
      const rest = arr(u.privileges).filter((g) => !(g.level === "global" && g.privilege === p));
      const grantable = arr(u.privileges).some((g) => g.level === "global" && g.grantable);
      return on ? { ...u, privileges: [...rest, { level: "global", privilege: p, grantable }] } : { ...u, privileges: rest };
    });

  const field = (label: string, el: React.ReactNode) => (
    <Field label={label}>{el}</Field>
  );
  const check = (label: string, value: boolean | undefined, on: (v: boolean) => void, hint?: string) => (
    <Field>
      <label className="check" title={hint}>
        <input type="checkbox" checked={!!value} onChange={(e) => on(e.target.checked)} /> {label}
      </label>
    </Field>
  );
  const num = (label: string, value: number | undefined, on: (v: number) => void, placeholder = "0 (unlimited)") =>
    field(label, <input type="number" min={0} placeholder={placeholder} value={value || ""} onChange={(e) => on(Number(e.target.value) || 0)} />);

  return (
    <div
      className="designer user-designer"
      onKeyDown={(e) => {
        if ((e.metaKey || e.ctrlKey) && e.key === "s") {
          e.preventDefault();
          save();
        }
      }}
    >
      <Toolbar>
        <button className={dirty ? "accent" : ""} disabled={saving || !dirty} onClick={save} title="Save (⌘S)">
          <Save size={14} /> {saving ? "Saving…" : "Save"}
        </button>
        <span className="spacer" />
        <span className="crumbs">
          {connById(connId)?.name} › {draft.name ? self : `new ${noun}`}
        </span>
      </Toolbar>
      <div className="seg-tabs designer-tabs">
        {sections
          .filter(([, , on]) => on)
          .map(([k, label]) => (
            <button key={k} className={section === k ? "active" : ""} onClick={() => setSection(k)}>
              {label}
            </button>
          ))}
      </div>
      {error && (
        <div className="error-banner" onClick={() => setError(null)}>
          {error.text}
          {error.sql && <pre>{error.sql}</pre>}
        </div>
      )}
      <div className="designer-body">
        {section === "general" && (
          <div className="form designer-form">
            {field(pg ? "Role Name" : "User Name", <input autoFocus={isNew} value={draft.name} onChange={(e) => set({ name: e.target.value })} />)}
            {!pg && field("Host", <input value={draft.host ?? ""} placeholder="% (any host)" onChange={(e) => set({ host: e.target.value })} />)}
            {!pg && field("Plugin", <Combobox value={draft.plugin ?? ""} options={arr(meta.plugins)} placeholder="(server default)" onChange={(v) => set({ plugin: v || undefined })} />)}
            {pg && check("Can log in", draft.canLogin, (v) => set({ canLogin: v }), "LOGIN: the role can be used to connect")}
            {field("Password", <input type="password" value={password} placeholder={isNew ? "" : "(unchanged)"} onChange={(e) => setPassword(e.target.value)} autoComplete="new-password" />)}
            {field("Confirm Password", <input type="password" value={confirmPw} placeholder={isNew ? "" : "(unchanged)"} onChange={(e) => setConfirmPw(e.target.value)} autoComplete="new-password" />)}
            {password !== confirmPw && confirmPw && <div className="muted small err">The passwords don't match.</div>}
            {!pg && check("Expire password (must change it at next login)", draft.passwordExpired, (v) => set({ passwordExpired: v }))}
            {pg && (
              <>
                {field("Valid Until", <input type="datetime-local" value={(draft.validUntil ?? "").slice(0, 16).replace(" ", "T")} onChange={(e) => set({ validUntil: e.target.value ? e.target.value.replace("T", " ") : undefined })} />)}
                {field("Connection Limit", <input type="number" min={-1} placeholder="-1 (no limit)" value={draft.connLimit < 0 ? "" : draft.connLimit} onChange={(e) => set({ connLimit: e.target.value === "" ? -1 : Number(e.target.value) })} />)}
                {check("Superuser", draft.superuser, (v) => set({ superuser: v }))}
                {check("Can create databases", draft.createDb, (v) => set({ createDb: v }))}
                {check("Can create roles", draft.createRole, (v) => set({ createRole: v }))}
                {check("Inherit privileges of member roles", draft.inherit, (v) => set({ inherit: v }))}
                {check("Can initiate replication", draft.replication, (v) => set({ replication: v }))}
                {check("Bypass row level security", draft.bypassRls, (v) => set({ bypassRls: v }))}
                {field("Comment", <textarea rows={3} value={draft.comment ?? ""} onChange={(e) => set({ comment: e.target.value || undefined })} />)}
              </>
            )}
          </div>
        )}
        {section === "advanced" && !pg && (
          <div className="form designer-form">
            {num("Max queries per hour", draft.maxQueries, (v) => set({ maxQueries: v }))}
            {num("Max updates per hour", draft.maxUpdates, (v) => set({ maxUpdates: v }))}
            {num("Max connections per hour", draft.maxConnections, (v) => set({ maxConnections: v }))}
            {num("Max user connections", draft.maxUserConnections, (v) => set({ maxUserConnections: v }))}
            {field(
              "Require SSL",
              <select value={draft.sslType ?? ""} onChange={(e) => set({ sslType: e.target.value || undefined })}>
                <option value="">No</option>
                <option value="SSL">SSL</option>
                <option value="X509">X509 (client certificate)</option>
              </select>,
            )}
            {check("Account locked", draft.locked, (v) => set({ locked: v }))}
          </div>
        )}
        {section === "members" && (
          <div className="list-tab">
            <div className="muted small" style={{ padding: "8px 12px" }}>
              Roles this {noun} is a member of{pg ? " (inherits their privileges when Inherit is on)" : ""}.
            </div>
            <table className="redis-table">
              <tbody>
                {roles.map((r) => (
                  <tr key={r}>
                    <td style={{ width: 28 }}>
                      <input
                        type="checkbox"
                        checked={arr(draft.memberOf).includes(r)}
                        onChange={(e) => set({ memberOf: e.target.checked ? [...arr(draft.memberOf), r] : arr(draft.memberOf).filter((x) => x !== r) })}
                      />
                    </td>
                    <td>{r}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
        {section === "server" && !pg && (
          <div className="list-tab">
            <Toolbar>
              <label className="check" title="WITH GRANT OPTION on *.*">
                <input
                  type="checkbox"
                  checked={grants.some((g) => g.level === "global" && g.grantable)}
                  disabled={!grants.some((g) => g.level === "global")}
                  onChange={(e) => update((u) => ({ ...u, privileges: arr(u.privileges).map((g) => (g.level === "global" ? { ...g, grantable: e.target.checked } : g)) }))}
                />{" "}
                Grant Option
              </label>
              <span className="spacer" />
              <button className="small-btn" onClick={() => arr(meta.levels?.global).forEach((p) => !globalHas(p) && setGlobal(p, true))}>
                All
              </button>
              <button className="small-btn" onClick={() => arr(meta.levels?.global).forEach((p) => globalHas(p) && setGlobal(p, false))}>
                None
              </button>
            </Toolbar>
            <div className="server-privs">
              {arr(meta.levels?.global).map((p) => (
                <label key={p} className="check">
                  <input type="checkbox" checked={!!globalHas(p)} onChange={(e) => setGlobal(p, e.target.checked)} /> {p}
                </label>
              ))}
            </div>
          </div>
        )}
        {section === "privileges" && (
          <PrivilegesTab connId={connId} pg={pg} user={draft} meta={meta} database={pgDb} databases={databases} onDatabase={switchDb} update={update} />
        )}
        {section === "sql" && (
          <div className="designer-sql">
            {preview.error && <div className="badge warn block">{preview.error}</div>}
            {preview.stmts.length ? (
              <CodeBlock code={preview.stmts.map((s) => (s.database ? `-- in ${s.database}\n` : "") + s.sql.replace(/(PASSWORD|BY) '([^']|'')*'/g, "$1 '********'")).join(";\n\n") + ";"} />
            ) : (
              !preview.error && <div className="center-hint">No changes.</div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
