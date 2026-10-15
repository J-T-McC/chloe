// MCP Server: endpoints (token, access per connection and database, danger mode)
// and the activity log of tool calls. See docs/specs/mcp-server.md.
import { Bot, ChevronDown, ChevronRight, Copy, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { create } from "zustand";
import { DBType, Events, Explorer, MCP, MCPLevel, arr, errorText, type MCPActivity, type MCPEndpoint, type MCPSettings, type MCPStatus } from "../../api";
import { useApp } from "../../store";
import { alert, confirm } from "../Dialogs";
import { ConnIcon } from "../icons";
import { Split } from "../Split";

const SQL_ENGINES: string[] = [DBType.MySQL, DBType.MariaDB, DBType.PostgreSQL, DBType.SQLite];

interface MCPState {
  settings: MCPSettings | null;
  status: MCPStatus | null;
  activity: MCPActivity[];
}

/** Server state shared by the tab and the titlebar button. */
export const useMCP = create<MCPState>(() => ({ settings: null, status: null, activity: [] }));

let listening = false;
export function listenMCP() {
  if (listening) return;
  listening = true;
  MCP.Settings().then((settings) => useMCP.setState({ settings })).catch(() => {});
  MCP.Status().then((status) => useMCP.setState({ status })).catch(() => {});
  MCP.Activity().then((a) => useMCP.setState({ activity: arr(a) })).catch(() => {});
  Events.On("mcp:status", (ev) => useMCP.setState({ status: ev.data }));
  Events.On("mcp:activity", (ev) => useMCP.setState((s) => ({ activity: [ev.data, ...s.activity].slice(0, 1000) })));
}

export function openMCPServer() {
  useApp.getState().openTab({ kind: "mcp", title: "MCP Server" });
}

async function save(next: MCPSettings) {
  // Show the change at once; the saved settings (new IDs, tokens) replace it.
  useMCP.setState({ settings: next });
  try {
    useMCP.setState({ settings: await MCP.Save(next) });
  } catch (e) {
    alert("Couldn't save the MCP settings", errorText(e));
    MCP.Settings().then((settings) => useMCP.setState({ settings })).catch(() => {});
  }
}

/** Titlebar button: off, running, or running with danger mode. */
export function MCPButton() {
  const status = useMCP((s) => s.status);
  useEffect(() => listenMCP(), []);
  const cls = status?.danger ? " danger-mode" : status?.running ? " running" : "";
  const title = status?.danger ? "MCP server running — danger mode is on for an endpoint" : status?.running ? `MCP server running at ${status.url}` : "MCP server (off)";
  return (
    <button className={"titlebar-button mcp-button" + cls} title={title} onClick={openMCPServer}>
      <Bot size={15} />
      {status?.danger && <span className="mcp-flag">Danger mode</span>}
    </button>
  );
}

type Page = "endpoints" | "activity";

export function MCPServer() {
  const settings = useMCP((s) => s.settings);
  const status = useMCP((s) => s.status);
  const [page, setPage] = useState<Page>("endpoints");
  const [port, setPort] = useState("");
  useEffect(() => listenMCP(), []);
  useEffect(() => setPort(String(settings?.port ?? "")), [settings?.port]);

  if (!settings) return <div className="center-hint">Loading…</div>;
  const applyPort = () => {
    const n = Number(port);
    if (n !== settings.port && n > 0 && n < 65536) save({ ...settings, port: n });
    else setPort(String(settings.port));
  };

  return (
    <div className="mcp">
      <div className="toolbar">
        <label className="check">
          <input type="checkbox" aria-label="Run MCP server" checked={settings.enabled} onChange={(e) => save({ ...settings, enabled: e.target.checked })} />
          Run MCP server
        </label>
        <label className="check">
          Port
          <input className="mcp-port" aria-label="Port" value={port} onChange={(e) => setPort(e.target.value)} onBlur={applyPort} onKeyDown={(e) => e.key === "Enter" && applyPort()} />
        </label>
        <span className={"mcp-status" + (status?.error ? " error" : "")}>{status?.error ? status.error : status?.running ? `Listening on ${status.url}` : "Stopped"}</span>
        <span className="spacer" />
        <div className="seg">
          {(["endpoints", "activity"] as Page[]).map((p) => (
            <button key={p} className={page === p ? "active" : ""} onClick={() => setPage(p)}>
              {p === "endpoints" ? "Endpoints" : "Activity"}
            </button>
          ))}
        </div>
      </div>
      {page === "endpoints" ? <Endpoints settings={settings} url={status?.url ?? `http://127.0.0.1:${settings.port}/mcp`} /> : <ActivityLog />}
    </div>
  );
}

function Endpoints({ settings, url }: { settings: MCPSettings; url: string }) {
  const endpoints = arr(settings.endpoints);
  const [selId, setSelId] = useState<string>(endpoints[0]?.id ?? "");
  const sel = endpoints.find((e) => e.id === selId) ?? endpoints[0];

  const update = (e: MCPEndpoint) => save({ ...settings, endpoints: endpoints.map((x) => (x.id === e.id ? e : x)) });
  const add = async () => {
    const taken = new Set(endpoints.map((e) => e.name));
    let name = "New Endpoint";
    for (let i = 2; taken.has(name); i++) name = `New Endpoint ${i}`;
    const before = new Set(endpoints.map((e) => e.id));
    const next = await MCP.Save({ ...settings, endpoints: [...endpoints, { id: "", name, enabled: true, token: "", danger: false, capabilities: { createConnections: false, savedQueries: false, monitor: false, models: false }, access: {} }] }).catch((e) => {
      alert("Couldn't add the endpoint", errorText(e));
      return null;
    });
    if (!next) return;
    useMCP.setState({ settings: next });
    setSelId(arr(next.endpoints).find((e) => !before.has(e.id))?.id ?? "");
  };
  const remove = async (e: MCPEndpoint) => {
    if (!(await confirm(`Delete endpoint “${e.name}”?`, { message: "Its token stops working at once.", okLabel: "Delete" }))) return;
    save({ ...settings, endpoints: endpoints.filter((x) => x.id !== e.id) });
  };

  return (
    <Split
      id="mcp-endpoints"
      className="mcp-body"
      firstSize={220}
      minFirst={150}
      minSecond={420}
      first={
        <div className="mcp-list">
          <div className="pane-title">Endpoints</div>
          {endpoints.map((e) => (
            <div key={e.id} className={"mcp-list-row" + (e.id === sel?.id ? " selected" : "") + (e.enabled ? "" : " disabled")} onClick={() => setSelId(e.id)}>
              <Bot size={13} />
              <span className="name">{e.name}</span>
              {e.danger && e.enabled && <span className="badge warn">danger</span>}
            </div>
          ))}
          {!endpoints.length && <div className="empty-hint">No endpoints yet. Each AI client gets its own endpoint and token.</div>}
          <div className="mcp-list-actions">
            <button onClick={add}>
              <Plus size={13} /> New Endpoint
            </button>
            <button disabled={!sel} onClick={() => sel && remove(sel)} title="Delete endpoint">
              <Trash2 size={13} />
            </button>
          </div>
        </div>
      }
      second={sel ? <EndpointDetail key={sel.id} ep={sel} url={url} update={update} /> : <div className="center-hint">Create an endpoint to give an AI client access.</div>}
    />
  );
}

type Client = "claude-code" | "json" | "claude-desktop";
const CLIENTS: Array<[Client, string]> = [
  ["claude-code", "Claude Code"],
  ["json", "JSON (.mcp.json, Cursor, …)"],
  ["claude-desktop", "Claude Desktop (mcp-remote)"],
];

function slug(name: string) {
  return "chloe-" + (name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "db");
}

export function clientConfig(client: Client, name: string, url: string, token: string): string {
  const id = slug(name);
  switch (client) {
    case "claude-code":
      return `claude mcp add --transport http ${id} ${url} --header "Authorization: Bearer ${token}"`;
    case "json":
      return JSON.stringify({ mcpServers: { [id]: { type: "http", url, headers: { Authorization: `Bearer ${token}` } } } }, null, 2);
    case "claude-desktop":
      return JSON.stringify({ mcpServers: { [id]: { command: "npx", args: ["-y", "mcp-remote", url, "--header", "Authorization:${CHLOE_AUTH}"], env: { CHLOE_AUTH: `Bearer ${token}` } } } }, null, 2);
  }
}

function EndpointDetail({ ep, url, update }: { ep: MCPEndpoint; url: string; update(e: MCPEndpoint): void }) {
  const [name, setName] = useState(ep.name);
  const [client, setClient] = useState<Client>("claude-code");
  const [copied, setCopied] = useState("");
  const connections = useApp((s) => s.connections);
  const sqlConns = connections.filter((c) => SQL_ENGINES.includes(c.type));
  const masked = ep.token.slice(0, 10) + "…" + ep.token.slice(-4);

  const copy = (what: string, text: string) => {
    navigator.clipboard.writeText(text).catch(() => {});
    setCopied(what);
    setTimeout(() => setCopied(""), 1500);
  };
  const regenerate = async () => {
    if (!(await confirm("Regenerate the token?", { message: `Clients using the current token of “${ep.name}” stop working until they're given the new one.`, okLabel: "Regenerate" }))) return;
    update({ ...ep, token: await MCP.NewToken() });
  };
  const setDanger = async (on: boolean) => {
    if (on) {
      const ok = await confirm("Turn on danger mode?", {
        message: `Agents using “${ep.name}” will insert, update and delete rows on its read-write connections without asking you first. Table data can contain instructions aimed at the agent, so only use this with connections you can afford to lose changes on.`,
        okLabel: "Turn On",
      });
      if (!ok) return;
    }
    update({ ...ep, danger: on });
  };

  return (
    <div className="mcp-detail">
      <div className="form mcp-form">
        <label>
          <span>Name</span>
          <input aria-label="Endpoint name" value={name} onChange={(e) => setName(e.target.value)} onBlur={() => name.trim() && name !== ep.name ? update({ ...ep, name: name.trim() }) : setName(ep.name)} />
        </label>
        <label className="check">
          <input type="checkbox" checked={ep.enabled} onChange={(e) => update({ ...ep, enabled: e.target.checked })} />
          Enabled
        </label>
        <div className="mcp-row">
          <span className="mcp-label">Token</span>
          <code className="mcp-token">{masked}</code>
          <button onClick={() => copy("token", ep.token)}>
            <Copy size={13} /> {copied === "token" ? "Copied" : "Copy"}
          </button>
          <button onClick={regenerate}>
            <RefreshCw size={13} /> Regenerate…
          </button>
        </div>
        <div className="mcp-row">
          <span className="mcp-label">Client</span>
          <select aria-label="Client" value={client} onChange={(e) => setClient(e.target.value as Client)}>
            {CLIENTS.map(([k, l]) => (
              <option key={k} value={k}>
                {l}
              </option>
            ))}
          </select>
          <button onClick={() => copy("config", clientConfig(client, ep.name, url, ep.token))}>
            <Copy size={13} /> {copied === "config" ? "Copied" : "Copy Config"}
          </button>
        </div>
        <pre className="mcp-snippet">{clientConfig(client, ep.name, url, masked)}</pre>
        <label className="check">
          <input type="checkbox" aria-label="Danger mode" checked={ep.danger} onChange={(e) => setDanger(e.target.checked)} />
          Danger mode: run writes without asking
        </label>
        <p className="hint">{ep.danger ? "Writes run as soon as the agent asks. Read-only access and hidden databases still apply." : "Each insert, update or delete waits for you to approve it in Chloe."}</p>
      </div>
      <div className="pane-title mcp-access-title">Connection Access</div>
      <div className="mcp-access">
        <table>
          <thead>
            <tr>
              <th>Connection</th>
              <th>Access</th>
            </tr>
          </thead>
          <tbody>
            {sqlConns.map((c) => (
              <AccessRows key={c.id} connId={c.id} ep={ep} update={update} />
            ))}
          </tbody>
        </table>
        {!sqlConns.length && <div className="empty-hint">No MySQL, PostgreSQL or SQLite connections yet.</div>}
        <p className="hint">MongoDB and Redis connections aren't available over MCP yet.</p>
      </div>
    </div>
  );
}

const LEVELS: Array<[MCPLevel, string]> = [
  [MCPLevel.MCPOff, "Off"],
  [MCPLevel.MCPRead, "Read"],
  [MCPLevel.MCPWrite, "Read-write"],
];

function AccessRows({ connId, ep, update }: { connId: string; ep: MCPEndpoint; update(e: MCPEndpoint): void }) {
  const conn = useApp((s) => s.connections.find((c) => c.id === connId))!;
  const [expanded, setExpanded] = useState(false);
  const [dbs, setDbs] = useState<string[] | null>(null);
  const [error, setError] = useState("");
  const access = ep.access?.[connId] ?? { level: MCPLevel.MCPOff };
  const overrides = access.databases ?? {};

  const setAccess = (a: typeof access) => update({ ...ep, access: { ...ep.access, [connId]: a } });
  const expand = async () => {
    const open = !expanded;
    setExpanded(open);
    if (!open || dbs) return;
    if (!(await useApp.getState().connect(connId))) {
      setError(useApp.getState().errors[connId] || "Couldn't connect");
      return;
    }
    Explorer.ListDatabases(connId)
      .then((d) => setDbs(arr(d)))
      .catch((e) => setError(errorText(e)));
  };
  // Databases with overrides show even before the list loads (or if they're gone).
  const shown = useMemo(() => [...new Set([...(dbs ?? []), ...Object.keys(overrides)])], [dbs, overrides]);

  return (
    <>
      <tr>
        <td>
          <button className="icon mcp-expand" onClick={expand} title="Access per database" aria-label={`Databases of ${conn.name}`}>
            {expanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
          </button>
          <ConnIcon type={conn.type} preset={conn.preset} /> {conn.name}
        </td>
        <td>
          <select aria-label={`Access to ${conn.name}`} value={access.level} onChange={(e) => setAccess({ ...access, level: e.target.value as MCPLevel })}>
            {LEVELS.map(([v, l]) => (
              <option key={l} value={v}>
                {l}
              </option>
            ))}
          </select>
        </td>
      </tr>
      {expanded && error && (
        <tr>
          <td colSpan={2} className="mcp-db muted">
            {error}
          </td>
        </tr>
      )}
      {expanded &&
        shown.map((db) => (
          <tr key={db}>
            <td className="mcp-db">{db}</td>
            <td>
              <select
                aria-label={`Access to ${conn.name} ${db}`}
                value={db in overrides ? overrides[db] || "off" : "inherit"}
                onChange={(e) => {
                  const next = { ...overrides };
                  const v = e.target.value;
                  if (v === "inherit") delete next[db];
                  else next[db] = (v === "off" ? MCPLevel.MCPOff : v) as MCPLevel;
                  setAccess({ ...access, databases: next });
                }}
              >
                <option value="inherit">Same as connection</option>
                {LEVELS.map(([v, l]) => (
                  <option key={l} value={v || "off"}>
                    {l}
                  </option>
                ))}
              </select>
            </td>
          </tr>
        ))}
    </>
  );
}

const time = (iso: string) => {
  const d = new Date(iso);
  return isNaN(+d) ? iso : d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", second: "2-digit" });
};

const OUTCOMES: Record<string, string> = { ok: "OK", error: "Error", approved: "Approved", denied: "Denied", unasked: "Ran without asking" };

function ActivityLog() {
  const activity = useMCP((s) => s.activity);
  const [sel, setSel] = useState<string>("");
  const picked = activity.find((a) => a.id === sel);
  return (
    <div className="mcp-activity">
      <div className="toolbar">
        <span className="muted">{activity.length ? `${activity.length} calls (kept until Chloe quits)` : "No tool calls yet"}</span>
        <span className="spacer" />
        <button
          disabled={!activity.length}
          onClick={async () => {
            await MCP.ClearActivity();
            useMCP.setState({ activity: [] });
          }}
        >
          <Trash2 size={13} /> Clear
        </button>
      </div>
      <Split
        id="mcp-activity"
        vertical
        className="mcp-body"
        secondSize={140}
        minFirst={100}
        minSecond={60}
        first={
          <div className="monitor-table">
            <table>
              <thead>
                <tr>
                  <th>Time</th>
                  <th>Endpoint</th>
                  <th>Tool</th>
                  <th>Connection</th>
                  <th>Database</th>
                  <th>Outcome</th>
                  <th className="num">Rows</th>
                  <th className="num">ms</th>
                  <th>SQL</th>
                </tr>
              </thead>
              <tbody>
                {activity.map((a) => (
                  <tr key={a.id} className={(a.id === sel ? "selected" : "") + " outcome-" + a.outcome} onClick={() => setSel(a.id)}>
                    <td>{time(a.time)}</td>
                    <td>{a.endpoint}</td>
                    <td className="mono">{a.tool}</td>
                    <td>{a.connection}</td>
                    <td>{a.database}</td>
                    <td className="mcp-outcome">{OUTCOMES[a.outcome] ?? a.outcome}</td>
                    <td className="num">{a.rows || ""}</td>
                    <td className="num">{a.durationMs}</td>
                    <td className="query" title={a.sql}>
                      {a.error || a.sql}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        }
        second={
          <div className="hist-detail">
            <pre>{picked ? [picked.sql, picked.error && `-- ${picked.error}`].filter(Boolean).join("\n\n") : ""}</pre>
          </div>
        }
      />
    </div>
  );
}
