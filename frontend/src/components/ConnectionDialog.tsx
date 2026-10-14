import { Clipboard, FolderOpen, Info, Link2, Plus, Search, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Connection, Connections, DBType, Preset, SSHAuth, Transfer, arr, errorText } from "../api";
import { useApp } from "../store";
import { prompt } from "./Dialogs";
import { DBIcon, PresetIcon } from "./icons";

const ENGINES: Array<{ type: DBType; label: string; ready: boolean }> = [
  { type: DBType.MySQL, label: "MySQL", ready: true },
  { type: DBType.MariaDB, label: "MariaDB", ready: true },
  { type: DBType.PostgreSQL, label: "PostgreSQL", ready: true },
  { type: DBType.MongoDB, label: "MongoDB", ready: true },
  { type: DBType.Redis, label: "Redis", ready: true },
  { type: DBType.SQLite, label: "SQLite", ready: true },
  { type: DBType.SQLServer, label: "SQL Server", ready: false },
  { type: DBType.Oracle, label: "Oracle", ready: false },
];

const engineLabel = (t: DBType) => ENGINES.find((e) => e.type === t)?.label ?? t;
const isMySQL = (t: DBType) => t === DBType.MySQL || t === DBType.MariaDB;

type Section = "general" | "advanced" | "databases" | "ssl" | "ssh" | "remarks";

const DEFAULT_PORTS: Partial<Record<DBType, number>> = { mysql: 3306, mariadb: 3306, postgres: 5432, redis: 6379, mongodb: 27017 } as never;

export function blankConnection(type: DBType, preset?: Preset): Connection {
  const d = preset?.defaults;
  return {
    id: "",
    name: "",
    type,
    preset: preset?.id ?? "",
    group: useApp.getState().selectedGroup ?? "",
    host: d?.host || (preset ? "" : "localhost"),
    port: d?.port || DEFAULT_PORTS[type] || 0,
    user: d?.user || (preset || type === DBType.Redis || type === DBType.MongoDB ? "" : type === DBType.PostgreSQL ? "postgres" : "root"),
    password: "",
    savePassword: true,
    initialDatabase: d?.initialDatabase ?? "",
    databases: { enabled: false, list: [] },
    advanced: { autoConnect: false, sessionLimit: 0, keepaliveSec: 240, connectTimeoutSec: 0, readTimeoutSec: 0, writeTimeoutSec: 0, compression: false, cleartextAuth: false },
    ssh: { enabled: false, host: "", port: 22, user: "", auth: SSHAuth.SSHPassword, savePassword: true },
    ssl: { enabled: false, verifyHost: false, ...(d?.ssl ?? {}) },
    sqlite: {},
    mongo: type === DBType.MongoDB ? { topology: "standalone", auth: "password" } : {},
  } as Connection;
}

export function ConnectionDialog() {
  const editing = useApp((s) => s.editing);
  const close = () => useApp.getState().editConnection(undefined);
  const [conn, setConn] = useState<Connection | null>(null);

  useEffect(() => {
    if (!editing || editing === "new") setConn(null);
    else if ("id" in editing && editing.id) Connections.Get(editing.id).then(setConn);
    else setConn(editing as Connection); // prefilled, unsaved (e.g. from a URL)
  }, [editing]);

  if (!editing) return null;
  if (!conn) return <TypePicker onPick={setConn} onCancel={close} />;
  return <ConnectionForm key={conn.id || "new"} initial={conn} onClose={close} />;
}

// ---- step 1: engine / preset / URL --------------------------------------------

