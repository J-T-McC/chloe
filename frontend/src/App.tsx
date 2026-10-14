import { Activity, Database, FilePlus2, Monitor, Moon, Network, PencilRuler, Plug, Shapes, Sun, Table2, TerminalSquare, UserRound, UsersRound, X } from "lucide-react";
import { ThemePref, onThemeChange, setThemePref, themePref } from "./theme";
import { useEffect, useState } from "react";
import { Events, ObjectKind } from "./api";
import { ConnectionDialog } from "./components/ConnectionDialog";
import { ContextMenuHost } from "./components/ContextMenu";
import { DialogHost } from "./components/Dialogs";
import { DatabaseDialog } from "./components/DatabaseDialog";
import { KindIcon } from "./components/icons";
import { ManageConnections } from "./components/ManageConnections";
import { Split } from "./components/Split";
import { ExecuteFileDialog } from "./components/transfer/ExecuteFileDialog";
import { ExportWizard } from "./components/transfer/ExportWizard";
import { ImportWizard } from "./components/transfer/ImportWizard";
import { JobsButton } from "./components/transfer/JobsPanel";
import { ServerMonitor } from "./components/ServerMonitor";
import { TableDesigner } from "./components/designer/TableDesigner";
import { ViewDesigner } from "./components/designer/ViewDesigner";
import { RedisConsole } from "./components/redis/RedisConsole";
import { UsersPane } from "./components/users/UsersPane";
import { UserDesigner } from "./components/users/UserDesigner";
import { SetPrivilegesDialog } from "./components/users/SetPrivilegesDialog";
import { openUsers } from "./components/users/actions";
import { ERDiagram } from "./components/erd/ERDiagram";
import { ModelEditor } from "./components/model/ModelEditor";
import { newModel, openModel } from "./components/model/actions";
import { MenuItem, openMenu } from "./components/ContextMenu";
import { DBType, Models, arr } from "./api";
import { DDLView, ObjectsPane } from "./components/ObjectsPane";
import { QueryEditor } from "./components/QueryEditor";
import { Sidebar, newQuery, openMonitor, selectAllSQL, useInitialLoad } from "./components/Sidebar";
import { TableViewer } from "./components/TableViewer";
import { Tab, connById, useApp } from "./store";
import { openRedisConsole } from "./components/redis/actions";
import { newTable } from "./tableActions";
import { RevealTarget, useTree } from "./tree";

