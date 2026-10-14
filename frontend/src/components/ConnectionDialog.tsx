import { useEffect, useState } from "react";
import { Connection, Connections, DBType, SSHAuth, errorText } from "../api";
import { useApp } from "../store";
import { DBIcon } from "./icons";

const TYPES: Array<{ type: DBType; label: string; ready: boolean }> = [
  { type: DBType.MySQL, label: "MySQL", ready: true },
  { type: DBType.MariaDB, label: "MariaDB", ready: true },
  { type: DBType.PostgreSQL, label: "PostgreSQL", ready: true },
  { type: DBType.MongoDB, label: "MongoDB", ready: false },
  { type: DBType.Redis, label: "Redis", ready: false },
  { type: DBType.SQLite, label: "SQLite", ready: false },
  { type: DBType.SQLServer, label: "SQL Server", ready: false },
  { type: DBType.Oracle, label: "Oracle", ready: false },
  { type: DBType.Snowflake, label: "Snowflake", ready: false },
];

type Section = "general" | "advanced" | "databases" | "ssl" | "ssh";

function blank(type: DBType): Connection {
  return {
    id: "",
    type,
    name: "",
    host: "localhost",
    port: defaultPort(type),
    user: type === DBType.PostgreSQL ? "postgres" : "root",
    savePassword: true,
    ssh: { enabled: false, host: "", port: 22, user: "", auth: SSHAuth.SSHPassword, savePassword: true },
    ssl: { enabled: false, verifyHost: false },
    options: {},
  };
}

function defaultPort(t: DBType) {
  return { mysql: 3306, mariadb: 3306, postgres: 5432, mongodb: 27017, redis: 6379, sqlserver: 1433, oracle: 1521 }[t as string] ?? 0;
}