function TypePicker({ onPick, onCancel }: { onPick(c: Connection): void; onCancel(): void }) {
  const [presets, setPresets] = useState<Preset[]>([]);
  const [filter, setFilter] = useState("");
  const [url, setUrl] = useState("");
  const [err, setErr] = useState("");

  useEffect(() => {
    Connections.Presets().then((p) => setPresets(arr(p)));
  }, []);

  const f = filter.toLowerCase();
  const engines = ENGINES.filter((e) => e.label.toLowerCase().includes(f));
  const hosted = presets.filter((p) => (p.name + p.description).toLowerCase().includes(f));

  const fromUrl = async () => {
    setErr("");
    try {
      const c = await Connections.ParseURL(url);
      onPick({ ...blankConnection(c.type, presets.find((p) => p.id === c.preset)), ...c, id: "", group: useApp.getState().selectedGroup ?? "" });
    } catch (e) {
      setErr(errorText(e));
    }
  };

  return (
    <div className="modal-backdrop" onMouseDown={onCancel}>
      <div className="modal picker" onMouseDown={(e) => e.stopPropagation()} onKeyDown={(e) => e.key === "Escape" && onCancel()}>
        <div className="modal-title">New Connection</div>
        <div className="modal-body">
          <div className="url-paste">
            <Link2 size={14} />
            <input
              placeholder="Paste a connection URL (postgresql://… or mysql://…)"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && url && fromUrl()}
            />
            <button disabled={!url} onClick={fromUrl}>
              Use URL
            </button>
          </div>
          {err && <div className="status-line err">{err}</div>}
          <div className="picker-search">
            <Search size={13} />
            <input autoFocus placeholder="Search connection types" value={filter} onChange={(e) => setFilter(e.target.value)} />
          </div>
          {!!engines.length && <div className="picker-heading">Databases</div>}
          <div className="type-grid">
            {engines.map((t) => (
              <button key={t.type} className="type-card" disabled={!t.ready} onClick={() => onPick(blankConnection(t.type))} title={t.ready ? "" : "Coming in a later phase"}>
                <DBIcon type={t.type} size={28} />
                <span>{t.label}</span>
              </button>
            ))}
          </div>
          {!!hosted.length && <div className="picker-heading">Hosted services</div>}
          <div className="type-grid">
            {hosted.map((p) => (
              <button key={p.id} className="type-card" onClick={() => onPick(blankConnection(p.type, p))} title={p.description}>
                <PresetIcon id={p.id} size={28} />
                <span>{p.name}</span>
                <small>{engineLabel(p.type)}</small>
              </button>
            ))}
          </div>
        </div>
        <div className="modal-footer">
          <span className="spacer" />
          <button onClick={onCancel}>Cancel</button>
        </div>
      </div>
    </div>
  );
}

// ---- step 2: the connection form --------------------------------------------------

