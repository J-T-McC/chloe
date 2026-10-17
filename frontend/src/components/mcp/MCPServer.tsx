// MCP Server: endpoints (token, access per connection and database, danger mode)
// and the activity log of tool calls. See docs/specs/mcp-server.md.
import { Bot, ChevronDown, ChevronRight, Copy, Folder, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useVirtualizer } from "@tanstack/react-virtual";
import { useEffect, useMemo, useRef, useState } from "react";
import { create } from "zustand";
import { DBType, Events, Explorer, MCP, Mongo, Redis, MCPLevel, arr, errorText, type MCPActivity, type MCPEndpoint, type MCPSettings, type MCPStatus, copyText } from "../../api";
import { openAgentQuery, refreshFolder } from "../../queries";
import { openModel } from "../model/actions";
import { useApp } from "../../store";
import { alert, confirm } from "../Dialogs";
import { ConnIcon } from "../icons";
import { Split } from "../Split";
import { Toolbar } from "../Toolbar";

/** Engines MCP serves. */
const MCP_ENGINES: string[] = [DBType.MySQL, DBType.MariaDB, DBType.PostgreSQL, DBType.SQLite, DBType.Redis, DBType.MongoDB];

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
  // A group rename moves its access; an agent can add connections.
  Events.On("mcp:settings", (ev) => useMCP.setState({ settings: ev.data }));
  Events.On("mcp:connection-added", () => useApp.getState().loadConnections());
  Events.On("mcp:query-saved", (ev) => refreshFolder(ev.data.connId, ev.data.database, ev.data.schema || undefined));
  Events.On("mcp:open-query", (ev) => openAgentQuery(ev.data));
  Events.On("mcp:open-model", (ev) => openModel(ev.data));
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
      <Toolbar>
        <label className="check">
          <input type="checkbox" aria-label="Run MCP server" checked={settings.enabled} onChange={(e) => save({ ...settings, enabled: e.target.checked })} />
          Run MCP server
        </label>
        <label className="check" title="Clients can also use the bridge (chloe mcp), which needs no network port">
          <input type="checkbox" aria-label="HTTP" checked={!settings.noHttp} onChange={(e) => save({ ...settings, noHttp: !e.target.checked })} />
          HTTP on port
          <input className="mcp-port" aria-label="Port" disabled={settings.noHttp} value={port} onChange={(e) => setPort(e.target.value)} onBlur={applyPort} onKeyDown={(e) => e.key === "Enter" && applyPort()} />
        </label>
        <span className={"mcp-status" + (status?.error ? " error" : "")}>{status?.error ? status.error : !status?.running ? "Stopped" : status.http ? `Listening on ${status.url} and the bridge` : "Bridge only (no network port)"}</span>
        <span className="spacer" />
        <div className="seg">
          {(["endpoints", "activity"] as Page[]).map((p) => (
            <button key={p} className={page === p ? "active" : ""} onClick={() => setPage(p)}>
              {p === "endpoints" ? "Endpoints" : "Activity"}
            </button>
          ))}
        </div>
      </Toolbar>
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
    const next = await MCP.Save({ ...settings, endpoints: [...endpoints, { id: "", name, enabled: true, token: "", danger: false, capabilities: { createConnections: false, savedQueries: false, monitor: false, models: false, reports: false }, access: {} }] }).catch((e) => {
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

type Client = "claude-code-bridge" | "claude-code" | "json-bridge" | "json" | "claude-desktop";
const CLIENTS: Array<[Client, string, "bridge" | "http"]> = [
  ["claude-code-bridge", "Claude Code (bridge)", "bridge"],
  ["claude-code", "Claude Code (HTTP)", "http"],
  ["json-bridge", "JSON, bridge (Claude Desktop, .mcp.json, Cursor, …)", "bridge"],
  ["json", "JSON, HTTP (.mcp.json, Cursor, …)", "http"],
  ["claude-desktop", "Claude Desktop (HTTP via mcp-remote)", "http"],
];

function slug(name: string) {
  return "chloe-" + (name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || "db");
}

/** The client snippet. The bridge passes the token in the environment, never on the command line. */
export function clientConfig(client: Client, name: string, url: string, token: string, bridge = ""): string {
  const id = slug(name);
  switch (client) {
    case "claude-code-bridge":
      return `claude mcp add ${id} --env CHLOE_MCP_TOKEN=${token} -- "${bridge}" mcp`;
    case "claude-code":
      return `claude mcp add --transport http ${id} ${url} --header "Authorization: Bearer ${token}"`;
    case "json-bridge":
      return JSON.stringify({ mcpServers: { [id]: { command: bridge, args: ["mcp"], env: { CHLOE_MCP_TOKEN: token } } } }, null, 2);
    case "json":
      return JSON.stringify({ mcpServers: { [id]: { type: "http", url, headers: { Authorization: `Bearer ${token}` } } } }, null, 2);
    case "claude-desktop":
      return JSON.stringify({ mcpServers: { [id]: { command: "npx", args: ["-y", "mcp-remote", url, "--header", "Authorization:${CHLOE_AUTH}"], env: { CHLOE_AUTH: `Bearer ${token}` } } } }, null, 2);
  }
}

function EndpointDetail({ ep, url, update }: { ep: MCPEndpoint; url: string; update(e: MCPEndpoint): void }) {
  const [name, setName] = useState(ep.name);
  const status = useMCP((s) => s.status);
  // The bridge needs the server running (it reports the command); HTTP needs its port on.
  const available = CLIENTS.filter(([, , via]) => (via === "bridge" ? !!status?.bridge : !status?.running || status.http));
  const [picked, setClient] = useState<Client>("claude-code-bridge");
  const client = available.some(([k]) => k === picked) ? picked : (available[0]?.[0] ?? "claude-code");
  const bridge = status?.bridge ?? "";
  const [copied, setCopied] = useState("");
  const masked = ep.token.slice(0, 10) + "…" + ep.token.slice(-4);

  const copy = async (what: string, text: string) => {
    try {
      await copyText(text);
    } catch (e) {
      return alert("Couldn't copy to the clipboard", errorText(e));
    }
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
            {available.map(([k, l]) => (
              <option key={k} value={k}>
                {l}
              </option>
            ))}
          </select>
          <button onClick={() => copy("config", clientConfig(client, ep.name, url, ep.token, bridge))}>
            <Copy size={13} /> {copied === "config" ? "Copied" : "Copy Config"}
          </button>
        </div>
        <pre className="mcp-snippet">{clientConfig(client, ep.name, url, masked, bridge)}</pre>
        <p className="hint">The token is shortened here; Copy Config copies it in full.</p>
        <label className="check">
          <input type="checkbox" aria-label="Danger mode" checked={ep.danger} onChange={(e) => setDanger(e.target.checked)} />
          Danger mode: run writes without asking
        </label>
        <p className="hint">{ep.danger ? "Writes run as soon as the agent asks. Read-only access and hidden databases still apply." : "Each change an agent makes waits for you to approve it in Chloe."}</p>
      </div>
      <div className="pane-title mcp-access-title">Connection Access</div>
      <div className="mcp-access">
        <AccessTable ep={ep} update={update} />
        <p className="hint">Connections take their group's access unless set otherwise.</p>
      </div>
      <div className="pane-title mcp-access-title">Capabilities</div>
      <div className="form mcp-form">
        <label className="check">
          <input
            type="checkbox"
            aria-label="Create connections"
            checked={ep.capabilities.createConnections}
            onChange={(e) => update({ ...ep, capabilities: { ...ep.capabilities, createConnections: e.target.checked } })}
          />
          Create connections
        </label>
        {ep.capabilities.createConnections && <NewConnectionGroup ep={ep} update={update} />}
        <label className="check">
          <input
            type="checkbox"
            aria-label="Saved queries and query tabs"
            checked={ep.capabilities.savedQueries}
            onChange={(e) => update({ ...ep, capabilities: { ...ep.capabilities, savedQueries: e.target.checked } })}
          />
          Saved queries and query tabs
        </label>
        <p className="hint">Read and save queries, and open query tabs for you to review and run. Agents never replace a saved query or run a tab.</p>
        <label className="check">
          <input
            type="checkbox"
            aria-label="Monitor"
            checked={ep.capabilities.monitor}
            onChange={(e) => update({ ...ep, capabilities: { ...ep.capabilities, monitor: e.target.checked } })}
          />
          Monitor
        </label>
        <p className="hint">Sessions, status and variables of connections whose databases are all shared. Killing a session needs Read-write and asks unless danger mode is on.</p>
        <label className="check">
          <input
            type="checkbox"
            aria-label="ER models"
            checked={ep.capabilities.models}
            onChange={(e) => update({ ...ep, capabilities: { ...ep.capabilities, models: e.target.checked } })}
          />
          ER models
        </label>
        <p className="hint">Read the models in Chloe’s library, build models from databases and generate SQL. Agents edit only models they created; nothing is applied to a database.</p>
        <label className="check">
          <input
            type="checkbox"
            aria-label="Reports"
            checked={ep.capabilities.reports}
            onChange={(e) => update({ ...ep, capabilities: { ...ep.capabilities, reports: e.target.checked } })}
          />
          Reports
        </label>
        <p className="hint">Build BI workspaces: data sources on shared databases (always read-only), charts, and the data they show. Agents edit only charts they created.</p>
      </div>
    </div>
  );
}

const DEFAULT_GROUP = "Created by AI";

function NewConnectionGroup({ ep, update }: { ep: MCPEndpoint; update(e: MCPEndpoint): void }) {
  const groups = useApp((s) => s.groups);
  const group = ep.connectionGroup || DEFAULT_GROUP;
  const [value, setValue] = useState(group);
  useEffect(() => setValue(group), [group]);
  const level = ep.groups?.[group] ?? MCPLevel.MCPOff;
  const apply = () => {
    const v = value.trim() || DEFAULT_GROUP;
    if (v !== group) update({ ...ep, connectionGroup: v === DEFAULT_GROUP ? "" : v });
    else setValue(group);
  };
  return (
    <>
      <div className="mcp-row">
        <span className="mcp-label">Group</span>
        <input aria-label="Group for new connections" list="mcp-groups" value={value} onChange={(e) => setValue(e.target.value)} onBlur={apply} onKeyDown={(e) => e.key === "Enter" && apply()} />
        <datalist id="mcp-groups">
          {[...new Set([DEFAULT_GROUP, ...groups])].map((g) => (
            <option key={g} value={g} />
          ))}
        </datalist>
        <select aria-label={`Access to group ${group}`} value={level} onChange={(e) => update({ ...ep, groups: { ...ep.groups, [group]: e.target.value as MCPLevel } })}>
          {LEVELS.map(([v, l]) => (
            <option key={l} value={v}>
              {l}
            </option>
          ))}
        </select>
      </div>
      <p className="hint">
        Agents can add MySQL, MariaDB, PostgreSQL and SQLite connections to this group. You review each one, even in danger mode, and can type its password yourself. It gets the group's access, which also
        applies to other endpoints that have access to the group.
      </p>
    </>
  );
}

const LEVELS: Array<[MCPLevel, string]> = [
  [MCPLevel.MCPOff, "Off"],
  [MCPLevel.MCPRead, "Read"],
  [MCPLevel.MCPWrite, "Read-write"],
];

const levelName = (l: MCPLevel | undefined) => LEVELS.find(([v]) => v === (l ?? MCPLevel.MCPOff))?.[1] ?? "Off";

/** Groups with their connections, then top-level connections. */
function AccessTable({ ep, update }: { ep: MCPEndpoint; update(e: MCPEndpoint): void }) {
  const connections = useApp((s) => s.connections);
  const groups = useApp((s) => s.groups);
  const shared = connections.filter((c) => MCP_ENGINES.includes(c.type));
  // Groups set on the endpoint show even when empty (e.g. the new-connection group).
  const allGroups = [...new Set([...groups, ...Object.keys(ep.groups ?? {})])];
  const top = shared.filter((c) => !c.group);
  if (!shared.length && !allGroups.length) return <div className="empty-hint">No database connections yet.</div>;
  return (
    <table>
      <thead>
        <tr>
          <th>Connection</th>
          <th>Access</th>
        </tr>
      </thead>
      <tbody>
        {allGroups.map((g) => (
          <GroupRows key={g} group={g} conns={shared.filter((c) => c.group === g).map((c) => c.id)} ep={ep} update={update} />
        ))}
        {top.map((c) => (
          <AccessRows key={c.id} connId={c.id} ep={ep} update={update} />
        ))}
      </tbody>
    </table>
  );
}

function GroupRows({ group, conns, ep, update }: { group: string; conns: string[]; ep: MCPEndpoint; update(e: MCPEndpoint): void }) {
  const [expanded, setExpanded] = useState(true);
  const level = ep.groups?.[group] ?? MCPLevel.MCPOff;
  return (
    <>
      <tr className="mcp-group">
        <td>
          <button className="icon mcp-expand" onClick={() => setExpanded(!expanded)} aria-label={`Connections in ${group}`}>
            {expanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
          </button>
          <Folder size={14} className="mcp-folder" /> {group}
        </td>
        <td>
          <select aria-label={`Access to group ${group}`} value={level} onChange={(e) => update({ ...ep, groups: { ...ep.groups, [group]: e.target.value as MCPLevel } })}>
            {LEVELS.map(([v, l]) => (
              <option key={l} value={v}>
                {l}
              </option>
            ))}
          </select>
        </td>
      </tr>
      {expanded && conns.map((id) => <AccessRows key={id} connId={id} ep={ep} update={update} groupLevel={level} />)}
      {expanded && !conns.length && (
        <tr>
          <td colSpan={2} className="mcp-db muted">
            No connections yet
          </td>
        </tr>
      )}
    </>
  );
}

function AccessRows({ connId, ep, update, groupLevel }: { connId: string; ep: MCPEndpoint; update(e: MCPEndpoint): void; groupLevel?: MCPLevel }) {
  const conn = useApp((s) => s.connections.find((c) => c.id === connId))!;
  const [expanded, setExpanded] = useState(false);
  const [dbs, setDbs] = useState<string[] | null>(null);
  const [error, setError] = useState("");
  const inGroup = groupLevel !== undefined;
  const access = ep.access?.[connId] ?? { level: MCPLevel.MCPInherit };
  const inherits = access.level === MCPLevel.MCPInherit;
  const effective = inherits ? (groupLevel ?? MCPLevel.MCPOff) : access.level;
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
    // Redis databases are numbers; access is keyed by the number, shown as db0, db1…
    const list =
      conn.type === DBType.Redis
        ? Redis.Databases(connId).then((d) => arr(d).map((x) => String(x.index)))
        : conn.type === DBType.MongoDB
          ? Mongo.Databases(connId).then((d) => arr(d).map((x) => x.name))
          : Explorer.ListDatabases(connId).then(arr);
    list
      .then((d) => setDbs(d))
      .catch((e) => setError(errorText(e)));
  };
  // Databases with overrides show even before the list loads (or if they're gone).
  const shown = useMemo(() => [...new Set([...(dbs ?? []), ...Object.keys(overrides)])], [dbs, overrides]);
  // Top-level connections have no group to inherit from: unset means Off.
  const value = inherits ? (inGroup ? "inherit" : "off") : access.level || "off";

  return (
    <>
      <tr>
        <td className={inGroup ? "mcp-in-group" : ""}>
          <button className="icon mcp-expand" onClick={expand} title="Access per database" aria-label={`Databases of ${conn.name}`}>
            {expanded ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
          </button>
          <ConnIcon type={conn.type} preset={conn.preset} /> {conn.name}
        </td>
        <td>
          <select
            aria-label={`Access to ${conn.name}`}
            value={value}
            onChange={(e) => {
              const v = e.target.value;
              setAccess({ ...access, level: (v === "off" ? MCPLevel.MCPOff : v) as MCPLevel });
            }}
          >
            {inGroup && <option value="inherit">Same as group ({levelName(groupLevel)})</option>}
            {LEVELS.map(([v, l]) => (
              <option key={l} value={v || "off"}>
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
            <td className={"mcp-db" + (inGroup ? " mcp-in-group" : "")}>{conn.type === DBType.Redis ? `db${db}` : db}</td>
            <td>
              <select
                aria-label={`Access to ${conn.name} ${conn.type === DBType.Redis ? `db${db}` : db}`}
                value={db in overrides ? overrides[db] || "off" : "inherit"}
                onChange={(e) => {
                  const next = { ...overrides };
                  const v = e.target.value;
                  if (v === "inherit") delete next[db];
                  else next[db] = (v === "off" ? MCPLevel.MCPOff : v) as MCPLevel;
                  setAccess({ ...access, databases: next });
                }}
              >
                <option value="inherit">Same as connection ({levelName(effective)})</option>
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

const OUTCOMES: Record<string, string> = { ok: "OK", error: "Error", approved: "Approved", denied: "Denied", timeout: "No answer", unasked: "Ran without asking" };

function ActivityLog() {
  const activity = useMCP((s) => s.activity);
  const [sel, setSel] = useState<string>("");
  const picked = activity.find((a) => a.id === sel);
  // Up to 1000 calls are kept; only the rows in view are drawn.
  const scroller = useRef<HTMLDivElement>(null);
  const v = useVirtualizer({ count: activity.length, getScrollElement: () => scroller.current, estimateSize: () => 24, overscan: 20 });
  const rows = v.getVirtualItems();
  const pad = (h: number) => (h > 0 ? <tr style={{ height: h }} aria-hidden /> : null);
  return (
    <div className="mcp-activity">
      <Toolbar>
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
      </Toolbar>
      <Split
        id="mcp-activity"
        vertical
        className="mcp-body"
        secondSize={140}
        minFirst={100}
        minSecond={60}
        first={
          <div className="monitor-table" ref={scroller}>
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
                {pad(rows[0]?.start ?? 0)}
                {rows.map((row) => {
                  const a = activity[row.index];
                  return (
                  <tr key={a.id} data-index={row.index} ref={v.measureElement} className={(a.id === sel ? "selected" : "") + " outcome-" + a.outcome} onClick={() => setSel(a.id)}>
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
                  );
                })}
                {pad(v.getTotalSize() - (rows[rows.length - 1]?.end ?? 0))}
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