export function ConnectionDialog() {
  const editing = useApp((s) => s.editing);
  const close = () => useApp.getState().editConnection(undefined);
  const [conn, setConn] = useState<Connection | null>(null);
  const [section, setSection] = useState<Section>("general");
  const [status, setStatus] = useState<{ ok: boolean; text: string } | null>(null);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setStatus(null);
    setSection("general");
    if (editing === "new") setConn(null);
    else if (editing) Connections.Get(editing.id).then(setConn);
  }, [editing]);

  if (!editing) return null;

  // Step 1 for new connections: pick the engine.
  if (!conn) {
    return (
      <div className="modal-backdrop" onMouseDown={close}>
        <div className="modal" onMouseDown={(e) => e.stopPropagation()}>
          <div className="modal-title">New Connection</div>
          <div className="type-grid">
            {TYPES.map((t) => (
              <button key={t.type} className="type-card" disabled={!t.ready} onClick={() => setConn(blank(t.type))} title={t.ready ? "" : "Coming in a later phase"}>
                <DBIcon type={t.type} size={30} />
                <span>{t.label}</span>
              </button>
            ))}
          </div>
          <div className="modal-footer">
            <span className="spacer" />
            <button onClick={close}>Cancel</button>
          </div>
        </div>
      </div>
    );
  }

  const set = (patch: Partial<Connection>) => setConn({ ...conn, ...patch });
  const setSSH = (patch: Partial<Connection["ssh"]>) => set({ ssh: { ...conn.ssh, ...patch } });
  const setSSL = (patch: Partial<Connection["ssl"]>) => set({ ssl: { ...conn.ssl, ...patch } });
  const setOpt = (k: string, v: string) => set({ options: { ...(conn.options ?? {}), [k]: v } });
  const isPG = conn.type === DBType.PostgreSQL;

  const test = async () => {
    setBusy(true);
    setStatus(null);
    try {
      const v = await Connections.Test(conn);
      setStatus({ ok: true, text: `Connection successful — server ${v}` });
    } catch (e) {
      setStatus({ ok: false, text: errorText(e) });
    } finally {
      setBusy(false);
    }
  };

  const save = async () => {
    const c: Connection = { ...conn, name: conn.name.trim() || `${conn.host}` };
    try {
      await Connections.Save(c);
      await useApp.getState().loadConnections();
      // Saving disconnects a live connection; reflect that in the UI.
      if (c.id) useApp.getState().disconnect(c.id);
      close();
    } catch (e) {
      setStatus({ ok: false, text: errorText(e) });
    }
  };

  const sections: Array<[Section, string]> = [
    ["general", "General"],
    ["advanced", "Advanced"],
    ["databases", "Databases"],
    ["ssl", "SSL"],
    ["ssh", "SSH"],
  ];

  return (
    <div className="modal-backdrop" onMouseDown={close}>
      <div className="modal conn-dialog" onMouseDown={(e) => e.stopPropagation()} onKeyDown={(e) => e.key === "Escape" && close()}>
        <div className="modal-title">
          <DBIcon type={conn.type} size={18} />
          {conn.id ? `Edit Connection — ${conn.name}` : `New Connection — ${TYPES.find((t) => t.type === conn.type)?.label}`}
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
              <Field label="Connection Name">
                <input autoFocus value={conn.name} placeholder={conn.host} onChange={(e) => set({ name: e.target.value })} />
              </Field>
              <Field label="Group">
                <input value={conn.group ?? ""} placeholder="(none)" onChange={(e) => set({ group: e.target.value })} />
              </Field>
              <hr />
              <Field label="Host">
                <input value={conn.host} onChange={(e) => set({ host: e.target.value })} />
              </Field>
              <Field label="Port">
                <input className="narrow" type="number" value={conn.port || ""} onChange={(e) => set({ port: Number(e.target.value) })} />
              </Field>
              {isPG && (
                <Field label="Initial Database">
                  <input value={conn.initialDatabase ?? ""} placeholder="postgres" onChange={(e) => set({ initialDatabase: e.target.value })} />
                </Field>
              )}
              <Field label="User Name">
                <input value={conn.user} onChange={(e) => set({ user: e.target.value })} />
              </Field>
              <Field label="Password">
                <SecretInput value={conn.password ?? ""} onChange={(v) => set({ password: v })} />
              </Field>
              <Field label="">
                <label className="check">
                  <input type="checkbox" checked={conn.savePassword} onChange={(e) => set({ savePassword: e.target.checked })} /> Save password
                </label>
              </Field>
            </>
          )}

          {section === "advanced" && (
            <>
              {!isPG && (
                <Field label="Encoding">
                  <select value={conn.options?.encoding ?? "auto"} onChange={(e) => setOpt("encoding", e.target.value)}>
                    {["auto", "utf8mb4", "utf8mb3", "latin1", "ascii", "binary"].map((x) => (
                      <option key={x}>{x}</option>
                    ))}
                  </select>
                </Field>
              )}
              {!isPG && (
                <Field label="">
                  <label className="check">
                    <input type="checkbox" checked={conn.options?.cleartextPlugin === "true"} onChange={(e) => setOpt("cleartextPlugin", String(e.target.checked))} /> Use cleartext
                    authentication plugin
                  </label>
                </Field>
              )}
              <Field label="Color">
                <div className="swatches">
                  {["", "#e5534b", "#e8963c", "#d6b63a", "#4fae5a", "#3b8eea", "#9a6bd8", "#8b8f98"].map((c) => (
                    <button key={c || "none"} className={"swatch" + (conn.color === c || (!conn.color && !c) ? " on" : "")} style={{ background: c || "transparent" }} onClick={() => set({ color: c })} />
                  ))}
                </div>
              </Field>
              <Field label="Remark">
                <textarea rows={3} value={conn.remark ?? ""} onChange={(e) => set({ remark: e.target.value })} />
              </Field>
            </>
          )}

          {section === "databases" && (
            <>
              <p className="hint">Show only these {isPG ? "databases" : "databases"} in the connection tree (one per line). Leave empty to show all.</p>
              <textarea rows={8} value={(conn.showDatabases ?? []).join("\n")} onChange={(e) => set({ showDatabases: e.target.value.split("\n").map((x) => x.trim()).filter(Boolean) })} />
            </>
          )}

          {section === "ssl" && (
            <>
              <Field label="">
                <label className="check">
                  <input type="checkbox" checked={conn.ssl.enabled} onChange={(e) => setSSL({ enabled: e.target.checked })} /> Use SSL
                </label>
              </Field>
              <fieldset disabled={!conn.ssl.enabled}>
                {isPG && (
                  <Field label="SSL Mode">
                    <select value={conn.ssl.mode || "require"} onChange={(e) => setSSL({ mode: e.target.value })}>
                      {["require", "verify-ca", "verify-full"].map((m) => (
                        <option key={m}>{m}</option>
                      ))}
                    </select>
                  </Field>
                )}
                <Field label="CA Certificate">
                  <input value={conn.ssl.caFile ?? ""} placeholder="/path/to/ca.pem" onChange={(e) => setSSL({ caFile: e.target.value })} />
                </Field>
                <Field label="Client Certificate">
                  <input value={conn.ssl.certFile ?? ""} placeholder="/path/to/client-cert.pem" onChange={(e) => setSSL({ certFile: e.target.value })} />
                </Field>
                <Field label="Client Key">
                  <input value={conn.ssl.keyFile ?? ""} placeholder="/path/to/client-key.pem" onChange={(e) => setSSL({ keyFile: e.target.value })} />
                </Field>
                {!isPG && (
                  <Field label="">
                    <label className="check">
                      <input type="checkbox" checked={conn.ssl.verifyHost} onChange={(e) => setSSL({ verifyHost: e.target.checked })} /> Verify server certificate
                    </label>
                  </Field>
                )}
              </fieldset>
            </>
          )}

          {section === "ssh" && (
            <>
              <Field label="">
                <label className="check">
                  <input type="checkbox" checked={conn.ssh.enabled} onChange={(e) => setSSH({ enabled: e.target.checked })} /> Use SSH Tunnel
                </label>
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
                    <option value={SSHAuth.SSHAgent}>SSH Agent</option>
                  </select>
                </Field>
                {conn.ssh.auth === SSHAuth.SSHPassword && (
                  <Field label="Password">
                    <SecretInput value={conn.ssh.password ?? ""} onChange={(v) => setSSH({ password: v })} />
                  </Field>
                )}
                {conn.ssh.auth === SSHAuth.SSHPublicKey && (
                  <>
                    <Field label="Private Key">
                      <input value={conn.ssh.privateKeyPath ?? ""} placeholder="~/.ssh/id_ed25519" onChange={(e) => setSSH({ privateKeyPath: e.target.value })} />
                    </Field>
                    <Field label="Passphrase">
                      <SecretInput value={conn.ssh.passphrase ?? ""} onChange={(v) => setSSH({ passphrase: v })} />
                    </Field>
                  </>
                )}
                {conn.ssh.auth !== SSHAuth.SSHAgent && (
                  <Field label="">
                    <label className="check">
                      <input type="checkbox" checked={conn.ssh.savePassword} onChange={(e) => setSSH({ savePassword: e.target.checked })} /> Save{" "}
                      {conn.ssh.auth === SSHAuth.SSHPublicKey ? "passphrase" : "password"}
                    </label>
                  </Field>
                )}
              </fieldset>
            </>
          )}
        </div>
        {status && <div className={"status-line " + (status.ok ? "ok" : "err")}>{status.text}</div>}
        <div className="modal-footer">
          <button onClick={test} disabled={busy}>
            {busy ? "Testing…" : "Test Connection"}
          </button>
          <span className="spacer" />
          <button onClick={close}>Cancel</button>
          <button className="primary" onClick={save}>
            Save
          </button>
        </div>
      </div>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="field">
      <label>{label}</label>
      <div className="field-input">{children}</div>
    </div>
  );
}

// The Go side sends this placeholder for saved secrets; never display it.
const SAVED = "\u0000saved\u0000";

function SecretInput({ value, onChange }: { value: string; onChange(v: string): void }) {
  const saved = value === SAVED;
  return (
    <input
      type="password"
      value={saved ? "" : value}
      placeholder={saved ? "••••••••  (saved)" : ""}
      onChange={(e) => onChange(e.target.value)}
      onKeyDown={(e) => {
        // Backspace on a saved secret clears it.
        if (saved && (e.key === "Backspace" || e.key === "Delete")) onChange("");
      }}
    />
  );
}