function ConnectionForm({ initial, onClose }: { initial: Connection; onClose(): void }) {
  const [conn, setConn] = useState<Connection>(initial);
  const [section, setSection] = useState<Section>("general");
  const [status, setStatus] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);
  const [preset, setPreset] = useState<Preset | null>(null);
  const groups = useApp((s) => s.groups);
  const mysql = isMySQL(conn.type);
  const pg = conn.type === DBType.PostgreSQL;
  const redis = conn.type === DBType.Redis;
  const sqlite = conn.type === DBType.SQLite;
  const mongo = conn.type === DBType.MongoDB;

  useEffect(() => {
    if (conn.preset) Connections.Presets().then((p) => setPreset(arr(p).find((x) => x.id === conn.preset) ?? null));
  }, [conn.preset]);

  const set = (patch: Partial<Connection>) => setConn((c) => ({ ...c, ...patch }));
  const setAdv = (patch: Partial<Connection["advanced"]>) => setConn((c) => ({ ...c, advanced: { ...c.advanced, ...patch } }));
  const setSSH = (patch: Partial<Connection["ssh"]>) => setConn((c) => ({ ...c, ssh: { ...c.ssh, ...patch } }));
  const setSSL = (patch: Partial<Connection["ssl"]>) => setConn((c) => ({ ...c, ssl: { ...c.ssl, ...patch } }));
  const setDBs = (patch: Partial<Connection["databases"]>) => setConn((c) => ({ ...c, databases: { ...c.databases, ...patch } }));
  const setLite = (patch: Partial<Connection["sqlite"]>) => setConn((c) => ({ ...c, sqlite: { ...c.sqlite, ...patch } }));
  const setMongo = (patch: Partial<Connection["mongo"]>) => setConn((c) => ({ ...c, mongo: { ...c.mongo, ...patch } }));

  const named = (): Connection => ({ ...conn, name: conn.name.trim() || preset?.name || (sqlite ? fileBase(conn.sqlite?.path) : conn.host) || engineLabel(conn.type) });

  const test = async () => {
    setBusy(true);
    setStatus(null);
    try {
      const v = await Connections.Test(named());
      setStatus({ ok: true, text: `Connection successful — server ${v}` });
    } catch (e) {
      setStatus({ ok: false, text: errorText(e) });
    } finally {
      setBusy(false);
    }
  };

  const save = async () => {
    try {
      const saved = await Connections.Save(named());
      await useApp.getState().loadConnections();
      if (conn.id) useApp.getState().disconnect(conn.id);
      useApp.getState().select({ connId: saved.id });
      onClose();
    } catch (e) {
      setStatus({ ok: false, text: errorText(e) });
    }
  };

  const pasteURL = async () => {
    const url = await prompt("Fill from connection URL", { message: preset?.urlExample ? `e.g. ${preset.urlExample}` : "postgresql://user:password@host:5432/database" });
    if (!url) return;
    try {
      const c = await Connections.ParseURL(url);
      setConn((cur) => ({ ...cur, ...c, id: cur.id, name: cur.name || c.name, group: cur.group, preset: cur.preset || c.preset, ssh: cur.ssh, advanced: { ...cur.advanced, ...c.advanced } }));
      setStatus({ ok: true, text: "Filled from URL" });
    } catch (e) {
      setStatus({ ok: false, text: errorText(e) });
    }
  };

  const sections: Array<[Section, string]> = [
    ["general", "General"],
    ["advanced", "Advanced"],
    ...(redis ? [] : ([["databases", sqlite ? "Attached Databases" : "Databases"]] as Array<[Section, string]>)),
    ...(sqlite
      ? []
      : ([
          ["ssl", "SSL"],
          ["ssh", "SSH"],
        ] as Array<[Section, string]>)),
    ["remarks", "Remarks"],
  ];

  return (
    <div className="modal-backdrop" onMouseDown={onClose}>
      <div className="modal conn-dialog" onMouseDown={(e) => e.stopPropagation()} onKeyDown={(e) => e.key === "Escape" && onClose()}>
        <div className="modal-title">
          {preset ? <PresetIcon id={preset.id} size={18} /> : <DBIcon type={conn.type} size={18} />}
          {conn.id ? `Edit Connection — ${conn.name}` : `New ${preset?.name ?? engineLabel(conn.type)} Connection`}
        </div>
        <div className="seg-tabs">
          {sections.map(([k, label]) => (
            <button key={k} className={section === k ? "active" : ""} onClick={() => setSection(k)}>
              {label}
            </button>
          ))}
        </div>
        <div className="modal-body form">
          {section === "general" && (
            <>
              {preset && (
                <div className="preset-help">
                  <Info size={14} />
                  <span>{preset.help}</span>
                </div>
              )}
              <Field label="Connection Name">
                <input autoFocus value={conn.name} placeholder={preset?.name || (sqlite ? fileBase(conn.sqlite?.path) : conn.host) || engineLabel(conn.type)} onChange={(e) => set({ name: e.target.value })} />
              </Field>
              <Field label="Add To">
                <select
                  value={conn.group ?? ""}
                  onChange={async (e) => {
                    if (e.target.value === "\u0000new") {
                      const g = await prompt("New Group", { message: "Group name" });
                      if (g?.trim()) {
                        await Connections.AddGroup(g.trim()).catch(() => {});
                        await useApp.getState().loadConnections();
                        set({ group: g.trim() });
                      }
                    } else set({ group: e.target.value });
                  }}
                >
                  <option value="">My Connections</option>
                  {groups.map((g) => (
                    <option key={g} value={g}>
                      {g}
                    </option>
                  ))}
                  <option value={"\u0000new"}>New Group…</option>
                </select>
              </Field>
              <hr />
              {sqlite ? (
                <SQLiteFields conn={conn} setLite={setLite} />
              ) : mongo ? (
                <MongoFields conn={conn} set={set} setMongo={setMongo} pasteURL={pasteURL} />
              ) : (
                <>
              <Field label="Host">
                <div className="row">
                  <input value={conn.host} placeholder={preset ? hostPlaceholder(preset) : "localhost"} onChange={(e) => set({ host: e.target.value })} />
                  <button title="Fill from a connection URL" onClick={pasteURL}>
                    <Link2 size={13} /> URL
                  </button>
                </div>
              </Field>
              <Field label="Port">
                <input className="narrow" type="number" value={conn.port || ""} placeholder={String(DEFAULT_PORTS[conn.type] ?? "")} onChange={(e) => set({ port: Number(e.target.value) })} />
              </Field>
              {!redis && (
                <Field label="Initial Database">
                  <input value={conn.initialDatabase ?? ""} placeholder={pg ? "postgres" : "(none)"} onChange={(e) => set({ initialDatabase: e.target.value })} />
                </Field>
              )}
              <Field label="User Name">
                <input value={conn.user} placeholder={redis ? "(default user; Redis 6+ ACL users)" : ""} onChange={(e) => set({ user: e.target.value })} />
              </Field>
              <Field label="Password">
                <SecretInput value={conn.password ?? ""} placeholder={conn.savePassword ? "" : "Used for testing only; asked on connect"} onChange={(v) => set({ password: v })} />
              </Field>
              <Field label="">
                <Check label="Save password" checked={conn.savePassword} onChange={(v) => set({ savePassword: v })} />
              </Field>
                </>
              )}
              <Field label="Color">
                <div className="swatches">
                  {["", "#e5534b", "#e8963c", "#d6b63a", "#4fae5a", "#3b8eea", "#9a6bd8", "#8b8f98"].map((c) => (
                    <button key={c || "none"} className={"swatch" + ((conn.color ?? "") === c ? " on" : "")} style={{ background: c || "transparent" }} onClick={() => set({ color: c })} />
                  ))}
                </div>
              </Field>
            </>
          )}

          {section === "advanced" && (
            <>
              <Field label="">
                <Check label="Auto connect when Chloe starts" checked={conn.advanced.autoConnect} onChange={(v) => setAdv({ autoConnect: v })} />
              </Field>
              <ToggleNumber label="Limit connection sessions" value={conn.advanced.sessionLimit} def={10} onChange={(v) => setAdv({ sessionLimit: v })} />
              {!sqlite && <ToggleNumber label="Keepalive interval (sec)" value={conn.advanced.keepaliveSec} def={240} onChange={(v) => setAdv({ keepaliveSec: v })} />}
              <ToggleNumber
                label={sqlite ? "Busy timeout (sec)" : "Connection timeout (sec)"}
                value={conn.advanced.connectTimeoutSec}
                def={sqlite ? 5 : 15}
                onChange={(v) => setAdv({ connectTimeoutSec: v })}
              />
              {(redis || mongo) && (
                <>
                  <ToggleNumber label="Read timeout (sec)" value={conn.advanced.readTimeoutSec} def={30} onChange={(v) => setAdv({ readTimeoutSec: v })} />
                  <ToggleNumber label="Write timeout (sec)" value={conn.advanced.writeTimeoutSec} def={30} onChange={(v) => setAdv({ writeTimeoutSec: v })} />
                </>
              )}
              {mysql && (
                <>
                  <ToggleNumber label="Read timeout (sec)" value={conn.advanced.readTimeoutSec} def={30} onChange={(v) => setAdv({ readTimeoutSec: v })} />
                  <ToggleNumber label="Write timeout (sec)" value={conn.advanced.writeTimeoutSec} def={30} onChange={(v) => setAdv({ writeTimeoutSec: v })} />
                  <Field label="Client Character Set">
                    <select value={conn.advanced.encoding || "auto"} onChange={(e) => setAdv({ encoding: e.target.value === "auto" ? "" : e.target.value })}>
                      {["auto", "utf8mb4", "utf8mb3", "latin1", "ascii", "binary", "gbk", "big5", "sjis", "euckr"].map((x) => (
                        <option key={x}>{x}</option>
                      ))}
                    </select>
                  </Field>
                  <Field label="">
                    <Check label="Use compression" checked={conn.advanced.compression} onChange={(v) => setAdv({ compression: v })} />
                  </Field>
                  <Field label="">
                    <Check label="Use cleartext authentication plugin" checked={conn.advanced.cleartextAuth} onChange={(v) => setAdv({ cleartextAuth: v })} />
                  </Field>
                  <ToggleText
                    label="Use socket file"
                    value={conn.advanced.socketFile ?? ""}
                    def="/tmp/mysql.sock"
                    disabled={conn.ssh.enabled}
                    hint={conn.ssh.enabled ? "Not available with an SSH tunnel" : undefined}
                    onChange={(v) => setAdv({ socketFile: v })}
                  />
                </>
              )}
              {pg && (
                <Field label="Connection Parameters">
                  <textarea rows={3} placeholder={"one key=value per line\napplication_name=reports\noptions=-c statement_timeout=30000"} value={conn.advanced.connectionParams ?? ""} onChange={(e) => setAdv({ connectionParams: e.target.value })} />
                </Field>
              )}
              {!mongo && (
              <>
              <Field label="Initial Queries">
                <textarea rows={3} placeholder={mysql ? "SET time_zone = '+00:00';" : sqlite ? "PRAGMA journal_mode = WAL;" : "SET search_path TO app, public;"} value={conn.advanced.initialQueries ?? ""} onChange={(e) => setAdv({ initialQueries: e.target.value })} />
              </Field>
              <p className="hint">Initial queries run on every new query tab and table view session.</p>
              </>
              )}
            </>
          )}

          {section === "databases" && (sqlite ? <AttachedTab conn={conn} setLite={setLite} /> : <DatabasesTab conn={conn} setDBs={setDBs} />)}

          {section === "ssl" && (
            <>
              <Field label="">
                <Check label="Use SSL" checked={conn.ssl.enabled} onChange={(v) => setSSL({ enabled: v })} />
              </Field>
              <fieldset disabled={!conn.ssl.enabled}>
                {pg && (
                  <Field label="SSL Mode">
                    <select value={conn.ssl.mode || "require"} onChange={(e) => setSSL({ mode: e.target.value })}>
                      {["prefer", "allow", "require", "verify-ca", "verify-full"].map((m) => (
                        <option key={m}>{m}</option>
                      ))}
                    </select>
                  </Field>
                )}
                <FileField label="CA Certificate" value={conn.ssl.caFile ?? ""} onChange={(v) => setSSL({ caFile: v })} />
                <FileField label="Client Certificate" value={conn.ssl.certFile ?? ""} onChange={(v) => setSSL({ certFile: v })} />
                <FileField label="Client Key" value={conn.ssl.keyFile ?? ""} onChange={(v) => setSSL({ keyFile: v })} />
                {pg && <FileField label="Revocation List" value={conn.ssl.crlFile ?? ""} onChange={(v) => setSSL({ crlFile: v })} />}
                {mysql && (
                  <>
                    <Field label="">
                      <Check label="Verify server certificate against CA" checked={conn.ssl.verifyHost} onChange={(v) => setSSL({ verifyHost: v })} />
                    </Field>
                    <Field label="Specified Cipher">
                      <input value={conn.ssl.cipher ?? ""} placeholder="(any) e.g. ECDHE-RSA-AES128-GCM-SHA256" onChange={(e) => setSSL({ cipher: e.target.value })} />
                    </Field>
                  </>
                )}
              </fieldset>
              {!conn.ssl.enabled && pg && <p className="hint">Without SSL, Chloe still uses TLS if the server offers it (sslmode=prefer).</p>}
            </>
          )}

          {section === "ssh" && (
            <>
              <Field label="">
                <Check label="Use SSH Tunnel" checked={conn.ssh.enabled} onChange={(v) => setSSH({ enabled: v })} />
              </Field>
              <fieldset disabled={!conn.ssh.enabled}>
                <Field label="Host">
                  <input value={conn.ssh.host} onChange={(e) => setSSH({ host: e.target.value })} />
                </Field>
                <Field label="Port">
                  <input className="narrow" type="number" value={conn.ssh.port || 22} onChange={(e) => setSSH({ port: Number(e.target.value) })} />
                </Field>
                <Field label="User Name">
                  <input value={conn.ssh.user} onChange={(e) => setSSH({ user: e.target.value })} />
                </Field>
                <Field label="Authentication">
                  <select value={conn.ssh.auth} onChange={(e) => setSSH({ auth: e.target.value as SSHAuth })}>
                    <option value={SSHAuth.SSHPassword}>Password</option>
                    <option value={SSHAuth.SSHPublicKey}>Public Key</option>
                    <option value={SSHAuth.SSHPasswordPublicKey}>Password and Public Key</option>
                    <option value={SSHAuth.SSHAgent}>SSH Agent</option>
                  </select>
                </Field>
                {(conn.ssh.auth === SSHAuth.SSHPassword || conn.ssh.auth === SSHAuth.SSHPasswordPublicKey) && (
                  <Field label="Password">
                    <SecretInput value={conn.ssh.password ?? ""} onChange={(v) => setSSH({ password: v })} />
                  </Field>
                )}
                {(conn.ssh.auth === SSHAuth.SSHPublicKey || conn.ssh.auth === SSHAuth.SSHPasswordPublicKey) && (
                  <>
                    <FileField label="Private Key" value={conn.ssh.privateKeyPath ?? ""} placeholder="~/.ssh/id_ed25519" onChange={(v) => setSSH({ privateKeyPath: v })} />
                    <Field label="Passphrase">
                      <SecretInput value={conn.ssh.passphrase ?? ""} onChange={(v) => setSSH({ passphrase: v })} />
                    </Field>
                  </>
                )}
                {conn.ssh.auth !== SSHAuth.SSHAgent && (
                  <Field label="">
                    <Check label="Save password / passphrase" checked={conn.ssh.savePassword} onChange={(v) => setSSH({ savePassword: v })} />
                  </Field>
                )}
              </fieldset>
            </>
          )}

          {section === "remarks" && <textarea className="remarks" rows={12} placeholder="Notes about this connection" value={conn.remark ?? ""} onChange={(e) => set({ remark: e.target.value })} />}
        </div>
        {status && <div className={"status-line " + (status.ok ? "ok" : "err")}>{status.text}</div>}
        <div className="modal-footer">
          <button onClick={test} disabled={busy}>
            {busy ? "Testing…" : "Test Connection"}
          </button>
          {conn.id && (
            <button title="Copy connection URL (without password)" onClick={async () => navigator.clipboard.writeText(await Connections.URL(conn.id))}>
              <Clipboard size={13} /> Copy URL
            </button>
          )}
          <span className="spacer" />
          <button onClick={onClose}>Cancel</button>
          <button className="primary" onClick={save}>
            Save
          </button>
        </div>
      </div>
    </div>
  );
}