export default function App() {
  useInitialLoad();
  const tabs = useApp((s) => s.tabs);
  const active = useApp((s) => s.activeTab);
  const selection = useApp((s) => s.selection);
  const open = useApp((s) => s.open);

  // The backend can drop a connection (e.g. when its profile is edited).
  useEffect(
    () =>
      Events.On("connection:closed", (ev) => {
        const id = ev.data;
        if (useApp.getState().open[id]) useApp.getState().disconnect(id);
        useTree.getState().collapseConnection(id);
      }),
    [],
  );

  // Global shortcuts.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const meta = e.metaKey || e.ctrlKey;
      if (meta && e.key === "w") {
        e.preventDefault();
        useApp.getState().closeTab(useApp.getState().activeTab);
      }
      // New Query: ⌘T on macOS (⌘Q is the system Quit); Ctrl+T or Ctrl+Q elsewhere,
      // matching Navicat on Windows. With a table or view selected, it starts with a SELECT.
      const isMac = /Mac/i.test(navigator.platform);
      const newQueryKey = meta && (e.key === "t" || (!isMac && e.key === "q"));
      if (newQueryKey && selection?.connId && open[selection.connId] && connById(selection.connId)?.type === DBType.Redis) {
        e.preventDefault();
        openRedisConsole(selection.connId, Number(selection.database ?? 0));
      } else if (newQueryKey && selection?.connId && open[selection.connId]) {
        e.preventDefault();
        const o = selection.object;
        const isData = o && (o.kind === ObjectKind.KindTable || o.kind === ObjectKind.KindView || o.kind === ObjectKind.KindMatView);
        newQuery(selection.connId, selection.database, selection.schema, isData ? selectAllSQL(selection.connId, o!) : "");
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [selection, open]);

  const selConnected = !!(selection?.connId && open[selection.connId]);
  const selRedis = connById(selection?.connId)?.type === DBType.Redis;

  return (
    <div className="app">
      <header className="titlebar">
        <div className="titlebar-drag" />
        <div className="main-toolbar">
          <ToolButton icon={<Plug size={20} />} label="Connection" onClick={() => useApp.getState().editConnection("new")} />
          <ToolButton
            icon={<FilePlus2 size={20} />}
            label="New Query"
            disabled={!selConnected}
            onClick={() => selection && (selRedis ? openRedisConsole(selection.connId, Number(selection.database ?? 0)) : newQuery(selection.connId, selection.database, selection.schema))}
          />
          <ToolButton
            icon={<PencilRuler size={20} />}
            label="New Table"
            disabled={!selection?.database || !selConnected || selRedis}
            onClick={() => selection?.database && newTable(selection.connId, selection.database, selection.schema)}
          />
          <ToolButton
            icon={<Table2 size={20} />}
            label="Tables"
            disabled={!selection?.database || selRedis}
            onClick={() => selection && useApp.getState().select({ ...selection, kind: ObjectKind.KindTable, object: undefined })}
          />
          <ToolButton icon={<UsersRound size={20} />} label="User" disabled={!selConnected || selRedis} onClick={() => selection && openUsers(selection.connId)} />
          <ToolButton icon={<Shapes size={20} />} label="Model" onClick={(e) => modelMenu(e)} />
          <ToolButton icon={<Activity size={20} />} label="Monitor" disabled={!Object.keys(open).length} onClick={() => openMonitor(selConnected ? selection!.connId : Object.keys(open)[0])} />
          <ToolButton
            icon={<Database size={20} />}
            label={selConnected ? "Disconnect" : "Connect"}
            disabled={!selection}
            onClick={() => {
              if (!selection) return;
              if (selConnected) {
                useApp.getState().disconnect(selection.connId);
                useTree.getState().collapseConnection(selection.connId);
              } else {
                const c = useApp.getState().connections.find((x) => x.id === selection.connId)!;
                useTree.getState().expand({ key: c.id, type: "conn", label: c.name, connId: c.id, depth: 0, expandable: true });
              }
            }}
          />
        </div>
        <div className="titlebar-right">
          <ThemeButton />
          <JobsButton />
        </div>
      </header>
      <Split
        id="main"
        className="body"
        firstSize={260}
        minFirst={160}
        maxFirst="50"
        first={<Sidebar />}
        second={
          <div className="workspace">
            <div className="tabbar">
              {tabs.map((t) => (
                <TabButton key={t.id} tab={t} active={t.id === active} />
              ))}
            </div>
            <div className="tab-content">
              {tabs.map((t) => (
                // Keep inactive tabs mounted so editors and grids keep their state.
                <div key={t.id} className="tab-pane" style={{ display: t.id === active ? "flex" : "none" }}>
                  <TabContent tab={t} />
                </div>
              ))}
            </div>
          </div>
        }
      />
      <ManageConnections />
      <ExportWizard />
      <ImportWizard />
      <ExecuteFileDialog />
      <DatabaseDialog />
      <SetPrivilegesDialog />
      <ConnectionDialog />
      <DialogHost />
      <ContextMenuHost />
    </div>
  );
}

function TabContent({ tab }: { tab: Tab }) {
  switch (tab.kind) {
    case "objects":
      return <ObjectsPane />;
    case "query":
      return <QueryEditor tab={tab} />;
    case "table":
      return <TableViewer tab={tab} />;
    case "ddl":
      return <DDLView connId={tab.connId!} objRef={tab.ref!} />;
    case "monitor":
      return <ServerMonitor tab={tab} />;
    case "design":
      return <TableDesigner tab={tab} />;
    case "erd":
      return <ERDiagram tab={tab} />;
    case "model":
      return <ModelEditor tab={tab} />;
    case "viewdesign":
      return <ViewDesigner tab={tab} />;
    case "redisconsole":
      return <RedisConsole tab={tab} />;
    case "users":
      return <UsersPane tab={tab} />;
    case "user":
      return <UserDesigner tab={tab} />;
  }
}

function TabButton({ tab, active }: { tab: Tab; active: boolean }) {
  const icon = tab.kind === "users" ? <UsersRound size={13} /> : tab.kind === "user" ? <UserRound size={13} /> : tab.kind === "redisconsole" ? <TerminalSquare size={13} /> : tab.kind === "model" ? <Shapes size={13} /> : tab.kind === "erd" ? <Network size={13} /> : tab.kind === "design" || tab.kind === "viewdesign" ? <PencilRuler size={13} /> : tab.kind === "monitor" ? <Activity size={13} /> : tab.kind === "query" ? <FilePlus2 size={13} /> : tab.kind === "objects" ? <Database size={13} /> : tab.ref ? <KindIcon kind={tab.ref.kind} size={13} /> : null;
  return (
    <div
      className={"tab" + (active ? " active" : "")}
      onMouseDown={(e) => {
        if (e.button === 1) useApp.getState().closeTab(tab.id);
        else if (e.button === 0) useApp.getState().setActive(tab.id);
      }}
      onContextMenu={(e) => openMenu(e, tabMenu(tab))}
      title={tab.title}
    >
      {icon}
      <span className="tab-title">{tab.title}</span>
      {tab.kind !== "objects" && (
        <button
          className="tab-close"
          onMouseDown={(e) => e.stopPropagation()}
          onClick={() => useApp.getState().closeTab(tab.id)}
        >
          <X size={12} />
        </button>
      )}
    </div>
  );
}

