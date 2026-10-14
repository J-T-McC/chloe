// Pieces shared by Data Transfer, Structure Synchronization and Data Synchronization.
import { useEffect, useRef, useState } from "react";
import { DBType, Explorer, arr, type Endpoint } from "../../api";
import { connById, useApp } from "../../store";
import type { useTransfer } from "./state";
import { Combobox } from "../Combobox";

// ---- source / target picker ---------------------------------------------------------------------

export function EndpointPicker({ title, value, onChange, conns, allowNew, create }: { title: string; value: Endpoint; onChange(e: Endpoint): void; conns: ReturnType<typeof useApp.getState>["connections"]; allowNew?: boolean; create?: boolean }) {
  const [dbs, setDbs] = useState<string[]>([]);
  const [schemas, setSchemas] = useState<string[]>([]);
  const [err, setErr] = useState("");
  const [version, setVersion] = useState("");
  const conn = connById(value.connId);
  const pg = conn?.type === DBType.PostgreSQL;
  const seq = useRef(0);
  // Async results apply to the value as it is by then, not as it was when they started.
  const latest = useRef(value);
  latest.current = value;

  useEffect(() => {
    setDbs([]);
    setErr("");
    setVersion("");
    if (!value.connId) return;
    const n = ++seq.current;
    (async () => {
      if (!(await useApp.getState().connect(value.connId))) return setErr(useApp.getState().errors[value.connId] || "Couldn't connect");
      const list: string[] = arr(await Explorer.ListDatabases(value.connId).catch(() => [] as string[]));
      if (n !== seq.current) return;
      setDbs(list);
      setVersion(useApp.getState().open[value.connId]?.version ?? "");
      const cur = latest.current;
      if (!cur.database && list.length) onChange({ ...cur, database: conn?.type === DBType.SQLite ? "main" : (conn?.initialDatabase && list.includes(conn.initialDatabase) ? conn.initialDatabase : "") });
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value.connId]);

  useEffect(() => {
    setSchemas([]);
    if (!pg || !value.database || !dbs.includes(value.database)) return;
    Explorer.ListSchemas(value.connId, value.database)
      .then((s) => {
        const list = arr(s);
        setSchemas(list);
        const cur = latest.current;
        if (!cur.schema && cur.database === value.database) onChange({ ...cur, schema: list.includes("public") ? "public" : (list[0] ?? "") });
      })
      .catch(() => {});
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value.connId, value.database, dbs, pg]);

  return (
    <div className="dt-side form">
      <div className="dt-side-title">{title}</div>
      <div className="field">
        <label>Connection</label>
        <div className="field-input">
          <select aria-label={`${title} Connection`} value={value.connId} onChange={(e) => onChange({ connId: e.target.value, database: "", schema: "" })}>
            <option value="">Choose…</option>
            {conns.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </div>
      </div>
      <div className="field">
        <label>Database</label>
        <div className="field-input">
          {allowNew && conn?.type !== DBType.SQLite ? (
            <Combobox
              ariaLabel={`${title} Database`}
              value={value.database}
              options={dbs}
              placeholder={dbs.length ? "Choose or type a name" : ""}
              onChange={(d) => onChange({ ...latest.current, database: d, schema: pg ? "" : latest.current.schema })}
            />
          ) : (
            <select aria-label={`${title} Database`} value={value.database} onChange={(e) => onChange({ ...value, database: e.target.value, schema: "" })}>
              <option value="">Choose…</option>
              {dbs.map((d) => (
                <option key={d}>{d}</option>
              ))}
            </select>
          )}
        </div>
      </div>
      {pg && (
        <div className="field">
          <label>Schema</label>
          <div className="field-input">
            {allowNew ? (
              <Combobox ariaLabel={`${title} Schema`} value={value.schema ?? ""} options={schemas} onChange={(sc) => onChange({ ...latest.current, schema: sc })} />
            ) : (
              <select aria-label={`${title} Schema`} value={value.schema ?? ""} onChange={(e) => onChange({ ...value, schema: e.target.value })}>
                {schemas.map((d) => (
                  <option key={d}>{d}</option>
                ))}
              </select>
            )}
          </div>
        </div>
      )}
      {allowNew && value.database && dbs.length > 0 && !dbs.includes(value.database) && (
        <p className={create ? "hint" : "hint err"}>{create ? `“${value.database}” will be created.` : `“${value.database}” doesn't exist. Turn on “Create target database/schema if not exist” in Options.`}</p>
      )}
      {allowNew && pg && value.schema && schemas.length > 0 && !schemas.includes(value.schema) && (
        <p className={create ? "hint" : "hint err"}>{create ? `Schema “${value.schema}” will be created.` : `Schema “${value.schema}” doesn't exist. Turn on “Create target database/schema if not exist” in Options.`}</p>
      )}
      <dl className="dt-info">
        <dt>Type</dt>
        <dd>{conn?.type ?? "—"}</dd>
        <dt>Host</dt>
        <dd>{conn ? (conn.type === DBType.SQLite ? conn.sqlite?.path : `${conn.host}:${conn.port}`) : "—"}</dd>
        <dt>Version</dt>
        <dd>{version || "—"}</dd>
      </dl>
      {err && <div className="err small">{err}</div>}
    </div>
  );
}

// ---- progress --------------------------------------------------------------------

export function JobProgress({ job }: { job: ReturnType<typeof useTransfer.getState>["jobs"][number] }) {
  const log = useRef<HTMLPreElement>(null);
  useEffect(() => {
    if (log.current) log.current.scrollTop = log.current.scrollHeight;
  }, [job.log?.length]);
  const end = job.finished && !job.finished.startsWith("0001") ? new Date(job.finished) : new Date();
  const ms = +end - +new Date(job.started);
  const secs = ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)}s`;
  return (
    <div className="dt-progress">
      <div className="dt-counters">
        <span>
          Processed Records <strong>{job.done.toLocaleString()}</strong>
        </span>
        <span>
          Errors <strong className={job.errors ? "err" : ""}>{job.errors}</strong>
        </span>
        <span>
          Time <strong>{secs}</strong>
        </span>
        <span className="spacer" />
        <span className={job.status === "failed" || job.errors ? "err" : job.status === "done" ? "ok" : "muted"}>{job.status === "running" ? "Running…" : job.message || job.status}</span>
      </div>
      <pre className="dt-log" ref={log}>
        {arr(job.log).join("\n")}
      </pre>
    </div>
  );
}