const fileBase = (p?: string) => (p ?? "").split(/[\\/]/).pop()?.replace(/\.(db|sqlite3?|db3)$/i, "") ?? "";

const SQLITE_FILTER = "*.db;*.sqlite;*.sqlite3;*.db3";

/** Asks for an existing SQLite file, or where to create a new one. */
export async function pickSQLiteFile(create: boolean, title = "Database File"): Promise<string> {
  return (create ? Transfer.SaveFileDialog("database.db", "SQLite Database", SQLITE_FILTER) : Transfer.OpenFileDialog(title, "SQLite Database", SQLITE_FILTER)).catch(() => "");
}

function SQLiteFields({ conn, setLite }: { conn: Connection; setLite(p: Partial<Connection["sqlite"]>): void }) {
  const create = !!conn.sqlite?.create;
  return (
    <>
      <Field label="Type">
        <div className="row radio-row">
          <label className="check">
            <input type="radio" checked={!create} onChange={() => setLite({ create: false })} /> Existing Database File
          </label>
          <label className="check">
            <input type="radio" checked={create} disabled={!!conn.id} onChange={() => setLite({ create: true })} /> New SQLite 3
          </label>
        </div>
      </Field>
      <Field label="Database File">
        <div className="row">
          <input value={conn.sqlite?.path ?? ""} placeholder={create ? "where to create the file" : "path to a .db / .sqlite file"} onChange={(e) => setLite({ path: e.target.value })} />
          <button
            title="Browse…"
            onClick={async () => {
              const p = await pickSQLiteFile(create);
              if (p) setLite({ path: p });
            }}
          >
            <FolderOpen size={13} />
          </button>
        </div>
      </Field>
      {create && <p className="hint">The file is created when you test or open the connection.</p>}
    </>
  );
}

