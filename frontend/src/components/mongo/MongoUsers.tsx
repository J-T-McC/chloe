// MongoDB users and roles: a list of either on the left, the selected one's editor
// on the right, with a preview of the commands saving runs.
import { Minus, Plus, RefreshCw, Save, Trash2, UserPlus } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Mongo, arr, errorText, type MG } from "../../api";
import type { Tab } from "../../store";
import { confirm } from "../Dialogs";
import { CodeBlock } from "../ObjectsPane";
import { Split } from "../Split";
import { Toolbar } from "../Toolbar";
import { Field, Check } from "../form";

type Section = "users" | "roles";

const blankUser = (db: string): MG.User => ({ user: "", db, roles: [], mechanisms: [], customData: "" });
const blankRole = (db: string): MG.Role => ({ role: "", db, builtin: false, roles: [], privileges: [] });
const MECHANISMS = ["SCRAM-SHA-1", "SCRAM-SHA-256"];

export function MongoUsers({ tab }: { tab: Tab }) {
  const connId = tab.connId!;
  const [section, setSection] = useState<Section>("users");
  const [users, setUsers] = useState<MG.User[]>([]);
  const [roles, setRoles] = useState<MG.Role[]>([]);
  const [roleDb, setRoleDb] = useState(tab.database || "admin");
  const [showBuiltin, setShowBuiltin] = useState(false);
  const [dbs, setDbs] = useState<string[]>([]);
  const [actions, setActions] = useState<string[]>([]);
  const [picked, setPicked] = useState<number>(-1);
  // The editor works on a copy; orig is what the server has (null for a new one).
  const [userDraft, setUserDraft] = useState<{ orig: MG.User | null; cur: MG.User; confirm: string } | null>(null);
  const [roleDraft, setRoleDraft] = useState<{ orig: MG.Role | null; cur: MG.Role } | null>(null);
  const [script, setScript] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  const loadUsers = () =>
    Mongo.Users(connId)
      .then((u) => {
        setUsers(arr(u));
        return arr(u);
      })
      .catch((e) => (setError(errorText(e)), [] as MG.User[]));
  const loadRoles = () => Mongo.Roles(connId, roleDb, showBuiltin).then((r) => setRoles(arr(r))).catch((e) => setError(errorText(e)));
  useEffect(() => {
    loadUsers();
    Mongo.Databases(connId).then((d) => setDbs(arr(d).map((x) => x.name))).catch(() => {});
    Mongo.Actions().then((a) => setActions(arr(a)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connId]);
  useEffect(() => {
    loadRoles();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connId, roleDb, showBuiltin]);

  // Roles a user can be granted: the built-in ones plus the database's own.
  const [grantable, setGrantable] = useState<string[]>([]);
  useEffect(() => {
    Mongo.Roles(connId, "admin", true)
      .then((r) => setGrantable([...new Set(arr(r).map((x) => x.role))].sort()))
      .catch(() => {});
  }, [connId]);

  // Script preview follows the draft.
  useEffect(() => {
    const p = userDraft
      ? Mongo.UserScript(userDraft.orig, userDraft.cur)
      : roleDraft
        ? Mongo.RoleScript(roleDraft.orig, roleDraft.cur)
        : null;
    if (!p) return setScript("");
    p.then((c) => setScript(arr(c).map((x) => x.shell).join(";\n") || "// No changes")).catch((e) => setScript("// " + errorText(e)));
  }, [userDraft, roleDraft]);

  const pick = (i: number) => {
    setPicked(i);
    setError("");
    if (section === "users") {
      const u = users[i];
      setUserDraft(u ? { orig: u, cur: { ...u, roles: [...arr(u.roles)], password: "" }, confirm: "" } : null);
      setRoleDraft(null);
    } else {
      const r = roles[i];
      setRoleDraft(r ? { orig: r, cur: { ...r, roles: [...arr(r.roles)], privileges: arr(r.privileges).map((p) => ({ ...p, actions: [...arr(p.actions)] })) } } : null);
      setUserDraft(null);
    }
  };

  const save = async () => {
    setError("");
    if (userDraft && (userDraft.cur.password ?? "") !== userDraft.confirm) return setError("The passwords don't match.");
    setBusy(true);
    try {
      if (userDraft) {
        await Mongo.SaveUser(connId, userDraft.orig, { ...userDraft.cur, roles: arr(userDraft.cur.roles).filter((r) => r.role.trim()) });
        // Show what the server now has (it fills in mechanisms, drops empty rows).
        const list = await loadUsers();
        const i = list.findIndex((u) => u.user === userDraft.cur.user && u.db === userDraft.cur.db);
        const saved = list[i] ?? { ...userDraft.cur, password: "" };
        setPicked(i);
        setUserDraft({ orig: saved, cur: { ...saved, roles: [...arr(saved.roles)], password: "" }, confirm: "" });
      } else if (roleDraft) {
        await Mongo.SaveRole(connId, roleDraft.orig, roleDraft.cur);
        await loadRoles();
        setRoleDraft({ orig: roleDraft.cur, cur: roleDraft.cur });
      }
    } catch (e) {
      setError(errorText(e));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    try {
      if (userDraft?.orig) {
        const u = userDraft.orig;
        if (!(await confirm(`Delete user “${u.user}” from ${u.db}?`, { okLabel: "Delete" }))) return;
        await Mongo.DropUser(connId, u.db, u.user);
        setUserDraft(null);
        await loadUsers();
      } else if (roleDraft?.orig && !roleDraft.orig.builtin) {
        const r = roleDraft.orig;
        if (!(await confirm(`Delete role “${r.role}” from ${r.db}?`, { message: "Users granted it lose its privileges.", okLabel: "Delete" }))) return;
        await Mongo.DropRole(connId, r.db, r.role);
        setRoleDraft(null);
        await loadRoles();
      }
      setPicked(-1);
    } catch (e) {
      setError(errorText(e));
    }
  };

  const readOnly = !!roleDraft?.orig?.builtin;
  const list: (MG.User | MG.Role)[] = section === "users" ? users : roles;

  return (
    <div className="objects-pane mongo-users">
      <Toolbar>
        <div className="seg-tabs inline">
          {(["users", "roles"] as const).map((s) => (
            <button key={s} className={section === s ? "active" : ""} onClick={() => (setSection(s), setPicked(-1), setUserDraft(null), setRoleDraft(null))}>
              {s === "users" ? "Users" : "Roles"}
            </button>
          ))}
        </div>
        <span className="tb-sep" />
        <button
          onClick={() => {
            setPicked(-1);
            if (section === "users") setUserDraft({ orig: null, cur: blankUser(tab.database || "admin"), confirm: "" });
            else setRoleDraft({ orig: null, cur: blankRole(roleDb) });
          }}
        >
          {section === "users" ? <UserPlus size={14} /> : <Plus size={14} />} {section === "users" ? "New User" : "New Role"}
        </button>
        <button disabled={!(userDraft?.orig || (roleDraft?.orig && !readOnly))} onClick={remove}>
          <Trash2 size={14} /> Delete {section === "users" ? "User" : "Role"}
        </button>
        <button title="Refresh" onClick={() => (section === "users" ? loadUsers() : loadRoles())}>
          <RefreshCw size={14} />
        </button>
        {section === "roles" && (
          <>
            <span className="tb-sep" />
            <label className="inline-field">
              Database
              <select value={roleDb} onChange={(e) => setRoleDb(e.target.value)}>
                {[...new Set([roleDb, ...dbs])].map((d) => (
                  <option key={d}>{d}</option>
                ))}
              </select>
            </label>
            <Check label="Built-in roles" checked={showBuiltin} onChange={(v) => setShowBuiltin(v)} />
          </>
        )}
      </Toolbar>
      {error && (
        <div className="error-banner" onClick={() => setError("")}>
          {error}
        </div>
      )}
      <Split
        id="mongo-users"
        className="objects-split"
        firstSize={360}
        minFirst={220}
        minSecond={320}
        first={
          <div className="object-list">
            <table>
              <thead>
                <tr>
                  <th>{section === "users" ? "User" : "Role"}</th>
                  <th>Database</th>
                  <th>{section === "users" ? "Roles" : "Inherits"}</th>
                </tr>
              </thead>
              <tbody>
                {list.map((x, i) => {
                  const name = "user" in x ? x.user : x.role;
                  return (
                    <tr key={x.db + "." + name} className={picked === i ? "selected" : ""} onClick={() => pick(i)}>
                      <td>
                        {name}
                        {"builtin" in x && x.builtin ? <span className="muted"> (built-in)</span> : null}
                      </td>
                      <td>{x.db}</td>
                      <td className="muted">{arr<MG.RoleRef>(x.roles).map((r) => (r.db === x.db ? r.role : `${r.role}@${r.db}`)).join(", ")}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            {!list.length && <div className="center-hint">No {section === "users" ? "users" : "roles"}.</div>}
          </div>
        }
        second={
          userDraft ? (
            <UserEditor draft={userDraft} setDraft={setUserDraft} dbs={dbs} grantable={grantable} script={script} busy={busy} onSave={save} />
          ) : roleDraft ? (
            <RoleEditor draft={roleDraft} setDraft={setRoleDraft} dbs={dbs} grantable={grantable} actions={actions} script={script} busy={busy} readOnly={readOnly} onSave={save} />
          ) : (
            <div className="info-panel empty">Select a {section === "users" ? "user" : "role"}, or create one.</div>
          )
        }
      />
    </div>
  );
}

function RolesGrid({ roles, onChange, dbs, grantable, readOnly, defaultDb }: { roles: MG.RoleRef[]; onChange(r: MG.RoleRef[]): void; dbs: string[]; grantable: string[]; readOnly?: boolean; defaultDb: string }) {
  const set = (i: number, patch: Partial<MG.RoleRef>) => onChange(roles.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  return (
    <div className="fields-grid compact">
      <table>
        <thead>
          <tr>
            <th>Role</th>
            <th>Database</th>
            <th className="narrow-col" />
          </tr>
        </thead>
        <tbody>
          {roles.map((r, i) => (
            <tr key={i}>
              <td>
                <input list="mongo-grantable" value={r.role} disabled={readOnly} onChange={(e) => set(i, { role: e.target.value })} />
              </td>
              <td>
                <input list="mongo-dbs" value={r.db} disabled={readOnly} onChange={(e) => set(i, { db: e.target.value })} />
              </td>
              <td className="narrow-col">
                {!readOnly && (
                  <button className="icon" title="Remove" onClick={() => onChange(roles.filter((_, j) => j !== i))}>
                    <Minus size={12} />
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {!readOnly && (
        <button className="small-btn" onClick={() => onChange([...roles, { role: "", db: defaultDb || "admin" }])}>
          <Plus size={12} /> Add Role
        </button>
      )}
      <datalist id="mongo-grantable">
        {grantable.map((g) => (
          <option key={g} value={g} />
        ))}
      </datalist>
      <datalist id="mongo-dbs">
        {dbs.map((d) => (
          <option key={d} value={d} />
        ))}
      </datalist>
    </div>
  );
}

function UserEditor(p: {
  draft: { orig: MG.User | null; cur: MG.User; confirm: string };
  setDraft(d: { orig: MG.User | null; cur: MG.User; confirm: string }): void;
  dbs: string[];
  grantable: string[];
  script: string;
  busy: boolean;
  onSave(): void;
}) {
  const { orig, cur } = p.draft;
  const set = (patch: Partial<MG.User>) => p.setDraft({ ...p.draft, cur: { ...cur, ...patch } });
  const mechs = arr(cur.mechanisms);
  return (
    <div className="info-panel user-editor">
      <div className="form">
        <Field label="User Name">
          <input value={cur.user} disabled={!!orig} onChange={(e) => set({ user: e.target.value })} />
        </Field>
        <Field label="Database">
          <input list="mongo-dbs" value={cur.db} disabled={!!orig} onChange={(e) => set({ db: e.target.value })} />
        </Field>
        <Field label={orig ? "New Password" : "Password"}>
          <input type="password" value={cur.password ?? ""} placeholder={orig ? "(unchanged)" : ""} onChange={(e) => set({ password: e.target.value })} />
        </Field>
        <Field label="Confirm Password">
          <input type="password" value={p.draft.confirm} onChange={(e) => p.setDraft({ ...p.draft, confirm: e.target.value })} />
        </Field>
        <div className="field">
          <label>Mechanisms</label>
          <div className="field-input row">
            {MECHANISMS.map((m) => (
              <label key={m} className="check">
                <input type="checkbox" checked={mechs.includes(m)} onChange={(e) => set({ mechanisms: e.target.checked ? [...mechs, m] : mechs.filter((x) => x !== m) })} /> {m}
              </label>
            ))}
          </div>
        </div>
        <div className="field top">
          <label>Roles</label>
          <div className="field-input">
            <RolesGrid roles={arr(cur.roles)} onChange={(roles) => set({ roles })} dbs={p.dbs} grantable={p.grantable} defaultDb={cur.db} />
          </div>
        </div>
        <div className="field top">
          <label>Custom Data</label>
          <div className="field-input">
            <textarea className="code" rows={3} spellCheck={false} placeholder="{ team: 'web' }" value={cur.customData} onChange={(e) => set({ customData: e.target.value })} />
          </div>
        </div>
      </div>
      <div className="info-ddl-head">Script Preview</div>
      <CodeBlock code={p.script} language="mongo" />
      <div className="editor-actions">
        <button className="primary" disabled={p.busy} onClick={p.onSave}>
          <Save size={14} /> {orig ? "Save" : "Create User"}
        </button>
      </div>
    </div>
  );
}

function RoleEditor(p: {
  draft: { orig: MG.Role | null; cur: MG.Role };
  setDraft(d: { orig: MG.Role | null; cur: MG.Role }): void;
  dbs: string[];
  grantable: string[];
  actions: string[];
  script: string;
  busy: boolean;
  readOnly: boolean;
  onSave(): void;
}) {
  const { orig, cur } = p.draft;
  const set = (patch: Partial<MG.Role>) => p.setDraft({ ...p.draft, cur: { ...cur, ...patch } });
  const privs = arr(cur.privileges);
  const setPriv = (i: number, patch: Partial<MG.Privilege>) => set({ privileges: privs.map((x, j) => (j === i ? { ...x, ...patch } : x)) });
  const actionList = useMemo(() => p.actions, [p.actions]);
  return (
    <div className="info-panel user-editor">
      <div className="form">
        <Field label="Role Name">
          <input value={cur.role} disabled={!!orig} onChange={(e) => set({ role: e.target.value })} />
        </Field>
        <Field label="Database">
          <input list="mongo-dbs" value={cur.db} disabled={!!orig} onChange={(e) => set({ db: e.target.value })} />
        </Field>
        <div className="field top">
          <label>Inherits</label>
          <div className="field-input">
            <RolesGrid roles={arr(cur.roles)} onChange={(roles) => set({ roles })} dbs={p.dbs} grantable={p.grantable} readOnly={p.readOnly} defaultDb={cur.db} />
          </div>
        </div>
        <div className="field top">
          <label>Privileges</label>
          <div className="field-input">
            <div className="fields-grid compact">
              <table>
                <thead>
                  <tr>
                    <th>Database</th>
                    <th>Collection</th>
                    <th>Cluster</th>
                    <th>Actions</th>
                    <th className="narrow-col" />
                  </tr>
                </thead>
                <tbody>
                  {privs.map((x, i) => (
                    <tr key={i}>
                      <td>
                        <input list="mongo-dbs" value={x.db} disabled={p.readOnly || x.cluster} placeholder="(any)" onChange={(e) => setPriv(i, { db: e.target.value })} />
                      </td>
                      <td>
                        <input value={x.collection} disabled={p.readOnly || x.cluster} placeholder="(any)" onChange={(e) => setPriv(i, { collection: e.target.value })} />
                      </td>
                      <td className="center">
                        <input type="checkbox" checked={x.cluster} disabled={p.readOnly} onChange={(e) => setPriv(i, { cluster: e.target.checked })} />
                      </td>
                      <td>
                        <input
                          list="mongo-actions"
                          value={arr(x.actions).join(", ")}
                          disabled={p.readOnly}
                          spellCheck={false}
                          placeholder="find, insert, update"
                          onChange={(e) => setPriv(i, { actions: e.target.value.split(",").map((a) => a.trimStart()) })}
                        />
                      </td>
                      <td className="narrow-col">
                        {!p.readOnly && (
                          <button className="icon" title="Remove" onClick={() => set({ privileges: privs.filter((_, j) => j !== i) })}>
                            <Minus size={12} />
                          </button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {!p.readOnly && (
                <button className="small-btn" onClick={() => set({ privileges: [...privs, { db: cur.db, collection: "", cluster: false, actions: [] }] })}>
                  <Plus size={12} /> Add Privilege
                </button>
              )}
              <datalist id="mongo-actions">
                {actionList.map((a) => (
                  <option key={a} value={a} />
                ))}
              </datalist>
            </div>
          </div>
        </div>
      </div>
      {!p.readOnly && (
        <>
          <div className="info-ddl-head">Script Preview</div>
          <CodeBlock code={p.script} language="mongo" />
          <div className="editor-actions">
            <button className="primary" disabled={p.busy} onClick={p.onSave}>
              <Save size={14} /> {orig ? "Save" : "Create Role"}
            </button>
          </div>
        </>
      )}
    </div>
  );
}