function ThemeButton() {
  const [pref, setPref] = useState<ThemePref>(themePref());
  useEffect(() => onThemeChange(() => setPref(themePref())), []);
  const Icon = pref === "light" ? Sun : pref === "dark" ? Moon : Monitor;
  const choose = (p: ThemePref) => setThemePref(p);
  return (
    <button
      className="titlebar-button"
      title="Appearance"
      onClick={(e) => {
        const r = e.currentTarget.getBoundingClientRect();
        openMenu({ clientX: r.left, clientY: r.bottom + 2, preventDefault() {} }, [
          { label: "System", checked: pref === "system", onClick: () => choose("system") },
          { label: "Light", checked: pref === "light", onClick: () => choose("light") },
          { label: "Dark", checked: pref === "dark", onClick: () => choose("dark") },
        ]);
      }}
    >
      <Icon size={15} />
    </button>
  );
}

/** Right-click menu of a tab (Navicat's). */
function tabMenu(tab: Tab): MenuItem[] {
  const app = useApp.getState();
  const tabs = app.tabs;
  const idx = tabs.findIndex((t) => t.id === tab.id);
  const closable = (t: Tab) => t.kind !== "objects";
  const others = tabs.filter((t) => t.id !== tab.id && closable(t)).map((t) => t.id);
  const right = tabs.slice(idx + 1).filter(closable).map((t) => t.id);
  const target = revealTarget(tab);
  return [
    { label: "Close", shortcut: "⌘W", disabled: !closable(tab), onClick: () => app.closeTab(tab.id) },
    { label: "Close Other Tabs", disabled: !others.length, onClick: () => app.closeTabs(others) },
    { label: "Close Tabs to the Right", disabled: !right.length, onClick: () => app.closeTabs(right) },
    { label: "Close All", disabled: !tabs.some(closable), onClick: () => app.closeTabs(tabs.filter(closable).map((t) => t.id)) },
    { separator: true },
    { label: "Move Tab to New Window", disabled: true },
    { separator: true },
    { label: "Add to Favorites", disabled: true },
    { label: "Reveal in Navigation Pane", disabled: !target, onClick: () => target && useTree.getState().reveal(target) },
  ];
}

/** Where a tab's object lives in the tree, if anywhere. */
function revealTarget(tab: Tab): RevealTarget | null {
  if (!tab.connId) return null;
  const r = tab.ref;
  const database = tab.database ?? r?.database;
  const schema = tab.schema ?? r?.schema;
  const hasObject = r && r.name && (tab.kind === "table" || tab.kind === "design" || tab.kind === "ddl" || tab.kind === "viewdesign");
  if (hasObject) return { connId: tab.connId, database: r.database, schema: r.schema, kind: r.kind, name: r.name, signature: r.signature };
  // New table/view designers point at their folder.
  if ((tab.kind === "design" || tab.kind === "viewdesign") && r) return { connId: tab.connId, database: r.database, schema: r.schema, kind: r.kind };
  return { connId: tab.connId, database: database || undefined, schema: schema || undefined };
}

async function modelMenu(e: React.MouseEvent) {
  const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
  const recent = arr(await Models.Recent().catch(() => [] as string[]));
  openMenu({ clientX: r.left, clientY: r.bottom + 2, preventDefault() {} }, [
    { label: "New MySQL Model", onClick: () => newModel("mysql") },
    { label: "New PostgreSQL Model", onClick: () => newModel("postgres") },
    { label: "Open Model…", onClick: () => openModel() },
    ...(recent.length
      ? [{ label: "", separator: true }, ...recent.map((p) => ({ label: p.split("/").pop()!.replace(/\.chloemodel$/, ""), shortcut: p.split("/").slice(-2, -1)[0], onClick: () => openModel(p) }))]
      : []),
  ]);
}

function ToolButton({ icon, label, onClick, disabled }: { icon: React.ReactNode; label: string; onClick(e: React.MouseEvent): void; disabled?: boolean }) {
  return (
    <button className="tool-button" onClick={onClick} disabled={disabled}>
      {icon}
      <span>{label}</span>
    </button>
  );
}