function AttachedTab({ conn, setLite }: { conn: Connection; setLite(p: Partial<Connection["sqlite"]>): void }) {
  const list = arr(conn.sqlite?.attached);
  const [name, setName] = useState("");
  const [path, setPath] = useState("");
  const add = () => {
    const n = name.trim() || fileBase(path);
    if (!n || !path.trim() || list.some((a) => a.name === n)) return;
    setLite({ attached: [...list, { name: n, path: path.trim() }] });
    setName("");
    setPath("");
  };
  return (
    <>
      <p className="hint">Attached database files appear next to “main” in the connection tree and can be queried as name.table.</p>
      <table className="db-list">
        <thead>
          <tr>
            <th>Name</th>
            <th>File</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {list.map((a, i) => (
            <tr key={a.name}>
              <td>{a.name}</td>
              <td className="muted small" title={a.path}>
                {a.path}
              </td>
              <td>
                <button className="icon" title="Detach" onClick={() => setLite({ attached: list.filter((_, j) => j !== i) })}>
                  <Trash2 size={12} />
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="row">
        <input style={{ width: 120, flex: "none" }} placeholder="name" value={name} onChange={(e) => setName(e.target.value)} />
        <input placeholder="database file" value={path} onChange={(e) => setPath(e.target.value)} onKeyDown={(e) => e.key === "Enter" && add()} />
        <button
          title="Browse…"
          onClick={async () => {
            const p = await pickSQLiteFile(false);
            if (p) setPath(p);
          }}
        >
          <FolderOpen size={13} />
        </button>
        <button disabled={!path.trim()} onClick={add}>
          <Plus size={13} /> Attach
        </button>
      </div>
    </>
  );
}

function hostPlaceholder(p: Preset) {
  const m = /@([^/:?]+)/.exec(p.urlExample);
  return m ? m[1] : "";
}

function DatabasesTab({ conn, setDBs }: { conn: Connection; setDBs(p: Partial<Connection["databases"]>): void }) {
  const list = arr(conn.databases.list);
  const [name, setName] = useState("");
  const connected = useApp((s) => !!(conn.id && s.open[conn.id]));
  const add = (n: string) => {
    const v = n.trim();
    if (!v || list.some((d) => d.name === v)) return;
    setDBs({ list: [...list, { name: v, autoOpen: false }] });
    setName("");
  };
  const fetchFromServer = async () => {
    const { Explorer } = await import("../api");
    const dbs = arr(await Explorer.ListDatabases(conn.id));
    const have = new Set(list.map((d) => d.name));
    setDBs({ list: [...list, ...dbs.filter((d) => !have.has(d)).map((d) => ({ name: d, autoOpen: false }))] });
  };
  return (
    <>
      <Field label="">
        <Check label="Use custom database list" checked={conn.databases.enabled} onChange={(v) => setDBs({ enabled: v })} />
      </Field>
      <p className="hint">Only the databases below appear in the connection tree, in this order. “Auto Open” opens a database as soon as the connection opens.</p>
      <table className="db-list">
        <thead>
          <tr>
            <th>Name</th>
            <th>Auto Open</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {list.map((d, i) => (
            <tr key={d.name}>
              <td>{d.name}</td>
              <td>
                <input type="checkbox" checked={d.autoOpen} onChange={(e) => setDBs({ list: list.map((x, j) => (j === i ? { ...x, autoOpen: e.target.checked } : x)) })} />
              </td>
              <td>
                <button className="icon" onClick={() => setDBs({ list: list.filter((_, j) => j !== i) })}>
                  <Trash2 size={12} />
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <div className="row">
        <input placeholder="database name" value={name} onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.key === "Enter" && add(name)} />
        <button onClick={() => add(name)}>
          <Plus size={13} /> Add
        </button>
        <button disabled={!connected} title={connected ? "" : "Open the connection to fetch its databases"} onClick={fetchFromServer}>
          Fetch from server
        </button>
      </div>
    </>
  );
}

// ---- form controls ------------------------------------------------------------------

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="field">
      <label>{label}</label>
      <div className="field-input">{children}</div>
    </div>
  );
}

function Check({ label, checked, onChange }: { label: string; checked: boolean; onChange(v: boolean): void }) {
  return (
    <label className="check">
      <input type="checkbox" checked={checked} onChange={(e) => onChange(e.target.checked)} /> {label}
    </label>
  );
}

/** Checkbox that enables a number; unchecked stores 0. */
function ToggleNumber({ label, value, def, onChange }: { label: string; value: number; def: number; onChange(v: number): void }) {
  const on = value > 0;
  return (
    <Field label="">
      <div className="row">
        <Check label={label} checked={on} onChange={(v) => onChange(v ? def : 0)} />
        <input className="narrow" type="number" min={1} disabled={!on} value={on ? value : def} onChange={(e) => onChange(Math.max(1, Number(e.target.value)))} />
      </div>
    </Field>
  );
}

function ToggleText({ label, value, def, disabled, hint, onChange }: { label: string; value: string; def: string; disabled?: boolean; hint?: string; onChange(v: string): void }) {
  const on = value !== "";
  return (
    <Field label="">
      <div className="row" title={hint}>
        <label className="check">
          <input type="checkbox" disabled={disabled} checked={on} onChange={(e) => onChange(e.target.checked ? def : "")} /> {label}
        </label>
        <input disabled={!on || disabled} value={on ? value : def} onChange={(e) => onChange(e.target.value)} />
      </div>
    </Field>
  );
}

function FileField({ label, value, placeholder, onChange }: { label: string; value: string; placeholder?: string; onChange(v: string): void }) {
  return (
    <Field label={label}>
      <div className="row">
        <input value={value} placeholder={placeholder ?? "path to file"} onChange={(e) => onChange(e.target.value)} />
        <button
          title="Browse…"
          onClick={async () => {
            const p = await Connections.PickFile(label).catch(() => "");
            if (p) onChange(p);
          }}
        >
          <FolderOpen size={13} />
        </button>
      </div>
    </Field>
  );
}

// The Go side sends this placeholder for saved secrets; never display it.
const SAVED = "\u0000saved\u0000";

function SecretInput({ value, onChange, disabled, placeholder }: { value: string; onChange(v: string): void; disabled?: boolean; placeholder?: string }) {
  const saved = value === SAVED;
  const ph = useMemo(() => (saved ? "••••••••  (saved)" : (placeholder ?? "")), [saved, placeholder]);
  return (
    <input
      type="password"
      disabled={disabled}
      value={saved ? "" : value}
      placeholder={ph}
      onChange={(e) => onChange(e.target.value)}
      onKeyDown={(e) => {
        // Backspace on a saved secret clears it.
        if (saved && (e.key === "Backspace" || e.key === "Delete")) onChange("");
      }}
    />
  );
}

function MongoFields({ conn, set, setMongo, pasteURL }: { conn: Connection; set(p: Partial<Connection>): void; setMongo(p: Partial<Connection["mongo"]>): void; pasteURL(): void }) {
  const m = conn.mongo ?? {};
  const topology = m.topology || "standalone";
  const auth = m.auth || "password";
  const members = !m.srv && topology !== "standalone";
  return (
    <>
      <Field label="Connection">
        <select value={topology} onChange={(e) => setMongo({ topology: e.target.value })}>
          <option value="standalone">Standalone</option>
          <option value="replicaset">Replica Set</option>
          <option value="shard">Shard Cluster</option>
        </select>
      </Field>
      <Field label="">
        <Check label="SRV Record (mongodb+srv)" checked={!!m.srv} onChange={(v) => setMongo({ srv: v })} />
      </Field>
      {members ? (
        <Field label={topology === "shard" ? "Routers" : "Members"}>
          <textarea
            rows={3}
            spellCheck={false}
            placeholder={"host1:27017\nhost2:27017\nhost3:27017"}
            value={(m.members ?? []).join("\n")}
            onChange={(e) => setMongo({ members: e.target.value.split("\n") })}
          />
        </Field>
      ) : (
        <>
          <Field label="Host">
            <div className="row">
              <input value={conn.host} placeholder={m.srv ? "cluster0.example.mongodb.net" : "localhost"} onChange={(e) => set({ host: e.target.value })} />
              <button title="Fill from a mongodb:// URL" onClick={pasteURL}>
                <Link2 size={13} /> URL
              </button>
            </div>
          </Field>
          {!m.srv && (
            <Field label="Port">
              <input className="narrow" type="number" value={conn.port || ""} placeholder="27017" onChange={(e) => set({ port: Number(e.target.value) })} />
            </Field>
          )}
        </>
      )}
      {topology === "replicaset" && (
        <Field label="Replica Set">
          <input value={m.replicaSet ?? ""} placeholder="(discover)" onChange={(e) => setMongo({ replicaSet: e.target.value })} />
        </Field>
      )}
      <Field label="Read Preference">
        <select value={m.readPreference ?? ""} onChange={(e) => setMongo({ readPreference: e.target.value })}>
          <option value="">Primary</option>
          <option value="primaryPreferred">Primary Preferred</option>
          <option value="secondary">Secondary</option>
          <option value="secondaryPreferred">Secondary Preferred</option>
          <option value="nearest">Nearest</option>
        </select>
      </Field>
      <hr />
      <Field label="Authentication">
        <select value={auth} onChange={(e) => setMongo({ auth: e.target.value })}>
          <option value="none">None</option>
          <option value="password">Password</option>
          <option value="ldap">LDAP</option>
          <option value="x509">X.509</option>
        </select>
      </Field>
      {auth === "password" && (
        <>
          <Field label="Authentication Database">
            <input value={m.authSource ?? ""} placeholder="admin" onChange={(e) => setMongo({ authSource: e.target.value })} />
          </Field>
          <Field label="Mechanism">
            <select value={m.mechanism ?? ""} onChange={(e) => setMongo({ mechanism: e.target.value })}>
              <option value="">Default</option>
              <option value="SCRAM-SHA-256">SCRAM-SHA-256</option>
              <option value="SCRAM-SHA-1">SCRAM-SHA-1</option>
            </select>
          </Field>
        </>
      )}
      {auth !== "none" && (
        <Field label="User Name">
          <input value={conn.user} placeholder={auth === "x509" ? "(from the certificate)" : ""} onChange={(e) => set({ user: e.target.value })} />
        </Field>
      )}
      {(auth === "password" || auth === "ldap") && (
        <>
          <Field label="Password">
            <SecretInput value={conn.password ?? ""} placeholder={conn.savePassword ? "" : "Used for testing only; asked on connect"} onChange={(v) => set({ password: v })} />
          </Field>
          <Field label="">
            <Check label="Save password" checked={conn.savePassword} onChange={(v) => set({ savePassword: v })} />
          </Field>
        </>
      )}
      {auth === "x509" && <p className="hint">Set the client certificate and key on the SSL tab.</p>}
      <Field label="Default Database">
        <input value={conn.initialDatabase ?? ""} placeholder="test" onChange={(e) => set({ initialDatabase: e.target.value })} />
      </Field>
    </>
  );
}
